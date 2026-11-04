package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"interview-bot/internal/config"
	"interview-bot/internal/domain"
)

func TestSaveAttemptAndProgress(t *testing.T) {
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

	// Автоочистка тестового пользователя после завершения тестов
	const testUserID = int64(999999999)
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanCancel()
		_, _ = pool.Exec(cleanCtx, "DELETE FROM users WHERE id = $1", testUserID)
	})

	// Убедимся, что тестовый пользователь существует в БД
	userRepo := NewUserRepository(pool)
	_ = userRepo.UpsertUser(ctx, domain.User{
		ID:        testUserID,
		Username:  "test_user",
		FirstName: "Test",
	})

	repo := NewProgressRepository(pool)

	testCases := []struct {
		name          string
		missingPoints []string
		score         int
	}{
		{
			name:          "Empty missing points",
			missingPoints: []string{},
			score:         9,
		},
		{
			name:          "Nil missing points",
			missingPoints: nil,
			score:         10,
		},
		{
			name:          "With missing points",
			missingPoints: []string{"point 1", "point 2"},
			score:         5,
		},
	}

	// Получаем реальный существующий вопрос и тему из БД
	var testQuestionID int
	var testTopicID string
	err = pool.QueryRow(ctx, "SELECT id, topic_id FROM questions ORDER BY id LIMIT 1").Scan(&testQuestionID, &testTopicID)
	if err != nil {
		t.Fatalf("Ошибка получения тестового вопроса из БД: %v", err)
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			attempt := domain.AnswerAttempt{
				UserID:     testUserID,
				QuestionID: testQuestionID,
				UserAnswer: "Тестовый ответ",
				IsVoice:    false,
				Eval: domain.Evaluation{
					Score:         tc.score,
					IsCorrect:     tc.score >= 6,
					Feedback:      "Тестовый фидбек",
					MissingPoints: tc.missingPoints,
				},
			}

			err := repo.SaveAttemptAndProgress(ctx, attempt, testTopicID)
			if err != nil {
				t.Fatalf("Ошибка сохранения попытки: %v", err)
			}
		})
	}

	// Проверяем получение прогресса
	progressMap, err := repo.GetUserProgressByTopic(ctx, testUserID, testTopicID)
	if err != nil {
		t.Fatalf("Ошибка GetUserProgressByTopic: %v", err)
	}

	p, ok := progressMap[testQuestionID]
	if !ok {
		t.Fatalf("Ожидался прогресс по вопросу %d, но не найден", testQuestionID)
	}

	if p.Attempts < 3 {
		t.Errorf("Ожидалось >= 3 попыток, получили %d", p.Attempts)
	}
}
