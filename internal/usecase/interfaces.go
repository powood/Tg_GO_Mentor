package usecase

import (
	"context"
	"interview-bot/internal/domain"
	"io"
	"time"
)

type QuestionRepository interface {
	GetTopics(ctx context.Context) ([]domain.Topic, error)
	GetQuestionByTopic(ctx context.Context, topicID string, grade string) ([]domain.Question, error)
	GetQuestionsByTopics(ctx context.Context, topicIDs []string, grade string) ([]domain.Question, error)
	GetQuestionByID(ctx context.Context, id int) (*domain.Question, error)
	GetRandomQuestion(ctx context.Context, grade string) (*domain.Question, error)
	GetTopicQuestionCounts(ctx context.Context) (map[string]int, int, error)
	GetTopicQuestionCountsByGrade(ctx context.Context, diffs []string) (map[string]int, int, error)
	GetWeakQuestions(ctx context.Context, userID int64, limit int) ([]domain.Question, error)
	GetWeakQuestionsByTopic(ctx context.Context, userID int64, topicID string, limit int) ([]domain.Question, error)
	GetWeakTopics(ctx context.Context, userID int64) ([]domain.WeakTopic, int, error)
}

type ProgressRepository interface {
	GetUserProgressByTopic(ctx context.Context, userID int64, topicID string) (map[int]domain.QuestionProgress, error)
	GetUserOverallProgress(ctx context.Context, userID int64) ([]domain.QuestionProgress, error)
	GetUserGradeProgress(ctx context.Context, userID int64, diffs []string) ([]domain.QuestionProgress, error)
	SaveAttemptAndProgress(ctx context.Context, attempt domain.AnswerAttempt, topicID string) error
}

type UserRepository interface {
	UpsertUser(ctx context.Context, u domain.User) error
	GetUserByID(ctx context.Context, id int64) (*domain.User, error)
	SetUserGrade(ctx context.Context, id int64, grade string) error
}

type QuizRepository interface {
	GetRandomQuizQuestion(ctx context.Context, grade string, topicIDs []string) (*domain.QuizQuestion, error)
	GetQuizQuestionByID(ctx context.Context, id int) (*domain.QuizQuestion, error)
}

// FSMStorage — контракт для хранения активной сессии и очереди вопросов в Redis
type FSMStorage interface {
	SetState(ctx context.Context, userID int64, state domain.UserState) error
	GetState(ctx context.Context, userID int64) (*domain.UserState, bool, error)
	ResetState(ctx context.Context, userID int64) error
	AcquireLock(ctx context.Context, userID int64, ttl time.Duration) (string, bool, error)
	ReleaseLock(ctx context.Context, userID int64, token string) error
	AllowEval(ctx context.Context, userID int64) (bool, error)
	ToggleSelectedTopic(ctx context.Context, userID int64, topicID string) (bool, error)
	GetSelectedTopics(ctx context.Context, userID int64) ([]string, error)
	ClearSelectedTopics(ctx context.Context, userID int64) error
	SelectAllTopics(ctx context.Context, userID int64, topicIDs []string) error
	SaveQuestionMsgID(ctx context.Context, userID int64, msgID int) error
	GetQuestionMsgID(ctx context.Context, userID int64) (int, error)
}

// AIEvaluator — контракт для оценки ответа кандидата нейросетью
type AIEvaluator interface {
	Evaluate(ctx context.Context, q domain.Question, userAnswer string) (*domain.Evaluation, error)
}

// AIMockEvaluator — контракт для оценки в режиме Mock-интервью
type AIMockEvaluator interface {
	EvaluateMockAnswer(ctx context.Context, q domain.Question, userAnswer string) (*domain.MockEvalResponse, error)
	EvaluateMockFollowUp(ctx context.Context, q domain.Question, firstAnswer, followUpQ, secondAnswer string) (*domain.Evaluation, error)
	GenerateMockVerdict(ctx context.Context, grade string, history []domain.MockQuestionReport) (*domain.MockVerdict, error)
}

// AITranscriber — контракт для перевода аудио в текст (Whisper)
type AITranscriber interface {
	Transcribe(ctx context.Context, fetchAudio func(context.Context) (io.Reader, error), filename string) (string, error)
}

type InterviewUsecase interface {
	GetTopics(ctx context.Context) ([]domain.Topic, error)
	StartInterview(ctx context.Context, userID int64, topicID string) (*domain.Question, error)
	StartRandomInterview(ctx context.Context, userID int64) (*domain.Question, error)
	SubmitTextAnswer(ctx context.Context, userID int64, answerText string) (*domain.Evaluation, *domain.Question, bool, error)
	SubmitVoiceAnswer(ctx context.Context, userID int64, fetchAudio func(context.Context) (io.Reader, error), filename string) (string, *domain.Evaluation, *domain.Question, bool, error)
	GetCurrentQuestion(ctx context.Context, userID int64) (*domain.Question, error)
	NextQuestion(ctx context.Context, userID int64) (*domain.Question, error)
	StopInterview(ctx context.Context, userID int64) error
	// Методы для режима карточек (Flashcards)
	ShowCurrentAnswer(ctx context.Context, userID int64) (*domain.Question, error)
	SubmitFlashcard(ctx context.Context, userID int64, knew bool) (*domain.Question, bool, error)
	SkipQuestion(ctx context.Context, userID int64) (*domain.Question, error)
	// Профиль и работа над ошибками
	GetUserStats(ctx context.Context, userID int64, grade string) (*domain.UserStats, error)
	StartWeakDrill(ctx context.Context, userID int64) (*domain.Question, int, error)
	ToggleTopicSelection(ctx context.Context, userID int64, topicID string) (bool, error)
	GetSelectedTopics(ctx context.Context, userID int64) ([]string, error)
	ClearSelectedTopics(ctx context.Context, userID int64) error
	SelectAllCategoryTopics(ctx context.Context, userID int64, topicIDs []string) error
	StartMultiTopicInterview(ctx context.Context, userID int64, topicIDs []string, grade string) (*domain.Question, int, error)
	GetWeakTopics(ctx context.Context, userID int64) ([]domain.WeakTopic, int, error)
	StartWeakDrillByTopic(ctx context.Context, userID int64, topicID string) (*domain.Question, int, error)
	SaveQuestionMsgID(ctx context.Context, userID int64, msgID int) error
	GetQuestionMsgID(ctx context.Context, userID int64) (int, error)
}

type UserUsecase interface {
	RegisterUser(ctx context.Context, user domain.User) error
	GetUser(ctx context.Context, id int64) (*domain.User, error)
	SetGrade(ctx context.Context, id int64, grade string) error
}

type QuizUsecase interface {
	GetNextQuizQuestion(ctx context.Context, userID int64, filter string) (*domain.QuizQuestion, error)
	AnswerQuizQuestion(ctx context.Context, userID int64, questionID int, selectedOption string) (*domain.QuizQuestion, bool, error)
}

// MockInterviewUsecase — контракт для управления процессом мок-собеседования
type MockInterviewUsecase interface {
	StartMockInterview(ctx context.Context, userID int64, topicIDs []string, grade string) (*domain.Question, int, error)
	SubmitMockTextAnswer(ctx context.Context, userID int64, answerText string) (*domain.MockAnswerResult, error)
	SubmitMockVoiceAnswer(ctx context.Context, userID int64, fetchAudio func(context.Context) (io.Reader, error), filename string) (string, *domain.MockAnswerResult, error)
	StopMockInterview(ctx context.Context, userID int64) error
	FinishMockEarly(ctx context.Context, userID int64) (*domain.MockVerdict, error)
}

type transcriptCallbackKey struct{}
type evalStartCallbackKey struct{}

// WithTranscriptCallback прикрепляет к контексту колбэк, вызываемый сразу после распознавания голоса (до оценки LLM)
func WithTranscriptCallback(ctx context.Context, fn func(transcript string)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, transcriptCallbackKey{}, fn)
}

// NotifyTranscript вызывает колбэк распознанного текста, если он задан в контексте
func NotifyTranscript(ctx context.Context, transcript string) {
	if fn, ok := ctx.Value(transcriptCallbackKey{}).(func(string)); ok && fn != nil {
		fn(transcript)
	}
}

// WithEvalStartCallback прикрепляет к контексту колбэк, вызываемый при старте оценки ответа нейросетью
func WithEvalStartCallback(ctx context.Context, fn func(isMock bool)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, evalStartCallbackKey{}, fn)
}

// NotifyEvalStart уведомляет хендлер, что сессия валидна и началась оценка нейросетью
func NotifyEvalStart(ctx context.Context, isMock bool) {
	if fn, ok := ctx.Value(evalStartCallbackKey{}).(func(bool)); ok && fn != nil {
		fn(isMock)
	}
}
