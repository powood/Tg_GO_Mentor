package usecase

import (
	"context"
	"errors"
	"fmt"
	"interview-bot/internal/domain"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrNoActiveSession   = errors.New("нет активной сессии интервью")
	ErrInvalidState      = errors.New("некорректное состояние диалога")
	ErrWaitingNext       = errors.New("ожидается переход к следующему вопросу")
	ErrAlreadyEvaluating = errors.New("предыдущий ответ еще проверяется")
	ErrRateLimited       = errors.New("превышен лимит запросов, подождите немного")
	ErrAnswerTooShort    = errors.New("ответ слишком короткий")
	ErrVoiceTooLarge     = errors.New("аудиофайл слишком большой")
	ErrVoiceDownload     = errors.New("не удалось загрузить аудиосообщение")
)

type interviewUsecase struct {
	questions   QuestionRepository
	progress    ProgressRepository
	userRepo    UserRepository
	fsm         FSMStorage
	evaluator   AIEvaluator
	transcriber AITranscriber
	deck        *domain.DeckManager
}

// NewInterviewUsecase внедряет все необходимые зависимости (DI)
func NewInterviewUsecase(
	qRepo QuestionRepository,
	pRepo ProgressRepository,
	uRepo UserRepository,
	fsm FSMStorage,
	eval AIEvaluator,
	trans AITranscriber,
) InterviewUsecase {
	return &interviewUsecase{
		questions:   qRepo,
		progress:    pRepo,
		userRepo:    uRepo,
		fsm:         fsm,
		evaluator:   eval,
		transcriber: trans,
		deck:        domain.NewDeckManager(),
	}
}

func (u *interviewUsecase) lockUser(ctx context.Context, userID int64, ttl time.Duration) (func(), error) {
	token, ok, err := u.fsm.AcquireLock(ctx, userID, ttl)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrAlreadyEvaluating
	}
	unlock := func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = u.fsm.ReleaseLock(releaseCtx, userID, token)
	}
	return unlock, nil
}

func (u *interviewUsecase) GetTopics(ctx context.Context) ([]domain.Topic, error) {
	return u.questions.GetTopics(ctx)
}

func (u *interviewUsecase) StartInterview(ctx context.Context, userID int64, topicID string) (*domain.Question, error) {
	unlock, err := u.lockUser(ctx, userID, 10*time.Second)
	if err != nil {
		return nil, err
	}
	defer unlock()

	// 1. Достаем все вопросы по теме
	grade := "all"
	if u.userRepo != nil {
		if user, err := u.userRepo.GetUserByID(ctx, userID); err == nil && user != nil && user.TargetGrade != "" {
			grade = user.TargetGrade
		}
	}

	questions, err := u.questions.GetQuestionByTopic(ctx, topicID, grade)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения вопросов по теме %s: %w", topicID, err)
	}
	// 2. Достаем историю прогресса пользователя
	progressMap, err := u.progress.GetUserProgressByTopic(ctx, userID, topicID)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения прогресса пользователя %d: %w", userID, err)
	}
	// 3. Формируем адаптивную колоду спринта через доменный менеджер
	queue := u.deck.BuildInitialQueue(questions, progressMap)
	if len(queue) == 0 {
		return nil, domain.ErrQuestionNotFound
	}
	// 4. Первый вопрос достаем сразу, остальные остаются в очереди
	currentQID := queue[0]
	activeQueue := queue[1:]
	state := domain.UserState{
		TopicID:     topicID,
		QuestionID:  currentQID,
		ActiveQueue: activeQueue,
		State:       domain.StateWaitingAnswer,
	}

	// 5. Фиксируем сессию в Redis
	if err := u.fsm.SetState(ctx, userID, state); err != nil {
		return nil, fmt.Errorf("ошибка сохранения состояния FSM в Redis: %w", err)
	}
	// 6. Возвращаем карточку первого вопроса
	return u.questions.GetQuestionByID(ctx, currentQID)
}

func (u *interviewUsecase) prepareAnswer(ctx context.Context, userID int64) (*domain.UserState, error) {
	state, exists, err := u.fsm.GetState(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения состояния FSM: %w", err)
	}
	if !exists {
		return nil, ErrNoActiveSession
	}
	if state.State == domain.StateWaitingNext {
		return nil, ErrWaitingNext
	}
	if state.State != domain.StateWaitingAnswer {
		return nil, ErrNoActiveSession
	}
	if state.IsMock() {
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

func (u *interviewUsecase) SubmitTextAnswer(ctx context.Context, userID int64, answerText string) (*domain.Evaluation, *domain.Question, bool, error) {
	answerText = strings.TrimSpace(strings.ReplaceAll(answerText, "\x00", ""))
	if utf8.RuneCountInString(answerText) < 2 {
		return nil, nil, false, ErrAnswerTooShort
	}
	unlock, err := u.lockUser(ctx, userID, 75*time.Second)
	if err != nil {
		return nil, nil, false, err
	}
	defer unlock()

	state, err := u.prepareAnswer(ctx, userID)
	if err != nil {
		return nil, nil, false, err
	}
	return u.processAnswer(ctx, userID, state, answerText, false)
}

func (u *interviewUsecase) SubmitVoiceAnswer(ctx context.Context, userID int64, fetchAudio func(context.Context) (io.Reader, error), filename string) (string, *domain.Evaluation, *domain.Question, bool, error) {
	unlock, err := u.lockUser(ctx, userID, 75*time.Second)
	if err != nil {
		return "", nil, nil, false, err
	}
	defer unlock()

	// 1. Проверяем сессию и антиспам ДО скачивания файла в RAM и ДО платного Whisper (CRIT-2)
	state, err := u.prepareAnswer(ctx, userID)
	if err != nil {
		return "", nil, nil, false, err
	}

	// 3. Транскрибируем аудио через Whisper
	transcript, err := u.transcriber.Transcribe(ctx, fetchAudio, filename)
	if err != nil {
		return "", nil, nil, false, fmt.Errorf("ошибка распознавания аудио: %w", err)
	}
	transcript = strings.TrimSpace(strings.ReplaceAll(transcript, "\x00", ""))
	if utf8.RuneCountInString(transcript) < 2 {
		return "", nil, nil, false, ErrAnswerTooShort
	}
	// Ограничиваем слишком длинную расшифровку перед отправкой в LLM (C-4)
	if runes := []rune(transcript); len(runes) > 2000 {
		transcript = string(runes[:2000])
	}
	NotifyTranscript(ctx, transcript)

	// 4. Отправляем распознанный текст в общий процесс оценки
	eval, currentQ, isFinished, err := u.processAnswer(ctx, userID, state, transcript, true)
	if err != nil {
		return transcript, nil, nil, false, err
	}
	return transcript, eval, currentQ, isFinished, nil
}

// processAnswer — общий внутренний пайплайн оценки (вызывается уже под локом пользователя)
func (u *interviewUsecase) processAnswer(ctx context.Context, userID int64, state *domain.UserState, answerText string, isVoice bool) (*domain.Evaluation, *domain.Question, bool, error) {
	// 1. Получаем текущий вопрос из БД (если вопрос был удален из БД — сбрасываем устаревшую сессию, MED-2)
	currentQ, err := u.questions.GetQuestionByID(ctx, state.QuestionID)
	if err != nil {
		if errors.Is(err, domain.ErrQuestionNotFound) {
			_ = u.fsm.ResetState(ctx, userID)
			return nil, nil, false, ErrNoActiveSession
		}
		return nil, nil, false, fmt.Errorf("ошибка загрузки вопроса %d: %w", state.QuestionID, err)
	}

	// 2. Запрос к AI для оценки
	if !isVoice {
		NotifyEvalStart(ctx, false)
	}
	eval, err := u.evaluator.Evaluate(ctx, *currentQ, answerText)
	if err != nil {
		return nil, nil, false, fmt.Errorf("ошибка вызова AI: %w", err)
	}

	// 3. Оценка УЖЕ оплачена! Выделяем отвязанный контекст 6с для записи в PostgreSQL и Redis (HIGH-2),
	// чтобы истечение основного таймера не уничтожило готовый разбор.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 6*time.Second)
	defer cancel()

	attempt := domain.AnswerAttempt{
		UserID:     userID,
		QuestionID: currentQ.ID,
		UserAnswer: answerText,
		IsVoice:    isVoice,
		Eval:       *eval,
	}
	if err := u.progress.SaveAttemptAndProgress(saveCtx, attempt, currentQ.TopicID); err != nil {
		slog.Error("не удалось сохранить оплаченную оценку в PostgreSQL",
			"user_id", userID,
			"question_id", currentQ.ID,
			"score", eval.Score,
			"error", err,
		)
	}

	// 4. Адаптивная колода: вычисляем новую очередь с учетом оценки
	newQueue := u.deck.ReinsertQuestion(state.ActiveQueue, currentQ.ID, eval.Score)
	if len(newQueue) == 0 {
		if err := u.fsm.ResetState(saveCtx, userID); err != nil {
			slog.Error("ошибка сброса завершенной сессии в Redis", "user_id", userID, "error", err)
		}
		return eval, currentQ, true, nil
	}

	state.QuestionID = newQueue[0]
	state.ActiveQueue = newQueue[1:]
	state.State = domain.StateWaitingNext
	if err := u.fsm.SetState(saveCtx, userID, *state); err != nil {
		slog.Error("ошибка обновления FSM в Redis после оценки", "user_id", userID, "error", err)
	}
	return eval, currentQ, false, nil
}

// NextQuestion активирует и возвращает следующий вопрос из очереди сессии
func (u *interviewUsecase) NextQuestion(ctx context.Context, userID int64) (*domain.Question, error) {
	unlock, err := u.lockUser(ctx, userID, 10*time.Second)
	if err != nil {
		return nil, err
	}
	defer unlock()

	state, exists, err := u.fsm.GetState(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения состояния: %w", err)
	}
	if !exists {
		return nil, ErrNoActiveSession
	}
	if state.State == domain.StateWaitingNext {
		state.State = domain.StateWaitingAnswer
		if err := u.fsm.SetState(ctx, userID, *state); err != nil {
			return nil, fmt.Errorf("ошибка обновления FSM: %w", err)
		}
	}
	q, err := u.questions.GetQuestionByID(ctx, state.QuestionID)
	if err != nil {
		if errors.Is(err, domain.ErrQuestionNotFound) {
			_ = u.fsm.ResetState(ctx, userID)
			return nil, ErrNoActiveSession
		}
		return nil, err
	}
	return q, nil
}

// GetCurrentQuestion возвращает вопрос, на который пользователь сейчас должен ответить
func (u *interviewUsecase) GetCurrentQuestion(ctx context.Context, userID int64) (*domain.Question, error) {
	state, exists, err := u.fsm.GetState(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения состояния: %w", err)
	}
	if !exists {
		return nil, ErrNoActiveSession
	}
	q, err := u.questions.GetQuestionByID(ctx, state.QuestionID)
	if err != nil {
		if errors.Is(err, domain.ErrQuestionNotFound) {
			_ = u.fsm.ResetState(ctx, userID)
			return nil, ErrNoActiveSession
		}
		return nil, err
	}
	return q, nil
}

// StopInterview прерывает текущую сессию
func (u *interviewUsecase) StopInterview(ctx context.Context, userID int64) error {
	unlock, err := u.lockUser(ctx, userID, 10*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	return u.fsm.ResetState(ctx, userID)
}
func (u *interviewUsecase) StartRandomInterview(ctx context.Context, userID int64) (*domain.Question, error) {
	grade := "all"
	if u.userRepo != nil {
		if user, err := u.userRepo.GetUserByID(ctx, userID); err == nil && user != nil && user.TargetGrade != "" {
			grade = user.TargetGrade
		}
	}

	q, err := u.questions.GetRandomQuestion(ctx, grade)
	if err != nil {
		return nil, fmt.Errorf("ошибка выбора случайного вопроса: %w", err)
	}
	return u.StartInterview(ctx, userID, q.TopicID)
}

// ShowCurrentAnswer возвращает карточку текущего вопроса для показа эталона
func (u *interviewUsecase) ShowCurrentAnswer(ctx context.Context, userID int64) (*domain.Question, error) {
	unlock, err := u.lockUser(ctx, userID, 10*time.Second)
	if err != nil {
		return nil, err
	}
	defer unlock()

	state, exists, err := u.fsm.GetState(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения состояния: %w", err)
	}
	if !exists || state.State != domain.StateWaitingAnswer {
		return nil, ErrNoActiveSession
	}

	q, err := u.questions.GetQuestionByID(ctx, state.QuestionID)
	if err != nil {
		if errors.Is(err, domain.ErrQuestionNotFound) {
			_ = u.fsm.ResetState(ctx, userID)
			return nil, ErrNoActiveSession
		}
		return nil, err
	}
	return q, nil
}

// SubmitFlashcard фиксирует результат самопроверки и выдает следующий вопрос из адаптивной колоды
func (u *interviewUsecase) SubmitFlashcard(ctx context.Context, userID int64, knew bool) (*domain.Question, bool, error) {
	unlock, err := u.lockUser(ctx, userID, 15*time.Second)
	if err != nil {
		return nil, false, err
	}
	defer unlock()

	state, exists, err := u.fsm.GetState(ctx, userID)
	if err != nil {
		return nil, false, fmt.Errorf("ошибка чтения состояния: %w", err)
	}
	if !exists || state.State != domain.StateWaitingAnswer {
		return nil, false, ErrNoActiveSession
	}

	currentQ, err := u.questions.GetQuestionByID(ctx, state.QuestionID)
	if err != nil {
		if errors.Is(err, domain.ErrQuestionNotFound) {
			_ = u.fsm.ResetState(ctx, userID)
			return nil, false, ErrNoActiveSession
		}
		return nil, false, err
	}

	score := 3
	feedback := "Самопроверка (режим карточек): требуется повторение"
	if knew {
		score = 8
		feedback = "Самопроверка (режим карточек): ответ известен"
	}

	eval := domain.Evaluation{
		Score:     score,
		IsCorrect: knew,
		Feedback:  feedback,
	}

	// Сохраняем попытку в базу в отдельном контексте (HIGH-2)
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 6*time.Second)
	defer cancel()

	attempt := domain.AnswerAttempt{
		UserID:     userID,
		QuestionID: currentQ.ID,
		UserAnswer: "[Режим карточек: самопроверка]",
		IsVoice:    false,
		Eval:       eval,
	}
	if err := u.progress.SaveAttemptAndProgress(saveCtx, attempt, currentQ.TopicID); err != nil {
		slog.Error("не удалось сохранить самопроверку в PostgreSQL", "user_id", userID, "error", err)
	}

	// Адаптивная колода: пересчитываем очередь
	newQueue := u.deck.ReinsertQuestion(state.ActiveQueue, currentQ.ID, score)
	if len(newQueue) == 0 {
		_ = u.fsm.ResetState(saveCtx, userID)
		return nil, true, nil
	}

	nextQID := newQueue[0]
	state.QuestionID = nextQID
	state.ActiveQueue = newQueue[1:]
	state.State = domain.StateWaitingAnswer
	if err := u.fsm.SetState(saveCtx, userID, *state); err != nil {
		slog.Error("ошибка обновления FSM после самопроверки", "user_id", userID, "error", err)
	}

	nextQ, err := u.questions.GetQuestionByID(ctx, nextQID)
	if err != nil {
		return nil, false, err
	}
	return nextQ, false, nil
}

// SkipQuestion перемещает текущий вопрос в конец очереди и берет следующий
func (u *interviewUsecase) SkipQuestion(ctx context.Context, userID int64) (*domain.Question, error) {
	unlock, err := u.lockUser(ctx, userID, 10*time.Second)
	if err != nil {
		return nil, err
	}
	defer unlock()

	state, exists, err := u.fsm.GetState(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения состояния: %w", err)
	}
	if !exists || state.State != domain.StateWaitingAnswer {
		return nil, ErrNoActiveSession
	}

	if len(state.ActiveQueue) == 0 {
		return u.questions.GetQuestionByID(ctx, state.QuestionID)
	}

	currentQID := state.QuestionID
	nextQID := state.ActiveQueue[0]
	state.ActiveQueue = append(state.ActiveQueue[1:], currentQID)
	state.QuestionID = nextQID

	if err := u.fsm.SetState(ctx, userID, *state); err != nil {
		return nil, fmt.Errorf("ошибка обновления FSM: %w", err)
	}

	return u.questions.GetQuestionByID(ctx, nextQID)
}

// GetUserStats формирует сводную статистику прогресса пользователя
// GetUserStats формирует сводную статистику с динамическим расчетом готовности под выбранный грейд
func (u *interviewUsecase) GetUserStats(ctx context.Context, userID int64, grade string) (*domain.UserStats, error) {
	diffs := domain.GradeDifficulties(grade)

	topicCounts, totalGradeQuestions, err := u.questions.GetTopicQuestionCountsByGrade(ctx, diffs)
	if err != nil {
		return nil, fmt.Errorf("ошибка подсчета вопросов по темам: %w", err)
	}

	progressList, err := u.progress.GetUserGradeProgress(ctx, userID, diffs)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения прогресса пользователя: %w", err)
	}

	totalAnswered := len(progressList)
	masteredCount := 0
	totalScore := 0

	progressByTopic := make(map[string][]domain.QuestionProgress)
	for _, p := range progressList {
		progressByTopic[p.TopicID] = append(progressByTopic[p.TopicID], p)
		if p.LastScore >= 6 {
			masteredCount++
		}
		totalScore += p.LastScore
	}

	// Считаем общее число актуальных ошибок пользователя
	_, totalWeak, _ := u.questions.GetWeakTopics(ctx, userID)

	avgScore := 0.0
	if totalAnswered > 0 {
		avgScore = float64(totalScore) / float64(totalAnswered)
	}

	// Динамическая готовность строго под требования целевого грейда!
	readyPercent := 0
	if totalGradeQuestions > 0 {
		readyPercent = (masteredCount * 100) / totalGradeQuestions
		if readyPercent > 100 {
			readyPercent = 100
		}
	}

	var catStats []domain.CategoryStat
	for _, cat := range domain.Categories {
		catTotal := 0
		catMastered := 0
		for _, tid := range cat.TopicIDs {
			catTotal += topicCounts[tid]
			for _, p := range progressByTopic[tid] {
				if p.LastScore >= 6 {
					catMastered++
				}
			}
		}

		catPercent := 0
		if catTotal > 0 {
			catPercent = (catMastered * 100) / catTotal
		}

		catStats = append(catStats, domain.CategoryStat{
			CategoryID: cat.ID,
			Title:      cat.Title,
			TotalCount: catTotal,
			Mastered:   catMastered,
			Percent:    catPercent,
		})
	}

	return &domain.UserStats{
		TotalQuestions: totalGradeQuestions,
		AnsweredCount:  totalAnswered,
		MasteredCount:  masteredCount,
		WeakCount:      totalWeak,
		AverageScore:   avgScore,
		ReadyPercent:   readyPercent,
		TargetGrade:    grade,
		Categories:     catStats,
	}, nil
}

// GetWeakTopics возвращает список тем, где есть ошибки
func (u *interviewUsecase) GetWeakTopics(ctx context.Context, userID int64) ([]domain.WeakTopic, int, error) {
	return u.questions.GetWeakTopics(ctx, userID)
}

// StartWeakDrillByTopic запускает спринт по ошибкам в конкретно выбранной теме
func (u *interviewUsecase) StartWeakDrillByTopic(ctx context.Context, userID int64, topicID string) (*domain.Question, int, error) {
	unlock, err := u.lockUser(ctx, userID, 10*time.Second)
	if err != nil {
		return nil, 0, err
	}
	defer unlock()

	questions, err := u.questions.GetWeakQuestionsByTopic(ctx, userID, topicID, 15)
	if err != nil {
		return nil, 0, fmt.Errorf("ошибка загрузки слабых вопросов темы: %w", err)
	}
	if len(questions) == 0 {
		return nil, 0, nil
	}

	currentQ := questions[0]
	var activeQueue []int
	for _, q := range questions[1:] {
		activeQueue = append(activeQueue, q.ID)
	}

	state := domain.UserState{
		TopicID:     topicID,
		QuestionID:  currentQ.ID,
		ActiveQueue: activeQueue,
		State:       domain.StateWaitingAnswer,
	}

	if err := u.fsm.SetState(ctx, userID, state); err != nil {
		return nil, 0, fmt.Errorf("ошибка сохранения FSM: %w", err)
	}

	return &currentQ, len(questions), nil
}

// StartWeakDrill запускает спринт строго по вопросам с ошибками (last_score < 6)
func (u *interviewUsecase) StartWeakDrill(ctx context.Context, userID int64) (*domain.Question, int, error) {
	unlock, err := u.lockUser(ctx, userID, 10*time.Second)
	if err != nil {
		return nil, 0, err
	}
	defer unlock()

	questions, err := u.questions.GetWeakQuestions(ctx, userID, 15)
	if err != nil {
		return nil, 0, fmt.Errorf("ошибка загрузки слабых вопросов: %w", err)
	}
	if len(questions) == 0 {
		return nil, 0, nil
	}

	currentQ := questions[0]
	var activeQueue []int
	for _, q := range questions[1:] {
		activeQueue = append(activeQueue, q.ID)
	}

	state := domain.UserState{
		TopicID:     currentQ.TopicID,
		QuestionID:  currentQ.ID,
		ActiveQueue: activeQueue,
		State:       domain.StateWaitingAnswer,
	}

	if err := u.fsm.SetState(ctx, userID, state); err != nil {
		return nil, 0, fmt.Errorf("ошибка сохранения FSM для работы над ошибками: %w", err)
	}

	return &currentQ, len(questions), nil
}
func (u *interviewUsecase) ToggleTopicSelection(ctx context.Context, userID int64, topicID string) (bool, error) {
	return u.fsm.ToggleSelectedTopic(ctx, userID, topicID)
}

func (u *interviewUsecase) GetSelectedTopics(ctx context.Context, userID int64) ([]string, error) {
	return u.fsm.GetSelectedTopics(ctx, userID)
}

func (u *interviewUsecase) ClearSelectedTopics(ctx context.Context, userID int64) error {
	return u.fsm.ClearSelectedTopics(ctx, userID)
}

func (u *interviewUsecase) SelectAllCategoryTopics(ctx context.Context, userID int64, topicIDs []string) error {
	return u.fsm.SelectAllTopics(ctx, userID, topicIDs)
}

// StartMultiTopicInterview формирует единую колоду из нескольких тем с учетом грейда
func (u *interviewUsecase) StartMultiTopicInterview(ctx context.Context, userID int64, topicIDs []string, grade string) (*domain.Question, int, error) {
	unlock, err := u.lockUser(ctx, userID, 10*time.Second)
	if err != nil {
		return nil, 0, err
	}
	defer unlock()

	questions, err := u.questions.GetQuestionsByTopics(ctx, topicIDs, grade)
	if err != nil {
		return nil, 0, fmt.Errorf("ошибка получения вопросов: %w", err)
	}

	progressList, _ := u.progress.GetUserOverallProgress(ctx, userID)
	progressMap := make(map[int]domain.QuestionProgress)
	for _, p := range progressList {
		progressMap[p.QuestionID] = p
	}

	queue := u.deck.BuildInitialQueue(questions, progressMap)
	if len(queue) == 0 {
		return nil, 0, domain.ErrQuestionNotFound
	}

	currentQID := queue[0]
	activeQueue := queue[1:]
	state := domain.UserState{
		TopicID:     "multi_topics",
		QuestionID:  currentQID,
		ActiveQueue: activeQueue,
		State:       domain.StateWaitingAnswer,
	}

	if err := u.fsm.SetState(ctx, userID, state); err != nil {
		return nil, 0, fmt.Errorf("ошибка сохранения FSM: %w", err)
	}

	_ = u.fsm.ClearSelectedTopics(ctx, userID)

	currentQ, err := u.questions.GetQuestionByID(ctx, currentQID)
	if err != nil {
		return nil, 0, err
	}
	return currentQ, len(questions), nil
}

// SaveQuestionMsgID сохраняет ID сообщения с вопросом в Telegram для последующей очистки кнопок
func (u *interviewUsecase) SaveQuestionMsgID(ctx context.Context, userID int64, msgID int) error {
	return u.fsm.SaveQuestionMsgID(ctx, userID, msgID)
}

// GetQuestionMsgID возвращает ID сообщения с вопросом в Telegram
func (u *interviewUsecase) GetQuestionMsgID(ctx context.Context, userID int64) (int, error) {
	return u.fsm.GetQuestionMsgID(ctx, userID)
}
