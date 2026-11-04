package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"

	"interview-bot/internal/delivery/telegram/view"
	"interview-bot/internal/domain"
	"interview-bot/internal/usecase"
)

// CategorySelect открывает список тем выбранного раздела
// CategorySelect открывает список тем выбранного раздела
func CategorySelect(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		catID := c.Data()
		cat, ok := domain.GetCategory(catID)
		if !ok {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		topics, err := interviewUC.GetTopics(ctx)
		if err != nil {
			return nil
		}

		selectedList, _ := interviewUC.GetSelectedTopics(ctx, c.Sender().ID)
		selectedMap := make(map[string]bool)
		for _, id := range selectedList {
			selectedMap[id] = true
		}

		filtered := cat.FilterTopics(topics)
		msg := view.CategoryHeaderMessage(cat, len(filtered))
		kb := view.CategoryTopicsKeyboard(cat, filtered, selectedMap, 0)

		return c.Edit(msg, kb, tele.ModeHTML)
	}
}

// CategoryPage переключает страницы тем внутри раздела
func CategoryPage(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		data := strings.Split(c.Data(), "|")
		if len(data) < 2 {
			return nil
		}
		catID := data[0]
		page, _ := strconv.Atoi(data[1])

		cat, ok := domain.GetCategory(catID)
		if !ok {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		topics, err := interviewUC.GetTopics(ctx)
		if err != nil {
			return nil
		}

		selectedList, _ := interviewUC.GetSelectedTopics(ctx, c.Sender().ID)
		selectedMap := make(map[string]bool)
		for _, id := range selectedList {
			selectedMap[id] = true
		}

		filtered := cat.FilterTopics(topics)
		msg := view.CategoryHeaderMessage(cat, len(filtered))
		kb := view.CategoryTopicsKeyboard(cat, filtered, selectedMap, page)

		return c.Edit(msg, kb, tele.ModeHTML)
	}
}

// AllCategories возвращает в меню выбора 6 макро-категорий
func AllCategories(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
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
		_ = interviewUC.StopInterview(ctx, sender.ID)

		selectedList, _ := interviewUC.GetSelectedTopics(ctx, sender.ID)
		msg := view.CategoriesHeaderMessage(len(selectedList))
		kb := view.CategoriesKeyboard(domain.Categories, len(selectedList))

		if c.Callback() != nil {
			return c.Edit(msg, kb, tele.ModeHTML)
		}
		return c.Send(msg, kb, tele.ModeHTML)
	}
}

// ClearSelectedCats сбрасывает выбранные темы прямо с экрана разделов
func ClearSelectedCats(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond(&tele.CallbackResponse{Text: "Выбор тем сброшен"})
		if c.Sender() == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = interviewUC.ClearSelectedTopics(ctx, c.Sender().ID)

		msg := view.CategoriesHeaderMessage(0)
		kb := view.CategoriesKeyboard(domain.Categories, 0)
		return c.Edit(msg, kb, tele.ModeHTML)
	}
}

// RandomQuestion запускает тренировку по случайному вопросу из базы
func RandomQuestion(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		sender := c.Sender()
		if sender == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		q, err := interviewUC.StartRandomInterview(ctx, sender.ID)
		if err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Подожди пару секунд, предыдущий ответ ещё проверяется.")
			}
			slog.Error("ошибка старта случайного вопроса", "user_id", sender.ID, "error", err)
			return c.Send("⚠️ Не удалось загрузить случайный вопрос, попробуй ещё раз.")
		}

		msg := view.QuestionMessage(q)
		kb := view.QuestionKeyboard()

		sent, sendErr := c.Bot().Send(c.Chat(), msg, kb, tele.ModeHTML)
		if sendErr == nil && sent != nil {
			_ = interviewUC.SaveQuestionMsgID(ctx, c.Sender().ID, sent.ID)
		}
		return sendErr
	}
}

// TopicSelect запускает интервью по выбранной теме
func TopicSelect(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond() // Гасим спиннер загрузки кнопки в Telegram
		if c.Sender() == nil {
			return nil
		}

		topicID := c.Data()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		q, err := interviewUC.StartInterview(ctx, c.Sender().ID, topicID)
		if err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Подожди пару секунд, предыдущий ответ ещё проверяется.")
			}
			slog.Error("ошибка старта интервью", "user_id", c.Sender().ID, "topic_id", topicID, "error", err)
			return c.Send("⚠️ Не удалось начать интервью по этой теме. Попробуй чуть позже.")
		}

		msg := view.QuestionMessage(q)
		kb := view.QuestionKeyboard()

		sent, sendErr := c.Bot().Send(c.Chat(), msg, kb, tele.ModeHTML)
		if sendErr == nil && sent != nil {
			_ = interviewUC.SaveQuestionMsgID(ctx, c.Sender().ID, sent.ID)
		}
		return sendErr
	}
}

// StopInterview принудительно завершает текущую сессию
func StopInterview(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		if c.Sender() == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := interviewUC.StopInterview(ctx, c.Sender().ID); err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Дождись окончания проверки ответа (пару секунд) и нажми ещё раз.")
			}
			slog.Error("ошибка остановки интервью", "user_id", c.Sender().ID, "error", err)
			return c.Send("⚠️ Не удалось завершить сессию, попробуй ещё раз.")
		}

		// Гасим кнопки у сообщения с вопросом
		if c.Message() != nil {
			_, _ = c.Bot().EditReplyMarkup(c.Message(), nil)
		}

		msg := "🛑 <b>Тренировка завершена.</b>\n\nВыбери режим для продолжения подготовки:"
		kb := view.MainMenuKeyboard()
		return c.Send(msg, kb, tele.ModeHTML)
	}
}

// NextQuestion выдает следующий вопрос из текущей сессии
func NextQuestion(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		if c.Sender() == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		q, err := interviewUC.NextQuestion(ctx, c.Sender().ID)
		if err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Подожди пару секунд, предыдущий ответ ещё проверяется.")
			}
			if c.Message() != nil {
			_, _ = c.Bot().EditReplyMarkup(c.Message(), nil)
		}
			msg := "🎉 <b>Вопросы по этой теме закончились!</b>\n\nВыбери следующий раздел или режим:"
			kb := view.CategoriesKeyboard(domain.Categories)
			return c.Send(msg, kb, tele.ModeHTML)
		}

		// Гасим кнопки у карточки предыдущего ответа
		if c.Message() != nil {
			_, _ = c.Bot().EditReplyMarkup(c.Message(), nil)
		}

		msg := view.QuestionMessage(q)
		kb := view.QuestionKeyboard()

		sent, sendErr := c.Bot().Send(c.Chat(), msg, kb, tele.ModeHTML)
		if sendErr == nil && sent != nil {
			_ = interviewUC.SaveQuestionMsgID(ctx, c.Sender().ID, sent.ID)
		}
		return sendErr
	}
}

// ShowAnswer показывает эталонный ответ для самопроверки
func ShowAnswer(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		if c.Sender() == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		q, err := interviewUC.ShowCurrentAnswer(ctx, c.Sender().ID)
		if err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Дождись окончания проверки (пару секунд).")
			}
			return c.Send("⚠️ Не удалось загрузить ответ. Возможно, сессия завершена. Нажми /start")
		}

		msg := view.FlashcardAnswerMessage(q)
		kb := view.FlashcardSelfCheckKeyboard()

		return c.Edit(msg, kb, tele.ModeHTML)
	}
}

// SubmitFlashcard фиксирует результат самопроверки (знал / не знал) и выдает следующий вопрос
func SubmitFlashcard(interviewUC usecase.InterviewUsecase, knew bool) tele.HandlerFunc {
	return func(c tele.Context) error {
		toast := "Засчитано: 8/10 🟢"
		if !knew {
			toast = "Добавлено в повторение 🔁"
		}
		_ = c.Respond(&tele.CallbackResponse{Text: toast})

		if c.Sender() == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		nextQ, isFinished, err := interviewUC.SubmitFlashcard(ctx, c.Sender().ID, knew)
		if err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Подожди пару секунд...")
			}
			slog.Error("ошибка обработки флешкарты", "user_id", c.Sender().ID, "error", err)
			return c.Send("⚠️ Ошибка сохранения самопроверки. Нажми /start для нового раунда.")
		}

		if isFinished || nextQ == nil {
			finishMsg := "🎉 <b>Спринт успешно завершён!</b>\n\nВсе вопросы этого раунда освоены. Выбери новый раздел:"
			kb := view.CategoriesKeyboard(domain.Categories)
			return c.Edit(finishMsg, kb, tele.ModeHTML)
		}

		msg := view.QuestionMessage(nextQ)
		kb := view.QuestionKeyboard()

		return c.Edit(msg, kb, tele.ModeHTML)
	}
}

// SkipQuestion пропускает текущий вопрос и переходит к следующему в очереди
func SkipQuestion(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond(&tele.CallbackResponse{Text: "Вопрос отложен в конец очереди ⏭"})
		if c.Sender() == nil {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		nextQ, err := interviewUC.SkipQuestion(ctx, c.Sender().ID)
		if err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Подожди пару секунд...")
			}
			return c.Send("⚠️ Не удалось переключить вопрос. Нажми /start")
		}

		msg := view.QuestionMessage(nextQ)
		kb := view.QuestionKeyboard()

		return c.Edit(msg, kb, tele.ModeHTML)
	}
}

// ToggleTopic переключает чекбокс темы и обновляет карточку на лету
func ToggleTopic(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		if c.Sender() == nil {
			return nil
		}
		data := strings.Split(c.Data(), "|")
		if len(data) < 3 {
			return nil
		}
		catID := data[0]
		topicID := data[1]
		page, _ := strconv.Atoi(data[2])

		cat, ok := domain.GetCategory(catID)
		if !ok {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _ = interviewUC.ToggleTopicSelection(ctx, c.Sender().ID, topicID)

		topics, err := interviewUC.GetTopics(ctx)
		if err != nil {
			return nil
		}

		selectedList, _ := interviewUC.GetSelectedTopics(ctx, c.Sender().ID)
		selectedMap := make(map[string]bool)
		for _, id := range selectedList {
			selectedMap[id] = true
		}

		filtered := cat.FilterTopics(topics)
		msg := view.CategoryHeaderMessage(cat, len(filtered))
		kb := view.CategoryTopicsKeyboard(cat, filtered, selectedMap, page)

		return c.Edit(msg, kb, tele.ModeHTML)
	}
}

// ClearSelectedTopics сбрасывает все чекбоксы
func ClearSelectedTopics(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond(&tele.CallbackResponse{Text: "Выбор сброшен"})
		if c.Sender() == nil {
			return nil
		}
		data := strings.Split(c.Data(), "|")
		if len(data) < 2 {
			return nil
		}
		catID := data[0]
		page, _ := strconv.Atoi(data[1])

		cat, ok := domain.GetCategory(catID)
		if !ok {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = interviewUC.ClearSelectedTopics(ctx, c.Sender().ID)

		topics, err := interviewUC.GetTopics(ctx)
		if err != nil {
			return nil
		}

		filtered := cat.FilterTopics(topics)
		msg := view.CategoryHeaderMessage(cat, len(filtered))
		kb := view.CategoryTopicsKeyboard(cat, filtered, nil, page)

		return c.Edit(msg, kb, tele.ModeHTML)
	}
}

// SelectAllCategoryTopics выбирает все темы раздела
func SelectAllCategoryTopics(interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond(&tele.CallbackResponse{Text: "Все темы раздела выбраны!"})
		if c.Sender() == nil {
			return nil
		}
		data := strings.Split(c.Data(), "|")
		if len(data) < 2 {
			return nil
		}
		catID := data[0]
		page, _ := strconv.Atoi(data[1])

		cat, ok := domain.GetCategory(catID)
		if !ok {
			return nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = interviewUC.SelectAllCategoryTopics(ctx, c.Sender().ID, cat.TopicIDs)

		topics, err := interviewUC.GetTopics(ctx)
		if err != nil {
			return nil
		}

		selectedList, _ := interviewUC.GetSelectedTopics(ctx, c.Sender().ID)
		selectedMap := make(map[string]bool, len(selectedList)+len(cat.TopicIDs))
		for _, tid := range selectedList {
			selectedMap[tid] = true
		}
		for _, tid := range cat.TopicIDs {
			selectedMap[tid] = true
		}

		filtered := cat.FilterTopics(topics)
		msg := view.CategoryHeaderMessage(cat, len(filtered))
		kb := view.CategoryTopicsKeyboard(cat, filtered, selectedMap, page)

		return c.Edit(msg, kb, tele.ModeHTML)
	}
}

// StartMultiSprint запускает тренировку по всем отмеченным темам с учетом грейда
func StartMultiSprint(userUC usecase.UserUsecase, interviewUC usecase.InterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		_ = c.Respond()
		if c.Sender() == nil {
			return nil
		}
		catID := c.Data()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		selectedTopics, _ := interviewUC.GetSelectedTopics(ctx, c.Sender().ID)
		if len(selectedTopics) == 0 {
			if cat, ok := domain.GetCategory(catID); ok {
				selectedTopics = cat.TopicIDs
			}
		}

		if len(selectedTopics) == 0 {
			return c.Send("⚠️ Выберите хотя бы одну тему для начала тренировки.")
		}

		user, _ := userUC.GetUser(ctx, c.Sender().ID)
		grade := "all"
		if user != nil && user.TargetGrade != "" {
			grade = user.TargetGrade
		}

		q, total, err := interviewUC.StartMultiTopicInterview(ctx, c.Sender().ID, selectedTopics, grade)
		if err != nil {
			if errors.Is(err, usecase.ErrAlreadyEvaluating) {
				return c.Send("⏳ Дождись окончания проверки предыдущего ответа.")
			}
			slog.Error("ошибка старта мульти-спринта", "user_id", c.Sender().ID, "error", err)
			return c.Send("⚠️ Не удалось запустить спринт по выбранным темам.")
		}

		gradeBadge := "🌐 Все уровни"
		switch grade {
		case "intern":
			gradeBadge = "🐣 Intern"
		case "junior":
			gradeBadge = "🟢 Junior"
		case "middle":
			gradeBadge = "🟡 Middle"
		case "senior":
			gradeBadge = "🔴 Senior"
		}

		introMsg := fmt.Sprintf(
			"🚀 <b>Спринт запущен!</b>\n"+
				"• Тем выбрано: <b>%d</b>\n"+
				"• Вопросов в колоде: <b>%d</b>\n"+
				"• Целевой грейд: <b>%s</b>\n\n"+
				"Первый вопрос:",
			len(selectedTopics),
			total,
			gradeBadge,
		)
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
