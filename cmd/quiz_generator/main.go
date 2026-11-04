package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
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

type QuizOption struct {
	ID   string `json:"id"`   // "A", "B", "C", "D"
	Text string `json:"text"` // Текст варианта ответа
}

type QuizQuestionItem struct {
	TopicID       string       `json:"topic_id"`
	Difficulty    string       `json:"difficulty"`     // "intern", "junior", "middle", "senior"
	Question      string       `json:"question"`       // Текст вопроса + код в бэктиках
	Options       []QuizOption `json:"options"`        // Ровно 4 варианта ответов (A, B, C, D)
	CorrectOption string       `json:"correct_option"` // Буква правильного ответа ("A", "B", "C" или "D")
	Explanation   string       `json:"explanation"`    // Краткое техническое объяснение правильного ответа (1-2 предложения)
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
	outFile := flag.String("out", "quiz_questions.json", "Путь к выходному файлу с вопросами квиза")
	targetTopic := flag.String("topic", "", "ID конкретной темы для генерации (если пусто — все темы)")
	concurrency := flag.Int("concurrency", 4, "Количество параллельных горутин-воркеров")
	limitTopics := flag.Int("limit", 0, "Ограничение на количество тем (0 — без ограничений)")
	delayMs := flag.Int("delay", 0, "Задержка в миллисекундах между запросами одного воркера")
	force := flag.Bool("force", false, "Принудительно генерировать заново, даже если тема уже есть в файле")
	saveDB := flag.Bool("db", true, "Сохранять ли сгенерированные вопросы сразу в PostgreSQL")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки конфигурации: %v", err)
	}

	// Подключение к PostgreSQL
	var dbPool *pgxpool.Pool
	if *saveDB {
		ctxInit, cancelInit := context.WithTimeout(context.Background(), 10*time.Second)
		dbPool, err = pgxpool.New(ctxInit, cfg.Postgres.URL)
		cancelInit()
		if err != nil {
			log.Printf("⚠️ Не удалось подключиться к PostgreSQL: %v (сохранение будет только в файл)", err)
		} else {
			defer dbPool.Close()
			log.Println("🐘 Подключение к PostgreSQL успешно установлено")
		}
	}

	// 1. Загружаем уже существующие вопросы из файла
	existingQuestions, existingTopicsMap := loadExistingQuizQuestions(*outFile)
	log.Printf("📂 В файле %s уже находится %d вопросов по %d темам", *outFile, len(existingQuestions), len(existingTopicsMap))

	// Синхронизируем уже загруженные темы из PostgreSQL
	if dbPool != nil {
		ctxCheck, cancelCheck := context.WithTimeout(context.Background(), 5*time.Second)
		if rows, err := dbPool.Query(ctxCheck, "SELECT DISTINCT topic_id FROM quiz_questions"); err == nil {
			pgCount := 0
			for rows.Next() {
				var tid string
				if err := rows.Scan(&tid); err == nil && tid != "" {
					existingTopicsMap[tid] = true
					pgCount++
				}
			}
			rows.Close()
			log.Printf("🐘 В PostgreSQL найдено %d уже сохранённых тем", pgCount)
		}
		cancelCheck()
	}

	// 2. Получаем список тем из PostgreSQL (или из локального json каталога)
	allTopics, err := fetchTopics(cfg.Postgres.URL)
	if err != nil {
		log.Printf("⚠️ Не удалось получить темы из БД (%v), пробуем локальный файл...", err)
		allTopics, err = loadTopicsFromDisk("deepseek_json_20261029_a02396.json")
		if err != nil {
			log.Fatalf("❌ Ошибка чтения каталога тем: %v", err)
		}
	}
	log.Printf("📚 Загружено %d тем из каталога", len(allTopics))

	// 3. Формируем список тем для обработки
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

	log.Printf("⚡ Запуск генератора: %d воркеров на %d тем (по 20 вопросов с ответом и объяснением: 5 intern, 8 junior, 4 middle, 3 senior)...",
		workerCount, totalPending)

	// Настройка HTTP клиентов с чистыми сокетами (без залипающих сессий)
	directClient := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   8 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   8 * time.Second,
			ResponseHeaderTimeout: 45 * time.Second,
			DisableKeepAlives:     true, // Не переиспользуем TCP-сокеты, чтобы избежать wsarecv-разрывов
		},
	}
	proxyClient := &http.Client{Timeout: 60 * time.Second}
	if cfg.AI.ProxyURL != "" {
		if pURL, err := url.Parse(cfg.AI.ProxyURL); err == nil {
			proxyClient.Transport = &http.Transport{Proxy: http.ProxyURL(pURL)}
		}
	}

	// Предварительная проверка ключей DeepSeek: оставляем только живые
	log.Println("🔍 Экспресс-проверка доступности ключей DeepSeek...")
	var activeDeepSeekKeys []string
	for i, k := range cfg.AI.DeepSeekKeys {
		testCtx, testCancel := context.WithTimeout(context.Background(), 7*time.Second)
		_, testErr := callCrassus(testCtx, directClient, k, cfg.AI.DeepSeekURL, cfg.AI.Model, "Ответь одним словом: ok")
		testCancel()
		if testErr == nil {
			activeDeepSeekKeys = append(activeDeepSeekKeys, k)
			log.Printf("🔑 DeepSeek ключ #%d: АКТИВЕН", i+1)
		} else {
			log.Printf("⚠️ DeepSeek ключ #%d: недоступен (%v), исключаем из пула", i+1, testErr)
		}
	}
	if len(activeDeepSeekKeys) > 0 {
		cfg.AI.DeepSeekKeys = activeDeepSeekKeys
		log.Printf("✨ Активно рабочих ключей DeepSeek: %d из %d", len(activeDeepSeekKeys), len(activeDeepSeekKeys))
	} else {
		log.Println("⚠️ Ни один ключ DeepSeek не ответил за 7с, оставляем исходный список на случай задержек сети")
	}

	tasksChan := make(chan Topic, totalPending)
	for _, t := range pendingTopics {
		tasksChan <- t
	}
	close(tasksChan)

	var (
		wg               sync.WaitGroup
		fileMutex        sync.Mutex
		completedCount   int64
		totalAddedCount  int64
		keyRotationIndex uint64
		startTime        = time.Now()
	)

	for w := 1; w <= workerCount; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for t := range tasksChan {
				questions, err := generateQuizQuestionsForTopic(directClient, proxyClient, cfg.AI, t, &keyRotationIndex)
				if err != nil {
					log.Printf("[Воркер #%d] ❌ Ошибка генерации темы '%s' (%s): %v", workerID, t.Title, t.ID, err)
					continue
				}

				if len(questions) == 0 {
					log.Printf("[Воркер #%d] ⚠️ Пустой результат для темы '%s'", workerID, t.Title)
					continue
				}

				// Подсчитываем распределение по грейдам
				intCount, junCount, midCount, senCount := 0, 0, 0, 0
				for _, q := range questions {
					switch q.Difficulty {
					case "intern":
						intCount++
					case "junior":
						junCount++
					case "middle":
						midCount++
					case "senior":
						senCount++
					}
				}

				// Сохраняем в PostgreSQL
				if dbPool != nil {
					ctxDB, cancelDB := context.WithTimeout(context.Background(), 10*time.Second)
					if err := saveTopicToDB(ctxDB, dbPool, t.ID, questions); err != nil {
						log.Printf("[Воркер #%d] ⚠️ Ошибка сохранения в PostgreSQL темы '%s': %v", workerID, t.ID, err)
					}
					cancelDB()
				}

				// Атомарно сохраняем в файл прогресса
				fileMutex.Lock()
				existingQuestions = append(existingQuestions, questions...)
				currentTotal := len(existingQuestions)
				if err := saveQuizQuestions(*outFile, existingQuestions); err != nil {
					log.Printf("[Воркер #%d] ❌ Ошибка сохранения %s: %v", workerID, *outFile, err)
				}
				fileMutex.Unlock()

				done := atomic.AddInt64(&completedCount, 1)
				atomic.AddInt64(&totalAddedCount, int64(len(questions)))

				log.Printf("[Воркер #%d] ✅ [%d/%d] Готово '%s' (%s): +%d вопросов (%d Int, %d Jun, %d Mid, %d Sen). Всего: %d!",
					workerID, done, totalPending, t.Title, t.ID, len(questions), intCount, junCount, midCount, senCount, currentTotal)

				if *delayMs > 0 {
					time.Sleep(time.Duration(*delayMs) * time.Millisecond)
				}
			}
		}(w)
	}

	wg.Wait()

	duration := time.Since(startTime).Round(time.Second)
	log.Printf("\n🎉 Все воркеры завершили работу за %v!", duration)
	log.Printf("📊 Сгенерировано тем: %d из %d. Добавлено вопросов: %d. Итого в %s: %d вопросов.\n",
		completedCount, totalPending, totalAddedCount, *outFile, len(existingQuestions))
}

func generateQuizQuestionsForTopic(
	directClient, proxyClient *http.Client,
	aiCfg config.AIConfig,
	t Topic,
	keyRotator *uint64,
) ([]QuizQuestionItem, error) {

	// Пачка 1: Intern (5) + Junior (8) = 13 вопросов
	prompt1 := fmt.Sprintf(`Ты — Senior Go-архитектор и составитель тестов.
Тема: "%s" (ID: "%s", Описание: "%s").
Составь РОВНО 13 вопросов с 4 вариантами ответов (A, B, C, D), правильным ответом и техническим объяснением:
- "intern": РОВНО 5 вопросов (базовый синтаксис Go, типы, дефолтные zero values, простые циклы, ключевые слова).
- "junior": РОВНО 8 вопросов (ловушки в коде: слайсы, len/cap, append, мапы, defer, указатели vs значения, базовые интерфейсы, каналы).

ПРАВИЛА:
- В большинстве вопросов приводи короткий фрагмент Go-кода (3-7 строк) в `+"```go ... ```"+`.
- К каждому вопросу придумай РОВНО 4 РЕАЛИСТИЧНЫХ варианта ответа ("A", "B", "C", "D").
- "correct_option": строго буква правильного ответа ("A", "B", "C" или "D"). Только ОДИН правильный ответ.
- "explanation": 1-2 предложения с объяснением, почему этот вариант правильный и в чём подвох.
- ВАЖНО: Все переносы строк внутри кода экранируй строго как \n, кавычки в коде экранируй как \" или используй одинарные.

ФОРМАТ ВЫВОДА:
Выведи СТРОГО чистый JSON-массив из 13 элементов:
[
  {
    "topic_id": "%s",
    "difficulty": "intern",
    "question": "Какое значение по умолчанию (zero value) имеет переменная типа error в Go?",
    "options": [
      {"id": "A", "text": "пустая строка \"\""},
      {"id": "B", "text": "nil"},
      {"id": "C", "text": "0"},
      {"id": "D", "text": "false"}
    ],
    "correct_option": "B",
    "explanation": "Тип error в Go является интерфейсом, его zero value — nil."
  }
]`, t.Title, t.ID, t.Description, t.ID)

	// Пачка 2: Middle (4) + Senior (3) = 7 вопросов
	prompt2 := fmt.Sprintf(`Ты — Senior Go-архитектор и составитель тестов.
Тема: "%s" (ID: "%s", Описание: "%s").
Составь РОВНО 7 вопросов с 4 вариантами ответов (A, B, C, D), правильным ответом и техническим объяснением:
- "middle": РОВНО 4 вопроса (замыкания в циклах, data race, sync.Mutex, sync.WaitGroup, context, поведение закрытых каналов, typed nil).
- "senior": РОВНО 3 вопроса (устройство рантайма GMP, mcache, аллокации стек vs куча, escape analysis, atomic, lock-free, memory layout, unsafe).

ПРАВИЛА:
- В большинстве вопросов приводи короткий фрагмент Go-кода (3-7 строк) в `+"```go ... ```"+`.
- К каждому вопросу придумай РОВНО 4 РЕАЛИСТИЧНЫХ варианта ответа ("A", "B", "C", "D").
- "correct_option": строго буква правильного ответа ("A", "B", "C" или "D"). Только ОДИН правильный ответ.
- "explanation": 1-2 предложения с объяснением, почему этот вариант правильный и в чём подвох.
- ВАЖНО: Все переносы строк внутри кода экранируй строго как \n, кавычки в коде экранируй как \" или используй одинарные.

ФОРМАТ ВЫВОДА:
Выведи СТРОГО чистый JSON-массив из 7 элементов:
[
  {
    "topic_id": "%s",
    "difficulty": "middle",
    "question": "Что выведет данный код?\n\n`+"```go\ns := make([]int, 5, 10)\nfmt.Println(s[0:8])\n```"+`",
    "options": [
      {"id": "A", "text": "[0 0 0 0 0 0 0 0]"},
      {"id": "B", "text": "[0 0 0 0 0]"},
      {"id": "C", "text": "panic: slice bounds out of range"},
      {"id": "D", "text": "ошибка компиляции"}
    ],
    "correct_option": "A",
    "explanation": "Срез можно повторно срезать в пределах его capacity, даже если индекс превышает текущий len."
  }
]`, t.Title, t.ID, t.Description, t.ID)

	items1, err := requestBatch(directClient, proxyClient, aiCfg, prompt1, keyRotator)
	if err != nil {
		return nil, fmt.Errorf("ошибка генерации intern/junior: %w", err)
	}

	items2, err := requestBatch(directClient, proxyClient, aiCfg, prompt2, keyRotator)
	if err != nil {
		return nil, fmt.Errorf("ошибка генерации middle/senior: %w", err)
	}

	result := append(items1, items2...)
	for i := range result {
		result[i].TopicID = t.ID
		result[i].Difficulty = strings.ToLower(strings.TrimSpace(result[i].Difficulty))
		result[i].CorrectOption = strings.ToUpper(strings.TrimSpace(result[i].CorrectOption))
		result[i].Explanation = strings.TrimSpace(result[i].Explanation)
	}

	return result, nil
}

func requestBatch(
	directClient, proxyClient *http.Client,
	aiCfg config.AIConfig,
	prompt string,
	keyRotator *uint64,
) ([]QuizQuestionItem, error) {

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var rawText string
	var err error

	// 1. Crassus DeepSeek с ротацией ключей (работает напрямую без прокси)
	if len(aiCfg.DeepSeekKeys) > 0 {
		startIdx := atomic.AddUint64(keyRotator, 1)
		attempts := len(aiCfg.DeepSeekKeys)
		if attempts > 2 {
			attempts = 2 // Пробуем максимум 2 ключа, чтобы не съедать весь таймаут при сетевых сбоях
		}
		for i := 0; i < attempts; i++ {
			if ctx.Err() != nil {
				break
			}
			idx := (int(startIdx) + i) % len(aiCfg.DeepSeekKeys)
			apiKey := aiCfg.DeepSeekKeys[idx]

			attemptCtx, attemptCancel := context.WithTimeout(ctx, 35*time.Second)
			rawText, err = callCrassus(attemptCtx, directClient, apiKey, aiCfg.DeepSeekURL, aiCfg.Model, prompt)
			attemptCancel()

			if err == nil && rawText != "" {
				break
			}
			log.Printf("[Crassus] ключ #%d вернул ошибку: %v", idx+1, err)
			time.Sleep(1 * time.Second)
		}
	}

	// 2. Fallback на Groq
	if rawText == "" && len(aiCfg.GroqKeys) > 0 {
		log.Println("⚡ Переключаемся на резервный Groq...")
		for i, apiKey := range aiCfg.GroqKeys {
			attemptCtx, attemptCancel := context.WithTimeout(context.Background(), 45*time.Second)
			rawText, err = callGroq(attemptCtx, proxyClient, apiKey, "llama-3.3-70b-versatile", prompt)
			if err != nil {
				rawText, err = callGroq(attemptCtx, directClient, apiKey, "llama-3.3-70b-versatile", prompt)
			}
			attemptCancel()

			if err == nil && rawText != "" {
				log.Printf("⚡ [Groq Fallback] Успешно сгенерировано через Groq (ключ #%d)", i+1)
				break
			}
			log.Printf("[Groq] ключ #%d вернул ошибку: %v", i+1, err)
		}
	}

	if rawText == "" {
		return nil, fmt.Errorf("все AI провайдеры вернули ошибку: %w", err)
	}

	cleanJSON := cleanAndExtractJSONArray(rawText)

	var rawItems []rawQuizQuestionItem
	if err := json.Unmarshal([]byte(cleanJSON), &rawItems); err != nil {
		return nil, fmt.Errorf("ошибка парсинга JSON: %w\nСырой ответ (первые 500 байт):\n%s", err, truncatePreview(cleanJSON, 500))
	}

	var items []QuizQuestionItem
	for _, raw := range rawItems {
		opts, err := parseOptions(raw.RawOptions)
		if err != nil || len(opts) < 2 {
			continue
		}
		topID := raw.TopicID
		if topID == "" {
			topID = raw.Topic
		}
		items = append(items, QuizQuestionItem{
			TopicID:       topID,
			Difficulty:    raw.Difficulty,
			Question:      raw.Question,
			Options:       opts,
			CorrectOption: raw.CorrectOption,
			Explanation:   raw.Explanation,
		})
	}

	return items, nil
}

type rawQuizQuestionItem struct {
	TopicID       string          `json:"topic_id"`
	Topic         string          `json:"topic"`
	Difficulty    string          `json:"difficulty"`
	Question      string          `json:"question"`
	RawOptions    json.RawMessage `json:"options"`
	CorrectOption string          `json:"correct_option"`
	Explanation   string          `json:"explanation"`
}

func parseOptions(raw json.RawMessage) ([]QuizOption, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("пустые options")
	}

	// 1. Формат массива: [{"id": "A", "text": "..."}, ...]
	if trimmed[0] == '[' {
		var list []QuizOption
		if err := json.Unmarshal(trimmed, &list); err == nil && len(list) > 0 {
			return list, nil
		}
	}

	// 2. Формат объекта: {"A": "...", "B": "...", "C": "...", "D": "..."}
	if trimmed[0] == '{' {
		var m map[string]string
		if err := json.Unmarshal(trimmed, &m); err == nil && len(m) > 0 {
			var list []QuizOption
			for _, id := range []string{"A", "B", "C", "D"} {
				if txt, ok := m[id]; ok {
					list = append(list, QuizOption{ID: id, Text: txt})
				}
			}
			if len(list) >= 2 {
				return list, nil
			}
		}
	}

	return nil, fmt.Errorf("неизвестный формат options: %s", string(raw))
}

func callCrassus(ctx context.Context, client *http.Client, apiKey, baseURL, model, prompt string) (string, error) {
	if model == "" {
		model = "deepseek-v4.1-flash"
	}
	if baseURL == "" {
		baseURL = "https://crassusmarket.xyz"
	}
	apiURL := strings.TrimRight(baseURL, "/") + "/v1/messages"

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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(jsonBytes))
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

func cleanAndExtractJSONArray(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start != -1 && end != -1 && end > start {
		s = s[start : end+1]
	}

	// Санитизируем неэкранированные переносы строк и управляющие символы внутри JSON-строк
	var buf bytes.Buffer
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			buf.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			buf.WriteByte(c)
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			buf.WriteByte(c)
			continue
		}
		if inString {
			if c == '\n' {
				buf.WriteString(`\n`)
				continue
			}
			if c == '\r' {
				continue
			}
			if c == '\t' {
				buf.WriteString(`\t`)
				continue
			}
		}
		buf.WriteByte(c)
	}
	return buf.String()
}

func truncatePreview(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func fetchTopics(pgURL string) ([]Topic, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		return nil, err
	}
	defer pool.Close()

	rows, err := pool.Query(ctx, "SELECT id, title, description FROM topics ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var topics []Topic
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.ID, &t.Title, &t.Description); err != nil {
			return nil, err
		}
		topics = append(topics, t)
	}
	return topics, nil
}

func loadTopicsFromDisk(path string) ([]Topic, error) {
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

func loadExistingQuizQuestions(path string) ([]QuizQuestionItem, map[string]bool) {
	topicsMap := make(map[string]bool)
	data, err := os.ReadFile(path)
	if err != nil {
		return []QuizQuestionItem{}, topicsMap
	}
	var items []QuizQuestionItem
	if err := json.Unmarshal(data, &items); err != nil {
		return []QuizQuestionItem{}, topicsMap
	}
	for _, it := range items {
		if it.TopicID != "" {
			topicsMap[it.TopicID] = true
		}
	}
	return items, topicsMap
}

func saveQuizQuestions(path string, items []QuizQuestionItem) error {
	data, err := json.MarshalIndent(items, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func saveTopicToDB(ctx context.Context, pool *pgxpool.Pool, topicID string, questions []QuizQuestionItem) error {
	// Очищаем старые вопросы по теме перед вставкой
	_, _ = pool.Exec(ctx, "DELETE FROM quiz_questions WHERE topic_id = $1", topicID)

	for _, q := range questions {
		optJSON, err := json.Marshal(q.Options)
		if err != nil {
			continue
		}
		_, err = pool.Exec(ctx, `
			INSERT INTO quiz_questions (topic_id, difficulty, question, options, correct_option, explanation)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, q.TopicID, q.Difficulty, q.Question, optJSON, q.CorrectOption, q.Explanation)
		if err != nil {
			return fmt.Errorf("ошибка вставки quiz вопроса: %w", err)
		}
	}
	return nil
}
