package models

import "time"

type Topic struct {
	ID          string
	Title       string
	Description string
}

type Difficulty int

const (
	Junior   Difficulty = 1
	Middle   Difficulty = 2
	Advanced Difficulty = 3
)

type Question struct {
	ID         int        `db:"id"`
	TopicID    string     `db:"topic_id"`
	Difficulty Difficulty `db:"difficulty"`
	Text       string     `db:"text"`
	Answer     string     `db:"answer"`
	KeyPoints  []string   `db:"key_points"`
}

type User struct {
	ID        int64     `db:"id"`
	Username  string    `db:"username"`
	FirstName string    `db:"first_name"`
	CreatedAt time.Time `db:"created_at"`
}

type SessionStatus string

const (
	Active    SessionStatus = "active"
	Completed SessionStatus = "completed"
)

type InterviewSession struct {
	ID        string        `json:"id"`
	UserID    int64         `json:"user_id"`
	TopicID   string        `json:"topic_id"`
	Status    SessionStatus `json:"status"`
	CreatedAt time.Time     `json:"created_at"`
}
type Evaluation struct {
	Score         int      `json:"score"`          // Оценка от 1 до 10
	IsCorrect     bool     `json:"is_correct"`     // Зачтен ли ответ
	Feedback      string   `json:"feedback"`       // Человеческий комментарий ментора
	MissingPoints []string `json:"missing_points"` // Что кандидат забыл сказать
}

type AnswerAttempt struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"session_id"`
	QuestionID int        `json:"question_id"`
	UserAnswer string     `json:"user_answer"` // Текст или расшифровка войса
	IsVoice    bool       `json:"is_voice"`    // Был ли ответ голосовым
	Eval       Evaluation `json:"evaluation"`  // Оценка ИИ
	CreatedAt  time.Time  `json:"created_at"`
}
