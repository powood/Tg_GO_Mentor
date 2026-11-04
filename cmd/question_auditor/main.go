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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"interview-bot/internal/config"
)

type DBQuestion struct {
	ID         int      `json:"id"`
	TopicID    string   `json:"topic_id"`
	TopicTitle string   `json:"topic_title"`
	Difficulty string   `json:"difficulty"`
	Text       string   `json:"text"`
	Answer     string   `json:"answer"`
	KeyPoints  []string `json:"key_points"`
}

type rawAuditItem struct {
	ID                any      `json:"id"`
	IsSuspicious      bool     `json:"is_suspicious"`
	IssueTypes        []string `json:"issue_types"`
	Critique          string   `json:"critique"`
	SuggestedQuestion string   `json:"suggested_question"`
	AnswerFixNote     string   `json:"answer_fix_note"`
}

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
	topicFilter := flag.String("topic", "", "Фильтр по topic_id (по умолчанию все темы)")
	limitFlag := flag.Int("limit", 0, "Ограничить количество проверяемых вопросов (0 = без ограничений)")
	workersFlag := flag.Int("workers", 3, "Количество параллельных воркеров (рекомендуется 2-3)")
	batchSizeFlag := flag.Int("batch", 3, "Размер батча вопросов в одном запросе к DeepSeek (рекомендуется 3)")
	applyFlag := flag.Bool("apply", false, "Автоматически применить исправления вопросов в PostgreSQL")
	flag.Parse()

	log.Println("🔍 Инициализация аудитора базы вопросов Go Mentor...")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки конфигурации: %v", err)
	}

	// 1. Подключение к PostgreSQL
	initCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pgPool, err := pgxpool.New(initCtx, cfg.Postgres.URL)
	if err != nil {
		log.Fatalf("❌ Не удалось подключиться к PostgreSQL: %v", err)
	}
	defer pgPool.Close()
	log.Println("🐘 Подключение к PostgreSQL успешно установлено")

	// 2. HTTP-клиенты для DeepSeek (прямой + с прокси)
	directClient := &http.Client{
		Timeout: 75 * time.Second,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			DisableKeepAlives:     true,
		},
	}

	var proxyClient *http.Client
	if cfg.AI.ProxyURL != "" {
		if pURL, err := url.Parse(cfg.AI.ProxyURL); err == nil {
			proxyClient = &http.Client{
				Timeout: 75 * time.Second,
				Transport: &http.Transport{
					Proxy:                 http.ProxyURL(pURL),
					ResponseHeaderTimeout: 60 * time.Second,
					DisableKeepAlives:     true,
				},
			}
		}
	}

	// 3. Проверка ключей DeepSeek
	log.Println("🔍 Экспресс-проверка доступности ключей DeepSeek...")
	var activeKeys []string
	for i, k := range cfg.AI.DeepSeekKeys {
		testCtx, testCancel := context.WithTimeout(context.Background(), 8*time.Second)
		_, testErr := callCrassus(testCtx, directClient, proxyClient, k, cfg.AI.DeepSeekURL, cfg.AI.Model, "Ответь одним словом: ok")
		testCancel()
		if testErr == nil {
			activeKeys = append(activeKeys, k)
			log.Printf("🔑 DeepSeek ключ #%d: АКТИВЕН", i+1)
		} else {
			log.Printf("⚠️ DeepSeek ключ #%d: недоступен (%v), пробуем через прокси...", i+1, testErr)
			if proxyClient != nil {
				testCtx2, testCancel2 := context.WithTimeout(context.Background(), 8*time.Second)
				_, testErr2 := callCrassus(testCtx2, proxyClient, nil, k, cfg.AI.DeepSeekURL, cfg.AI.Model, "Ответь одним словом: ok")
				testCancel2()
				if testErr2 == nil {
					activeKeys = append(activeKeys, k)
					log.Printf("🔑 DeepSeek ключ #%d: АКТИВЕН (через прокси)", i+1)
				}
			}
		}
	}
	if len(activeKeys) == 0 {
		log.Fatal("❌ Ни один ключ DeepSeek не доступен!")
	}
	log.Printf("✨ Активно рабочих ключей DeepSeek: %d из %d", len(activeKeys), len(cfg.AI.DeepSeekKeys))

	// 4. Загрузка вопросов из БД
	questions, err := fetchQuestions(pgPool, *topicFilter, *limitFlag)
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки вопросов: %v", err)
	}
	log.Printf("📚 Загружено вопросов для аудита: %d", len(questions))
	if len(questions) == 0 {
		log.Println("ℹ️ Вопросов для проверки не найдено.")
		return
	}

	// 5. Формирование батчей
	batchSize := *batchSizeFlag
	if batchSize <= 0 {
		batchSize = 3
	}
	var batches [][]DBQuestion
	for i := 0; i < len(questions); i += batchSize {
		end := i + batchSize
		if end > len(questions) {
			end = len(questions)
		}
		batches = append(batches, questions[i:end])
	}
	log.Printf("📦 Сформировано батчей: %d (по ~%d вопросов)", len(batches), batchSize)

	// 6. Запуск воркеров аудита
	numWorkers := *workersFlag
	if numWorkers > len(activeKeys) {
		numWorkers = len(activeKeys)
	}
	if numWorkers <= 0 {
		numWorkers = 1
	}

	batchCh := make(chan struct {
		index int
		batch []DBQuestion
	}, len(batches))

	for i, b := range batches {
		batchCh <- struct {
			index int
			batch []DBQuestion
		}{index: i, batch: b}
	}
	close(batchCh)

	var (
		mu               sync.Mutex
		wg               sync.WaitGroup
		suspiciousList   []AuditedQuestion
		totalAudited     int64
		keyCounter       uint64
		processedBatches int64
	)

	startTime := time.Now()

	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for item := range batchCh {
				batch := item.batch
				batchIdx := item.index

				// Выбираем ключ с ротацией
				kIdx := atomic.AddUint64(&keyCounter, 1) % uint64(len(activeKeys))
				apiKey := activeKeys[kIdx]

				results, err := auditBatch(directClient, proxyClient, apiKey, cfg.AI.DeepSeekURL, cfg.AI.Model, batch)
				if err != nil {
					log.Printf("[Воркер #%d] ⚠️ Батч #%d ошибка: %v. Повтор через 2с...", workerID, batchIdx+1, err)
					time.Sleep(2 * time.Second)
					altKey := activeKeys[(int(kIdx)+1)%len(activeKeys)]
					results, err = auditBatch(directClient, proxyClient, altKey, cfg.AI.DeepSeekURL, cfg.AI.Model, batch)
					if err != nil {
						log.Printf("[Воркер #%d] ❌ Батч #%d пропущен из-за ошибки: %v", workerID, batchIdx+1, err)
						continue
					}
				}

				resultMap := make(map[int]rawAuditItem)
				for i, r := range results {
					id := parseID(r.ID)
					if id <= 0 && i < len(batch) {
						id = batch[i].ID
					}
					resultMap[id] = r
				}

				var batchSuspicious []AuditedQuestion
				for _, q := range batch {
					res, exists := resultMap[q.ID]
					aq := AuditedQuestion{
						ID:                q.ID,
						TopicID:           q.TopicID,
						TopicTitle:        q.TopicTitle,
						Difficulty:        q.Difficulty,
						OriginalQuestion:  q.Text,
						OriginalAnswer:    q.Answer,
						OriginalKeyPoints: q.KeyPoints,
					}
					if exists && res.IsSuspicious {
						aq.IsSuspicious = true
						aq.IssueTypes = res.IssueTypes
						aq.Critique = res.Critique
						aq.SuggestedQuestion = res.SuggestedQuestion
						aq.AnswerFixNote = res.AnswerFixNote
						batchSuspicious = append(batchSuspicious, aq)
					}
				}

				atomic.AddInt64(&totalAudited, int64(len(batch)))
				curDone := atomic.AddInt64(&processedBatches, 1)

				mu.Lock()
				suspiciousList = append(suspiciousList, batchSuspicious...)
				if curDone%10 == 0 {
					_ = saveReports(suspiciousList, atomic.LoadInt64(&totalAudited), time.Since(startTime))
				}
				mu.Unlock()

				if len(batchSuspicious) > 0 {
					log.Printf("[Воркер #%d] ⚠️ Батч [%d/%d]: найдено %d подозрительных! (Всего странных: %d)",
						workerID, curDone, len(batches), len(batchSuspicious), len(suspiciousList))
				} else {
					log.Printf("[Воркер #%d] ✅ Батч [%d/%d]: все %d вопросов в норме (прогресс %d/%d)",
						workerID, curDone, len(batches), len(batch), atomic.LoadInt64(&totalAudited), len(questions))
				}

				// Небольшая пауза между батчами одного воркера для разгрузки сокета
				time.Sleep(500 * time.Millisecond)
			}
		}(w)
	}

	wg.Wait()
	duration := time.Since(startTime)

	log.Printf("\n🎉 Аудит полностью завершён за %v!", duration.Round(time.Second))
	log.Printf("📊 Проверено вопросов: %d. Найдено со странностями: %d (%.1f%%)",
		totalAudited, len(suspiciousList), float64(len(suspiciousList))/float64(totalAudited)*100)

	// 7. Сохранение отчетов
	if err := saveReports(suspiciousList, totalAudited, duration); err != nil {
		log.Printf("❌ Ошибка сохранения отчетов: %v", err)
	} else {
		log.Println("💾 Отчёты успешно сохранены в audit_report.json и AUDIT_REPORT.md!")
	}

	// 8. Автоматическое применение (если указан флаг -apply)
	if *applyFlag && len(suspiciousList) > 0 {
		log.Println("🚀 Применяем исправления вопросов в PostgreSQL...")
		appliedCount := applyFixes(pgPool, suspiciousList)
		log.Printf("✅ Успешно обновлено вопросов в БД: %d", appliedCount)
	}
}

func parseID(v any) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(val))
		return n
	case json.Number:
		n, _ := val.Int64()
		return int(n)
	}
	return 0
}

func auditBatch(directClient, proxyClient *http.Client, apiKey, baseURL, model string, batch []DBQuestion) ([]rawAuditItem, error) {
	prompt := buildAuditPrompt(batch)

	ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
	defer cancel()

	rawText, err := callCrassus(ctx, directClient, proxyClient, apiKey, baseURL, model, prompt)
	if err != nil {
		return nil, err
	}

	cleanedJSON := cleanAndExtractJSONArray(rawText)

	var items []rawAuditItem
	if err := json.Unmarshal([]byte(cleanedJSON), &items); err != nil {
		return nil, fmt.Errorf("ошибка парсинга JSON: %w | ответ: %s", err, truncatePreview(rawText, 250))
	}

	return items, nil
}

func buildAuditPrompt(batch []DBQuestion) string {
	var sb strings.Builder
	sb.WriteString(`Ты — ведущий Go TechLead, технический редактор и эксперт собеседований.
Твоя задача — проверить вопросы технических собеседований по Go на логичность, правильность русского языка и ясность формулировок.

Критерии выявления СТРАННОСТЕЙ (is_suspicious = true):
1. [рассинхрон] Вопрос спрашивает одно (например, процедурное "Как получить X?"), а эталонный ответ и критерии требуют совсем другое (например, структуру данных X в памяти, или разницу между len и cap). Кандидат ответит в одно слово, а его накажут за нераскрытую теорию.
2. [корявый_язык] Неестественный русский язык, кальки с английского, речевые ошибки, тяжеловесные фразы, следы небрежного машинного перевода.
3. [перегружен] Вопрос пытается спросить 2-3 разные темы в одном предложении (например, "Как добавить элемент в срез и что такое len/cap?").
4. [неточность] В вопросе или ответе содержатся фактические ошибки либо устаревшие сведения (Go < 1.20).

Для ХОРОШИХ, чётких вопросов, где вопрос и ответ логично совпадают:
{"id": <числовой_id>, "is_suspicious": false}

Для вопросов со СТРАННОСТЯМИ или НЕДОЧЁТАМИ:
{"id": <числовой_id>, "is_suspicious": true, "issue_types": ["рассинхрон", "корявый_язык"], "critique": "в чём конкретно нелогичность (1-2 предложения)", "suggested_question": "исправленный чистый вопрос (1 предложение)", "answer_fix_note": "краткая заметка, что поправить в ответе (или пусто, если ответ ок)"}

ВАЖНО:
- В поле "id" пиши строго ТО ЖЕ ЦЕЛОЕ ЧИСЛО, что указано в [ВОПРОС #id].
- НЕ пиши длинных эссе и портянок кода в ответе — пиши ёмко и по делу!
- ВЕРНИ СТРОГО ВАЛИДНЫЙ JSON-МАССИВ:
[ ... ]

Батч вопросов:
`)

	for _, q := range batch {
		sb.WriteString(fmt.Sprintf("\n---\n[ВОПРОС #%d]\n", q.ID))
		sb.WriteString(fmt.Sprintf("ID (число): %d\n", q.ID))
		sb.WriteString(fmt.Sprintf("Тема: %s\n", q.TopicTitle))
		sb.WriteString(fmt.Sprintf("Уровень: %s\n", q.Difficulty))
		sb.WriteString(fmt.Sprintf("Текст вопроса: %s\n", q.Text))

		// Ограничиваем ответ первыми 1000 символами, чтобы не перегружать контекст
		ansPreview := q.Answer
		if len(ansPreview) > 1000 {
			ansPreview = ansPreview[:1000] + "..."
		}
		sb.WriteString(fmt.Sprintf("Эталонный ответ: %s\n", ansPreview))

		kpJSON, _ := json.Marshal(q.KeyPoints)
		sb.WriteString(fmt.Sprintf("Критерии оценки: %s\n", string(kpJSON)))
	}

	return sb.String()
}

func fetchQuestions(pool *pgxpool.Pool, topicFilter string, limit int) ([]DBQuestion, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	query := `
		SELECT q.id, q.topic_id, t.title, q.difficulty, q.text, q.answer, q.key_points
		FROM questions q
		JOIN topics t ON t.id = q.topic_id
	`
	var args []any
	var whereClauses []string

	if topicFilter != "" {
		whereClauses = append(whereClauses, "q.topic_id = $1")
		args = append(args, topicFilter)
	}

	if len(whereClauses) > 0 {
		query += " WHERE " + strings.Join(whereClauses, " AND ")
	}

	query += " ORDER BY q.id ASC"

	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []DBQuestion
	for rows.Next() {
		var q DBQuestion
		if err := rows.Scan(&q.ID, &q.TopicID, &q.TopicTitle, &q.Difficulty, &q.Text, &q.Answer, &q.KeyPoints); err != nil {
			return nil, err
		}
		result = append(result, q)
	}

	return result, rows.Err()
}

func callCrassus(ctx context.Context, directClient, proxyClient *http.Client, apiKey, baseURL, model, prompt string) (string, error) {
	if model == "" {
		model = "deepseek-v4.1-flash"
	}
	if baseURL == "" {
		baseURL = "https://crassusmarket.xyz"
	}
	apiURL := strings.TrimRight(baseURL, "/") + "/v1/messages"

	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: 2048,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}
	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	makeReq := func(client *http.Client) (string, error) {
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
			return "", fmt.Errorf("ошибка парсинга ответа Crassus: %w | ответ: %s", err, string(bodyBytes))
		}

		if len(anthropicResp.Content) == 0 {
			return "", fmt.Errorf("пустой ответ от Crassus")
		}

		return anthropicResp.Content[0].Text, nil
	}

	// 1. Пробуем прямой вызов
	res, err := makeReq(directClient)
	if err == nil {
		return res, nil
	}

	// 2. Fallback через прокси
	if proxyClient != nil && ctx.Err() == nil {
		return makeReq(proxyClient)
	}

	return "", err
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

	cleaned := buf.String()
	trailingCommaRx := regexp.MustCompile(`,\s*([\]}])`)
	return trailingCommaRx.ReplaceAllString(cleaned, "$1")
}

func truncatePreview(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func saveReports(suspicious []AuditedQuestion, total int64, duration time.Duration) error {
	jsonData, err := json.MarshalIndent(suspicious, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile("audit_report.json", jsonData, 0644); err != nil {
		return err
	}

	var md strings.Builder
	md.WriteString("# 🕵️ Результаты аудита вопросов Go Mentor\n\n")
	md.WriteString(fmt.Sprintf("> **Всего проверено вопросов:** %d  \n", total))
	md.WriteString(fmt.Sprintf("> **Вопросов со странностями:** %d (%.1f%%)  \n", len(suspicious), float64(len(suspicious))/float64(total)*100))
	md.WriteString(fmt.Sprintf("> **Время аудита:** %v  \n\n", duration.Round(time.Second)))

	issuesCount := make(map[string]int)
	for _, q := range suspicious {
		for _, t := range q.IssueTypes {
			issuesCount[t]++
		}
	}

	md.WriteString("### 📊 Распределение проблем:\n")
	for k, v := range issuesCount {
		md.WriteString(fmt.Sprintf("- **%s:** %d\n", k, v))
	}
	md.WriteString("\n---\n\n")

	md.WriteString("## 📋 Детальный разбор подозрительных вопросов\n\n")

	for i, q := range suspicious {
		md.WriteString(fmt.Sprintf("### %d. [ID: %d] %s • `%s` (%s)\n\n", i+1, q.ID, q.TopicTitle, q.TopicID, q.Difficulty))
		md.WriteString(fmt.Sprintf("**Категории:** `%s`  \n", strings.Join(q.IssueTypes, "`, `")))
		md.WriteString(fmt.Sprintf("**В чём странность:** %s\n\n", q.Critique))

		md.WriteString("**Текущий вопрос:**\n")
		md.WriteString(fmt.Sprintf("> %s\n\n", q.OriginalQuestion))

		if q.SuggestedQuestion != "" {
			md.WriteString("**✨ Рекомендуемый вопрос:**\n")
			md.WriteString(fmt.Sprintf("> **%s**\n\n", q.SuggestedQuestion))
		}

		if q.AnswerFixNote != "" {
			md.WriteString(fmt.Sprintf("**💡 Заметка по ответу:** %s\n\n", q.AnswerFixNote))
		}

		md.WriteString("---\n\n")
	}

	return os.WriteFile("AUDIT_REPORT.md", []byte(md.String()), 0644)
}

func applyFixes(pool *pgxpool.Pool, suspicious []AuditedQuestion) int {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	applied := 0
	for _, q := range suspicious {
		if q.SuggestedQuestion == "" {
			continue
		}

		_, err := pool.Exec(ctx,
			"UPDATE questions SET text = $1 WHERE id = $2",
			q.SuggestedQuestion, q.ID)

		if err != nil {
			log.Printf("❌ Ошибка обновления вопроса #%d: %v", q.ID, err)
		} else {
			applied++
		}
	}
	return applied
}
