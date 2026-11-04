package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"interview-bot/internal/config"
)

type Topic struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type QuestionItem struct {
	TopicID    string `json:"topic_id"`
	Difficulty string `json:"difficulty"`
	Question   string `json:"question"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func main() {
	topicsFile := flag.String("topics", "deepseek_json_20261029_a02396.json", "Путь к файлу со списком тем")
	answersFile := flag.String("out", "answers.json", "Путь к выходному файлу с вопросами")
	targetTopic := flag.String("topic", "", "ID конкретной темы для генерации (если пусто — все темы)")
	concurrency := flag.Int("concurrency", 4, "Количество параллельных горутин-воркеров")
	limitTopics := flag.Int("limit", 0, "Ограничение на количество тем (0 — без ограничений)")
	delayMs := flag.Int("delay", 0, "Задержка в миллисекундах между запросами одного воркера")
	force := flag.Bool("force", false, "Принудительно генерировать заново, даже если тема уже есть в answers.json")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки конфигурации: %v", err)
	}

	// 1. Загружаем уже существующие вопросы
	existingQuestions, existingTopicsMap := loadExistingAnswers(*answersFile)
	log.Printf("📂 В файле %s уже находится %d вопросов по %d темам", *answersFile, len(existingQuestions), len(existingTopicsMap))

	// 2. Читаем полный каталог тем
	allTopics, err := loadTopics(*topicsFile)
	if err != nil {
		log.Fatalf("❌ Ошибка чтения тем из %s: %v", *topicsFile, err)
	}
	log.Printf("📚 Загружено %d тем из каталога %s", len(allTopics), *topicsFile)

	// 3. Формируем список тем, которые действительно нужно сгенерировать
	var pendingTopics []Topic
	for _, t := range allTopics {
		if strings.TrimSpace(t.ID) == "" {
			continue
		}
		if *targetTopic != "" && t.ID != *targetTopic {
			continue
		}
		if !*force && existingTopicsMap[t.ID] {
			continue
		}
		pendingTopics = append(pendingTopics, t)
	}

	if *limitTopics > 0 && len(pendingTopics) > *limitTopics {
		pendingTopics = pendingTopics[:*limitTopics]
	}

	totalPending := len(pendingTopics)
	if totalPending == 0 {
		log.Println("✨ Все выбранные темы уже сгенерированы! Нечего делать.")
		return
	}

	workerCount := *concurrency
	if workerCount > totalPending {
		workerCount = totalPending
	}
	if workerCount < 1 {
		workerCount = 1
	}

	log.Printf("⚡ Запускаем Worker Pool: %d горутин на %d тем (delay=%dms)...", workerCount, totalPending, *delayMs)

	// Настройка HTTP клиентов
	directClient := &http.Client{Timeout: 90 * time.Second}
	proxyClient := &http.Client{Timeout: 90 * time.Second}
	if cfg.AI.ProxyURL != "" {
		if pURL, err := url.Parse(cfg.AI.ProxyURL); err == nil {
			proxyClient.Transport = &http.Transport{Proxy: http.ProxyURL(pURL)}
		}
	}

	// 4. Очередь задач (Channel)
	jobs := make(chan Topic, totalPending)
	for _, t := range pendingTopics {
		jobs <- t
	}
	close(jobs)

	// Синхронизация записи в файл и статистики
	var fileMutex sync.Mutex
	var wg sync.WaitGroup
	var completedCount int64
	var totalAddedCount int64

	startTime := time.Now()

	// 5. Запуск пула воркеров
	for w := 1; w <= workerCount; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for t := range jobs {
				log.Printf("[Воркер #%d] 🚀 Начинает генерацию темы: '%s' (%s)...", workerID, t.Title, t.ID)

				var questions []QuestionItem
				var genErr error

				// Retry-цикл при сбоях сети или 429
				for attempt := 1; attempt <= 3; attempt++ {
					questions, genErr = generateQuestionsForTopic(directClient, proxyClient, cfg.AI, t)
					if genErr == nil && len(questions) > 0 {
						break
					}
					log.Printf("[Воркер #%d] ⚠️ Попытка %d для темы %s не удалась (%v), повтор через 3с...", workerID, attempt, t.ID, genErr)
					time.Sleep(3 * time.Second)
				}

				if genErr != nil || len(questions) == 0 {
					log.Printf("[Воркер #%d] ❌ Ошибка генерации для темы %s: %v", workerID, t.ID, genErr)
					continue
				}

				// Подсчет по грейдам
				var jun, mid, sen int
				for _, q := range questions {
					switch q.Difficulty {
					case "junior":
						jun++
					case "middle":
						mid++
					case "senior":
						sen++
					}
				}

				// Потокобезопасная запись в файл
				fileMutex.Lock()
				existingQuestions = append(existingQuestions, questions...)
				currentTotal := len(existingQuestions)
				if err := saveAnswers(*answersFile, existingQuestions); err != nil {
					log.Printf("[Воркер #%d] ❌ Ошибка сохранения %s: %v", workerID, *answersFile, err)
				}
				fileMutex.Unlock()

				done := atomic.AddInt64(&completedCount, 1)
				atomic.AddInt64(&totalAddedCount, int64(len(questions)))

				log.Printf("[Воркер #%d] ✅ [%d/%d] Готово '%s' (%s): +%d вопросов (%d Jun, %d Mid, %d Sen). Всего в файле: %d вопросов!",
					workerID, done, totalPending, t.Title, t.ID, len(questions), jun, mid, sen, currentTotal)

				if *delayMs > 0 {
					time.Sleep(time.Duration(*delayMs) * time.Millisecond)
				}
			}
		}(w)
	}

	wg.Wait()

	duration := time.Since(startTime).Round(time.Second)
	log.Printf("\n🎉 Все воркеры завершили работу за %v!", duration)
	log.Printf("📊 Сгенерировано тем: %d из %d. Добавлено вопросов: %d. Всего в %s: %d вопросов.\n",
		completedCount, totalPending, totalAddedCount, *answersFile, len(existingQuestions))
}

func generateQuestionsForTopic(directClient, proxyClient *http.Client, aiCfg config.AIConfig, t Topic) ([]QuestionItem, error) {
	prompt := fmt.Sprintf(`Ты — Senior Go архитектор и автор технических собеседований.

Тема:
ID: "%s"
Название: "%s"
Описание: "%s"

Твоя задача:
Составь полный банк вопросов для собеседования по этой теме, отталкиваясь от ее описания.
Количество вопросов НЕ ограничено 6 — их должно быть ровно столько, чтобы ПОЛНОСТЬЮ и точечно покрыть ВСЕ концепции из описания (обычно от 6 до 12 вопросов).

Правила:
1. ОДИН ВОПРОС = ОДНА ТЕМА/ПРОБЛЕМА. Не пытайся запихнуть всё в один вопрос. Кандидату должно быть удобно отвечать ёмко и по делу.
2. Распредели вопросы по уровням сложности:
   - "junior": базовое понимание концепции, идиомы, типовые ошибки новичков.
   - "middle": внутреннее устройство, рантайм, грабли, тонкости работы стандартной библиотеки.
   - "senior": архитектурные решения, конкурентность, производительность, нестандартные edge-cases.
3. Вопросы на разных грейдах должны быть АБСОЛЮТНО РАЗНЫМИ по смыслу и глубине.

ФОРМАТ ВЫВОДА:
- Выведи СТРОГО чистый JSON-массив без какого-либо текста до и после него.
- Ответ начинается с [ и заканчивается на ].

[
  {
    "topic_id": "%s",
    "difficulty": "junior",
    "question": "..."
  },
  {
    "topic_id": "%s",
    "difficulty": "middle",
    "question": "..."
  },
  {
    "topic_id": "%s",
    "difficulty": "senior",
    "question": "..."
  }
]`, t.ID, t.Title, t.Description, t.ID, t.ID, t.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()

	var rawText string
	var err error

	// 1. Crassus DeepSeek (через Anthropic Messages)
	for _, apiKey := range aiCfg.DeepSeekKeys {
		rawText, err = callCrassus(ctx, directClient, apiKey, aiCfg.Model, prompt)
		if err == nil && rawText != "" {
			break
		}
	}

	// 2. Fallback на Groq
	if rawText == "" && len(aiCfg.GroqKeys) > 0 {
		for _, apiKey := range aiCfg.GroqKeys {
			rawText, err = callGroq(ctx, proxyClient, apiKey, "openai/gpt-oss-120b", prompt)
			if err == nil && rawText != "" {
				break
			}
		}
	}

	if rawText == "" {
		return nil, fmt.Errorf("все AI провайдеры вернули ошибку: %w", err)
	}

	cleanJSON := extractJSONArray(rawText)

	var items []QuestionItem
	if err := json.Unmarshal([]byte(cleanJSON), &items); err != nil {
		return nil, fmt.Errorf("ошибка парсинга JSON: %w\nСырой ответ:\n%s", err, cleanJSON)
	}

	for i := range items {
		items[i].TopicID = t.ID
		items[i].Difficulty = strings.ToLower(strings.TrimSpace(items[i].Difficulty))
		if items[i].Difficulty != "junior" && items[i].Difficulty != "middle" && items[i].Difficulty != "senior" {
			items[i].Difficulty = "middle"
		}
	}

	return items, nil
}

func callCrassus(ctx context.Context, client *http.Client, apiKey, model, prompt string) (string, error) {
	if model == "" {
		model = "deepseek-v4.1-flash"
	}
	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: 4096,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}
	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://crassusmarket.xyz/v1/messages", bytes.NewReader(jsonBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("User-Agent", "interview-bot-generator/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Crassus status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var anthropicResp anthropicResponse
	if err := json.Unmarshal(bodyBytes, &anthropicResp); err != nil {
		return "", err
	}
	if len(anthropicResp.Content) == 0 {
		return "", fmt.Errorf("пустой ответ от Crassus")
	}

	return anthropicResp.Content[0].Text, nil
}

func callGroq(ctx context.Context, client *http.Client, apiKey, model, prompt string) (string, error) {
	reqBody := chatRequest{
		Model: model,
		Messages: []chatMessage{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
	}
	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(jsonBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Groq status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var groqResp chatResponse
	if err := json.Unmarshal(bodyBytes, &groqResp); err != nil {
		return "", err
	}
	if len(groqResp.Choices) == 0 {
		return "", fmt.Errorf("пустой ответ от Groq")
	}

	return groqResp.Choices[0].Message.Content, nil
}

func extractJSONArray(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start != -1 && end != -1 && end > start {
		return s[start : end+1]
	}
	return s
}

func loadTopics(path string) ([]Topic, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var topics []Topic
	if err := json.Unmarshal(data, &topics); err != nil {
		return nil, err
	}
	return topics, nil
}

func loadExistingAnswers(path string) ([]QuestionItem, map[string]bool) {
	topicsMap := make(map[string]bool)
	data, err := os.ReadFile(path)
	if err != nil {
		return []QuestionItem{}, topicsMap
	}
	var items []QuestionItem
	if err := json.Unmarshal(data, &items); err != nil {
		return []QuestionItem{}, topicsMap
	}
	for _, it := range items {
		if it.TopicID != "" {
			topicsMap[it.TopicID] = true
		}
	}
	return items, topicsMap
}

func saveAnswers(path string, items []QuestionItem) error {
	data, err := json.MarshalIndent(items, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
