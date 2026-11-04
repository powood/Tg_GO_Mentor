package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"interview-bot/internal/ai"
	"interview-bot/internal/config"
	"interview-bot/internal/delivery/telegram"
	"interview-bot/internal/repository/postgres"
	"interview-bot/internal/repository/redis"
	"interview-bot/internal/usecase"
	"interview-bot/pkg/logger"
)

func main() {
	// 1. Загрузка строго типизированной конфигурации из .env / окружения
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ Критическая ошибка конфигурации: %v", err)
	}

	// 2. Инициализация структурированного логгера slog с централизованной защитой секретов
	secrets := []string{cfg.Bot.Token, cfg.AI.ProxyURL, cfg.Postgres.URL, cfg.Redis.Password}
	secrets = append(secrets, cfg.AI.GroqKeys...)
	secrets = append(secrets, cfg.AI.DeepSeekKeys...)
	if cfg.AI.DeepgramKey != "" {
		secrets = append(secrets, cfg.AI.DeepgramKey)
	}

	appLogger := logger.Setup(cfg.Env, secrets...)
	slog.SetDefault(appLogger)
	appLogger.Info("Запуск сервиса Tg_GO_Mentor", "env", cfg.Env)

	// 3. Контекст для инициализации инфраструктуры с таймаутом
	initCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 4. Подключение к PostgreSQL (pgxpool)
	pgPool, err := postgres.NewPool(initCtx, cfg.Postgres)
	if err != nil {
		appLogger.Error("Не удалось подключиться к PostgreSQL", "error", err)
		os.Exit(1)
	}
	defer pgPool.Close()
	appLogger.Info("Успешное подключение к PostgreSQL")

	// 5. Подключение к Redis (сессии и FSM диалогов)
	redisClient, err := redis.NewClient(initCtx, cfg.Redis)
	if err != nil {
		appLogger.Error("Не удалось подключиться к Redis", "error", err)
		os.Exit(1)
	}
	defer redisClient.Close()
	appLogger.Info("Успешное подключение к Redis")

	// 6. Инициализация репозиториев (адаптеров данных)
	questionRepo := postgres.NewQuestionRepository(pgPool)
	progressRepo := postgres.NewProgressRepository(pgPool)
	userRepo := postgres.NewUserRepository(pgPool)
	quizRepo := postgres.NewQuizRepository(pgPool)
	fsmStorage := redis.NewFSMStorage(redisClient, cfg.Redis.StateTTL)

	// 7. Инициализация AI-клиента (Groq Whisper + DeepSeek/Groq LLaMA)
	aiClient := ai.NewClient(cfg.AI)

	// 8. Инициализация слоя бизнес-логики (Usecase)
	interviewUC := usecase.NewInterviewUsecase(questionRepo, progressRepo, userRepo, fsmStorage, aiClient, aiClient)
	userUC := usecase.NewUserUsecase(userRepo)
	quizUC := usecase.NewQuizUsecase(quizRepo, userRepo, fsmStorage)
	mockUC := usecase.NewMockInterviewUsecase(questionRepo, fsmStorage, aiClient, aiClient, progressRepo)

	// 9. Инициализация транспортного слоя Telegram
	bot, err := telegram.NewBot(cfg.Bot, userUC, interviewUC, quizUC, mockUC, appLogger)
	if err != nil {
		appLogger.Error("Ошибка инициализации Telegram-бота", "error", err)
		os.Exit(1)
	}

	// 10. Асинхронный запуск бота
	go bot.Start()

	// 11. Graceful Shutdown (перехват сигналов завершения ОС)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	appLogger.Info("Получен сигнал завершения. Выполняется Graceful Shutdown...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 80*time.Second)
	defer shutdownCancel()

	if err := bot.Shutdown(shutdownCtx); err != nil {
		appLogger.Warn("Не все запросы успели завершиться до таймаута", "error", err)
	} else {
		appLogger.Info("Сервис успешно остановлен без утечек ресурсов")
	}
}
