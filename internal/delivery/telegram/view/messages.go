package view

import (
	"fmt"
	"html"
	"strings"
	"unicode/utf8"

	"interview-bot/internal/domain"
)

// WelcomeMessage возвращает приветственное сообщение для команды /start
// WelcomeMessage возвращает приветственное сообщение для команды /start
func WelcomeMessage(firstName string) string {
	name := html.EscapeString(firstName)
	if name == "" {
		name = "разработчик"
	}

	return fmt.Sprintf(
		"👋 <b>Привет, %s!</b>\n\n"+
			"Я твой технический AI-ментор по Go.\n\n"+
			"📌 <b>Доступно 3 режима подготовки:</b>\n"+
			"• <b>🎯 Тренировка</b> — вопросы и карточки с голосовым ответом и разбором от ИИ\n"+
			"• <b>🎓 Mock-собеседование</b> — интервью с наводящими вопросами от техлида и вердиктом Hire / No Hire\n"+
			"• <b>⚡ Блиц-квиз</b> — быстрые задачи по коду с выбором ответа A/B/C/D\n\n"+
			"Выбери режим для старта:",
		name,
	)
}

// TrainingMenuMessage оформляет экран режима классической тренировки
func TrainingMenuMessage(grade string, selectedCount int) string {
	badge := "🟢 Junior"
	switch strings.ToLower(grade) {
	case "intern":
		badge = "🐣 Intern / Стажер"
	case "middle":
		badge = "🟡 Middle"
	case "senior":
		badge = "🔴 Senior"
	case "all":
		badge = "🌐 Все уровни"
	}

	msg := fmt.Sprintf(
		"🎯 <b>Режим тренировки (Вопросы и Карточки)</b>\n\n"+
			"• Отвечай <b>голосовым сообщением</b> или текстом — ИИ оценит ответ по 10-балльной шкале и укажет пробелы\n"+
			"• Или используй кнопку <b>«👀 Показать ответ»</b> для быстрой самопроверки по карточкам\n"+
			"• Слабые вопросы автоматически возвращаются в очередь до полного освоения\n\n"+
			"Твой целевой грейд: <b>%s</b>\n",
		badge,
	)
	if selectedCount > 0 {
		msg += fmt.Sprintf("\nВыбрано тем чекбоксами: <b>%d</b>\n", selectedCount)
	}
	return msg
}

// CategoriesHeaderMessage оформляет заголовок экрана выбора разделов и тем
func CategoriesHeaderMessage(selectedCount int) string {
	msg := "📂 <b>Выбор разделов и тем</b>\n\n" +
		"Открой нужный раздел, отметь темы галочками и запусти любой режим прямо оттуда:"
	if selectedCount > 0 {
		msg += fmt.Sprintf("\n\n✅ Сейчас выбрано тем: <b>%d</b>", selectedCount)
	}
	return msg
}

// CategoryHeaderMessage оформляет заголовок выбранного раздела
func CategoryHeaderMessage(cat domain.Category, totalTopics int) string {
	return fmt.Sprintf(
		"<b>%s</b>\n<i>%s</i>\n\n"+
			"Тем в разделе: <b>%d</b>. Выбери тему для тренировки:",
		cat.Title,
		html.EscapeString(cat.Description),
		totalTopics,
	)
}

// QuestionMessage оформляет карточку вопроса
func QuestionMessage(q *domain.Question) string {
	diffBadge := "🟢 Junior"
	switch q.Difficulty {
	case domain.DifficultyIntern:
		diffBadge = "🐣 Стажер"
	case domain.DifficultyMiddle:
		diffBadge = "🟡 Middle"
	case domain.DifficultySenior:
		diffBadge = "🔴 Senior"
	}
	return fmt.Sprintf(
		"❓ <b>Вопрос [%s]:</b>\n\n"+
			"%s\n\n"+
			"<i>🎙 Отправь ответ голосовым сообщением или напиши текстом:</i>",
		diffBadge,
		formatAnswerHTML(q.Text),
	)
}

// maxTotalUTF16 — жесткий потолок под лимит Telegram 4096 UTF-16 единиц (с запасом на HTML-теги и баннер финиша)
const maxTotalUTF16 = 3800

// EvaluationMessage оформляет разбор ответа и эталон строго в ОДНО сообщение
func EvaluationMessage(eval *domain.Evaluation, currentQ *domain.Question, userAnswer string, isVoice bool) string {
	sb := strings.Builder{}
	usedUnits := 0

	// 1. Компактное эхо ответа пользователя в блоке цитаты
	if userAnswer != "" {
		shortUser := truncateUTF16(strings.TrimSpace(userAnswer), 250)
		label := "📝 <b>Твой ответ:</b>"
		if isVoice {
			label = "🎙 <b>Твой голосовой ответ:</b>"
		}
		sb.WriteString(fmt.Sprintf("<blockquote>%s\n<i>«%s»</i></blockquote>\n\n", label, html.EscapeString(shortUser)))
		usedUnits += utf16Len(shortUser) + 35
	}

	// 2. Оценка и визуальная шкала
	status := "🟡 Надо доработать"
	if eval.IsCorrect {
		status = "🟢 Ответ зачтен!"
	}
	bar := scoreBar(eval.Score)
	sb.WriteString(fmt.Sprintf("🏆 <b>Оценка ментора: %d / 10</b> (%s)\n%s\n\n", eval.Score, status, bar))
	usedUnits += 70 // с учетом суррогатных пар цветных кубиков

	// 3. Разбор ответа
	shortFeedback := truncateUTF16(strings.TrimSpace(eval.Feedback), 750)
	sb.WriteString(fmt.Sprintf("💡 <b>Разбор ответа:</b>\n%s\n\n", formatInlineCode(shortFeedback)))
	usedUnits += utf16Len(shortFeedback) + 25

	// 4. Упущенные ключевые тезисы
	if len(eval.MissingPoints) > 0 {
		sb.WriteString("⚠️ <b>Что ты упустил:</b>\n")
		usedUnits += 25
		for _, point := range eval.MissingPoints {
			p := truncateUTF16(strings.TrimSpace(point), 140)
			sb.WriteString(fmt.Sprintf("• %s\n", formatInlineCode(p)))
			usedUnits += utf16Len(p) + 4
		}
		sb.WriteString("\n")
	}

	// 5. Эталонный ответ с подсветкой Go-кода в рамках оставшегося бюджета UTF-16 единиц
	if currentQ != nil && strings.TrimSpace(currentQ.Answer) != "" {
		budget := maxTotalUTF16 - usedUnits - 50
		if budget > 300 {
			safeRawAnswer := truncateUTF16(strings.TrimSpace(currentQ.Answer), budget)
			sb.WriteString("📖 <b>Эталонный ответ:</b>\n")
			sb.WriteString(formatAnswerHTML(safeRawAnswer))
		}
	}

	return sb.String()
}

// utf16Len считает длину строки в UTF-16 code units (как Telegram Bot API)
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2 // суррогатная пара для цветных эмодзи и символов вне BMP
		} else {
			n++
		}
	}
	return n
}

// truncateUTF16 безопасно обрезает строку по UTF-16 code units
func truncateUTF16(s string, maxUnits int) string {
	units := 0
	for i, r := range s {
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if units+w > maxUnits {
			return strings.TrimRight(s[:i], "`") + "…"
		}
		units += w
	}
	return s
}

// scoreBar рисует шкалу из 10 кубиков по оценке 1..10
// scoreBar рисует идеально ровную моноширинную шкалу из 10 делений
// scoreBar рисует лаконичные сегменты оценки 1..10 без рамок и плашек
func scoreBar(score int) string {
	if score < 0 {
		score = 0
	}
	if score > 10 {
		score = 10
	}
	filled := strings.Repeat("▰", score)
	empty := strings.Repeat("▱", 10-score)
	return filled + empty
}

// formatAnswerHTML переводит блоки ```go ... ``` в <pre><code class="language-go">, безопасно экранируя остальной текст
func formatAnswerHTML(raw string) string {
	parts := strings.Split(raw, "```")
	var out strings.Builder

	for i, part := range parts {
		if i%2 == 0 {
			out.WriteString(formatInlineCode(part)) // вместо html.EscapeString(part)
			continue
		}

		codeBlock := part
		lang := ""
		if nl := strings.IndexByte(codeBlock, '\n'); nl != -1 {
			firstLine := strings.TrimSpace(codeBlock[:nl])
			if firstLine == "" {
				codeBlock = codeBlock[nl+1:]
			} else if isSimpleLang(firstLine) {
				lang = firstLine
				codeBlock = codeBlock[nl+1:]
			}
		}
		codeBlock = strings.TrimSuffix(codeBlock, "\n")
		if lang != "" {
			out.WriteString(`<pre><code class="language-`)
			out.WriteString(lang)
			out.WriteString(`">`)
		} else {
			out.WriteString("<pre><code>")
		}
		out.WriteString(html.EscapeString(codeBlock))
		out.WriteString("</code></pre>")
	}

	// Если блок кода был открыт и обрезан до закрывающих бэктиков — закрываем тег
	if len(parts)%2 == 0 {
		out.WriteString("</code></pre>")
	}

	return out.String()
}

func isSimpleLang(s string) bool {
	if len(s) == 0 || len(s) > 15 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

func truncateText(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + "…"
}

// FlashcardAnswerMessage оформляет вопрос и эталонный ответ для режима блиц-самопроверки
func FlashcardAnswerMessage(q *domain.Question) string {
	diffBadge := "🟢 Junior"
	switch q.Difficulty {
	case domain.DifficultyIntern:
		diffBadge = "🐣 Стажер"
	case domain.DifficultyMiddle:
		diffBadge = "🟡 Middle"
	case domain.DifficultySenior:
		diffBadge = "🔴 Senior"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("❓ <b>Вопрос [%s]:</b>\n\n%s\n\n", diffBadge, formatInlineCode(q.Text)))

	// Безопасно укладываемся в лимит Telegram (до 3300 UTF-16 единиц под ответ)
	safeAnswer := truncateUTF16(strings.TrimSpace(q.Answer), 3300)
	sb.WriteString(formatAnswerHTML(safeAnswer))
	sb.WriteString("\n\n<i>🎯 Честно оцени свои знания:</i>")

	return sb.String()
}

// ProfileMessage форматирует экран аналитики и готовности пользователя
// ProfileMessage форматирует экран аналитики и готовности пользователя с бейджем грейда
// ProfileMessage форматирует экран аналитики и готовности пользователя под целевой грейд
func ProfileMessage(user *domain.User, stats *domain.UserStats) string {
	name := "разработчик"
	targetGrade := "all"
	if user != nil {
		if user.FirstName != "" {
			name = user.FirstName
		}
		if user.TargetGrade != "" {
			targetGrade = user.TargetGrade
		}
	}
	escapedName := html.EscapeString(name)

	gradeBadge := "🌐 Все уровни"
	gradeTargetLabel := "собеседованиям"
	switch targetGrade {
	case "intern":
		gradeBadge = "🐣 Intern (Стажер)"
		gradeTargetLabel = "Intern-собеседованию"
	case "junior":
		gradeBadge = "🟢 Junior"
		gradeTargetLabel = "Junior-собеседованию"
	case "middle":
		gradeBadge = "🟡 Middle"
		gradeTargetLabel = "Middle-собеседованию"
	case "senior":
		gradeBadge = "🔴 Senior"
		gradeTargetLabel = "Senior-собеседованию"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("👤 <b>Профиль инженера:</b> %s\n", escapedName))
	sb.WriteString(fmt.Sprintf("🎯 <b>Целевой грейд:</b> %s\n\n", gradeBadge))

	// Динамическая шкала готовности к целевому грейду
	bar := progressBar(stats.ReadyPercent)
	sb.WriteString(fmt.Sprintf("📈 <b>Готовность к %s: %d%%</b>\n%s <i>(%d из %d вопросов)</i>\n\n",
		gradeTargetLabel, stats.ReadyPercent, bar, stats.MasteredCount, stats.TotalQuestions))

	// Общая статистика
	sb.WriteString("📊 <b>Статистика:</b>\n")
	sb.WriteString(fmt.Sprintf("• Освоено (балл 6+): <b>%d</b> 🟢\n", stats.MasteredCount))
	if stats.WeakCount > 0 {
		sb.WriteString(fmt.Sprintf("• Ошибок для отработки: <b>%d</b> ⚠️\n", stats.WeakCount))
	} else {
		sb.WriteString("• Ошибок нет: <b>0</b> 🏆\n")
	}
	sb.WriteString(fmt.Sprintf("• Средний балл: <b>%.1f / 10</b>\n\n", stats.AverageScore))

	// Детализация по 6 категориям в рамках выбранного грейда
	sb.WriteString("📚 <b>Прогресс по разделам:</b>\n")
	for _, c := range stats.Categories {
		sb.WriteString(fmt.Sprintf("• %s: <b>%d%%</b> <i>(%d/%d)</i>\n", c.Title, c.Percent, c.Mastered, c.TotalCount))
	}

	return sb.String()
}

// GradeSelectMessage выводит текст описания всех 4 грейдов
func GradeSelectMessage() string {
	return "🎯 <b>Выбери целевой грейд для собеседований:</b>\n\n" +
		"Программа и вопросы подстроятся под твой уровень:\n" +
		"• <b>Intern:</b> базовые типы, синтаксис, переменные, простые циклы (101 вопрос)\n" +
		"• <b>Junior:</b> слайсы, мапы, интерфейсы, основы горутин и каналов (330 вопросов)\n" +
		"• <b>Middle:</b> runtime, планировщик GMP, SQL/транзакции, гонки данных (600 вопросов)\n" +
		"• <b>Senior:</b> lock-free, шардирование, микросервисы, highload (860 вопросов)\n" +
		"• <b>Все уровни:</b> полный банк без ограничений"
}

// WeakTopicsMessage выводит меню выбора темы с ошибками
func WeakTopicsMessage(totalWeak int) string {
	return fmt.Sprintf(
		"🔥 <b>Работа над ошибками:</b>\n\n"+
			"У тебя найдено <b>%d</b> вопросов, требующих повторения (балл &lt; 6).\n\n"+
			"Выбери конкретную тему для отработки или нажми «Все ошибки сразу»:",
		totalWeak,
	)
}

// progressBar рисует ровную сегментную шкалу готовности 0..100%
func progressBar(percent int) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := percent / 10
	empty := 10 - filled
	return strings.Repeat("▰", filled) + strings.Repeat("▱", empty)
}

// formatInlineCode превращает парные `код` в <code>код</code>, безопасно экранируя остальной текст
func formatInlineCode(s string) string {
	parts := strings.Split(s, "`")
	if len(parts) == 1 {
		return html.EscapeString(s)
	}

	var out strings.Builder
	for i, part := range parts {
		if i%2 == 0 {
			out.WriteString(html.EscapeString(part))
		} else {
			// Если кавычка была непарной в конце строки — не ломаем HTML
			if i == len(parts)-1 {
				out.WriteString("`")
				out.WriteString(html.EscapeString(part))
			} else {
				out.WriteString("<code>")
				out.WriteString(html.EscapeString(part))
				out.WriteString("</code>")
			}
		}
	}
	return out.String()
}


// QuizQuestionMessage оформляет карточку блиц-вопроса
func QuizQuestionMessage(q *domain.QuizQuestion) string {
	diffBadge := "🟢 Junior"
	switch q.Difficulty {
	case domain.DifficultyIntern:
		diffBadge = "🐣 Intern"
	case domain.DifficultyMiddle:
		diffBadge = "🟡 Middle"
	case domain.DifficultySenior:
		diffBadge = "🔴 Senior"
	}

	var sb strings.Builder
	topicHeader := "⚡ <b>Блиц-тест</b>"
	if q.TopicTitle != "" {
		topicHeader += fmt.Sprintf(" • <b>%s</b>", html.EscapeString(q.TopicTitle))
	}
	sb.WriteString(fmt.Sprintf("%s\nУровень: <b>[%s]</b>\n\n", topicHeader, diffBadge))

	// Форматируем текст вопроса и сниппет кода
	sb.WriteString(formatAnswerHTML(q.Question))
	sb.WriteString("\n\n<b>Варианты ответа:</b>\n")

	for _, opt := range q.Options {
		sb.WriteString(fmt.Sprintf("<b>[%s]</b> %s\n", opt.ID, html.EscapeString(opt.Text)))
	}

	sb.WriteString("\n<i>Выбери вариант ответа на клавиатуре ниже:</i>")
	return sb.String()
}

// QuizResultMessage оформляет разбор после выбора ответа пользователем
func QuizResultMessage(q *domain.QuizQuestion, selectedOpt string, isCorrect bool) string {
	diffBadge := "🟢 Junior"
	switch q.Difficulty {
	case domain.DifficultyIntern:
		diffBadge = "🐣 Intern"
	case domain.DifficultyMiddle:
		diffBadge = "🟡 Middle"
	case domain.DifficultySenior:
		diffBadge = "🔴 Senior"
	}

	var sb strings.Builder
	if isCorrect {
		sb.WriteString(fmt.Sprintf("✅ <b>ВЕРНО!</b> Твой ответ: <b>[%s]</b>\n\n", selectedOpt))
	} else {
		sb.WriteString(fmt.Sprintf("❌ <b>НЕВЕРНО!</b> Ты выбрал <b>[%s]</b>, а правильный ответ: <b>[%s]</b>\n\n", selectedOpt, q.CorrectOption))
	}

	topicHeader := "⚡ <b>Блиц-тест</b>"
	if q.TopicTitle != "" {
		topicHeader += fmt.Sprintf(" • <b>%s</b>", html.EscapeString(q.TopicTitle))
	}
	sb.WriteString(fmt.Sprintf("%s [%s]\n\n", topicHeader, diffBadge))

	// Вопрос
	sb.WriteString(formatAnswerHTML(q.Question))
	sb.WriteString("\n\n<b>Варианты:</b>\n")

	for _, opt := range q.Options {
		icon := "  "
		if strings.EqualFold(opt.ID, q.CorrectOption) {
			icon = "✅ "
		} else if !isCorrect && strings.EqualFold(opt.ID, selectedOpt) {
			icon = "❌ "
		}
		sb.WriteString(fmt.Sprintf("%s<b>[%s]</b> %s\n", icon, opt.ID, html.EscapeString(opt.Text)))
	}

	if q.Explanation != "" {
		sb.WriteString(fmt.Sprintf("\n💡 <b>Разбор и объяснение:</b>\n%s\n", formatAnswerHTML(q.Explanation)))
	}

	return sb.String()
}


// QuizStartMenuMessage оформляет меню выбора режима блиц-тестирования
func QuizStartMenuMessage() string {
	return "⚡ <b>Блиц-квиз (Multiple Choice)</b>\n\n" +
		"Быстрые тесты с 4 вариантами ответов, мгновенной проверкой и подробным разбором кода.\n\n" +
		"В нашей базе <b>1 686 вопросов</b> по всем <b>85 темам</b> Go.\n\n" +
		"<b>Выбери формат тренировки:</b>\n" +
		"• <b>🎲 Случайный микс</b> — проверяет общую эрудицию по всем темам сразу\n" +
		"• <b>📂 Выбрать раздел и тему</b> — целенаправленная отработка конкретного топика"
}

// MockMenuMessage оформляет экран выбора формата мок-собеседования
func MockMenuMessage(grade string, selectedCount int) string {
	badge := "🟢 Junior"
	switch strings.ToLower(grade) {
	case "intern":
		badge = "🐣 Intern / Стажер"
	case "middle":
		badge = "🟡 Middle"
	case "senior":
		badge = "🔴 Senior"
	case "all":
		badge = "🌐 Все уровни"
	}

	questionsInfo := "<b>По 2 вопроса на каждую выбранную тему</b> (или 10 вопросов в режиме общего микса)"
	if selectedCount > 0 {
		questionsInfo = fmt.Sprintf("<b>До %d вопросов</b> (по 2 вопроса на каждую из %d выбранных тем)", selectedCount*2, selectedCount)
	}

	msg := fmt.Sprintf(
		"🎓 <b>Режим Mock-собеседования</b>\n\n"+
			"Полноценная имитация технического интервью на Go (~40 минут):\n"+
			"• %s\n"+
			"• <b>Интерактивный интервьюер:</b> ИИ задаст наводящие и уточняющие вопросы, чтобы помочь раскрыть тему\n"+
			"• <b>Итоговый вердикт:</b> Hire / No-Hire, подтвержденный грейд и разбор сильных/слабых мест (можно завершить и получить вердикт в любой момент)\n\n"+
			"Твой текущий грейд: <b>%s</b>\n",
		questionsInfo,
		badge,
	)
	if selectedCount > 0 {
		msg += fmt.Sprintf("\nВыбрано тем для собеседования: <b>%d</b>\n", selectedCount)
	}
	return msg
}

// MockQuestionMessage оформляет карточку вопроса мок-интервью
func MockQuestionMessage(q *domain.Question, num, total int) string {
	diffBadge := "🟢 Junior"
	switch q.Difficulty {
	case domain.DifficultyIntern:
		diffBadge = "🐣 Стажер"
	case domain.DifficultyMiddle:
		diffBadge = "🟡 Middle"
	case domain.DifficultySenior:
		diffBadge = "🔴 Senior"
	}

	return fmt.Sprintf(
		"🎓 <b>Mock-собеседование • Вопрос %d из %d</b> [%s]\n\n"+
			"%s\n\n"+
			"<i>🎙 Ответь голосовым сообщением или текстом:</i>",
		num,
		total,
		diffBadge,
		formatAnswerHTML(q.Text),
	)
}

// MockFollowUpMessage оформляет уточняющий вопрос от интервьюера
func MockFollowUpMessage(followUpQ string, num, total int) string {
	return fmt.Sprintf(
		"🎯 <b>Вопрос %d из %d • Уточнение от интервьюера:</b>\n\n"+
			"«<i>%s</i>»\n\n"+
			"<i>💡 Поясни свою мысль голосовым сообщением или текстом:</i>",
		num,
		total,
		html.EscapeString(followUpQ),
	)
}

// MockVerdictMessage оформляет итоговый вердикт собеседования
func MockVerdictMessage(v *domain.MockVerdict) string {
	verdictIcon := "🟢"
	switch v.Verdict {
	case domain.VerdictStrongHire:
		verdictIcon = "🔥"
	case domain.VerdictHire:
		verdictIcon = "✅"
	case domain.VerdictWeakHire:
		verdictIcon = "🟡"
	case domain.VerdictNoHire:
		verdictIcon = "🔴"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"🏁 <b>MOCK-СОБЕСЕДОВАНИЕ ЗАВЕРШЕНО</b>\n\n"+
			"🎯 <b>Вердикт:</b> %s <b>%s</b>\n"+
			"📊 <b>Подтвержденный грейд:</b> <b>%s</b>\n"+
			"⭐️ <b>Средний балл:</b> <b>%.1f / 10</b>\n\n"+
			"📝 <b>Резюме интервьюера:</b>\n<i>%s</i>\n\n",
		verdictIcon,
		v.Verdict,
		html.EscapeString(v.ConfirmedGrade),
		v.AverageScore,
		html.EscapeString(v.Summary),
	))

	if len(v.Strengths) > 0 {
		sb.WriteString("🌟 <b>Сильные стороны:</b>\n")
		for _, s := range v.Strengths {
			sb.WriteString(fmt.Sprintf("• %s\n", html.EscapeString(s)))
		}
		sb.WriteString("\n")
	}

	if len(v.Weaknesses) > 0 {
		sb.WriteString("⚠️ <b>Зоны роста:</b>\n")
		for _, w := range v.Weaknesses {
			sb.WriteString(fmt.Sprintf("• %s\n", html.EscapeString(w)))
		}
		sb.WriteString("\n")
	}

	if v.Recommendations != "" {
		sb.WriteString(fmt.Sprintf("💡 <b>Рекомендации:</b>\n<i>%s</i>\n", html.EscapeString(v.Recommendations)))
	}

	return sb.String()
}
