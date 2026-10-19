package main

import (
	"log"
	"os"
	"time"

	"github.com/joho/godotenv"

	tele "gopkg.in/telebot.v3"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("Внимание: .env файл не найден, читаем переменные из окружения системы")
	}
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatal("Ошибка: BOT_TOKEN не задан!")
	}
	pref := tele.Settings{
		Token: token,
		Poller: &tele.LongPoller{
			Timeout: 10 * time.Second,
		},
	}
	b, err := tele.NewBot(pref)
	if err != nil {
		log.Fatal(err)
	}
	b.Handle("/start", func(c tele.Context) error {
		return c.Send("Привет! Я твой будущий AI-ментор для подготовки к собесам. Напиши мне что-нибудь!")
	})

	b.Handle(tele.OnText, func(c tele.Context) error {
		return c.Send("Ты написал: " + c.Text())
	})
	log.Println("Бот успешно запущен!")
	b.Start()
}
