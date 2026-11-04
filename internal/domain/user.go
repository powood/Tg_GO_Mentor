package domain

import (
	"strings"
	"time"
)

type User struct {
	ID          int64
	Username    string
	FirstName   string
	TargetGrade string // "intern", "junior", "middle", "senior", "all"
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CategoryStat отражает прогресс пользователя внутри одного макро-раздела
type CategoryStat struct {
	CategoryID string
	Title      string
	TotalCount int
	Mastered   int
	Percent    int
}

// UserStats собирает агрегированную аналитику подготовки
type UserStats struct {
	TotalQuestions int
	AnsweredCount  int
	MasteredCount  int
	WeakCount      int
	AverageScore   float64
	ReadyPercent   int
	TargetGrade    string
	Categories     []CategoryStat
}

// WeakTopic описывает тему, в которой у пользователя есть заваленные вопросы
type WeakTopic struct {
	TopicID   string
	Title     string
	WeakCount int
}

// GradeDifficulties возвращает набор сложностей для целевого грейда
func GradeDifficulties(grade string) []string {
	switch strings.ToLower(grade) {
	case "intern":
		return []string{"intern"}
	case "junior":
		return []string{"intern", "junior"}
	case "middle":
		return []string{"intern", "junior", "middle"}
	case "senior", "all", "":
		return []string{"intern", "junior", "middle", "senior"}
	default:
		return []string{"intern", "junior", "middle", "senior"}
	}
}
