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

// MockMenu отображает главное меню запуска мок-собеседования
func MockMenu(userUC usecase.UserUsecase, interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
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
		msg := view.MockMenuMessage(grade, len(selectedTopics))
		kb := view.MockMenuKeyboard(len(selectedTopics))

		if c.Callback() != nil {
			return c.Edit(msg, kb, tele.ModeHTML)
		}
		return c.Send(msg, kb, tele.ModeHTML)
	}
}

// StartMockMix запускает мок-собеседование со случайным миксом вопросов по всем темам
func StartMockMix(userUC usecase.UserUsecase, mockUC usecase.MockInterviewUsecase, interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		if c.Sender() == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		user, _ := userUC.GetUser(ctx, c.Sender().ID)
		grade := "junior"
		if user != nil && user.TargetGrade != "" {
			grade = user.TargetGrade
		}

		q, total, err := mockUC.StartMockInterview(ctx, c.Sender().ID, nil, grade)
		if err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Дождись окончания проверки предыдущего ответа.")
			}
			slog.Error("ошибка старта mock mix", "user_id", c.Sender().ID, "error", err)
			return c.Send("⚠️ Не удалось подобрать вопросы для собеседования. Попробуй позже.")
		}

		msg := view.MockQuestionMessage(q, 1, total)
		kb := view.MockQuestionKeyboard()

		if c.Message() != nil {
			_, _ = c.Bot().EditReplyMarkup(c.Message(), nil)
		}

		sent, err := c.Bot().Send(c.Chat(), msg, kb, tele.ModeHTML)
		if err == nil && sent != nil {
			saveCtx, saveCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer saveCancel()
			_ = interviewUC.SaveQuestionMsgID(saveCtx, c.Sender().ID, sent.ID)
		}
		return err
	}
}

// StartMockCustom запускает мок-собеседование по темам, выбранным пользователем чекбоксами
func StartMockCustom(userUC usecase.UserUsecase, mockUC usecase.MockInterviewUsecase, interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		if c.Sender() == nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		selectedTopics, _ := interviewUC.GetSelectedTopics(ctx, c.Sender().ID)
		if len(selectedTopics) == 0 {
			return c.Send("⚠️ Сначала выберите хотя бы одну тему чекбоксами в разделах каталога.")
		}

		user, _ := userUC.GetUser(ctx, c.Sender().ID)
		grade := "junior"
		if user != nil && user.TargetGrade != "" {
			grade = user.TargetGrade
		}

		q, total, err := mockUC.StartMockInterview(ctx, c.Sender().ID, selectedTopics, grade)
		if err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Дождись окончания проверки предыдущего ответа.")
			}
			slog.Error("ошибка старта mock custom", "user_id", c.Sender().ID, "error", err)
			return c.Send("⚠️ В выбранных темах недостаточно вопросов под твой грейд. Выбери больше тем.")
		}

		msg := view.MockQuestionMessage(q, 1, total)
		kb := view.MockQuestionKeyboard()

		if c.Message() != nil {
			_, _ = c.Bot().EditReplyMarkup(c.Message(), nil)
		}

		sent, err := c.Bot().Send(c.Chat(), msg, kb, tele.ModeHTML)
		if err == nil && sent != nil {
			saveCtx, saveCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer saveCancel()
			_ = interviewUC.SaveQuestionMsgID(saveCtx, c.Sender().ID, sent.ID)
		}
		return err
	}
}

// StartMockCategory запускает мок-собеседование по всем темам выбранного раздела
func StartMockCategory(userUC usecase.UserUsecase, mockUC usecase.MockInterviewUsecase, interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		if c.Sender() == nil {
			return nil
		}
		catID := c.Data()
		cat, ok := domain.GetCategory(catID)
		if !ok || len(cat.TopicIDs) == 0 {
			return c.Send("⚠️ Не удалось определить темы раздела.")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		user, _ := userUC.GetUser(ctx, c.Sender().ID)
		grade := "junior"
		if user != nil && user.TargetGrade != "" {
			grade = user.TargetGrade
		}

		q, total, err := mockUC.StartMockInterview(ctx, c.Sender().ID, cat.TopicIDs, grade)
		if err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Дождись окончания проверки предыдущего ответа.")
			}
			slog.Error("ошибка старта mock category", "user_id", c.Sender().ID, "cat_id", catID, "error", err)
			return c.Send("⚠️ В этом разделе недостаточно вопросов под твой грейд.")
		}

		msg := view.MockQuestionMessage(q, 1, total)
		kb := view.MockQuestionKeyboard()

		if c.Message() != nil {
			_, _ = c.Bot().EditReplyMarkup(c.Message(), nil)
		}

		sent, err := c.Bot().Send(c.Chat(), msg, kb, tele.ModeHTML)
		if err == nil && sent != nil {
			saveCtx, saveCancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer saveCancel()
			_ = interviewUC.SaveQuestionMsgID(saveCtx, c.Sender().ID, sent.ID)
		}
		return err
	}
}

// StopMock завершает активное мок-собеседование (и выдает вердикт, если были ответы)
func StopMock(mockUC usecase.MockInterviewUsecase, interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond(&tele.CallbackResponse{Text: "Завершаем собеседование..."})
		if c.Sender() == nil {
			return nil
		}

		var statusMsg *tele.Message
		if c.Message() != nil {
			edited, err := c.Bot().Edit(c.Message(), "⏳ <i>Подвожу итоги собеседования и формирую вердикт...</i>", tele.ModeHTML)
			if err == nil {
				statusMsg = edited
			} else {
				_, _ = c.Bot().EditReplyMarkup(c.Message(), nil)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()

		verdict, err := mockUC.FinishMockEarly(ctx, c.Sender().ID)
		if err == nil && verdict != nil {
			vMsg := view.MockVerdictMessage(verdict)
			vKb := view.MockVerdictKeyboard(verdict.Verdict, verdict.AverageScore)
			if statusMsg != nil {
				if _, editErr := c.Bot().Edit(statusMsg, vMsg, vKb, tele.ModeHTML); editErr == nil {
					return nil
				}
			}
			return c.Send(vMsg, vKb, tele.ModeHTML)
		}

		kb := view.MainMenuKeyboard()
		stopText := "🛑 <b>Mock-собеседование прервано.</b>\n\nВыбери режим подготовки:"
		if statusMsg != nil {
			if _, editErr := c.Bot().Edit(statusMsg, stopText, kb, tele.ModeHTML); editErr == nil {
				return nil
			}
		}
		return c.Send(stopText, kb, tele.ModeHTML)
	}
}
