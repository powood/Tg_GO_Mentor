package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"interview-bot/internal/config"
)

func TestQuizRepository(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgrespassword@localhost:5434/tg_mentor?sslmode=disable"
	}

	cfg := config.PostgresConfig{
		URL:         dbURL,
		MaxConns:    2,
		MinConns:    1,
		MaxConnIdle: time.Minute,
	}

	pool, err := NewPool(ctx, cfg)
	if err != nil {
		t.Skipf("Пропускаем интеграционный тест (БД недоступна): %v", err)
	}
	defer pool.Close()

	repo := NewQuizRepository(pool)

	// 1. Тест выборки случайного вопроса
	q, err := repo.GetRandomQuizQuestion(ctx, "junior", nil)
	if err != nil {
		t.Fatalf("ошибка GetRandomQuizQuestion: %v", err)
	}
	if q == nil {
		t.Fatal("вопрос не должен быть nil")
	}
	if q.ID <= 0 {
		t.Errorf("некорректный ID вопроса: %d", q.ID)
	}
	if len(q.Options) != 4 {
		t.Errorf("ожидалось 4 варианта ответа, получено %d", len(q.Options))
	}
	if q.CorrectOption == "" {
		t.Errorf("correct_option не должен быть пустым")
	}

	// 2. Тест поиска по ID
	byID, err := repo.GetQuizQuestionByID(ctx, q.ID)
	if err != nil {
		t.Fatalf("ошибка GetQuizQuestionByID: %v", err)
	}
	if byID.ID != q.ID {
		t.Errorf("ожидался ID %d, получен %d", q.ID, byID.ID)
	}
	if byID.TopicTitle == "" {
		t.Errorf("topic_title не должен быть пустым")
	}
}

