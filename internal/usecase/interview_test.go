package usecase

import (
	"context"
	"testing"
	"time"

	"interview-bot/internal/domain"
)

type mockQuestionRepoForInterview struct {
	lastTopicID string
	lastGrade   string
	lastRandomGrade string
	questions   []domain.Question
}

func (m *mockQuestionRepoForInterview) GetTopics(ctx context.Context) ([]domain.Topic, error) {
	return nil, nil
}

func (m *mockQuestionRepoForInterview) GetQuestionByTopic(ctx context.Context, topicID string, grade string) ([]domain.Question, error) {
	m.lastTopicID = topicID
	m.lastGrade = grade
	return m.questions, nil
}

func (m *mockQuestionRepoForInterview) GetQuestionsByTopics(ctx context.Context, topicIDs []string, grade string) ([]domain.Question, error) {
	m.lastGrade = grade
	return m.questions, nil
}

func (m *mockQuestionRepoForInterview) GetQuestionByID(ctx context.Context, id int) (*domain.Question, error) {
	for _, q := range m.questions {
		if q.ID == id {
			return &q, nil
		}
	}
	return nil, domain.ErrQuestionNotFound
}

func (m *mockQuestionRepoForInterview) GetRandomQuestion(ctx context.Context, grade string) (*domain.Question, error) {
	m.lastRandomGrade = grade
	if len(m.questions) > 0 {
		return &m.questions[0], nil
	}
	return nil, domain.ErrQuestionNotFound
}

func (m *mockQuestionRepoForInterview) GetTopicQuestionCounts(ctx context.Context) (map[string]int, int, error) {
	return nil, 0, nil
}

func (m *mockQuestionRepoForInterview) GetTopicQuestionCountsByGrade(ctx context.Context, diffs []string) (map[string]int, int, error) {
	return nil, 0, nil
}

func (m *mockQuestionRepoForInterview) GetWeakQuestions(ctx context.Context, userID int64, limit int) ([]domain.Question, error) {
	return nil, nil
}

func (m *mockQuestionRepoForInterview) GetWeakQuestionsByTopic(ctx context.Context, userID int64, topicID string, limit int) ([]domain.Question, error) {
	return nil, nil
}

func (m *mockQuestionRepoForInterview) GetWeakTopics(ctx context.Context, userID int64) ([]domain.WeakTopic, int, error) {
	return nil, 0, nil
}

type mockProgressRepoForInterview struct{}

func (m *mockProgressRepoForInterview) GetUserProgressByTopic(ctx context.Context, userID int64, topicID string) (map[int]domain.QuestionProgress, error) {
	return make(map[int]domain.QuestionProgress), nil
}

func (m *mockProgressRepoForInterview) GetUserOverallProgress(ctx context.Context, userID int64) ([]domain.QuestionProgress, error) {
	return nil, nil
}

func (m *mockProgressRepoForInterview) GetUserGradeProgress(ctx context.Context, userID int64, diffs []string) ([]domain.QuestionProgress, error) {
	return nil, nil
}

func (m *mockProgressRepoForInterview) SaveAttemptAndProgress(ctx context.Context, attempt domain.AnswerAttempt, topicID string) error {
	return nil
}

type mockFSMForInterview struct{}

func (m *mockFSMForInterview) SetState(ctx context.Context, userID int64, state domain.UserState) error {
	return nil
}

func (m *mockFSMForInterview) GetState(ctx context.Context, userID int64) (*domain.UserState, bool, error) {
	return nil, false, nil
}

func (m *mockFSMForInterview) ResetState(ctx context.Context, userID int64) error {
	return nil
}

func (m *mockFSMForInterview) AcquireLock(ctx context.Context, userID int64, ttl time.Duration) (string, bool, error) {
	return "token", true, nil
}

func (m *mockFSMForInterview) ReleaseLock(ctx context.Context, userID int64, token string) error {
	return nil
}

func (m *mockFSMForInterview) AllowEval(ctx context.Context, userID int64) (bool, error) {
	return true, nil
}

func (m *mockFSMForInterview) ToggleSelectedTopic(ctx context.Context, userID int64, topicID string) (bool, error) {
	return true, nil
}

func (m *mockFSMForInterview) GetSelectedTopics(ctx context.Context, userID int64) ([]string, error) {
	return nil, nil
}

func (m *mockFSMForInterview) ClearSelectedTopics(ctx context.Context, userID int64) error {
	return nil
}

func (m *mockFSMForInterview) SelectAllTopics(ctx context.Context, userID int64, topicIDs []string) error {
	return nil
}

func (m *mockFSMForInterview) SaveQuestionMsgID(ctx context.Context, userID int64, msgID int) error {
	return nil
}

func (m *mockFSMForInterview) GetQuestionMsgID(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

func TestInterviewUsecase_GradeFiltering(t *testing.T) {
	sampleQuestions := []domain.Question{
		{
			ID:         101,
			TopicID:    "arrays_slices",
			Difficulty: domain.DifficultyIntern,
			Text:       "Что такое срез в Go?",
		},
	}

	qRepo := &mockQuestionRepoForInterview{questions: sampleQuestions}
	pRepo := &mockProgressRepoForInterview{}
	fsm := &mockFSMForInterview{}

	t.Run("intern user receives intern questions in StartInterview", func(t *testing.T) {
		uRepo := &mockUserRepoCustom{user: &domain.User{ID: 10, TargetGrade: "intern"}}
		uc := NewInterviewUsecase(qRepo, pRepo, uRepo, fsm, nil, nil)

		q, err := uc.StartInterview(context.Background(), 10, "arrays_slices")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if q == nil || q.ID != 101 {
			t.Fatalf("expected question ID 101, got %v", q)
		}
		if qRepo.lastGrade != "intern" {
			t.Errorf("expected grade 'intern' passed to repository, got '%s'", qRepo.lastGrade)
		}
	})

	t.Run("intern user receives intern questions in StartRandomInterview", func(t *testing.T) {
		uRepo := &mockUserRepoCustom{user: &domain.User{ID: 10, TargetGrade: "intern"}}
		uc := NewInterviewUsecase(qRepo, pRepo, uRepo, fsm, nil, nil)

		q, err := uc.StartRandomInterview(context.Background(), 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if q == nil || q.ID != 101 {
			t.Fatalf("expected question ID 101, got %v", q)
		}
		if qRepo.lastRandomGrade != "intern" {
			t.Errorf("expected random grade 'intern' passed to repository, got '%s'", qRepo.lastRandomGrade)
		}
		if qRepo.lastGrade != "intern" {
			t.Errorf("expected grade 'intern' passed to repository in StartInterview delegation, got '%s'", qRepo.lastGrade)
		}
	})

	t.Run("user without target grade defaults to all", func(t *testing.T) {
		uRepo := &mockUserRepoCustom{user: &domain.User{ID: 20, TargetGrade: ""}}
		uc := NewInterviewUsecase(qRepo, pRepo, uRepo, fsm, nil, nil)

		_, err := uc.StartInterview(context.Background(), 20, "arrays_slices")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if qRepo.lastGrade != "all" {
			t.Errorf("expected grade 'all', got '%s'", qRepo.lastGrade)
		}
	})
}

type mockUserRepoCustom struct {
	user *domain.User
}

func (m *mockUserRepoCustom) UpsertUser(ctx context.Context, u domain.User) error { return nil }
func (m *mockUserRepoCustom) GetUserByID(ctx context.Context, id int64) (*domain.User, error) {
	return m.user, nil
}
func (m *mockUserRepoCustom) SetUserGrade(ctx context.Context, id int64, grade string) error { return nil }

