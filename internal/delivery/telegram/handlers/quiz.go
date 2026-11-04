package handlers

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"

	"interview-bot/internal/delivery/telegram/view"
	"interview-bot/internal/usecase"
)

// StartQuiz открывает меню выбора формата блиц-тестирования
func StartQuiz(quizUC usecase.QuizUsecase, interviewUCs ...usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		sender := c.Sender()
		if sender == nil {
			return nil
		}

		if c.Callback() != nil {
			_ = c.Respond()
		}

		selectedCount := 0
		if len(interviewUCs) > 0 && interviewUCs[0] != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if selected, err := interviewUCs[0].GetSelectedTopics(ctx, sender.ID); err == nil {
				selectedCount = len(selected)
			}
		}

		msg := view.QuizStartMenuMessage()
		kb := view.QuizStartMenuKeyboard(selectedCount)

		if c.Callback() != nil {
			return c.Edit(msg, kb, tele.ModeHTML)
		}
		return c.Send(msg, kb, tele.ModeHTML)
	}
}

// StartQuizMix запускает случайный блиц со всеми темами
func StartQuizMix(quizUC usecase.QuizUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		if c.Callback() != nil {
			_ = c.Respond()
		}
		return sendQuizQuestion(c, quizUC, "mix")
	}
}

// StartQuizCat запускает блиц по выбранному разделу
func StartQuizCat(quizUC usecase.QuizUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		if c.Callback() != nil {
			_ = c.Respond()
		}
		catID := c.Data()
		return sendQuizQuestion(c, quizUC, "cat:"+catID)
	}
}

// StartQuizSprint запускает блиц по темам, выбранным чекбоксами в разделе
func StartQuizSprint(quizUC usecase.QuizUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		if c.Callback() != nil {
			_ = c.Respond()
		}
		return sendQuizQuestion(c, quizUC, "sprint")
	}
}

// AnswerQuiz проверяет выбранный вариант ответа мгновенно и выводит детальный разбор
func AnswerQuiz(quizUC usecase.QuizUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		sender := c.Sender()
		if sender == nil {
			return nil
		}

		parts := strings.Split(c.Data(), "|")
		if len(parts) < 2 {
			return nil
		}

		qID, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil
		}
		selectedOpt := strings.TrimSpace(parts[1])

		filter := "mix"
		if len(parts) >= 3 && parts[2] != "" {
			filter = parts[2]
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		q, isCorrect, err := quizUC.AnswerQuizQuestion(ctx, sender.ID, qID, selectedOpt)
		if err != nil {
			slog.Error("ошибка проверки ответа квиза", "user_id", sender.ID, "question_id", qID, "error", err)
			return c.Send("⚠️ Не удалось проверить ответ на вопрос.")
		}

		msg := view.QuizResultMessage(q, selectedOpt, isCorrect)
		kb := view.QuizResultKeyboard(filter)

		return c.Edit(msg, kb, tele.ModeHTML)
	}
}

// NextQuizQuestion выдает следующий вопрос блиц-теста по кнопке с сохранением фильтра
func NextQuizQuestion(quizUC usecase.QuizUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		filter := c.Data()
		if filter == "" {
			filter = "mix"
		}
		return sendQuizQuestion(c, quizUC, filter)
	}
}

func sendQuizQuestion(c tele.Context, quizUC usecase.QuizUsecase, filter string) error {
	sender := c.Sender()
	if sender == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	q, err := quizUC.GetNextQuizQuestion(ctx, sender.ID, filter)
	if err != nil {
		slog.Error("ошибка получения вопроса квиза", "user_id", sender.ID, "filter", filter, "error", err)
		return c.Send("❌ Не удалось загрузить вопрос. Попробуй выбрать другой раздел или режим.")
	}

	msg := view.QuizQuestionMessage(q)
	kb := view.QuizOptionsKeyboard(q.ID, q.Options, filter)

	if c.Callback() != nil {
		return c.Edit(msg, kb, tele.ModeHTML)
	}
	return c.Send(msg, kb, tele.ModeHTML)
}
