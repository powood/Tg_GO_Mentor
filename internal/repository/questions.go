package repository

import (
	"errors"
	"interview-bot/internal/models"
	"math/rand"
	"time"
)

var (
	ErrQuestionNotFound = errors.New("вопрос не найден")
	ErrTopicNotFound    = errors.New("тема не найдена")
)

type QuestionRepository interface {
	GetTopics() ([]models.Topic, error)
	GetTopicByID(id string) (models.Topic, error)
	GetQuestionsByTopicID(topicID string) ([]models.Question, error)
	GetRandomQuestion(topicID string) (*models.Question, error)
	GetQuestionByID(id int) (*models.Question, error)
}
type MemoryQuestionRepository struct {
	topics    []models.Topic
	questions []models.Question
	rnd       *rand.Rand
}

// NewMemoryQuestionRepository создает репозиторий и заполняет его стартовыми вопросами
func NewMemoryQuestionRepository() *MemoryQuestionRepository {
	repo := &MemoryQuestionRepository{
		rnd: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	repo.seedData()
	return repo
}

// seedData наполняет репозиторий реальными вопросами по Go
func (r *MemoryQuestionRepository) seedData() {
	r.topics = []models.Topic{
		{
			ID:          "slices",
			Title:       "Слайсы и массивы",
			Description: "Устройство слайса, заголовок, len, cap, аллокации и append",
		},
		{
			ID:          "concurrency",
			Title:       "Конкурентность (Goroutines & Channels)",
			Description: "Горутины, планировщик GMP, каналы, sync.Mutex и race condition",
		},
		{
			ID:          "interfaces",
			Title:       "Интерфейсы и структуры",
			Description: "Внутреннее устройство интерфейсов (iface, eface), nil-интерфейс",
		},
	}
	r.questions = []models.Question{
		// Тема: Слайсы
		{
			ID:         1,
			TopicID:    "slices",
			Difficulty: models.Junior,
			Text:       "Что представляет собой срез (slice) под капотом в Go? Из каких полей состоит его структура?",
			Answer:     "Срез в Go — это структура из трех полей (заголовок слайса / SliceHeader): 1. Указатель на базовый массив (Data unsafe.Pointer); 2. Длина слайса (Len int); 3. Вместимость слайса (Cap int). Сам срез не хранит данные, он лишь ссылается на сегмент массива.",
			KeyPoints:  []string{"указатель на массив", "длина len", "вместимость cap", "ссылается на массив", "slice header"},
		},
		{
			ID:         2,
			TopicID:    "slices",
			Difficulty: models.Middle,
			Text:       "Что происходит со слайсом при вызове функции append, если длина (len) достигает вместимости (cap)?",
			Answer:     "Если cap недостаточно, Go аллоцирует новый, более емкий массив в памяти, копирует туда старые элементы, добавляет новый и возвращает новый срез с обновленным указателем. До Go 1.18 емкость удваивалась до 1024, затем росла на ~25%. Начиная с Go 1.18 рост более плавный (порог 256, формула 1.25x + 3/4).",
			KeyPoints:  []string{"аллокация нового массива", "копирование элементов", "рост вместимости", "увеличение cap"},
		},
		// Тема: Конкурентность
		{
			ID:         3,
			TopicID:    "concurrency",
			Difficulty: models.Junior,
			Text:       "Что произойдет при чтении из закрытого канала? А при записи в закрытый канал?",
			Answer:     "При чтении из закрытого канала: если в нем остались данные, они будут прочитаны. Когда канал пуст, чтение возвращает дефолтное нулевое значение типа и false вторым параметром (val, ok := <-ch). При записи в закрытый канал всегда возникает panic.",
			KeyPoints:  []string{"запись вызывает panic", "чтение возвращает дефолтное значение", "второй флаг false", "паника при записи"},
		},
		{
			ID:         4,
			TopicID:    "concurrency",
			Difficulty: models.Middle,
			Text:       "Что такое состояние гонки (data race) и как встроенными средствами Go его обнаружить?",
			Answer:     "Состояние гонки возникает, когда две или более горутины одновременно обращаются к одной области памяти, и хотя бы одно из обращений — это запись, без должной синхронизации. В Go обнаружить гонку можно с помощью флага -race при запуске тестов или сборке (go test -race, go run -race).",
			KeyPoints:  []string{"одновременный доступ к памяти", "хотя бы одна запись", "флаг -race", "race detector"},
		},
	}
}
func (r *MemoryQuestionRepository) GetTopics() []models.Topic {
	return r.topics
}
func (r *MemoryQuestionRepository) GetTopicByID(id string) (*models.Topic, error) {
	for _, t := range r.topics {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, ErrTopicNotFound
}
func (r *MemoryQuestionRepository) GetQuestionsByTopic(topicID string) []models.Question {
	var res []models.Question
	for _, q := range r.questions {
		if q.TopicID == topicID {
			res = append(res, q)
		}
	}
	return res
}
func (r *MemoryQuestionRepository) GetRandomQuestion(topicID string) (*models.Question, error) {
	qs := r.GetQuestionsByTopic(topicID)
	if len(qs) == 0 {
		return nil, ErrQuestionNotFound
	}
	idx := r.rnd.Intn(len(qs))
	return &qs[idx], nil
}
func (r *MemoryQuestionRepository) GetQuestionByID(id int) (*models.Question, error) {
	for _, q := range r.questions {
		if q.ID == id {
			return &q, nil
		}
	}
	return nil, ErrQuestionNotFound
}
