package domain

import "errors"

var (
	ErrQuestionNotFound = errors.New("вопрос не найден")
	ErrTopicNotFound    = errors.New("тема не найдена")
	ErrUserNotFound     = errors.New("пользователь не найден")
)
