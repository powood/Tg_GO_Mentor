package domain

import (
	"strings"
	"time"
)

type Evaluation struct {
	Score         int      `json:"score"`
	IsCorrect     bool     `json:"is_correct"`
	Feedback      string   `json:"feedback"`
	MissingPoints []string `json:"missing_points"`
}

func (e *Evaluation) Normalize() {
	if e.Score < 1 {
		e.Score = 1
	} else if e.Score > 10 {
		e.Score = 10
	}
	e.IsCorrect = e.Score >= 6

	e.Feedback = truncateRunes(cleanText(e.Feedback), 600)
	if len(e.MissingPoints) == 0 {
		e.MissingPoints = []string{}
		return
	}
	if len(e.MissingPoints) > 4 {
		e.MissingPoints = e.MissingPoints[:4]
	}
	cleaned := make([]string, 0, len(e.MissingPoints))
	for _, p := range e.MissingPoints {
		p = truncateRunes(cleanText(p), 200)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	e.MissingPoints = cleaned
}

// cleanText удаляет нулевые байты \x00 (которые ломают UTF-8 в PostgreSQL) и лишние пробелы по краям
func cleanText(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\x00", ""))
}

// truncateRunes безопасно режет UTF-8 строку по символам (рунам), чтобы не порвать кириллицу пополам
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

type AnswerAttempt struct {
	ID         string
	UserID     int64
	QuestionID int
	UserAnswer string
	IsVoice    bool
	Eval       Evaluation
	CreatedAt  time.Time
}

type QuestionProgress struct {
	UserID       int64
	QuestionID   int
	TopicID      string
	Attempts     int
	SuccessCount int
	LastScore    int
	LastAnswerAt time.Time
}

func (p *QuestionProgress) IsMastered() bool {
	return p.LastScore >= 8
}
