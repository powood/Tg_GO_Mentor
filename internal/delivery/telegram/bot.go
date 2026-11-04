package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	tele "gopkg.in/telebot.v3"
	"gopkg.in/telebot.v3/middleware"

	"interview-bot/internal/config"
	"interview-bot/internal/delivery/telegram/handlers"
	"interview-bot/internal/usecase"
)

type userFloodWindow struct {
	lastSec int64
	count   int
}

type Bot struct {
	bot       *tele.Bot
	logger    *slog.Logger
	mu        sync.Mutex
	stopping  bool
	wg        sync.WaitGroup
	floodMu   sync.Mutex
	userFlood map[int64]*userFloodWindow
}

func redactToken(msg, token string) string {
	if token == "" {
		return msg
	}
	return strings.ReplaceAll(msg, token, "<redacted>")
}

func NewBot(
	cfg config.BotConfig,
	userUC usecase.UserUsecase,
	interviewUC usecase.InterviewUsecase,
	quizUC usecase.QuizUsecase,
	mockUC usecase.MockInterviewUsecase,
	log *slog.Logger,
) (*Bot, error) {
	pref := tele.Settings{
		Token: cfg.Token,
		Poller: &tele.LongPoller{
			Timeout:        cfg.Timeout,
			AllowedUpdates: []string{"message", "callback_query"},
		},
		OnError: func(err error, c tele.Context) {
			if err == nil {
				return
			}
			var userID int64
			if c != nil && c.Sender() != nil {
				userID = c.Sender().ID
			}
			log.Error("ошибка в обработчике telegram",
				"user_id", userID,
				"error", redactToken(err.Error(), cfg.Token),
			)
		},
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		return nil, fmt.Errorf("ошибка инициализации telebot: %s", redactToken(err.Error(), cfg.Token))
	}

	wrapper := &Bot{
		bot:       b,
		logger:    log,
		userFlood: make(map[int64]*userFloodWindow),
	}

	b.Use(middleware.Recover())

	b.Use(func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(c tele.Context) error {
			if c.Chat() == nil || c.Chat().Type != tele.ChatPrivate {
				return nil
			}
			return next(c)
		}
	})

	b.Use(func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(c tele.Context) error {
			if c.Sender() != nil {
				uid := c.Sender().ID
				nowSec := time.Now().Unix()
				wrapper.floodMu.Lock()
				win, exists := wrapper.userFlood[uid]
				if !exists || win.lastSec != nowSec {
					wrapper.userFlood[uid] = &userFloodWindow{lastSec: nowSec, count: 1}
				} else {
					win.count++
					if win.count > 3 {
						wrapper.floodMu.Unlock()
						return nil
					}
				}

				if len(wrapper.userFlood) > 5000 {
					for k, v := range wrapper.userFlood {
						if nowSec-v.lastSec > 60 {
							delete(wrapper.userFlood, k)
						}
					}
				}
				wrapper.floodMu.Unlock()
			}
			return next(c)
		}
	})

	b.Use(func(next tele.HandlerFunc) tele.HandlerFunc {
		return func(c tele.Context) error {
			wrapper.mu.Lock()
			if wrapper.stopping {
				wrapper.mu.Unlock()
				return nil
			}
			wrapper.wg.Add(1)
			wrapper.mu.Unlock()
			defer wrapper.wg.Done()

			return next(c)
		}
	})

	b.Handle("/start", handlers.Start(userUC, interviewUC))
	b.Handle("/stop", handlers.StopInterview(interviewUC))
	b.Handle("/profile", handlers.Profile(userUC, interviewUC))
	b.Handle("/quiz", handlers.StartQuiz(quizUC, interviewUC))
	b.Handle("/mock", handlers.MockMenu(userUC, interviewUC))

	b.Handle("⚡ Блиц-квиз", handlers.StartQuiz(quizUC, interviewUC))
	b.Handle("🎯 Тренировка", handlers.TrainingMenu(userUC, interviewUC))
	b.Handle("🎯 Собеседование", handlers.TrainingMenu(userUC, interviewUC))
	b.Handle("🎓 Mock-собеседование", handlers.MockMenu(userUC, interviewUC))
	b.Handle("📊 Профиль", handlers.Profile(userUC, interviewUC))
	b.Handle("📊 Мой профиль", handlers.Profile(userUC, interviewUC))
	b.Handle("🔥 Ошибки", handlers.WeakDrill(interviewUC))

	menu := &tele.ReplyMarkup{}
	btnTrainingMenu := menu.Data("", "training_menu")
	btnCat := menu.Data("", "cat")
	btnCatPage := menu.Data("", "cat_page")
	btnAllCats := menu.Data("", "all_categories")
	btnMainMenu := menu.Data("", "main_menu")
	btnRandomQ := menu.Data("", "random_question")
	btnTopic := menu.Data("", "topic")
	btnNoop := menu.Data("", "noop")
	btnStop := menu.Data("", "stop_interview")
	btnNextQ := menu.Data("", "next_question")
	btnShowAns := menu.Data("", "show_answer")
	btnFlashcardKnew := menu.Data("", "flashcard_knew")
	btnFlashcardForgot := menu.Data("", "flashcard_forgot")
	btnSkipQ := menu.Data("", "skip_question")
	btnProfile := menu.Data("", "profile")
	btnWeakDrills := menu.Data("", "weak_drills")
	btnChangeGrade := menu.Data("", "change_grade")
	btnSetGrade := menu.Data("", "set_grade")
	btnToggleTopic := menu.Data("", "toggle_topic")
	btnStartSprint := menu.Data("", "start_sprint")
	btnClearSelected := menu.Data("", "clear_selected")
	btnClearSelectedCats := menu.Data("", "clear_selected_cats")
	btnSelectAll := menu.Data("", "select_all")
	btnWeakMenu := menu.Data("", "weak_menu")
	btnWeakDrillAll := menu.Data("", "weak_drill_all")
	btnWeakTopic := menu.Data("", "weak_topic")

	// Кнопки блиц-квиза
	btnStartQuiz := menu.Data("", "start_quiz")
	btnQuizMix := menu.Data("", "quiz_mix")
	btnQuizCat := menu.Data("", "quiz_cat")
	btnQuizSprint := menu.Data("", "quiz_sprint")
	btnQuizAns := menu.Data("", "quiz_ans")
	btnQuizNext := menu.Data("", "quiz_next")

	// Кнопки Mock-собеседования
	btnMockMenu := menu.Data("", "mock_menu")
	btnMockStartMix := menu.Data("", "mock_start_mix")
	btnMockStartCustom := menu.Data("", "mock_start_custom")
	btnMockStartCat := menu.Data("", "mock_start_cat")
	btnStopMock := menu.Data("", "stop_mock")

	b.Handle(&btnTrainingMenu, handlers.TrainingMenu(userUC, interviewUC))
	b.Handle(&btnMockMenu, handlers.MockMenu(userUC, interviewUC))
	b.Handle(&btnMockStartMix, handlers.StartMockMix(userUC, mockUC, interviewUC))
	b.Handle(&btnMockStartCustom, handlers.StartMockCustom(userUC, mockUC, interviewUC))
	b.Handle(&btnMockStartCat, handlers.StartMockCategory(userUC, mockUC, interviewUC))
	b.Handle(&btnStopMock, handlers.StopMock(mockUC, interviewUC))

	b.Handle(&btnStartQuiz, handlers.StartQuiz(quizUC, interviewUC))
	b.Handle(&btnQuizMix, handlers.StartQuizMix(quizUC))
	b.Handle(&btnQuizCat, handlers.StartQuizCat(quizUC))
	b.Handle(&btnQuizSprint, handlers.StartQuizSprint(quizUC))
	b.Handle(&btnQuizAns, handlers.AnswerQuiz(quizUC))
	b.Handle(&btnQuizNext, handlers.NextQuizQuestion(quizUC))

	b.Handle(&btnWeakMenu, handlers.WeakMenu(interviewUC))
	b.Handle(&btnWeakDrillAll, handlers.WeakDrill(interviewUC))
	b.Handle(&btnWeakTopic, handlers.WeakDrillTopic(interviewUC))
	b.Handle(&btnToggleTopic, handlers.ToggleTopic(interviewUC))
	b.Handle(&btnStartSprint, handlers.StartMultiSprint(userUC, interviewUC))
	b.Handle(&btnClearSelected, handlers.ClearSelectedTopics(interviewUC))
	b.Handle(&btnClearSelectedCats, handlers.ClearSelectedCats(interviewUC))
	b.Handle(&btnSelectAll, handlers.SelectAllCategoryTopics(interviewUC))
	b.Handle(&btnChangeGrade, handlers.ChangeGrade())
	b.Handle(&btnSetGrade, handlers.SetGrade(userUC, interviewUC))
	b.Handle(&btnProfile, handlers.Profile(userUC, interviewUC))
	b.Handle(&btnWeakDrills, handlers.WeakDrill(interviewUC))
	b.Handle(&btnCat, handlers.CategorySelect(interviewUC))
	b.Handle(&btnCatPage, handlers.CategoryPage(interviewUC))
	b.Handle(&btnAllCats, handlers.AllCategories(interviewUC))
	b.Handle(&btnMainMenu, handlers.MainMenu(userUC, interviewUC))
	b.Handle(&btnRandomQ, handlers.RandomQuestion(interviewUC))
	b.Handle(&btnTopic, handlers.TopicSelect(interviewUC))
	b.Handle(&btnNoop, func(c tele.Context) error { return c.Respond() })
	b.Handle(&btnStop, handlers.StopInterview(interviewUC))
	b.Handle(&btnNextQ, handlers.NextQuestion(interviewUC))
	b.Handle(&btnShowAns, handlers.ShowAnswer(interviewUC))
	b.Handle(&btnFlashcardKnew, handlers.SubmitFlashcard(interviewUC, true))
	b.Handle(&btnFlashcardForgot, handlers.SubmitFlashcard(interviewUC, false))
	b.Handle(&btnSkipQ, handlers.SkipQuestion(interviewUC))

	// Регистрация текстовых и голосовых сообщений
	b.Handle(tele.OnText, handlers.TextAnswer(interviewUC, mockUC))
	b.Handle(tele.OnVoice, handlers.VoiceAnswer(b, interviewUC, mockUC))

	return wrapper, nil
}

func (b *Bot) Start() {
	b.logger.Info("Telegram-бот успешно запущен в режиме Long Polling")
	b.bot.Start()
}

func (b *Bot) Shutdown(ctx context.Context) error {
	b.logger.Info("Остановка приема новых сообщений Telegram (stop poller)...")
	b.bot.Stop()

	b.mu.Lock()
	b.stopping = true
	b.mu.Unlock()

	b.logger.Info("Ожидание завершения активных оценок AI...")
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		b.logger.Info("Все активные запросы пользователей успешно завершены")
	case <-ctx.Done():
		b.logger.Warn("Таймаут ожидания активных запросов при остановке бота")
	}

	return nil
}
