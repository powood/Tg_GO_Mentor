package domain

import "sort"

type DeckManager struct{}

func NewDeckManager() *DeckManager {
	return &DeckManager{}
}

func (d *DeckManager) BuildInitialQueue(questions []Question, progress map[int]QuestionProgress) []int {
	type item struct {
		id       int
		priority int
	}
	items := make([]item, 0, len(questions))
	for _, q := range questions {
		p, exists := progress[q.ID]
		priority := 0
		if !exists || p.Attempts == 0 {
			priority = 50
		} else if p.LastScore < 6 {
			priority = 100 + (6-p.LastScore)*10
		} else if p.LastScore < 8 {
			priority = 30
		} else {
			priority = 10
		}
		items = append(items, item{id: q.ID, priority: priority})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].priority > items[j].priority
	})
	result := make([]int, len(items))
	for i, it := range items {
		result[i] = it.id
	}
	return result
}

func (d *DeckManager) ReinsertQuestion(remainingQueue []int, questionID int, score int) []int {
	n := len(remainingQueue)

	// Если кандидат освоил вопрос (>= 8 баллов), убираем его из текущего спринта
	if score >= 8 {
		result := make([]int, n)
		copy(result, remainingQueue)
		return result
	}

	if n == 0 {
		return []int{questionID}
	}

	var insertIdx int
	if score < 6 {
		insertIdx = 3
		if insertIdx > n {
			insertIdx = n
		}
	} else {
		insertIdx = n / 2
		if insertIdx == 0 {
			insertIdx = 1
		}
	}

	result := make([]int, 0, n+1)
	result = append(result, remainingQueue[:insertIdx]...)
	result = append(result, questionID)
	result = append(result, remainingQueue[insertIdx:]...)
	return result
}
