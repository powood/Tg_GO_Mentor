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
	"time"
)

type Evaluator interface {
	Evaluate(ctx context.Context, q models.Question, userAnswer string) (*models.Evaluation, error)
}

type GroqClient struct {
	apiKey     string
	httpClient *http.Client
}

func NewGroqClient(apikey string) *GroqClient {
	return &GroqClient{
		apiKey:     apikey,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
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
		Model: "llama-3.3-70b-versatile",
		Messages: []groqMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.2, // низкая температура для точных и строгих оценок
	}
	reqBody.ResponseFormat.Type = "json_object" // заставляем Groq вернуть чистый JSON!
	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ошибка сериализации запроса: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.groq.com/openai/v1/chat/completions", bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("ошибка создания HTTP-запроса: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ошибка отправки запроса в Groq: %w", err)
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
	// Парсим сгенерированный нейросетью JSON прямо в нашу модель models.Evaluation!
	var evaluation models.Evaluation
	aiText := groqResp.Choices[0].Message.Content
	if err := json.Unmarshal([]byte(aiText), &evaluation); err != nil {
		return nil, fmt.Errorf("ошибка парсинга JSON оценки: %w", err)
	}
	return &evaluation, nil
}
