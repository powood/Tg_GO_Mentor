package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"interview-bot/internal/domain"
)

type mockInterviewUsecase struct {
	questions   QuestionRepository
	fsm         FSMStorage
	aiMock      AIMockEvaluator
	transcriber AITranscriber
	progress    ProgressRepository
}

func NewMockInterviewUsecase(
	questions QuestionRepository,
	fsm FSMStorage,
	aiMock AIMockEvaluator,
	transcriber AITranscriber,
	progress ProgressRepository,
) MockInterviewUsecase {
	return &mockInterviewUsecase{
		questions:   questions,
		fsm:         fsm,
		aiMock:      aiMock,
		transcriber: transcriber,
		progress:    progress,
	}
}

// lockUser захватывает распределенный лок в Redis на время обработки ответа
func (u *mockInterviewUsecase) lockUser(ctx context.Context, userID int64, ttl time.Duration) (func(), error) {
	token, ok, err := u.fsm.AcquireLock(ctx, userID, ttl)
	if err != nil {
		return nil, fmt.Errorf("ошибка захвата блокировки: %w", err)
	}
	if !ok {
		return nil, ErrAlreadyEvaluating
	}
	return func() {
		relCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = u.fsm.ReleaseLock(relCtx, userID, token)
	}, nil
}

// pickBalancedMockQuestions выбирает до limit вопросов, балансируя их по разным темам
func pickBalancedMockQuestions(questions []domain.Question, limit int) []domain.Question {
	if len(questions) <= limit {
		return questions
	}

	byTopic := make(map[string][]domain.Question)
	var topicOrder []string
	for _, q := range questions {
		if _, exists := byTopic[q.TopicID]; !exists {
			topicOrder = append(topicOrder, q.TopicID)
		}
		byTopic[q.TopicID] = append(byTopic[q.TopicID], q)
	}

	var result []domain.Question
	for len(result) < limit && len(topicOrder) > 0 {
		nextOrder := topicOrder[:0]
		for _, tid := range topicOrder {
			list := byTopic[tid]
			if len(list) > 0 {
				result = append(result, list[0])
				byTopic[tid] = list[1:]
				if len(result) == limit {
					break
				}
				if len(byTopic[tid]) > 0 {
					nextOrder = append(nextOrder, tid)
				}
			}
		}
		topicOrder = nextOrder
	}
	return result
}

// pickMockQuestionsPerTopic выбирает до maxPerTopic вопросов из КАЖДОЙ темы в пуле, чередуя темы между собой
func pickMockQuestionsPerTopic(questions []domain.Question, maxPerTopic int) []domain.Question {
	byTopic := make(map[string][]domain.Question)
	var topicOrder []string
	for _, q := range questions {
		if _, exists := byTopic[q.TopicID]; !exists {
			topicOrder = append(topicOrder, q.TopicID)
		}
		if len(byTopic[q.TopicID]) < maxPerTopic {
			byTopic[q.TopicID] = append(byTopic[q.TopicID], q)
		}
	}

	var result []domain.Question
	for round := 0; round < maxPerTopic; round++ {
		added := false
		for _, tid := range topicOrder {
			if round < len(byTopic[tid]) {
				result = append(result, byTopic[tid][round])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return result
}

// StartMockInterview начинает сессию мок-собеседования
func (u *mockInterviewUsecase) StartMockInterview(ctx context.Context, userID int64, topicIDs []string, grade string) (*domain.Question, int, error) {
	unlock, err := u.lockUser(ctx, userID, 15*time.Second)
	if err != nil {
		return nil, 0, err
	}
	defer unlock()

	// 1. Если список тем пуст — берем все темы системы (микс-раунд)
	customTopicsCount := len(topicIDs)
	if customTopicsCount == 0 {
		allTopics, err := u.questions.GetTopics(ctx)
		if err != nil {
			return nil, 0, fmt.Errorf("ошибка загрузки тем: %w", err)
		}
		topicIDs = make([]string, 0, len(allTopics))
		for _, t := range allTopics {
			topicIDs = append(topicIDs, t.ID)
		}
	}

	// 2. Загружаем пул вопросов
	pool, err := u.questions.GetQuestionsByTopics(ctx, topicIDs, grade)
	if err != nil {
		return nil, 0, err
	}
	if len(pool) == 0 {
		return nil, 0, domain.ErrQuestionNotFound
	}

	// 3. Определяем вопросы раунда:
	// - если пользователь выбрал конкретные темы — берем по 2 вопроса на КАЖДУЮ выбранную тему (например, 10 тем -> 20 вопросов)
	// - если выбран общий микс по всем темам — 10 сбалансированных вопросов
	var selectedQs []domain.Question
	if customTopicsCount > 0 {
		const questionsPerTopic = 2
		selectedQs = pickMockQuestionsPerTopic(pool, questionsPerTopic)
	} else {
		const defaultMixQuestions = 10
		selectedQs = pickBalancedMockQuestions(pool, defaultMixQuestions)
	}
	if len(selectedQs) == 0 {
		return nil, 0, domain.ErrQuestionNotFound
	}

	firstQ := selectedQs[0]
	activeQueue := make([]int, 0, len(selectedQs)-1)
	for _, q := range selectedQs[1:] {
		activeQueue = append(activeQueue, q.ID)
	}

	// 4. Формируем состояние раунда
	state := domain.UserState{
		State:       domain.StateWaitingAnswer,
		TopicID:     firstQ.TopicID,
		QuestionID:  firstQ.ID,
		ActiveQueue: activeQueue,
		Mode:        domain.SessionModeMock,
		MockRound: &domain.MockRoundState{
			Grade:          grade,
			TopicIDs:       topicIDs,
			CurrentIndex:   0,
			TotalQuestions: len(selectedQs),
			InFollowUp:     false,
			History:        make([]domain.MockQuestionReport, 0, len(selectedQs)),
		},
		UpdatedAt: time.Now(),
	}

	if err := u.fsm.SetState(ctx, userID, state); err != nil {
		return nil, 0, fmt.Errorf("ошибка сохранения FSM: %w", err)
	}

	return &firstQ, len(selectedQs), nil
}

// prepareMockAnswer проверяет валидность сессии и антиспам
func (u *mockInterviewUsecase) prepareMockAnswer(ctx context.Context, userID int64) (*domain.UserState, error) {
	state, exists, err := u.fsm.GetState(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения состояния: %w", err)
	}
	if !exists || !state.IsMock() {
		return nil, ErrNoActiveSession
	}
	if state.State != domain.StateWaitingAnswer {
		return nil, ErrNoActiveSession
	}

	allowed, err := u.fsm.AllowEval(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ошибка проверки лимита: %w", err)
	}
	if !allowed {
		return nil, ErrRateLimited
	}
	return state, nil
}

// SubmitMockTextAnswer обрабатывает текстовый ответ кандидата в мок-режиме
func (u *mockInterviewUsecase) SubmitMockTextAnswer(ctx context.Context, userID int64, answerText string) (*domain.MockAnswerResult, error) {
	answerText = strings.TrimSpace(strings.ReplaceAll(answerText, "\x00", ""))
	if utf8.RuneCountInString(answerText) < 2 {
		return nil, ErrAnswerTooShort
	}
	if runes := []rune(answerText); len(runes) > 2000 {
		answerText = string(runes[:2000])
	}

	unlock, err := u.lockUser(ctx, userID, 75*time.Second)
	if err != nil {
		return nil, err
	}
	defer unlock()

	state, err := u.prepareMockAnswer(ctx, userID)
	if err != nil {
		return nil, err
	}

	return u.processMockAnswer(ctx, userID, state, answerText, false)
}

// SubmitMockVoiceAnswer обрабатывает голосовой ответ кандидата в мок-режиме
func (u *mockInterviewUsecase) SubmitMockVoiceAnswer(ctx context.Context, userID int64, fetchAudio func(context.Context) (io.Reader, error), filename string) (string, *domain.MockAnswerResult, error) {
	unlock, err := u.lockUser(ctx, userID, 75*time.Second)
	if err != nil {
		return "", nil, err
	}
	defer unlock()

	state, err := u.prepareMockAnswer(ctx, userID)
	if err != nil {
		return "", nil, err
	}

	transcript, err := u.transcriber.Transcribe(ctx, fetchAudio, filename)
	if err != nil {
		return "", nil, fmt.Errorf("ошибка распознавания аудио: %w", err)
	}
	transcript = strings.TrimSpace(strings.ReplaceAll(transcript, "\x00", ""))
	if utf8.RuneCountInString(transcript) < 2 {
		return "", nil, ErrAnswerTooShort
	}
	if runes := []rune(transcript); len(runes) > 2000 {
		transcript = string(runes[:2000])
	}
	NotifyTranscript(ctx, transcript)

	res, err := u.processMockAnswer(ctx, userID, state, transcript, true)
	if err != nil {
		return transcript, nil, err
	}
	return transcript, res, nil
}

// processMockAnswer — центральный пайплайн обработки ответов мок-интервью
func (u *mockInterviewUsecase) processMockAnswer(ctx context.Context, userID int64, state *domain.UserState, answerText string, isVoice bool) (*domain.MockAnswerResult, error) {
	currentQ, err := u.questions.GetQuestionByID(ctx, state.QuestionID)
	if err != nil {
		if errors.Is(err, domain.ErrQuestionNotFound) {
			_ = u.fsm.ResetState(ctx, userID)
			return nil, ErrNoActiveSession
		}
		return nil, fmt.Errorf("ошибка загрузки вопроса %d: %w", state.QuestionID, err)
	}

	if !isVoice {
		NotifyEvalStart(ctx, true)
	}

	round := state.MockRound

	// СЦЕНАРИЙ 1: Первый ответ на текущий вопрос (еще не в follow-up)
	if !round.InFollowUp {
		mockResp, err := u.aiMock.EvaluateMockAnswer(ctx, *currentQ, answerText)
		if err != nil {
			return nil, fmt.Errorf("ошибка вызова AI: %w", err)
		}

		// Если нужен Follow-up вопрос:
		if mockResp.NeedsFollowUp && mockResp.FollowUpQuestion != "" {
			round.InFollowUp = true
			round.FollowUpQ = mockResp.FollowUpQuestion
			round.FirstAnswer = answerText
			state.State = domain.StateWaitingAnswer
			state.UpdatedAt = time.Now()

			if err := u.fsm.SetState(ctx, userID, *state); err != nil {
				slog.Error("ошибка сохранения follow-up состояния в Redis", "user_id", userID, "error", err)
			}

			return &domain.MockAnswerResult{
				IsFollowUp:  true,
				FollowUpQ:   mockResp.FollowUpQuestion,
				QuestionNum: round.CurrentIndex + 1,
				TotalNum:    round.TotalQuestions,
			}, nil
		}

		// Иначе (ответ идеален или оценка готова сразу без уточнения):
		eval := mockResp.Evaluation
		if eval == nil {
			eval = &domain.Evaluation{Score: 8, IsCorrect: true, Feedback: "Ответ принят без дополнительных вопросов."}
			eval.Normalize()
		}

		return u.finishQuestion(ctx, userID, state, currentQ, answerText, "", "", eval, isVoice)
	}

	// СЦЕНАРИЙ 2: Ответ на уточняющий вопрос (Follow-up)
	eval, err := u.aiMock.EvaluateMockFollowUp(ctx, *currentQ, round.FirstAnswer, round.FollowUpQ, answerText)
	if err != nil {
		return nil, fmt.Errorf("ошибка вызова AI для follow-up: %w", err)
	}

	return u.finishQuestion(ctx, userID, state, currentQ, round.FirstAnswer, round.FollowUpQ, answerText, eval, isVoice)
}

// finishQuestion сохраняет прогресс вопроса и продвигает интервью к следующему вопросу или финальному вердикту
func (u *mockInterviewUsecase) finishQuestion(
	ctx context.Context,
	userID int64,
	state *domain.UserState,
	currentQ *domain.Question,
	firstAnswer, followUpQ, followUpAnswer string,
	eval *domain.Evaluation,
	isVoice bool,
) (*domain.MockAnswerResult, error) {
	round := state.MockRound

	// 1. Сохраняем попытку в Postgres под отвязанным контекстом 6 секунд (HIGH-2)
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 6*time.Second)
	defer cancel()

	userAnswerForDb := firstAnswer
	if followUpQ != "" && followUpAnswer != "" {
		userAnswerForDb = fmt.Sprintf("1) %s\n2) [%s] -> %s", firstAnswer, followUpQ, followUpAnswer)
	}

	attempt := domain.AnswerAttempt{
		UserID:     userID,
		QuestionID: currentQ.ID,
		UserAnswer: userAnswerForDb,
		IsVoice:    isVoice,
		Eval:       *eval,
	}
	if err := u.progress.SaveAttemptAndProgress(saveCtx, attempt, currentQ.TopicID); err != nil {
		slog.Error("не удалось сохранить оценку мок-вопроса в PostgreSQL",
			"user_id", userID,
			"question_id", currentQ.ID,
			"error", err,
		)
	}

	// 2. Добавляем в историю раунда
	report := domain.MockQuestionReport{
		QuestionID:     currentQ.ID,
		QuestionText:   currentQ.Text,
		TopicTitle:     currentQ.TopicID,
		FirstAnswer:    firstAnswer,
		FollowUpQ:      followUpQ,
		FollowUpAnswer: followUpAnswer,
		Eval:           *eval,
	}
	round.History = append(round.History, report)

	// 3. Проверяем, остались ли еще вопросы
	if len(state.ActiveQueue) > 0 {
		nextQID := state.ActiveQueue[0]
		state.QuestionID = nextQID
		state.ActiveQueue = state.ActiveQueue[1:]
		round.CurrentIndex++
		round.InFollowUp = false
		round.FollowUpQ = ""
		round.FirstAnswer = ""
		state.State = domain.StateWaitingAnswer
		state.UpdatedAt = time.Now()

		if err := u.fsm.SetState(saveCtx, userID, *state); err != nil {
			slog.Error("ошибка перехода к следующему мок-вопросу", "user_id", userID, "error", err)
		}

		nextQ, err := u.questions.GetQuestionByID(ctx, nextQID)
		if err != nil {
			return nil, fmt.Errorf("ошибка получения следующего вопроса: %w", err)
		}

		return &domain.MockAnswerResult{
			IsFollowUp:   false,
			Eval:         eval,
			NextQuestion: nextQ,
			QuestionNum:  round.CurrentIndex + 1,
			TotalNum:     round.TotalQuestions,
		}, nil
	}

	// 4. ВСЕ ВОПРОСЫ РАУНДА ПРОЙДЕНЫ -> Генерируем финальный вердикт!
	verdict, err := u.aiMock.GenerateMockVerdict(ctx, round.Grade, round.History)
	if err != nil {
		slog.Error("ошибка генерации финального вердикта мок-интервью", "user_id", userID, "error", err)
		// Фоллбэк: локальный расчет вердикта без ИИ
		var totalScore int
		for _, h := range round.History {
			totalScore += h.Eval.Score
		}
		avg := float64(totalScore) / float64(len(round.History))
		verdict = &domain.MockVerdict{
			AverageScore:    avg,
			ConfirmedGrade:  round.Grade,
			Summary:         "Собеседование завершено. Ты ответил на все вопросы раунда.",
			Strengths:       []string{"Уверенное прохождение раунда собеседования"},
			Weaknesses:      []string{"Повтори темы вопросов с оценкой ниже 7"},
			Recommendations: "Продолжай регулярные тренировки для закрепления материала.",
		}
		verdict.Normalize()
	}

	// Завершаем сессию в Redis
	_ = u.fsm.ResetState(saveCtx, userID)

	return &domain.MockAnswerResult{
		IsFinished:   true,
		Eval:         eval,
		FinalVerdict: verdict,
		QuestionNum:  round.TotalQuestions,
		TotalNum:     round.TotalQuestions,
	}, nil
}

// StopMockInterview прерывает активное мок-собеседование
func (u *mockInterviewUsecase) StopMockInterview(ctx context.Context, userID int64) error {
	unlock, err := u.lockUser(ctx, userID, 10*time.Second)
	if err != nil {
		return err
	}
	defer unlock()

	return u.fsm.ResetState(ctx, userID)
}

// FinishMockEarly завершает мок-собеседование досрочно.
// Если кандидат уже ответил хотя бы на 1 вопрос, формирует итоговый вердикт по отвеченным вопросам.
func (u *mockInterviewUsecase) FinishMockEarly(ctx context.Context, userID int64) (*domain.MockVerdict, error) {
	unlock, err := u.lockUser(ctx, userID, 45*time.Second)
	if err != nil {
		return nil, err
	}
	defer unlock()

	state, exists, err := u.fsm.GetState(ctx, userID)
	if err != nil || !exists || !state.IsMock() || len(state.MockRound.History) == 0 {
		_ = u.fsm.ResetState(ctx, userID)
		return nil, nil
	}

	round := state.MockRound
	verdict, err := u.aiMock.GenerateMockVerdict(ctx, round.Grade, round.History)
	if err != nil {
		slog.Error("ошибка генерации досрочного вердикта мок-интервью", "user_id", userID, "error", err)
		var totalScore int
		for _, h := range round.History {
			totalScore += h.Eval.Score
		}
		avg := float64(totalScore) / float64(len(round.History))
		verdict = &domain.MockVerdict{
			AverageScore:    avg,
			ConfirmedGrade:  round.Grade,
			Summary:         fmt.Sprintf("Собеседование завершено досрочно (отвечено вопросов: %d).", len(round.History)),
			Strengths:       []string{"Пройдена часть вопросов технического собеседования"},
			Weaknesses:      []string{"Повтори темы вопросов с оценкой ниже 7"},
			Recommendations: "Продолжай регулярные тренировки для закрепления материала.",
		}
		verdict.Normalize()
	}

	_ = u.fsm.ResetState(ctx, userID)
	return verdict, nil
}


