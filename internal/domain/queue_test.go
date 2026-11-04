package domain

import (
	"slices"
	"testing"
)

func TestBuildInitialQueue(t *testing.T) {
	manager := NewDeckManager()
	questions := []Question{
		{ID: 1, TopicID: "slices", Text: "Новый вопрос"},
		{ID: 2, TopicID: "slices", Text: "Заваленный вопрос"},
		{ID: 3, TopicID: "slices", Text: "Выученный вопрос"},
	}
	progress := map[int]QuestionProgress{
		2: {QuestionID: 2, Attempts: 2, LastScore: 3}, // Завалил
		3: {QuestionID: 3, Attempts: 5, LastScore: 9}, // Отлично знаем
	}
	expected := []int{2, 1, 3}
	actual := manager.BuildInitialQueue(questions, progress)
	if !slices.Equal(actual, expected) {
		t.Errorf("Очередь сформирована неверно!\nОжидали: %v\nПолучили: %v", expected, actual)
	}
}

func TestReinsertQuestion(t *testing.T) {
	manager := NewDeckManager()
	queue := []int{10, 20, 30, 40}
	tests := []struct {
		name       string
		queue      []int
		questionID int
		score      int
		expected   []int
	}{
		{
			name:       "Завалил (score 3) -> вставка на позицию 3",
			queue:      queue,
			questionID: 99,
			score:      3,
			expected:   []int{10, 20, 30, 99, 40},
		},
		{
			name:       "Средне (score 7) -> вставка в середину (индекс 2)",
			queue:      queue,
			questionID: 99,
			score:      7,
			expected:   []int{10, 20, 99, 30, 40},
		},
		{
			name:       "Средне при 1 оставшемся вопросе -> после него, без повтора подряд",
			queue:      []int{10},
			questionID: 99,
			score:      7,
			expected:   []int{10, 99},
		},
		{
			name:       "Освоил (score 8+) -> вопрос уходит из колоды спринта",
			queue:      queue,
			questionID: 99,
			score:      8,
			expected:   []int{10, 20, 30, 40},
		},
		{
			name:       "Освоил последний вопрос -> пустая очередь (финал темы!)",
			queue:      []int{},
			questionID: 99,
			score:      10,
			expected:   []int{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := manager.ReinsertQuestion(tt.queue, tt.questionID, tt.score)
			if !slices.Equal(actual, tt.expected) {
				t.Errorf("Сценарий '%s' провален!\nОжидали: %v\nПолучили: %v", tt.name, tt.expected, actual)
			}
		})
	}
}
