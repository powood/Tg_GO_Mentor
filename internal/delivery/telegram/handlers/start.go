package handlers

import (
	"context"
	"errors"
	"log/slog"
	"time"

	tele "gopkg.in/telebot.v3"

	"interview-bot/internal/delivery/telegram/view"
	"interview-bot/internal/domain"
	"interview-bot/internal/usecase"
)

// Start обрабатывает команду /start
func Start(userUC usecase.UserUsecase, interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		sender := c.Sender()
		if sender == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = userUC.RegisterUser(ctx, domain.User{
			ID:        sender.ID,
			Username:  sender.Username,
			FirstName: sender.FirstName,
		})

		if err := interviewUC.StopInterview(ctx, sender.ID); err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Предыдущий ответ ещё проверяется, подожди пару секунд и нажми /start заново.")
			}
		}

		msg := view.WelcomeMessage(sender.FirstName)
		kb := view.MainMenuKeyboard()

		_ = c.Send("🚀 Меню быстрого доступа закреплено внизу клавиатуры.", view.MainReplyKeyboard())
		return c.Send(msg, kb, tele.ModeHTML)
	}
}

// MainMenu сбрасывает текущую сессию и возвращает в главное меню с 3 режимами
func MainMenu(userUC usecase.UserUsecase, interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		if c.Callback() != nil {
			_ = c.Respond()
		}
		sender := c.Sender()
		if sender == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := interviewUC.StopInterview(ctx, sender.ID); err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Предыдущий ответ ещё проверяется нейросетью. Подожди пару секунд.")
			}
			slog.Warn("ошибка при сбросе интервью в MainMenu", "user_id", sender.ID, "error", err)
		}

		msg := view.WelcomeMessage(sender.FirstName)
		kb := view.MainMenuKeyboard()

		if c.Callback() != nil {
			return c.Edit(msg, kb, tele.ModeHTML)
		}
		return c.Send(msg, kb, tele.ModeHTML)
	}
}

// TrainingMenu открывает меню режима классической тренировки
func TrainingMenu(userUC usecase.UserUsecase, interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		if c.Callback() != nil {
			_ = c.Respond()
		}
		if c.Sender() == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		user, _ := userUC.GetUser(ctx, c.Sender().ID)
		grade := "junior"
		if user != nil && user.TargetGrade != "" {
			grade = user.TargetGrade
		}

		selectedTopics, _ := interviewUC.GetSelectedTopics(ctx, c.Sender().ID)
		msg := view.TrainingMenuMessage(grade, len(selectedTopics))
		kb := view.TrainingMenuKeyboard(len(selectedTopics))

		if c.Callback() != nil {
			return c.Edit(msg, kb, tele.ModeHTML)
		}
		return c.Send(msg, kb, tele.ModeHTML)
	}
}
