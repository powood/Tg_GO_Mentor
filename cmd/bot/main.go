package main

import (
	"context"
	"fmt"
	"html"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	tele "gopkg.in/telebot.v3"

	"interview-bot/internal/ai"
	"interview-bot/internal/repository"
	"interview-bot/internal/session"
)

func main() {
	// 1. Загрузка конфигурации из .env
	if err := godotenv.Load(); err != nil {
		log.Println("ℹ️  .env файл не найден, используем переменные окружения ОС")
	}

	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatal("❌ Ошибка: переменная BOT_TOKEN не задана в .env или окружении")
	}

	// 2. Инициализация репозитория (источник данных)
	repo := repository.NewMemoryQuestionRepository()
	sessionMgr := session.NewManager()
	var groqKeys []string
	if keysStr := os.Getenv("GROQ_API_KEYS"); keysStr != "" {
		for _, k := range strings.Split(keysStr, ",") {
			k = strings.TrimSpace(k)
			if k != "" {
				groqKeys = append(groqKeys, k)
			}
		}
	} else if key := strings.TrimSpace(os.Getenv("GROQ_API_KEY")); key != "" {
		groqKeys = append(groqKeys, key)
	}
	if len(groqKeys) == 0 {
		log.Fatal("❌ Ошибка: не задан ни GROQ_API_KEY, ни GROQ_API_KEYS в .env")
	}
	aiClient := ai.NewGroqClient(groqKeys)

	// 3. Конфигурация клиента Telegram Bot API
	pref := tele.Settings{
		Token: token,
		Poller: &tele.LongPoller{
			Timeout: 10 * time.Second,
		},
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		log.Fatalf("❌ Ошибка инициализации Telegram-бота: %v", err)
	}

	// 4. Селекторы кнопок (шаблоны маршрутизации событий)
	menu := &tele.ReplyMarkup{}
	btnTopic := menu.Data("Тема", "btn_topic")
	btnShowAnswer := menu.Data("Показать ответ", "btn_show_answer")
	btnAnswerMyself := menu.Data("Ответить самому", "btn_answer_myself")
	btnCancelAnswer := menu.Data("Отменить", "btn_cancel_answer")
	btnMainMenu := menu.Data("Все темы", "btn_main_menu")

	// 5. Хендлер команды /start — главное меню с темами
	b.Handle("/start", func(c tele.Context) error {
		topics := repo.GetTopics()

		// Локальный ReplyMarkup для каждого запроса (защита от Data Race между пользователями)
		userMenu := &tele.ReplyMarkup{}
		var rows []tele.Row
		for _, t := range topics {
			btn := userMenu.Data("📚 "+t.Title, "btn_topic", t.ID)
			rows = append(rows, userMenu.Row(btn))
		}
		userMenu.Inline(rows...)

		welcomeText := "👋 <b>Привет, будущий Go-мидл!</b>\n\n" +
			"Я помогу тебе подготовиться к техническим собеседованиям по Go.\n\n" +
			"Выбери тему для тренировки:"

		return c.Send(welcomeText, userMenu, tele.ModeHTML)
	})

	// 6. Хендлер выбора темы и кнопки «Другой вопрос»
	b.Handle(&btnTopic, func(c tele.Context) error {
		topicID := c.Data() // Получаем ID выбранной темы из callback data

		q, err := repo.GetRandomQuestion(topicID)
		if err != nil {
			return c.Send("⚠️ В этой теме пока нет доступных вопросов.")
		}

		// Локальная клавиатура под конкретным вопросом
		qMenu := &tele.ReplyMarkup{}
		btnAnswerMyself := qMenu.Data("✍️ Ответить самому", "btn_answer_myself", fmt.Sprintf("%s:%d", topicID, q.ID))
		btnAnswer := qMenu.Data("💡 Показать ответ", "btn_show_answer", strconv.Itoa(q.ID))
		btnNext := qMenu.Data("➡️ Другой вопрос", "btn_topic", topicID)

		qMenu.Inline(
			qMenu.Row(btnAnswerMyself),
			qMenu.Row(btnAnswer, btnNext),
		)

		text := fmt.Sprintf(
			"❓ <b>Вопрос по теме (%s):</b>\n\n%s",
			html.EscapeString(topicID),
			html.EscapeString(q.Text),
		)

		return c.Send(text, qMenu, tele.ModeHTML)
	})

	// 7. Хендлер кнопки «💡 Показать ответ»
	b.Handle(&btnShowAnswer, func(c tele.Context) error {
		qIDStr := c.Data()
		qID, err := strconv.Atoi(qIDStr)
		if err != nil {
			return c.Send("⚠️ Некорректный ID вопроса.")
		}

		q, err := repo.GetQuestionByID(qID)
		if err != nil {
			return c.Send("⚠️ Вопрос не найден.")
		}

		// Формируем структурированный эталонный ответ
		text := fmt.Sprintf("✅ <b>Эталонный ответ:</b>\n%s\n\n🎯 <b>Ключевые тезисы:</b>\n", html.EscapeString(q.Answer))
		for _, kp := range q.KeyPoints {
			text += fmt.Sprintf("• %s\n", html.EscapeString(kp))
		}
		answerMenu := &tele.ReplyMarkup{}
		btnNext := answerMenu.Data("➡️ Другой вопрос", "btn_topic", q.TopicID)
		btnMenu := answerMenu.Data("📚 Все темы", "btn_main_menu")
		answerMenu.Inline(answerMenu.Row(btnNext, btnMenu))

		// Отвечаем цитатой на сообщение с вопросом
		return c.Reply(text, answerMenu, tele.ModeHTML)
	})
	b.Handle(&btnAnswerMyself, func(c tele.Context) error {
		payload := c.Data()
		parts := strings.Split(payload, ":")
		if len(parts) != 2 {
			return c.Send("⚠️ Ошибка данных вопроса.")

		}
		topicID := parts[0]
		qID, err := strconv.Atoi(parts[1])
		if err != nil {
			return c.Send("⚠️ Некорректный ID вопроса.")

		}

		if _, err := repo.GetQuestionByID(qID); err != nil {

			return c.Send("⚠️ Вопрос не найден.")

		}
		sessionMgr.SetAnswering(c.Sender().ID, topicID, qID)
		cancelMenu := &tele.ReplyMarkup{}
		btnCancel := cancelMenu.Data("❌ Отмена", "btn_cancel_answer", topicID)
		cancelMenu.Inline(cancelMenu.Row(btnCancel))
		promptText := "🎙️ <b>Твой выход!</b>\n\n" +
			"Отправь свой ответ следующим сообщением — <b>текстом</b> или <b>голосовым</b> 🎤.\n\n" +
			"Постарайся вспомнить ключевые детали (как на реальном собеседовании)."
		return c.Send(promptText, cancelMenu, tele.ModeHTML)
	})
	b.Handle(&btnCancelAnswer, func(c tele.Context) error {
		sessionMgr.ClearState(c.Sender().ID)
		topicID := c.Data()
		menuBack := &tele.ReplyMarkup{}
		btnNext := menuBack.Data("➡️ Другой вопрос", "btn_topic", topicID)
		btnMenu := menuBack.Data("📚 Все темы", "btn_main_menu")
		menuBack.Inline(menuBack.Row(btnNext, btnMenu))
		return c.Send("Окей, отменили! Можешь взять другой вопрос или вернуться к темам:", menuBack, tele.ModeHTML)
	})
	b.Handle(&btnMainMenu, func(c tele.Context) error {
		topics := repo.GetTopics()
		userMenu := &tele.ReplyMarkup{}
		var rows []tele.Row
		for _, t := range topics {
			btn := userMenu.Data("📚 "+t.Title, "btn_topic", t.ID)
			rows = append(rows, userMenu.Row(btn))
		}
		userMenu.Inline(rows...)
		return c.Send("📚 <b>Все темы:</b>", userMenu, tele.ModeHTML)
	})
	b.Handle(tele.OnText, func(c tele.Context) error {
		state, ok := sessionMgr.ReadState(c.Sender().ID)
		if !ok || !state.IsAnswering {
			return c.Send("👋 Чтобы начать тренировку, нажми /start и выбери тему собеседования!")
		}
		q, err := repo.GetQuestionByID(state.QuestionID)
		if err != nil {
			return c.Send("⚠️ Вопрос устарел или не найден. Начни заново через /start.")
		}
		// Сбрасываем стейт: ответ принят
		sessionMgr.ClearState(c.Sender().ID)
		userAnswer := c.Text()
		// Показываем в Telegram плашку "печатает...", пока нейросеть думает
		_ = c.Notify(tele.Typing)
		// Отправляем ответ в Groq (Llama 3.3)
		eval, err := aiClient.Evaluate(context.Background(), *q, userAnswer)
		var resText string
		if err != nil {
			log.Printf("⚠️ Ошибка оценки ИИ: %v", err)
			// Если нейросеть временно недоступна — отдаем базовый ответ без паники
			resText = fmt.Sprintf(
				"📝 <b>Твой ответ принят:</b>\n<i>«%s»</i>\n\n"+
					"📖 <b>Эталонный ответ ментора:</b>\n%s\n\n"+
					"🎯 <b>Ключевые тезисы:</b>\n",
				html.EscapeString(userAnswer),
				html.EscapeString(q.Answer),
			)
			for _, kp := range q.KeyPoints {
				resText += fmt.Sprintf("• %s\n", html.EscapeString(kp))
			}
		} else {
			// Красивый вердикт от ИИ-ментора
			status := "🟡 Надо доработать"
			if eval.IsCorrect {
				status = "🟢 Ответ зачтен!"
			}
			resText = fmt.Sprintf(
				"📝 <b>Твой ответ:</b>\n<i>«%s»</i>\n\n"+
					"🏆 <b>Оценка ментора: %d / 10</b> (%s)\n\n"+
					"💡 <b>Разбор ответа:</b>\n%s\n\n",
				html.EscapeString(userAnswer),
				eval.Score,
				status,
				html.EscapeString(eval.Feedback),
			)
			if len(eval.MissingPoints) > 0 {
				resText += "⚠️ <b>Что ты упустил:</b>\n"
				for _, mp := range eval.MissingPoints {
					resText += fmt.Sprintf("• %s\n", html.EscapeString(mp))
				}
				resText += "\n"
			}
			resText += fmt.Sprintf("📖 <b>Эталонный ответ:</b>\n%s", html.EscapeString(q.Answer))
		}
		resMenu := &tele.ReplyMarkup{}
		btnNext := resMenu.Data("➡️ Другой вопрос", "btn_topic", state.TopicID)
		btnMenu := resMenu.Data("📚 Все темы", "btn_main_menu")
		resMenu.Inline(resMenu.Row(btnNext, btnMenu))
		return c.Send(resText, resMenu, tele.ModeHTML)
	})

	// 8. Корректное завершение работы (Graceful Shutdown)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-stop
		log.Println("🛑 Получен сигнал завершения. Останавливаем бота...")
		b.Stop()
	}()

	log.Println("🚀 Бот успешно запущен и готов к собеседованиям! (Нажми Ctrl+C для выхода)")
	b.Start()
}
