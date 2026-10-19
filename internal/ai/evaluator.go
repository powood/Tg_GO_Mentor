package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"interview-bot/internal/models"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync/atomic"
	"time"
)

type Evaluator interface {
	Evaluate(ctx context.Context, q models.Question, userAnswer string) (*models.Evaluation, error)
}

type GroqClient struct {
	apiKeys    []string
	counter    uint64
	httpClient *http.Client
}

func NewGroqClient(apiKeys []string) *GroqClient {
	transport := &http.Transport{}
	// Если в .env задан прокси — направляем запросы через него
	proxyStr := os.Getenv("GROQ_PROXY_URL")
	if proxyStr != "" {
		if proxyURL, err := url.Parse(proxyStr); err == nil {
			transport.Proxy = http.ProxyURL(proxyURL)
		}
	}
	return &GroqClient{
		apiKeys: apiKeys,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
	}
}
func (c *GroqClient) getNextAPIKey() string {
	if len(c.apiKeys) == 0 {
		return ""
	}
	// Атомарно увеличиваем счетчик на 1 без всяких мьютексов
	idx := atomic.AddUint64(&c.counter, 1) - 1
	// Оператор % (остаток от деления) зацикливает счетчик по длине слайса: 0, 1, 0, 1...
	return c.apiKeys[idx%uint64(len(c.apiKeys))]
}

type groqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type groqRequest struct {
	Model          string        `json:"model"`
	Messages       []groqMessage `json:"messages"`
	ResponseFormat struct {
		Type string `json:"type"`
	} `json:"response_format"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
}
type groqResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (c *GroqClient) Evaluate(ctx context.Context, q models.Question, userAnswer string) (*models.Evaluation, error) {
	systemPrompt := `Ты — опытный Senior Go-разработчик и строгий, но справедливый технический интервьюер.
Твоя задача — оценить ответ кандидата на собеседовании.
Сравни ответ кандидата с эталонным ответом и ключевыми тезисами.
Верни результат СТРОГО в формате JSON со следующими полями:
{
  "score": (число от 1 до 10),
  "is_correct": (true, если ответ в целом верный и оценка >= 6, иначе false),
  "feedback": "(краткий конструктивный комментарий (до 2–3 предложений, емко, без воды)",
  "missing_points": ["список ключевых тезисов на русском, которые кандидат забыл упомянуть"]
}`
	userPrompt := fmt.Sprintf(
		"Вопрос: %s\n\nЭталонный ответ: %s\n\nОбязательные ключевые тезисы: %v\n\nОтвет кандидата: %s",
		q.Text,
		q.Answer,
		q.KeyPoints,
		userAnswer,
	)
	reqBody := groqRequest{
		Model: "qwen/qwen3.8-27b", // <-- активная модель на Groq
		Messages: []groqMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.2,
		MaxTokens:   300,
	}

	reqBody.ResponseFormat.Type = "json_object" // заставляем Groq вернуть чистый JSON!
	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ошибка сериализации запроса: %w", err)
	}
	attempts := len(c.apiKeys)
	if attempts == 0 {
		return nil, errors.New("список API-ключей Groq пуст")
	}
	var lastErr error
	// Пробуем отправить запрос (если ключ словит 429, цикл перейдет к следующему!)
	for i := 0; i < attempts; i++ {
		apiKey := c.getNextAPIKey() // берем следующий ключ из пула
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(jsonBytes))
		if err != nil {
			return nil, fmt.Errorf("ошибка создания HTTP-запроса: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+apiKey) // <-- ТЕПЕРЬ ОШИБКИ НЕТ!
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("ошибка отправки запроса в Groq: %w", err)
			continue
		}
		// Если ключ исчерпал лимит (429) — закрываем тело и пробуем следующий ключ!
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = fmt.Errorf("ключ исчерпал лимиты (429), пробуем следующий")
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("Groq API вернул ошибку (%d): %s", resp.StatusCode, string(body))
		}
		var groqResp groqResponse
		if err := json.NewDecoder(resp.Body).Decode(&groqResp); err != nil {
			return nil, fmt.Errorf("ошибка декодирования ответа Groq: %w", err)
		}
		if len(groqResp.Choices) == 0 {
			return nil, errors.New("Groq вернул пустой список ответов")
		}
		var evaluation models.Evaluation
		aiText := groqResp.Choices[0].Message.Content
		if err := json.Unmarshal([]byte(aiText), &evaluation); err != nil {
			return nil, fmt.Errorf("ошибка парсинга JSON оценки: %w", err)
		}
		return &evaluation, nil // УСПЕХ!
	}
	return nil, fmt.Errorf("все API-ключи недоступны: %w", lastErr)
}
