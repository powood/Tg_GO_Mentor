package main

import (
	"fmt"
	"html"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	tele "gopkg.in/telebot.v3"

	"interview-bot/internal/repository"
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
		btnAnswer := qMenu.Data("💡 Показать ответ", "btn_show_answer", strconv.Itoa(q.ID))
		btnNext := qMenu.Data("➡️ Другой вопрос", "btn_topic", topicID)

		qMenu.Inline(
			qMenu.Row(btnAnswer),
			qMenu.Row(btnNext),
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

		// Отвечаем цитатой на сообщение с вопросом
		return c.Reply(text, tele.ModeHTML)
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
