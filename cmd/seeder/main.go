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

	"github.com/jackc/pgx/v5/pgxpool"

	"interview-bot/internal/config"
)

type Topic struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type QuestionInput struct {
	TopicID    string `json:"topic_id"`
	Difficulty string `json:"difficulty"`
	Question   string `json:"question"`
}

type AIEnrichResult struct {
	Answer    string   `json:"answer"`
	KeyPoints []string `json:"key_points"`
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
	topicsFile := flag.String("topics", "deepseek_json_20261029_a02396.json", "Путь к файлу каталога тем")
	answersFile := flag.String("answers", "answers.json", "Путь к файлу вопросов")
	targetTopic := flag.String("topic", "", "Фильтр по конкретной теме (если пусто — все темы)")
	concurrency := flag.Int("concurrency", 5, "Количество параллельных горутин (по 1 воркеру на каждый API-ключ)")
	limitQuestions := flag.Int("limit", 0, "Ограничение на количество вопросов для генерации (0 — без ограничений)")
	delayMs := flag.Int("delay", 0, "Задержка в миллисекундах между запросами воркера")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки конфигурации: %v", err)
	}

	ctx := context.Background()

	// 1. Подключение к PostgreSQL
	log.Printf("🔌 Подключение к базе данных PostgreSQL...")
	dbPool, err := pgxpool.New(ctx, cfg.Postgres.URL)
	if err != nil {
		log.Fatalf("❌ Ошибка подключения к БД: %v", err)
	}
	defer dbPool.Close()

	if err := dbPool.Ping(ctx); err != nil {
		log.Fatalf("❌ База данных не отвечает: %v", err)
	}
	log.Printf("✅ Успешное подключение к PostgreSQL")

	// 2. Синхронизация всех 85 тем из deepseek_json в таблицу topics
	topics, err := loadTopics(*topicsFile)
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки тем из %s: %v", *topicsFile, err)
	}
	syncTopicsToDB(ctx, dbPool, topics)

	// 3. Загружаем уже существующие вопросы из базы, чтобы не дублировать
	existingInDB := loadExistingQuestionsFromDB(ctx, dbPool)
	log.Printf("📊 В базе данных уже сохранено %d вопросов", len(existingInDB))

	// 4. Читаем вопросы из answers.json
	allQuestions, err := loadQuestionsInput(*answersFile)
	if err != nil {
		log.Fatalf("❌ Ошибка чтения %s: %v", *answersFile, err)
	}
	log.Printf("📂 В файле %s найдено %d вопросов", *answersFile, len(allQuestions))

	// 5. Фильтруем вопросы, которых еще нет в БД
	var pending []QuestionInput
	for _, q := range allQuestions {
		if *targetTopic != "" && q.TopicID != *targetTopic {
			continue
		}
		cleanText := strings.TrimSpace(q.Question)
		if cleanText == "" {
			continue
		}
		if existingInDB[cleanText] {
			continue
		}
		pending = append(pending, q)
	}

	if *limitQuestions > 0 && len(pending) > *limitQuestions {
		pending = pending[:*limitQuestions]
	}

	totalPending := len(pending)
	if totalPending == 0 {
		log.Println("✨ Все вопросы уже есть в базе данных! Нечего генерировать.")
		return
	}

	workerCount := *concurrency
	if workerCount > totalPending {
		workerCount = totalPending
	}
	if workerCount < 1 {
		workerCount = 1
	}

	log.Printf("⚡ Запускаем обогащение и сохранение: %d горутин на %d новых вопросов...", workerCount, totalPending)

	directClient := &http.Client{Timeout: 90 * time.Second}
	proxyClient := &http.Client{Timeout: 90 * time.Second}
	if cfg.AI.ProxyURL != "" {
		if pURL, err := url.Parse(cfg.AI.ProxyURL); err == nil {
			proxyClient.Transport = &http.Transport{Proxy: http.ProxyURL(pURL)}
		}
	}

	jobs := make(chan QuestionInput, totalPending)
	for _, q := range pending {
		jobs <- q
	}
	close(jobs)

	var wg sync.WaitGroup
	var completedCount int64
	startTime := time.Now()

	for w := 1; w <= workerCount; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for q := range jobs {
				var res *AIEnrichResult
				var enrichErr error

				for attempt := 1; attempt <= 3; attempt++ {
					res, enrichErr = enrichQuestion(directClient, proxyClient, cfg.AI, q, workerID)
					if enrichErr == nil && res != nil && res.Answer != "" {
						break
					}
					log.Printf("[Воркер #%d] ⚠️ Попытка %d для вопроса [%s] не удалась (%v), повтор через 3с...", workerID, attempt, q.TopicID, enrichErr)
					time.Sleep(3 * time.Second)
				}

				if enrichErr != nil || res == nil || res.Answer == "" {
					log.Printf("[Воркер #%d] ❌ Ошибка получения ответа для: %s", workerID, q.Question)
					continue
				}

				// Сохраняем в PostgreSQL
				insertErr := insertQuestionToDB(ctx, dbPool, q, res)
				if insertErr != nil {
					log.Printf("[Воркер #%d] ❌ Ошибка записи в БД: %v", workerID, insertErr)
					continue
				}

				done := atomic.AddInt64(&completedCount, 1)
				shortQ := q.Question
				if len([]rune(shortQ)) > 55 {
					shortQ = string([]rune(shortQ)[:55]) + "..."
				}

				log.Printf("[Воркер #%d] ✅ [%d/%d] [%s] [%s] \"%s\" -> Записан в БД (%d триггеров)",
					workerID, done, totalPending, q.TopicID, q.Difficulty, shortQ, len(res.KeyPoints))

				if *delayMs > 0 {
					time.Sleep(time.Duration(*delayMs) * time.Millisecond)
				}
			}
		}(w)
	}

	wg.Wait()

	duration := time.Since(startTime).Round(time.Second)
	log.Printf("\n🎉 Все воркеры завершили работу за %v! Записано в базу: %d вопросов.\n", duration, completedCount)
}

func syncTopicsToDB(ctx context.Context, pool *pgxpool.Pool, topics []Topic) {
	query := `
		INSERT INTO topics (id, title, description)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET title = EXCLUDED.title, description = EXCLUDED.description;
	`
	synced := 0
	for _, t := range topics {
		if strings.TrimSpace(t.ID) == "" {
			continue
		}
		_, err := pool.Exec(ctx, query, t.ID, t.Title, t.Description)
		if err != nil {
			log.Printf("⚠️ Ошибка синхронизации темы %s: %v", t.ID, err)
		} else {
			synced++
		}
	}
	log.Printf("📚 Синхронизировано %d тем в таблице topics", synced)
}

func loadExistingQuestionsFromDB(ctx context.Context, pool *pgxpool.Pool) map[string]bool {
	existing := make(map[string]bool)
	rows, err := pool.Query(ctx, "SELECT text FROM questions")
	if err != nil {
		log.Printf("⚠️ Не удалось прочитать вопросы из БД: %v", err)
		return existing
	}
	defer rows.Close()

	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err == nil {
			existing[strings.TrimSpace(text)] = true
		}
	}
	return existing
}

func insertQuestionToDB(ctx context.Context, pool *pgxpool.Pool, q QuestionInput, res *AIEnrichResult) error {
	query := `
		INSERT INTO questions (topic_id, difficulty, text, answer, key_points)
		VALUES ($1, $2, $3, $4, $5);
	`
	keyPoints := res.KeyPoints
	if keyPoints == nil {
		keyPoints = []string{}
	}
	_, err := pool.Exec(ctx, query, q.TopicID, q.Difficulty, q.Question, res.Answer, keyPoints)
	return err
}
func enrichQuestion(directClient, proxyClient *http.Client, aiCfg config.AIConfig, q QuestionInput, workerID int) (*AIEnrichResult, error) {
	var levelGuide string
	switch strings.ToLower(q.Difficulty) {
	case "intern":
		levelGuide = "Уровень: СТАЖЕР (intern). Ответ должен быть простым, понятным, объясняющим суть концепции понятными словами с компактным примером кода. Без углубления во внутренности рантайма."
	case "junior":
		levelGuide = "Уровень: JUNIOR. Ответ должен содержать точное знание синтаксиса Go, стандартной библиотеки, правил языка и базовых подводных камней с понятным примером кода."
	case "middle":
		levelGuide = "Уровень: MIDDLE. Ответ должен быть глубоким и уверенным: внутреннее устройство под капотом, структуры данных, модель памяти (стек/куча), поведение рантайма и частые ошибки в продакшене."
	case "senior":
		levelGuide = "Уровень: SENIOR. Экспертный ответ: глубокое знание компилятора и рантайма Go (GC, планировщик GMP, memory layout), trade-offs, влияние на производительность, архитектурные практики."
	default:
		levelGuide = fmt.Sprintf("Уровень сложности: %s.", q.Difficulty)
	}

	prompt := fmt.Sprintf(`Ты — опытный Go-разработчик и ведущий технических интервью.
Для следующего вопроса по Go составь эталонный ответ для собеседования и список ключевых триггеров для проверки кандидата.

Тема: %s
Целевой уровень: %s
%s

Вопрос: %s

КРИТИЧЕСКИ ВАЖНЫЕ ТРЕБОВАНИЯ:
1. "answer": Сформируй ЕДИНЫЙ, цельный и логичный ответ кандидата на собеседовании.
   СТРОГО ЗАПРЕЩЕНО разбивать ответ на блоки разных грейдов (категорически НЕ пиши заголовки вроде "### Intern level", "### Junior level", "### Middle level"!).
   Весь ответ целиком должен быть составлен ИСКЛЮЧИТЕЛЬНО для уровня %s.
   Ответ должен содержать:
   - Четкое и емкое объяснение сути своими словами.
   - Детали работы и технические нюансы, ожидаемые именно от уровня %s.
   - Компактный, аккуратный пример кода на Go (если это уместно).
   - Подводные камни или тонкости, о которых важно помнить.
2. "key_points": Массив из 4-7 ключевых тезисов, фактов или терминов (триггеров), которые интервьюер ожидает услышать в ответе кандидата для подтверждения уровня %s.

Верни ответ СТРОГО в формате JSON без какого-либо дополнительного текста:
{
  "answer": "...",
  "key_points": ["...", "..."]
}`, q.TopicID, q.Difficulty, levelGuide, q.Question, q.Difficulty, q.Difficulty, q.Difficulty)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var rawText string
	var lastErr error

	numKeys := len(aiCfg.DeepSeekKeys)
	startIdx := 0
	if numKeys > 0 {
		startIdx = (workerID - 1) % numKeys
	}

	// Используем прокси-клиент в приоритете для обхода блокировок и rate-limit провайдера
	httpClient := directClient
	if aiCfg.ProxyURL != "" && proxyClient != nil {
		httpClient = proxyClient
	}

	for i := 0; i < numKeys; i++ {
		keyIdx := (startIdx + i) % numKeys
		apiKey := aiCfg.DeepSeekKeys[keyIdx]
		rawText, lastErr = callCrassus(ctx, httpClient, apiKey, aiCfg.Model, prompt)
		if lastErr == nil && rawText != "" {
			break
		}
		// Запасная попытка через прямой клиент, если прокси упал
		if httpClient == proxyClient {
			rawText, lastErr = callCrassus(ctx, directClient, apiKey, aiCfg.Model, prompt)
			if lastErr == nil && rawText != "" {
				break
			}
		}
	}

	if rawText == "" {
		return nil, fmt.Errorf("ошибка вызова DeepSeek (Crassus): %w", lastErr)
	}

	cleanJSON := extractJSONObject(rawText)

	var res AIEnrichResult
	if err := json.Unmarshal([]byte(cleanJSON), &res); err != nil {
		return nil, fmt.Errorf("ошибка парсинга JSON: %w\nСырой ответ:\n%s", err, cleanJSON)
	}

	if res.KeyPoints == nil {
		res.KeyPoints = []string{}
	}

	return &res, nil
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
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

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
		Temperature: 0.2,
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

func extractJSONObject(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
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

func loadQuestionsInput(path string) ([]QuestionInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var items []QuestionInput
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}
