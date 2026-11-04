package usecase

import (
	"context"
	"io"
	"testing"
	"time"

	"interview-bot/internal/domain"
)

type mockFSMForMockInterview struct {
	state      *domain.UserState
	lockOwner  string
	allowEval  bool
	selectedTs []string
}

func (m *mockFSMForMockInterview) SetState(ctx context.Context, userID int64, state domain.UserState) error {
	m.state = &state
	return nil
}

func (m *mockFSMForMockInterview) GetState(ctx context.Context, userID int64) (*domain.UserState, bool, error) {
	if m.state == nil {
		return nil, false, nil
	}
	return m.state, true, nil
}

func (m *mockFSMForMockInterview) ResetState(ctx context.Context, userID int64) error {
	m.state = nil
	return nil
}

func (m *mockFSMForMockInterview) AcquireLock(ctx context.Context, userID int64, ttl time.Duration) (string, bool, error) {
	return "test-token", true, nil
}

func (m *mockFSMForMockInterview) ReleaseLock(ctx context.Context, userID int64, token string) error {
	return nil
}

func (m *mockFSMForMockInterview) AllowEval(ctx context.Context, userID int64) (bool, error) {
	return m.allowEval, nil
}

func (m *mockFSMForMockInterview) ToggleSelectedTopic(ctx context.Context, userID int64, topicID string) (bool, error) {
	return true, nil
}

func (m *mockFSMForMockInterview) GetSelectedTopics(ctx context.Context, userID int64) ([]string, error) {
	return m.selectedTs, nil
}

func (m *mockFSMForMockInterview) ClearSelectedTopics(ctx context.Context, userID int64) error {
	m.selectedTs = nil
	return nil
}

func (m *mockFSMForMockInterview) SelectAllTopics(ctx context.Context, userID int64, topicIDs []string) error {
	m.selectedTs = topicIDs
	return nil
}

func (m *mockFSMForMockInterview) SaveQuestionMsgID(ctx context.Context, userID int64, msgID int) error {
	return nil
}

func (m *mockFSMForMockInterview) GetQuestionMsgID(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

type mockAIMockEvaluator struct {
	mockEvalResp    *domain.MockEvalResponse
	followUpEval    *domain.Evaluation
	verdict         *domain.MockVerdict
	lastFirstAns    string
	lastFollowUpAns string
}

func (m *mockAIMockEvaluator) EvaluateMockAnswer(ctx context.Context, q domain.Question, userAnswer string) (*domain.MockEvalResponse, error) {
	m.lastFirstAns = userAnswer
	return m.mockEvalResp, nil
}

func (m *mockAIMockEvaluator) EvaluateMockFollowUp(ctx context.Context, q domain.Question, firstAnswer, followUpQ, secondAnswer string) (*domain.Evaluation, error) {
	m.lastFollowUpAns = secondAnswer
	return m.followUpEval, nil
}

func (m *mockAIMockEvaluator) GenerateMockVerdict(ctx context.Context, grade string, history []domain.MockQuestionReport) (*domain.MockVerdict, error) {
	return m.verdict, nil
}

type dummyTranscriber struct{}

func (d *dummyTranscriber) Transcribe(ctx context.Context, fetchAudio func(context.Context) (io.Reader, error), filename string) (string, error) {
	return "тестовый голосовой ответ", nil
}

func TestMockInterview_FullFlow(t *testing.T) {
	ctx := context.Background()

	questions := []domain.Question{
		{ID: 101, TopicID: "goroutines", Text: "Как работают горутины?", Answer: "...", KeyPoints: []string{"m:n", "стек"}},
		{ID: 102, TopicID: "channels", Text: "Что такое буферизованный канал?", Answer: "...", KeyPoints: []string{"буфер"}},
	}

	qRepo := &mockQuestionRepoForInterview{questions: questions}
	fsm := &mockFSMForMockInterview{allowEval: true}
	progRepo := &mockProgressRepoForInterview{}
	aiMock := &mockAIMockEvaluator{
		mockEvalResp: &domain.MockEvalResponse{
			NeedsFollowUp:    true,
			FollowUpQuestion: "А какой начальный размер стека у горутины в Go?",
		},
		followUpEval: &domain.Evaluation{
			Score:     8,
			IsCorrect: true,
			Feedback:  "Отлично справился с уточнением!",
		},
		verdict: &domain.MockVerdict{
			Verdict:        domain.VerdictHire,
			ConfirmedGrade: "Junior+",
			AverageScore:   8.0,
			Summary:        "Кандидат показал крепкие базовые знания.",
		},
	}

	uc := NewMockInterviewUsecase(qRepo, fsm, aiMock, &dummyTranscriber{}, progRepo)

	// 1. Старт мок-интервью
	firstQ, total, err := uc.StartMockInterview(ctx, 12345, []string{"goroutines", "channels"}, "junior")
	if err != nil {
		t.Fatalf("ошибка старта мок-интервью: %v", err)
	}
	if firstQ.ID != 101 {
		t.Errorf("ожидался ID 101, получен %d", firstQ.ID)
	}
	if total != 2 {
		t.Errorf("ожидалось 2 вопроса, получено %d", total)
	}

	// 2. Отправляем первый ответ на вопрос 1 -> ожидаем Follow-Up
	res1, err := uc.SubmitMockTextAnswer(ctx, 12345, "Горутины — это легковесные потоки под управлением Go runtime.")
	if err != nil {
		t.Fatalf("ошибка первого ответа: %v", err)
	}
	if !res1.IsFollowUp {
		t.Fatalf("ожидался IsFollowUp = true")
	}
	if res1.FollowUpQ != "А какой начальный размер стека у горутины в Go?" {
		t.Errorf("неверный текст follow-up: %s", res1.FollowUpQ)
	}

	// 3. Отправляем ответ на Follow-Up -> переход ко 2-му вопросу
	res2, err := uc.SubmitMockTextAnswer(ctx, 12345, "Начальный размер 2 КБ, стек растет динамически.")
	if err != nil {
		t.Fatalf("ошибка ответа на follow-up: %v", err)
	}
	if res2.IsFollowUp {
		t.Fatalf("не ожидали повторного follow-up")
	}
	if res2.NextQuestion == nil || res2.NextQuestion.ID != 102 {
		t.Fatalf("ожидался переход к вопросу 102, получен %v", res2.NextQuestion)
	}

	// 4. На второй вопрос даем идеальный ответ -> ИИ сразу принимает без follow-up
	aiMock.mockEvalResp = &domain.MockEvalResponse{
		NeedsFollowUp: false,
		Evaluation: &domain.Evaluation{
			Score:     9,
			IsCorrect: true,
			Feedback:  "Исчерпывающий ответ про каналы.",
		},
	}

	res3, err := uc.SubmitMockTextAnswer(ctx, 12345, "Буферизованный канал имеет емкость и блокирует запись только при заполнении буфера.")
	if err != nil {
		t.Fatalf("ошибка второго ответа: %v", err)
	}

	// Это был последний вопрос раунда -> должно быть завершено с финальным вердиктом!
	if !res3.IsFinished {
		t.Fatalf("ожидалось IsFinished = true")
	}
	if res3.FinalVerdict == nil {
		t.Fatalf("ожидался FinalVerdict != nil")
	}
	if res3.FinalVerdict.Verdict != domain.VerdictHire {
		t.Errorf("ожидался вердикт Hire, получен %s", res3.FinalVerdict.Verdict)
	}

	// 5. Сессия в FSM должна быть очищена
	if fsm.state != nil {
		t.Errorf("FSM состояние должно быть сброшено после окончания интервью")
	}
}

func TestMockInterview_Stop(t *testing.T) {
	ctx := context.Background()
	fsm := &mockFSMForMockInterview{
		state: &domain.UserState{State: domain.StateWaitingAnswer, Mode: domain.SessionModeMock},
	}
	uc := NewMockInterviewUsecase(&mockQuestionRepoForInterview{}, fsm, &mockAIMockEvaluator{}, &dummyTranscriber{}, &mockProgressRepoForInterview{})

	err := uc.StopMockInterview(ctx, 999)
	if err != nil {
		t.Fatalf("ошибка остановки: %v", err)
	}
	if fsm.state != nil {
		t.Errorf("сессия должна быть удалена")
	}
}

func TestMockInterview_CustomTopicsScaling(t *testing.T) {
	ctx := context.Background()

	var questions []domain.Question
	var selectedTopics []string
	qid := 1
	for i := 1; i <= 10; i++ {
		topicID := string(rune('a'+i-1)) + "_topic"
		selectedTopics = append(selectedTopics, topicID)
		// По 6 вопросов в каждой из 10 тем (ожидаем по 2 на каждую = 20 вопросов)
		for j := 1; j <= 6; j++ {
			questions = append(questions, domain.Question{ID: qid, TopicID: topicID, Text: "Q"})
			qid++
		}
	}

	qRepo := &mockQuestionRepoForInterview{questions: questions}
	fsm := &mockFSMForMockInterview{allowEval: true}
	uc := NewMockInterviewUsecase(qRepo, fsm, &mockAIMockEvaluator{}, &dummyTranscriber{}, &mockProgressRepoForInterview{})

	_, total, err := uc.StartMockInterview(ctx, 777, selectedTopics, "junior")
	if err != nil {
		t.Fatalf("ошибка старта мок-интервью на 10 тем: %v", err)
	}
	if total != 20 {
		t.Errorf("при выборе 10 тем (по 2 вопроса на тему) ожидалось 20 вопросов, получено %d", total)
	}
}


