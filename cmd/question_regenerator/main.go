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
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"interview-bot/internal/config"
)

type AuditedQuestion struct {
	ID                int      `json:"id"`
	TopicID           string   `json:"topic_id"`
	TopicTitle        string   `json:"topic_title"`
	Difficulty        string   `json:"difficulty"`
	OriginalQuestion  string   `json:"original_question"`
	OriginalAnswer    string   `json:"original_answer"`
	OriginalKeyPoints []string `json:"original_key_points"`
	IsSuspicious      bool     `json:"is_suspicious"`
	IssueTypes        []string `json:"issue_types,omitempty"`
	Critique          string   `json:"critique,omitempty"`
	SuggestedQuestion string   `json:"suggested_question,omitempty"`
	AnswerFixNote     string   `json:"answer_fix_note,omitempty"`
}

type RegeneratedQuestion struct {
	ID                int      `json:"id"`
	TopicID           string   `json:"topic_id"`
	TopicTitle        string   `json:"topic_title"`
	Difficulty        string   `json:"difficulty"`
	OriginalQuestion  string   `json:"original_question"`
	OriginalAnswer    string   `json:"original_answer"`
	OriginalKeyPoints []string `json:"original_key_points"`
	IssueTypes        []string `json:"issue_types,omitempty"`
	Critique          string   `json:"critique,omitempty"`
	NewQuestion       string   `json:"new_question"`
	NewAnswer         string   `json:"new_answer"`
	NewKeyPoints      []string `json:"new_key_points"`
	UpdatedAt         string   `json:"updated_at"`
}

type deepSeekResponse struct {
	Question  string   `json:"question"`
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

func main() {
	inputPath := flag.String("input", "audit_report.json", "Путь к исходному отчёту аудита")
	outputPath := flag.String("output", "regenerated_questions.json", "Путь к файлу с исправленными вопросами")
	mdOutputPath := flag.String("md", "REGENERATED_QUESTIONS.md", "Путь к отчёту в формате Markdown")
	targetID := flag.Int("id", 0, "Сгенерировать только конкретный ID вопроса (0 = все)")
	limitFlag := flag.Int("limit", 0, "Ограничить количество обрабатываемых вопросов (0 = без ограничений)")
	workersFlag := flag.Int("workers", 3, "Количество параллельных воркеров (рекомендуется 3-4)")
	resumeFlag := flag.Bool("resume", true, "Пропускать уже обработанные ID при повторном запуске")
	flag.Parse()

	log.Println("🔄 Инициализация генератора исправленных вопросов Go Mentor...")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки конфигурации: %v", err)
	}

	if len(cfg.AI.DeepSeekKeys) == 0 {
		log.Fatalf("❌ В .env не найдены DEEPSEEK_API_KEYS")
	}

	// Читаем audit_report.json
	data, err := os.ReadFile(*inputPath)
	if err != nil {
		log.Fatalf("❌ Не удалось прочитать %s: %v", *inputPath, err)
	}

	var audited []AuditedQuestion
	if err := json.Unmarshal(data, &audited); err != nil {
		log.Fatalf("❌ Ошибка парсинга %s: %v", *inputPath, err)
	}
	log.Printf("📋 Загружено %d вопросов из аудита", len(audited))

	// Загружаем уже сделанное (resume)
	var existingResults []RegeneratedQuestion
	processedMap := make(map[int]bool)
	if *resumeFlag {
		if exData, err := os.ReadFile(*outputPath); err == nil {
			if err := json.Unmarshal(exData, &existingResults); err == nil {
				for _, r := range existingResults {
					processedMap[r.ID] = true
				}
				log.Printf("⏩ Найдено %d уже обработанных вопросов в %s", len(processedMap), *outputPath)
			}
		}
	}

	// Отбираем вопросы для обработки
	var toProcess []AuditedQuestion
	for _, q := range audited {
		if *targetID > 0 && q.ID != *targetID {
			continue
		}
		if *resumeFlag && processedMap[q.ID] {
			continue
		}
		toProcess = append(toProcess, q)
		if *limitFlag > 0 && len(toProcess) >= *limitFlag {
			break
		}
	}

	log.Printf("🎯 К регенерации готово: %d вопросов (воркеров: %d)", len(toProcess), *workersFlag)
	if len(toProcess) == 0 {
		log.Println("🎉 Все вопросы уже обработаны! Ничего делать не требуется.")
		return
	}

	// HTTP-клиенты для CrassusMarket (DeepSeek)
	directClient := &http.Client{
		Timeout: 90 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 80 * time.Second,
			DisableKeepAlives:     true,
		},
	}

	var proxyClient *http.Client
	if cfg.AI.ProxyURL != "" {
		if pURL, err := url.Parse(cfg.AI.ProxyURL); err == nil {
			proxyClient = &http.Client{
				Timeout: 90 * time.Second,
				Transport: &http.Transport{
					Proxy: http.ProxyURL(pURL),
					DialContext: (&net.Dialer{
						Timeout:   10 * time.Second,
						KeepAlive: 30 * time.Second,
					}).DialContext,
					TLSHandshakeTimeout:   10 * time.Second,
					ResponseHeaderTimeout: 80 * time.Second,
					DisableKeepAlives:     true,
				},
			}
		}
	}

	tasksChan := make(chan AuditedQuestion, len(toProcess))
	for _, q := range toProcess {
		tasksChan <- q
	}
	close(tasksChan)

	var (
		resMu        sync.Mutex
		resultsMap   = make(map[int]RegeneratedQuestion)
		successCount atomic.Int32
		errorCount   atomic.Int32
		totalDone    atomic.Int32
		keyCounter   atomic.Uint64
		saveTicker   = time.NewTicker(5 * time.Second)
		wg           sync.WaitGroup
	)
	for _, r := range existingResults {
		resultsMap[r.ID] = r
	}

	saveProgress := func() {
		resMu.Lock()
		defer resMu.Unlock()

		// Сохраняем в JSON
		var list []RegeneratedQuestion
		for _, q := range audited {
			if r, ok := resultsMap[q.ID]; ok {
				list = append(list, r)
			}
		}
		if bytes, err := json.MarshalIndent(list, "", "  "); err == nil {
			_ = os.WriteFile(*outputPath, bytes, 0644)
		}

		// Сохраняем в Markdown
		saveMarkdownReport(*mdOutputPath, list)
	}

	// Фоновое сохранение каждые 5 секунд
	stopSave := make(chan struct{})
	doneSave := make(chan struct{})
	go func() {
		defer close(doneSave)
		for {
			select {
			case <-saveTicker.C:
				saveProgress()
			case <-stopSave:
				saveProgress()
				return
			}
		}
	}()

	startTime := time.Now()
	workers := *workersFlag
	if workers < 1 {
		workers = 1
	}

	log.Printf("🚀 Запуск %d воркеров генерации...", workers)

	for w := 1; w <= workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for item := range tasksChan {
				time.Sleep(300 * time.Millisecond) // микропауза для сокетов

				res, err := regenerateSingleQuestion(
					item,
					cfg,
					directClient,
					proxyClient,
					&keyCounter,
				)

				done := totalDone.Add(1)
				if err != nil {
					errorCount.Add(1)
					log.Printf("❌ [Воркер #%d | %d/%d] ID %d: ошибка: %v",
						workerID, done, len(toProcess), item.ID, err)
					continue
				}

				successCount.Add(1)
				log.Printf("✅ [Воркер #%d | %d/%d] ID %d (%s) исправлен!\n   Вопрос: %s",
					workerID, done, len(toProcess), item.ID, item.TopicID, res.NewQuestion)

				resMu.Lock()
				resultsMap[res.ID] = *res
				resMu.Unlock()
			}
		}(w)
	}

	wg.Wait()
	saveTicker.Stop()
	close(stopSave)
	<-doneSave

	elapsed := time.Since(startTime).Round(time.Second)
	log.Printf("\n🏁 Регенерация завершена за %s!", elapsed)
	log.Printf("   ✅ Успешно обновлено: %d", successCount.Load())
	log.Printf("   ❌ Ошибок: %d", errorCount.Load())
	log.Printf("   📁 Результаты в JSON: %s", *outputPath)
	log.Printf("   📄 Читаемый отчёт в Markdown: %s", *mdOutputPath)
}

func regenerateSingleQuestion(
	item AuditedQuestion,
	cfg *config.Config,
	directClient, proxyClient *http.Client,
	keyCounter *atomic.Uint64,
) (*RegeneratedQuestion, error) {

	systemPrompt := `Ты ведущий архитектор Go и автор профессионального тренажёра для подготовки к техническим собеседованиям.
Твоя задача — ПЕРЕРАБОТАТЬ и СБАЛАНСИРОВАТЬ вопрос, эталонный ответ и критерии оценки (key_points).

ЖЁСТКИЕ ПРАВИЛА:
1. ПРАВИЛО ОДНОГО ВОПРОСА (100% АТОМАРНОСТЬ):
   - Вопрос должен содержать ровно ОДНО вопросительное предложение и проверять ровно ОДНО действие / механизм / концепцию.
   - Ровно ОДИН знак вопроса '?' в самом конце! Никаких промежуточных знаков вопроса.
   - КАТЕГОРИЧЕСКИ ЗАПРЕЩЕНЫ склейки двух или более вопросов через союзы!
     * ЗАПРЕЩЕНО: «..., и как...» (например: «Что такое X и как работает Y?»)
     * ЗАПРЕЩЕНО: «..., и почему...» (например: «Как сделать X и почему нельзя Y?»)
     * ЗАПРЕЩЕНО: «..., а также...», «..., и что произойдёт...», «..., и какой тип...»
   - ЕСЛИ В СТАРОМ ВОПРОСЕ БЫЛО НЕСКОЛЬКО ПОДТЕМ — ОСТАВЬ ТОЛЬКО ОДНУ! Выбери самый глубокий и ключевой аспект для темы и грейда. Всё остальное выкинь без жалости (мы вынесем это в отдельные вопросы).

2. СОГЛАСОВАНИЕ ЧИСЛА И ОЖИДАНИЙ (НЕТ РАССИНХРОНУ):
   - Если эталонный ответ и критерии (key_points) содержат перечисление нескольких пунктов/условий/факторов (например, 2-3 условия), вопрос ОБЯЗАН быть сформулирован во множественном числе:
     * «При каких ключевых условиях вызов recover перехватывает панику в Go?» (а НЕ «При каком условии...»)
     * «Какие факторы учитывает инлайнер Go при решении о встраивании функции?» (а НЕ «Каким критерием...»)
     * «Какие функции пакета strconv преобразуют число в строку в Go?»
   - Категорически запрещено спрашивать в единственном числе, если для полного балла кандидат обязан перечислить 2-4 разных фактора!

3. ЭТАЛОННЫЙ ОТВЕТ (answer):
   - Отвечает СТРОГО и ТОЛЬКО на этот один вопрос. Не уходи в смежные темы.
   - Объясняй суть глубоко, технически точно и без «воды» (2-3 ёмких абзаца).
   - Код и семантика должны быть строго актуальны для Go 1.22+! Если вопрос о поведении циклов до 1.22, явно оговаривай это («В Go до версии 1.22...»).
   - Если уместно — включи компактный, наглядный сниппет кода на Go в блоке ` + "```go ... ```" + `.

4. КРИТЕРИИ ОЦЕНКИ (key_points):
   - Ровно 3–4 чётких факта/тезиса, которые кандидат обязан упомянуть в своём ответе.
   - КРИТИЧЕСКИ ВАЖНО: тезисы должны проверять ТОЛЬКО то, о чём спросили в новом вопросе! Никаких скрытых требований, о которых кандидат не мог догадаться из вопроса.

Верни ответ СТРОГО в формате JSON без markdown-тегов:
{
  "question": "...",
  "answer": "...",
  "key_points": [
    "...",
    "..."
  ]
}`

	userPrompt := fmt.Sprintf(`ТЕМА: %s (%s)
ГРЕЙД: %s
СТАРЫЙ ВОПРОС (ПЕРЕГРУЖЕН): %s
СТАРЫЙ ЭТАЛОННЫЙ ОТВЕТ (фрагмент): %s
СТАРЫЕ КРИТЕРИИ: %v

ЗАМЕЧАНИЯ АУДИТА:
- Проблемы: %s
- Критика аудитора: %s
- Рекомендация по исправлению: %s
- Черновик нового вопроса: %s

ВАЖНО: Создай СТРОГО АТОМАРНЫЙ вопрос (1 предложение, 1 знак '?', БЕЗ склеек "и как / и почему / а также"). Выбери ТОЛЬКО ОДНУ главную суть. Чистый ответ и 3-4 ключевых тезиса по JSON-схеме.`,
		item.TopicTitle, item.TopicID,
		item.Difficulty,
		item.OriginalQuestion,
		truncateString(item.OriginalAnswer, 1000),
		item.OriginalKeyPoints,
		strings.Join(item.IssueTypes, ", "),
		item.Critique,
		item.AnswerFixNote,
		item.SuggestedQuestion,
	)

	reqPayload := anthropicRequest{
		Model:     cfg.AI.Model,
		MaxTokens: 2500,
		Messages: []anthropicMessage{
			{Role: "user", Content: systemPrompt + "\n\n" + userPrompt},
		},
	}

	jsonBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("ошибка сериализации запроса: %w", err)
	}

	apiURL := strings.TrimRight(cfg.AI.DeepSeekURL, "/") + "/v1/messages"
	var lastErr error

	for attempt := 0; attempt < 5; attempt++ {
		keyIdx := keyCounter.Add(1) % uint64(len(cfg.AI.DeepSeekKeys))
		apiKey := cfg.AI.DeepSeekKeys[keyIdx]

		client := directClient
		if attempt%2 == 1 && proxyClient != nil {
			client = proxyClient
		}

		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Second)
		req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(jsonBytes))
		if err != nil {
			cancel()
			return nil, err
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

		resp, err := client.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			time.Sleep(time.Duration(1+attempt) * time.Second)
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()

		if readErr != nil {
			lastErr = readErr
			continue
		}

		if resp.StatusCode == 429 || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
			time.Sleep(time.Duration(2+attempt) * time.Second)
			continue
		}

		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		var antResp anthropicResponse
		if err := json.Unmarshal(respBody, &antResp); err != nil {
			lastErr = fmt.Errorf("ошибка парсинга ответа Crassus: %w", err)
			continue
		}

		if len(antResp.Content) == 0 {
			lastErr = fmt.Errorf("пустой ответ content от модели")
			continue
		}

		rawText := antResp.Content[0].Text
		parsed, err := parseDeepSeekResponse(rawText)
		if err != nil {
			lastErr = fmt.Errorf("ошибка парсинга JSON регенерации: %w", err)
			continue
		}

		cleanQ := strings.TrimSpace(parsed.Question)
		if strings.TrimSpace(cleanQ) == "" || strings.TrimSpace(parsed.Answer) == "" || len(parsed.KeyPoints) == 0 {
			lastErr = fmt.Errorf("модель вернула неполные данные: пустой вопрос, ответ или key_points")
			continue
		}

		// Программная гарантия атомарности: если модель вернула два вопроса с '?', отсекаем по первому
		if strings.Count(cleanQ, "?") > 1 {
			parts := strings.Split(cleanQ, "?")
			if len(parts) > 0 && strings.TrimSpace(parts[0]) != "" {
				cleanQ = strings.TrimSpace(parts[0]) + "?"
			}
		}

		return &RegeneratedQuestion{
			ID:                item.ID,
			TopicID:           item.TopicID,
			TopicTitle:        item.TopicTitle,
			Difficulty:        item.Difficulty,
			OriginalQuestion:  item.OriginalQuestion,
			OriginalAnswer:    item.OriginalAnswer,
			OriginalKeyPoints: item.OriginalKeyPoints,
			IssueTypes:        item.IssueTypes,
			Critique:          item.Critique,
			NewQuestion:       cleanQ,
			NewAnswer:         strings.TrimSpace(parsed.Answer),
			NewKeyPoints:      parsed.KeyPoints,
			UpdatedAt:         time.Now().Format(time.RFC3339),
		}, nil
	}

	return nil, fmt.Errorf("все попытки исчерпаны: %v", lastErr)
}

var jsonBlockRegex = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\})\\s*```")

func parseDeepSeekResponse(raw string) (*deepSeekResponse, error) {
	clean := strings.TrimSpace(raw)
	if match := jsonBlockRegex.FindStringSubmatch(clean); len(match) > 1 {
		clean = strings.TrimSpace(match[1])
	} else {
		start := strings.Index(clean, "{")
		end := strings.LastIndex(clean, "}")
		if start >= 0 && end > start {
			clean = clean[start : end+1]
		}
	}

	var res deepSeekResponse
	if err := json.Unmarshal([]byte(clean), &res); err != nil {
		return nil, fmt.Errorf("ошибка парсинга: %w (исходник: %.200s)", err, raw)
	}
	return &res, nil
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func saveMarkdownReport(path string, items []RegeneratedQuestion) {
	var sb strings.Builder
	sb.WriteString("# 🎯 Отчёт по регенерации и разгрузке вопросов Go Mentor\n\n")
	sb.WriteString(fmt.Sprintf("Всего обработано: **%d вопросов**\n\n", len(items)))
	sb.WriteString("Каждый вопрос был избавлен от перегруза, приведён к строго ОДНОЙ теме, а эталонный ответ и критерии (key_points) синхронизированы.\n\n")
	sb.WriteString("---\n\n")

	for i, item := range items {
		sb.WriteString(fmt.Sprintf("### %d. [ID: %d] %s • `%s` (%s)\n\n",
			i+1, item.ID, item.TopicTitle, item.TopicID, item.Difficulty))

		if len(item.IssueTypes) > 0 {
			sb.WriteString(fmt.Sprintf("**Были проблемы:** `%s`\n\n", strings.Join(item.IssueTypes, "`, `")))
		}
		if item.Critique != "" {
			sb.WriteString(fmt.Sprintf("> ⚠️ **Критика аудита:** %s\n\n", item.Critique))
		}

		sb.WriteString("**❌ БЫЛО (Перегруженный вопрос):**\n")
		sb.WriteString(fmt.Sprintf("> %s\n\n", item.OriginalQuestion))

		sb.WriteString("**✨ СТАЛО (Чёткий сфокусированный вопрос):**\n")
		sb.WriteString(fmt.Sprintf("> **%s**\n\n", item.NewQuestion))

		sb.WriteString("<details>\n<summary><b>📖 Новый эталонный ответ и критерии (нажмите, чтобы развернуть)</b></summary>\n\n")
		sb.WriteString("**🎯 Новые ключевые критерии (Key Points):**\n")
		for _, kp := range item.NewKeyPoints {
			sb.WriteString(fmt.Sprintf("- %s\n", kp))
		}
		sb.WriteString("\n**📝 Новый эталонный ответ:**\n\n")
		sb.WriteString(item.NewAnswer)
		sb.WriteString("\n\n</details>\n\n---\n\n")
	}

	_ = os.WriteFile(path, []byte(sb.String()), 0644)
}
