package usecase

import (
	"context"
	"testing"

	"interview-bot/internal/domain"
)

type mockQuizRepo struct {
	q *domain.QuizQuestion
}

func (m *mockQuizRepo) GetRandomQuizQuestion(ctx context.Context, grade string, topicIDs []string) (*domain.QuizQuestion, error) {
	return m.q, nil
}

func (m *mockQuizRepo) GetQuizQuestionByID(ctx context.Context, id int) (*domain.QuizQuestion, error) {
	return m.q, nil
}

type mockUserRepo struct{}

func (m *mockUserRepo) UpsertUser(ctx context.Context, u domain.User) error { return nil }
func (m *mockUserRepo) GetUserByID(ctx context.Context, id int64) (*domain.User, error) {
	return &domain.User{ID: id, TargetGrade: "middle"}, nil
}
func (m *mockUserRepo) SetUserGrade(ctx context.Context, id int64, grade string) error { return nil }

type mockFSM struct {
	FSMStorage
}

func (m *mockFSM) GetSelectedTopics(ctx context.Context, userID int64) ([]string, error) {
	return []string{"channels"}, nil
}

func TestQuizUsecase_AnswerQuizQuestion(t *testing.T) {
	sampleQ := &domain.QuizQuestion{
		ID:            42,
		TopicID:       "channels",
		Difficulty:    domain.DifficultyMiddle,
		Question:      "Что произойдет при чтении из закрытого канала?",
		CorrectOption: "B",
		Explanation:   "Чтение вернет zero-value и false",
	}

	uc := NewQuizUsecase(&mockQuizRepo{q: sampleQ}, &mockUserRepo{}, &mockFSM{})

	// 1. Правильный ответ
	q, isCorrect, err := uc.AnswerQuizQuestion(context.Background(), 1, 42, "B")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !isCorrect {
		t.Errorf("expected answer to be correct")
	}
	if q.ID != 42 {
		t.Errorf("expected question ID 42, got %d", q.ID)
	}

	// 2. Неправильный ответ
	_, isCorrect, err = uc.AnswerQuizQuestion(context.Background(), 1, 42, "C")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if isCorrect {
		t.Errorf("expected answer to be incorrect")
	}

	// 3. Получение следующего вопроса с фильтром по теме
	nextQ, err := uc.GetNextQuizQuestion(context.Background(), 1, "topic:channels")
	if err != nil {
		t.Fatalf("unexpected error in GetNextQuizQuestion: %v", err)
	}
	if nextQ.TopicID != "channels" {
		t.Errorf("expected topic channels, got %s", nextQ.TopicID)
	}
}
