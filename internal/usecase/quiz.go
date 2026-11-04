package usecase

import (
	"context"
	"strings"

	"interview-bot/internal/domain"
)

type quizUsecase struct {
	quizRepo   QuizRepository
	userRepo   UserRepository
	fsmStorage FSMStorage
}

func NewQuizUsecase(quizRepo QuizRepository, userRepo UserRepository, fsmStorage FSMStorage) QuizUsecase {
	return &quizUsecase{
		quizRepo:   quizRepo,
		userRepo:   userRepo,
		fsmStorage: fsmStorage,
	}
}

func (u *quizUsecase) GetNextQuizQuestion(ctx context.Context, userID int64, filter string) (*domain.QuizQuestion, error) {
	grade := "junior"
	user, err := u.userRepo.GetUserByID(ctx, userID)
	if err == nil && user != nil && user.TargetGrade != "" {
		grade = user.TargetGrade
	}

	var topicIDs []string

	filter = strings.TrimSpace(filter)
	switch {
	case filter == "" || filter == "mix" || filter == "all":
		// Без фильтра по темам — случайный вопрос из всей базы
		topicIDs = nil

	case filter == "sprint":
		// Темы, отмеченные пользователем галочками в текущем разделе
		selectedTopics, _ := u.fsmStorage.GetSelectedTopics(ctx, userID)
		topicIDs = selectedTopics

	case strings.HasPrefix(filter, "cat:") || strings.HasPrefix(filter, "cat|"):
		catID := filter[4:]
		if cat, ok := domain.GetCategory(catID); ok {
			topicIDs = cat.TopicIDs
		}

	case strings.HasPrefix(filter, "topic:") || strings.HasPrefix(filter, "topic|"):
		topicID := filter[6:]
		topicIDs = []string{topicID}

	default:
		// Если передан ID категории напрямую
		if cat, ok := domain.GetCategory(filter); ok {
			topicIDs = cat.TopicIDs
		} else {
			// Иначе трактуем как единичный topic_id
			topicIDs = []string{filter}
		}
	}

	return u.quizRepo.GetRandomQuizQuestion(ctx, grade, topicIDs)
}

func (u *quizUsecase) AnswerQuizQuestion(ctx context.Context, userID int64, questionID int, selectedOption string) (*domain.QuizQuestion, bool, error) {
	q, err := u.quizRepo.GetQuizQuestionByID(ctx, questionID)
	if err != nil {
		return nil, false, err
	}

	isCorrect := strings.EqualFold(strings.TrimSpace(selectedOption), strings.TrimSpace(q.CorrectOption))
	return q, isCorrect, nil
}
