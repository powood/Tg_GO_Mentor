package domain

import "time"

type State string

const (
	StateIdle          State = "idle"           // В главном меню, вопрос не задан
	StateWaitingAnswer State = "waiting_answer" // Ожидаем ответ (текст или голос) на вопрос
	StateEvaluating    State = "evaluating"     // ИИ прямо сейчас оценивает ответ
	StateWaitingNext   State = "waiting_next"   // Показана оценка, ожидаем нажатия 'Другой вопрос'
)

type SessionMode string

const (
	SessionModePractice SessionMode = "practice" // Обычная тренировка (по умолчанию)
	SessionModeMock     SessionMode = "mock"     // Mock-интервью с follow-up и вердиктом
)

type UserState struct {
	State       State           `json:"state"`                  // Текущее состояние
	TopicID     string          `json:"topic_id"`               // Выбранная тема
	QuestionID  int             `json:"question_id"`            // ID текущего вопроса
	ActiveQueue []int           `json:"active_queue"`           // Оставшаяся очередь вопросов в этой сессии
	UpdatedAt   time.Time       `json:"updated_at"`             // Время последнего изменения
	Mode        SessionMode     `json:"mode,omitempty"`        // Режим сессии (practice или mock)
	MockRound   *MockRoundState `json:"mock_round,omitempty"`   // Состояние раунда мок-собеседования
}

// IsAnswering проверяет, ждет ли бот сейчас ответ от пользователя
func (s *UserState) IsAnswering() bool {
	return s.State == StateWaitingAnswer
}

// IsMock проверяет, запущено ли активное мок-собеседование
func (s *UserState) IsMock() bool {
	return s.Mode == SessionModeMock && s.MockRound != nil
}
