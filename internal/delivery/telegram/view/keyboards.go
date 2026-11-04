package view

import (
	"fmt"
	"net/url"
	"unicode/utf8"

	tele "gopkg.in/telebot.v3"

	"interview-bot/internal/domain"
)

const TopicsPerPage = 8

// MainMenuKeyboard генерирует главное меню с чётким выбором из 3 основных режимов
func MainMenuKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}

	btnTraining := menu.Data("🎯 Тренировка (вопросы и карточки)", "training_menu")
	btnMock := menu.Data("🎓 Mock-собеседование (с техлидом)", "mock_menu")
	btnQuiz := menu.Data("⚡ Блиц-квиз (задачи по коду)", "start_quiz")
	btnProfile := menu.Data("📊 Мой профиль", "profile")
	btnWeak := menu.Data("🔥 Работа над ошибками", "weak_menu")

	menu.Inline(
		menu.Row(btnTraining),
		menu.Row(btnMock),
		menu.Row(btnQuiz),
		menu.Row(btnProfile, btnWeak),
	)
	return menu
}

// TrainingMenuKeyboard генерирует меню режима классической тренировки
func TrainingMenuKeyboard(selectedCount int) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	btnAll := menu.Data("🎲 Все темы (случайный вопрос)", "random_question")
	rows = append(rows, menu.Row(btnAll))

	if selectedCount > 0 {
		btnCustom := menu.Data(fmt.Sprintf("🚀 Начать по выбранным (%d тем)", selectedCount), "start_sprint", "")
		rows = append(rows, menu.Row(btnCustom))
	}

	btnChooseTopics := menu.Data("📂 Выбрать темы", "all_categories")
	btnGrade := menu.Data("⚙️ Сменить грейд", "change_grade")
	rows = append(rows, menu.Row(btnChooseTopics, btnGrade))

	btnBack := menu.Data("⬅️ В главное меню", "main_menu")
	rows = append(rows, menu.Row(btnBack))

	menu.Inline(rows...)
	return menu
}

// CategoriesKeyboard генерирует меню из 6 макро-категорий + кнопки запуска по выбранным темам
func CategoriesKeyboard(categories []domain.Category, selectedCount ...int) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	sel := 0
	if len(selectedCount) > 0 {
		sel = selectedCount[0]
	}

	for _, c := range categories {
		btn := menu.Data(fmt.Sprintf("%s (%d)", c.Title, len(c.TopicIDs)), "cat", c.ID)
		rows = append(rows, menu.Row(btn))
	}

	if sel > 0 {
		btnStartTrain := menu.Data(fmt.Sprintf("▶️ 🎯 Тренировка (%d)", sel), "start_sprint", "")
		btnStartMock := menu.Data(fmt.Sprintf("▶️ 🎓 Mock-собес (%d)", sel), "mock_start_custom")
		btnStartQuiz := menu.Data(fmt.Sprintf("▶️ ⚡ Блиц-квиз (%d)", sel), "quiz_sprint", "")
		btnClear := menu.Data(fmt.Sprintf("🧹 Сбросить (%d)", sel), "clear_selected_cats")
		rows = append(rows,
			menu.Row(btnStartTrain, btnStartMock),
			menu.Row(btnStartQuiz, btnClear),
		)
	}

	btnMain := menu.Data("⬅️ В главное меню", "main_menu")
	rows = append(rows, menu.Row(btnMain))

	menu.Inline(rows...)
	return menu
}

// ProfileKeyboard генерирует меню управления профилем
func ProfileKeyboard(weakCount int) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	btnGrade := menu.Data("⚙️ Сменить грейд", "change_grade")
	rows = append(rows, menu.Row(btnGrade))

	if weakCount > 0 {
		btnWeak := menu.Data(fmt.Sprintf("🔥 Работа над ошибками (%d)", weakCount), "weak_menu")
		rows = append(rows, menu.Row(btnWeak))
	}

	btnMain := menu.Data("⬅️ В главное меню", "main_menu")
	rows = append(rows, menu.Row(btnMain))

	menu.Inline(rows...)
	return menu
}

// GradeSelectorKeyboard генерирует кнопки выбора уровня подготовки с поддержкой Intern
func GradeSelectorKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnIntern := menu.Data("🐣 Intern / Стажер", "set_grade", "intern")
	btnJunior := menu.Data("🟢 Junior", "set_grade", "junior")
	btnMiddle := menu.Data("🟡 Middle", "set_grade", "middle")
	btnSenior := menu.Data("🔴 Senior", "set_grade", "senior")
	btnAll := menu.Data("🌐 Все уровни", "set_grade", "all")
	btnBack := menu.Data("⬅️ В главное меню", "main_menu")

	menu.Inline(
		menu.Row(btnIntern, btnJunior),
		menu.Row(btnMiddle, btnSenior),
		menu.Row(btnAll),
		menu.Row(btnBack),
	)
	return menu
}

// WeakTopicsKeyboard генерирует меню выбора конкретной темы для работы над ошибками
func WeakTopicsKeyboard(topics []domain.WeakTopic, totalWeak int) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	btnAllWeak := menu.Data(fmt.Sprintf("⚡ Все ошибки сразу (%d)", totalWeak), "weak_drill_all")
	rows = append(rows, menu.Row(btnAllWeak))

	for _, t := range topics {
		btn := menu.Data(fmt.Sprintf("%s (%d)", t.Title, t.WeakCount), "weak_topic", t.TopicID)
		rows = append(rows, menu.Row(btn))
	}

	btnBack := menu.Data("⬅️ В главное меню", "main_menu")
	rows = append(rows, menu.Row(btnBack))

	menu.Inline(rows...)
	return menu
}

// CategoryTopicsKeyboard генерирует список тем внутри раздела с чекбоксами и кнопками запуска всех 3 режимов
func CategoryTopicsKeyboard(cat domain.Category, topics []domain.Topic, selectedMap map[string]bool, page int) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	total := len(topics)
	if total == 0 {
		btnBack := menu.Data("📂 Все разделы", "all_categories")
		menu.Inline(menu.Row(btnBack))
		return menu
	}

	totalPages := (total + TopicsPerPage - 1) / TopicsPerPage
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	start := page * TopicsPerPage
	end := start + TopicsPerPage
	if end > total {
		end = total
	}

	var rows []tele.Row
	for _, t := range topics[start:end] {
		title := t.Title
		r, _ := utf8.DecodeRuneInString(title)
		if r < 128 {
			title = "📚 " + title
		}
		if selectedMap != nil && selectedMap[t.ID] {
			title = "✅ " + title
		} else {
			title = "◻️ " + title
		}
		btn := menu.Data(title, "toggle_topic", fmt.Sprintf("%s|%s|%d", cat.ID, t.ID, page))
		rows = append(rows, menu.Row(btn))
	}

	// 1. Сразу под темами — строка пагинации (визуальный разделитель списка тем)
	if totalPages > 1 {
		var navBtns []tele.Btn
		if page > 0 {
			navBtns = append(navBtns, menu.Data("⬅️ Назад", "cat_page", fmt.Sprintf("%s|%d", cat.ID, page-1)))
		}
		navBtns = append(navBtns, menu.Data(fmt.Sprintf("📄 %d / %d", page+1, totalPages), "noop"))
		if page < totalPages-1 {
			navBtns = append(navBtns, menu.Data("Вперёд ➡️", "cat_page", fmt.Sprintf("%s|%d", cat.ID, page+1)))
		}
		rows = append(rows, menu.Row(navBtns...))
	}

	// 2. Кнопка выбора всех тем раздела
	btnSelectAll := menu.Data("☑️ Выбрать все темы раздела", "select_all", fmt.Sprintf("%s|%d", cat.ID, page))
	rows = append(rows, menu.Row(btnSelectAll))

	// 3. Если выбрана хотя бы 1 тема — двухколоночная плитка кнопок ЗАПУСКА (визуально отделена от полосок тем)
	selectedCount := len(selectedMap)
	if selectedCount > 0 {
		btnTrain := menu.Data(fmt.Sprintf("▶️ 🎯 Тренировка (%d)", selectedCount), "start_sprint", cat.ID)
		btnMock := menu.Data(fmt.Sprintf("▶️ 🎓 Mock-собес (%d)", selectedCount), "mock_start_custom")
		btnQuiz := menu.Data(fmt.Sprintf("▶️ ⚡ Блиц-квиз (%d)", selectedCount), "quiz_sprint", cat.ID)
		btnClear := menu.Data(fmt.Sprintf("🧹 Сбросить (%d)", selectedCount), "clear_selected", fmt.Sprintf("%s|%d", cat.ID, page))
		rows = append(rows,
			menu.Row(btnTrain, btnMock),
			menu.Row(btnQuiz, btnClear),
		)
	}

	// 4. Нижний ряд навигации
	btnAllCats := menu.Data("📂 Все разделы", "all_categories")
	btnMain := menu.Data("⬅️ В главное меню", "main_menu")
	rows = append(rows, menu.Row(btnAllCats, btnMain))

	menu.Inline(rows...)
	return menu
}

// QuestionKeyboard генерирует кнопки под вопросом (самопроверка, пропуск, завершение)
func QuestionKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnShow := menu.Data("👀 Показать ответ", "show_answer")
	btnSkip := menu.Data("⏭ Пропустить", "skip_question")
	btnStop := menu.Data("🛑 Завершить", "stop_interview")

	menu.Inline(
		menu.Row(btnShow),
		menu.Row(btnSkip, btnStop),
	)
	return menu
}

// FlashcardSelfCheckKeyboard генерирует кнопки самопроверки после открытия эталона
func FlashcardSelfCheckKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnKnew := menu.Data("✅ Знал", "flashcard_knew")
	btnForgot := menu.Data("❌ Не знал / Забыл", "flashcard_forgot")
	btnStop := menu.Data("🛑 Завершить", "stop_interview")

	menu.Inline(
		menu.Row(btnKnew, btnForgot),
		menu.Row(btnStop),
	)
	return menu
}

// EvaluationKeyboard генерирует кнопки под разбором ответа от ментора
func EvaluationKeyboard(isFinished bool) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnMain := menu.Data("⬅️ В главное меню", "main_menu")

	if isFinished {
		btnProfile := menu.Data("📊 Мой профиль", "profile")
		menu.Inline(menu.Row(btnMain, btnProfile))
		return menu
	}

	btnNext := menu.Data("➡️ Другой вопрос", "next_question")
	menu.Inline(menu.Row(btnNext, btnMain))
	return menu
}

// QuizStartMenuKeyboard генерирует меню выбора формата блиц-квиза
func QuizStartMenuKeyboard(selectedCount ...int) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	sel := 0
	if len(selectedCount) > 0 {
		sel = selectedCount[0]
	}

	btnMix := menu.Data("🎲 Все темы (случайный микс)", "quiz_mix")
	rows = append(rows, menu.Row(btnMix))

	if sel > 0 {
		btnCustom := menu.Data(fmt.Sprintf("🚀 Начать Блиц-квиз (%d тем)", sel), "quiz_sprint", "")
		rows = append(rows, menu.Row(btnCustom))
	}

	btnChooseTopic := menu.Data("📂 Выбрать темы", "all_categories")
	btnGrade := menu.Data("⚙️ Сменить грейд", "change_grade")
	rows = append(rows, menu.Row(btnChooseTopic, btnGrade))

	btnBack := menu.Data("⬅️ В главное меню", "main_menu")
	rows = append(rows, menu.Row(btnBack))

	menu.Inline(rows...)
	return menu
}

// QuizOptionsKeyboard генерирует 4 кнопки выбора ответа для блиц-квиза
func QuizOptionsKeyboard(questionID int, options []domain.QuizOption, filter string) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var optBtns []tele.Btn
	for _, opt := range options {
		label := fmt.Sprintf("[%s]", opt.ID)
		payload := fmt.Sprintf("%d|%s|%s", questionID, opt.ID, filter)
		btn := menu.Data(label, "quiz_ans", payload)
		optBtns = append(optBtns, btn)
	}

	var rows []tele.Row
	for i := 0; i < len(optBtns); i += 2 {
		if i+1 < len(optBtns) {
			rows = append(rows, menu.Row(optBtns[i], optBtns[i+1]))
		} else {
			rows = append(rows, menu.Row(optBtns[i]))
		}
	}

	btnStop := menu.Data("🛑 Завершить квиз", "main_menu")
	rows = append(rows, menu.Row(btnStop))

	menu.Inline(rows...)
	return menu
}

// QuizResultKeyboard генерирует кнопки после ответа на вопрос квиза
func QuizResultKeyboard(filter string) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnNext := menu.Data("➡️ Следующий вопрос", "quiz_next", filter)
	btnChangeTopic := menu.Data("📂 Сменить тему", "all_categories")
	btnExit := menu.Data("⬅️ В главное меню", "main_menu")

	menu.Inline(
		menu.Row(btnNext),
		menu.Row(btnChangeTopic, btnExit),
	)
	return menu
}

// MainReplyKeyboard генерирует постоянную нижнюю клавиатуру быстрого доступа с 3 режимами
func MainReplyKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{
		ResizeKeyboard: true,
	}
	btnTraining := menu.Text("🎯 Тренировка")
	btnMock := menu.Text("🎓 Mock-собеседование")
	btnQuiz := menu.Text("⚡ Блиц-квиз")
	btnProfile := menu.Text("📊 Профиль")
	btnWeak := menu.Text("🔥 Ошибки")

	menu.Reply(
		menu.Row(btnTraining, btnMock),
		menu.Row(btnQuiz, btnProfile, btnWeak),
	)
	return menu
}

// MockMenuKeyboard генерирует меню старта мок-собеседования
func MockMenuKeyboard(selectedCount int) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	var rows []tele.Row

	btnMix := menu.Data("🎲 Все темы (микс раунд)", "mock_start_mix")
	rows = append(rows, menu.Row(btnMix))

	if selectedCount > 0 {
		btnCustom := menu.Data(fmt.Sprintf("🚀 Начать по выбранным (%d тем)", selectedCount), "mock_start_custom")
		rows = append(rows, menu.Row(btnCustom))
	}

	btnChooseTopics := menu.Data("📂 Выбрать темы", "all_categories")
	btnGrade := menu.Data("⚙️ Сменить грейд", "change_grade")
	rows = append(rows, menu.Row(btnChooseTopics, btnGrade))

	btnBack := menu.Data("⬅️ В главное меню", "main_menu")
	rows = append(rows, menu.Row(btnBack))

	menu.Inline(rows...)
	return menu
}

// MockQuestionKeyboard генерирует кнопку завершения мок-интервью (с выдачей вердикта по отвеченным вопросам)
func MockQuestionKeyboard() *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}
	btnStop := menu.Data("🏁 Завершить собеседование", "stop_mock")
	menu.Inline(menu.Row(btnStop))
	return menu
}

// MockVerdictKeyboard генерирует кнопки под карточкой вердикта
func MockVerdictKeyboard(verdict domain.MockVerdictType, score float64) *tele.ReplyMarkup {
	menu := &tele.ReplyMarkup{}

	shareText := fmt.Sprintf("Я прошел Mock-собеседование по Go на вердикт %s (средний балл %.1f/10) в @GoMentorBot!", verdict, score)
	shareURL := fmt.Sprintf("https://t.me/share/url?url=https://t.me/GoMentorBot&text=%s", url.QueryEscape(shareText))
	btnShare := menu.URL("📢 Поделиться результатом", shareURL)

	btnAgain := menu.Data("🔄 Пройти еще раз", "mock_menu")
	btnMain := menu.Data("⬅️ В главное меню", "main_menu")

	menu.Inline(
		menu.Row(btnShare),
		menu.Row(btnAgain, btnMain),
	)
	return menu
}
