package session

import "sync"

type UserState struct {
	QuestionID  int
	TopicID     string
	IsAnswering bool
}

type Manager struct {
	mu     sync.RWMutex
	states map[int64]*UserState
}

func NewManager() *Manager {
	return &Manager{
		states: make(map[int64]*UserState),
	}
}

func (m *Manager) SetAnswering(userID int64, topicID string, questionID int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[userID] = &UserState{
		QuestionID:  questionID,
		TopicID:     topicID,
		IsAnswering: true,
	}
}
func (m *Manager) ReadState(userID int64) (UserState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, ok := m.states[userID]
	if !ok {
		return UserState{}, false
	}
	return *state, true
}
func (m *Manager) ClearState(userID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.states, userID)
}
