package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	tele "gopkg.in/telebot.v3"

	"interview-bot/internal/delivery/telegram/view"
	"interview-bot/internal/usecase"
)

// Profile отображает статистику подготовки пользователя
func Profile(userUC usecase.UserUsecase, interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		sender := c.Sender()
		if sender == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		user, _ := userUC.GetUser(ctx, sender.ID)
		grade := "all"
		if user != nil && user.TargetGrade != "" {
			grade = user.TargetGrade
		}

		stats, err := interviewUC.GetUserStats(ctx, sender.ID, grade)
		if err != nil {
			slog.Error("ошибка получения статистики профиля", "user_id", sender.ID, "error", err)
			return c.Send("⚠️ Не удалось загрузить статистику профиля.")
		}

		msg := view.ProfileMessage(user, stats)
		kb := view.ProfileKeyboard(stats.WeakCount)

		if c.Callback() != nil {
			return c.Edit(msg, kb, tele.ModeHTML)
		}
		return c.Send(msg, kb, tele.ModeHTML)
	}
}

// ChangeGrade открывает меню выбора целевого грейда
func ChangeGrade() tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		return c.Edit(view.GradeSelectMessage(), view.GradeSelectorKeyboard(), tele.ModeHTML)
	}
}

// SetGrade фиксирует выбранный грейд и возвращает обновленный профиль
func SetGrade(userUC usecase.UserUsecase, interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		grade := c.Data()
		if grade == "" {
			grade = "all"
		}

		toastMap := map[string]string{
			"intern": "Грейд установлен: 🐣 Intern",
			"junior": "Грейд установлен: 🟢 Junior",
			"middle": "Грейд установлен: 🟡 Middle",
			"senior": "Грейд установлен: 🔴 Senior",
			"all":    "Грейд установлен: 🌐 Все уровни",
		}
		toast := toastMap[grade]
		if toast == "" {
			toast = "Грейд обновлен"
		}
		_ = c.Respond(&tele.CallbackResponse{Text: toast})

		sender := c.Sender()
		if sender == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := userUC.SetGrade(ctx, sender.ID, grade); err != nil {
			slog.Error("ошибка сохранения грейда", "user_id", sender.ID, "grade", grade, "error", err)
			return c.Send("⚠️ Не удалось обновить грейд.")
		}

		user, _ := userUC.GetUser(ctx, sender.ID)
		stats, err := interviewUC.GetUserStats(ctx, sender.ID, grade)
		if err != nil {
			return c.Send("✅ Грейд обновлен! Нажми /profile")
		}

		msg := view.ProfileMessage(user, stats)
		kb := view.ProfileKeyboard(stats.WeakCount)
		return c.Edit(msg, kb, tele.ModeHTML)
	}
}

// WeakMenu открывает список тем, в которых есть ошибки
func WeakMenu(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		if c.Sender() == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		topics, totalWeak, err := interviewUC.GetWeakTopics(ctx, c.Sender().ID)
		if err != nil || totalWeak == 0 || len(topics) == 0 {
			return c.Send("🎉 У тебя нет вопросов с низким баллом! Отличная работа. Выбери тему в /start для продолжения.")
		}

		msg := view.WeakTopicsMessage(totalWeak)
		kb := view.WeakTopicsKeyboard(topics, totalWeak)
		return c.Edit(msg, kb, tele.ModeHTML)
	}
}

// WeakDrillTopic запускает работу над ошибками по конкретной теме
func WeakDrillTopic(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		if c.Sender() == nil {
			return nil
		}
		topicID := c.Data()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		q, count, err := interviewUC.StartWeakDrillByTopic(ctx, c.Sender().ID, topicID)
		if err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Дождись окончания проверки предыдущего ответа.")
			}
			slog.Error("ошибка старта работы над ошибками по теме", "user_id", c.Sender().ID, "error", err)
			return c.Send("⚠️ Не удалось запустить тренировку.")
		}

		if count == 0 || q == nil {
			return c.Send("🎉 По этой теме больше нет ошибок! Выбери другую тему.")
		}

		introMsg := fmt.Sprintf("🔥 <b>Работа над ошибками:</b> найдено вопросов в этой теме: <b>%d</b>.\nПоехали!", count)
		_ = c.Send(introMsg, tele.ModeHTML)

		msg := view.QuestionMessage(q)
		kb := view.QuestionKeyboard()
		sent, sendErr := c.Bot().Send(c.Chat(), msg, kb, tele.ModeHTML)
		if sendErr == nil && sent != nil {
			_ = interviewUC.SaveQuestionMsgID(ctx, c.Sender().ID, sent.ID)
		}
		return sendErr
	}
}

// WeakDrill запускает тренировку по всем ошибкам сразу
func WeakDrill(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		sender := c.Sender()
		if sender == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		q, count, err := interviewUC.StartWeakDrill(ctx, sender.ID)
		if err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Подожди пару секунд, предыдущий ответ ещё проверяется.")
			}
			slog.Error("ошибка старта работы над ошибками", "user_id", sender.ID, "error", err)
			return c.Send("⚠️ Не удалось запустить тренировку.")
		}

		if count == 0 || q == nil {
			return c.Send("🎉 У тебя нет вопросов с низким баллом! Отличная работа. Выбери тему в /start для продолжения.")
		}

		introMsg := fmt.Sprintf("🔥 <b>Работа над ошибками:</b> найдено вопросов для повторения: <b>%d</b>.\nПоехали!", count)
		_ = c.Send(introMsg, tele.ModeHTML)

		msg := view.QuestionMessage(q)
		kb := view.QuestionKeyboard()
		sent, sendErr := c.Bot().Send(c.Chat(), msg, kb, tele.ModeHTML)
		if sendErr == nil && sent != nil {
			_ = interviewUC.SaveQuestionMsgID(ctx, c.Sender().ID, sent.ID)
		}
		return sendErr
	}
}
