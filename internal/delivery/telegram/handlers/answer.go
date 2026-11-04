package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	tele "gopkg.in/telebot.v3"

	"interview-bot/internal/delivery/telegram/view"
	"interview-bot/internal/domain"
	"interview-bot/internal/usecase"
)

const (
	maxTextAnswerRunes  = 2000
	maxVoiceDurationSec = 120
	maxVoiceBytes       = 2 << 20 // 2 МБ
	handlerTimeout      = 70 * time.Second
	voiceFetchTimeout   = 10 * time.Second
)

func TextAnswer(interviewUC usecase.InterviewUsecase, mockUC usecase.MockInterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		if c.Sender() == nil {
			return nil
		}
		text := strings.TrimSpace(c.Text())

		if text == "" || strings.HasPrefix(text, "/") {
			return nil
		}

		switch text {
		case "⚡ Блиц-квиз", "🎯 Тренировка", "🎯 Собеседование", "📊 Профиль", "📊 Мой профиль", "🔥 Ошибки", "🎓 Mock-собеседование":
			return nil
		}

		runeCount := utf8.RuneCountInString(text)
		if runeCount > maxTextAnswerRunes {
			return c.Send("⚠️ Ответ слишком длинный (максимум 2000 символов). Сформулируй мысль чуть компактнее — как на реальном собеседовании.")
		}
		if runeCount < 2 {
			return handleAnswerError(c, usecase.ErrAnswerTooShort, false)
		}

		_ = c.Notify(tele.Typing)
		ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
		defer cancel()

		if qMsgID, _ := interviewUC.GetQuestionMsgID(ctx, c.Sender().ID); qMsgID != 0 {
			_, _ = c.Bot().EditReplyMarkup(&tele.Message{ID: qMsgID, Chat: c.Chat()}, nil)
		}

		var statusMsg *tele.Message
		ctx = usecase.WithEvalStartCallback(ctx, func(isMock bool) {
			if statusMsg != nil {
				return
			}
			label := "⏳ <i>Оцениваю ответ...</i>"
			if isMock {
				label = "⏳ <i>Интервьюер анализирует ответ...</i>"
			}
			statusMsg, _ = c.Bot().Send(c.Chat(), label, tele.ModeHTML)
		})

		mockRes, mockErr := mockUC.SubmitMockTextAnswer(ctx, c.Sender().ID, text)
		if mockErr == nil {
			return handleMockAnswerResult(c, mockRes, interviewUC, statusMsg, "")
		}
		if !errors.Is(mockErr, usecase.ErrNoActiveSession) {
			deleteStatusMsg(c, statusMsg)
			return handleAnswerError(c, mockErr, false)
		}

		eval, currentQ, isFinished, err := interviewUC.SubmitTextAnswer(ctx, c.Sender().ID, text)
		if err != nil {
			deleteStatusMsg(c, statusMsg)
			return handleAnswerError(c, err, false)
		}
		evalMsg := view.EvaluationMessage(eval, currentQ, text, false)
		return sendOrEditEvaluation(c, statusMsg, evalMsg, eval.Score, isFinished)
	}
}

func VoiceAnswer(b *tele.Bot, interviewUC usecase.InterviewUsecase, mockUC usecase.MockInterviewUsecase) tele.HandlerFunc {
	return func(c tele.Context) error {
		if c.Sender() == nil || c.Message() == nil || c.Message().Voice == nil {
			return nil
		}
		voice := c.Message().Voice
		if voice.Duration > maxVoiceDurationSec {
			return c.Send("⚠️ Голосовое слишком длинное (максимум 2 минуты). Попробуй ответить короче и по сути.")
		}
		if voice.FileSize > maxVoiceBytes {
			return c.Send("⚠️ Аудиофайл слишком большой (максимум 2 МБ).")
		}

		ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
		defer cancel()

		var statusMsg *tele.Message

		fetchAudio := func(fetchCtx context.Context) (io.Reader, error) {
			_ = c.Notify(tele.RecordingAudio)
			if statusMsg == nil {
				statusMsg, _ = c.Bot().Send(c.Chat(), "🎙 <i>Распознаю голосовое...</i>", tele.ModeHTML)
			}
			return downloadVoice(fetchCtx, b, &voice.File)
		}

		ctx = usecase.WithTranscriptCallback(ctx, func(transcript string) {
			progressText := fmt.Sprintf("🗣 <i>«%s»</i>\n\n⏳ <i>Оцениваю ответ...</i>", html.EscapeString(transcript))
			if statusMsg != nil {
				if edited, err := c.Bot().Edit(statusMsg, progressText, tele.ModeHTML); err == nil && edited != nil {
					statusMsg = edited
				}
			} else {
				statusMsg, _ = c.Bot().Send(c.Chat(), progressText, tele.ModeHTML)
			}
		})

		if qMsgID, _ := interviewUC.GetQuestionMsgID(ctx, c.Sender().ID); qMsgID != 0 {
			_, _ = c.Bot().EditReplyMarkup(&tele.Message{ID: qMsgID, Chat: c.Chat()}, nil)
		}

		transcript, mockRes, mockErr := mockUC.SubmitMockVoiceAnswer(ctx, c.Sender().ID, fetchAudio, "voice.ogg")
		if mockErr == nil {
			return handleMockAnswerResult(c, mockRes, interviewUC, statusMsg, transcript)
		}
		if !errors.Is(mockErr, usecase.ErrNoActiveSession) {
			deleteStatusMsg(c, statusMsg)
			return handleAnswerError(c, mockErr, true)
		}

		transcript, eval, currentQ, isFinished, err := interviewUC.SubmitVoiceAnswer(ctx, c.Sender().ID, fetchAudio, "voice.ogg")
		if err != nil {
			deleteStatusMsg(c, statusMsg)
			return handleAnswerError(c, err, true)
		}
		evalMsg := view.EvaluationMessage(eval, currentQ, transcript, true)
		return sendOrEditEvaluation(c, statusMsg, evalMsg, eval.Score, isFinished)
	}
}

func handleMockAnswerResult(c tele.Context, res *domain.MockAnswerResult, interviewUC usecase.InterviewUsecase, statusMsg *tele.Message, voiceTranscript string) error {
	if res == nil {
		return nil
	}

	if res.IsFollowUp {
		msg := view.MockFollowUpMessage(res.FollowUpQ, res.QuestionNum, res.TotalNum)
		if voiceTranscript != "" {
			cleanVoice := fmt.Sprintf("🗣 <i>«%s»</i>", html.EscapeString(voiceTranscript))
			if statusMsg != nil {
				_, _ = c.Bot().Edit(statusMsg, cleanVoice, tele.ModeHTML)
			} else {
				_ = c.Send(cleanVoice, tele.ModeHTML)
			}
			sent, err := c.Bot().Send(c.Chat(), msg, view.MockQuestionKeyboard(), tele.ModeHTML)
			if err == nil && sent != nil {
				saveCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = interviewUC.SaveQuestionMsgID(saveCtx, c.Sender().ID, sent.ID)
			}
			return err
		}

		if statusMsg != nil {
			if edited, err := c.Bot().Edit(statusMsg, msg, view.MockQuestionKeyboard(), tele.ModeHTML); err == nil && edited != nil {
				saveCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = interviewUC.SaveQuestionMsgID(saveCtx, c.Sender().ID, edited.ID)
				return nil
			}
			deleteStatusMsg(c, statusMsg)
		}

		sent, err := c.Bot().Send(c.Chat(), msg, view.MockQuestionKeyboard(), tele.ModeHTML)
		if err == nil && sent != nil {
			saveCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = interviewUC.SaveQuestionMsgID(saveCtx, c.Sender().ID, sent.ID)
		}
		return err
	}

	if res.Eval != nil {
		badge := "🟡"
		if res.Eval.Score >= 8 {
			badge = "🟢"
		} else if res.Eval.Score < 6 {
			badge = "🔴"
		}
		scoreText := fmt.Sprintf("%s <b>Вопрос %d из %d оценен: %d / 10</b>\n\n<i>%s</i>",
			badge, res.QuestionNum, res.TotalNum, res.Eval.Score, html.EscapeString(res.Eval.Feedback))
		if voiceTranscript != "" {
			scoreText = fmt.Sprintf("🗣 <i>«%s»</i>\n\n%s", html.EscapeString(voiceTranscript), scoreText)
		}

		if statusMsg != nil {
			if _, err := c.Bot().Edit(statusMsg, scoreText, tele.ModeHTML); err != nil {
				deleteStatusMsg(c, statusMsg)
				_ = c.Send(scoreText, tele.ModeHTML)
			}
		} else {
			_ = c.Send(scoreText, tele.ModeHTML)
		}
	}

	if res.IsFinished && res.FinalVerdict != nil {
		verdictMsg := view.MockVerdictMessage(res.FinalVerdict)
		kb := view.MockVerdictKeyboard(res.FinalVerdict.Verdict, res.FinalVerdict.AverageScore)
		return c.Send(verdictMsg, kb, tele.ModeHTML)
	}

	if res.NextQuestion != nil {
		msg := view.MockQuestionMessage(res.NextQuestion, res.QuestionNum, res.TotalNum)
		sent, err := c.Bot().Send(c.Chat(), msg, view.MockQuestionKeyboard(), tele.ModeHTML)
		if err == nil && sent != nil {
			saveCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = interviewUC.SaveQuestionMsgID(saveCtx, c.Sender().ID, sent.ID)
		}
		return err
	}

	return nil
}

func downloadVoice(ctx context.Context, b *tele.Bot, file *tele.File) (io.Reader, error) {
	dlCtx, cancel := context.WithTimeout(ctx, voiceFetchTimeout)
	defer cancel()

	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		rc, err := b.File(file)
		if err != nil {
			ch <- result{err: fmt.Errorf("%w: %v", usecase.ErrVoiceDownload, err)}
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(rc, maxVoiceBytes+1))
		_ = rc.Close()
		if readErr != nil {
			ch <- result{err: fmt.Errorf("%w: %v", usecase.ErrVoiceDownload, readErr)}
			return
		}
		if len(data) > maxVoiceBytes {
			ch <- result{err: errors.New("размер аудиофайла превышает лимит")}
			return
		}
		ch <- result{data: data}
	}()

	select {
	case <-dlCtx.Done():
		return nil, fmt.Errorf("%w: таймаут скачивания аудио", usecase.ErrVoiceDownload)
	case res := <-ch:
		if res.err != nil {
			return nil, res.err
		}
		return bytes.NewReader(res.data), nil
	}
}

func sendOrEditEvaluation(c tele.Context, statusMsg *tele.Message, evalMsg string, score int, isFinished bool) error {
	kb := view.EvaluationKeyboard(isFinished)
	if statusMsg != nil {
		if _, err := c.Bot().Edit(statusMsg, evalMsg, kb, tele.ModeHTML); err == nil {
			return nil
		}
		deleteStatusMsg(c, statusMsg)
	}
	return c.Send(evalMsg, kb, tele.ModeHTML)
}

func deleteStatusMsg(c tele.Context, statusMsg *tele.Message) {
	if statusMsg != nil {
		_ = c.Bot().Delete(statusMsg)
	}
}

func handleAnswerError(c tele.Context, err error, isVoice bool) error {
	switch {
	case errors.Is(err, usecase.ErrAnswerTooShort):
		if isVoice {
			return c.Send("⚠️ Голосовое сообщение слишком короткое или не удалось разобрать слова. Попробуй наговорить ответ еще раз подробнее.")
		}
		return c.Send("⚠️ Ответ слишком короткий. Сформулируй мысль подробнее.")
	case errors.Is(err, usecase.ErrRateLimited):
		return c.Send("⏳ Слишком частые запросы к AI. Подожди 3 секунды перед отправкой следующего ответа.")
	case errors.Is(err, usecase.ErrNoActiveSession):
		kb := view.MainMenuKeyboard()
		return c.Send("⚠️ У тебя нет активного вопроса. Выбери режим подготовки в меню:", kb)
	case errors.Is(err, usecase.ErrWaitingNext):
		return c.Send("ℹ️ Ты уже ответил на этот вопрос. Нажми «Другой вопрос» или выбери новую тему в меню.")
	case errors.Is(err, usecase.ErrAlreadyEvaluating):
		return c.Send("⏳ Твой предыдущий ответ уже проверяется нейросетью. Дождись завершения.")
	case errors.Is(err, usecase.ErrVoiceDownload):
		return c.Send("⚠️ Не удалось загрузить аудиофайл из Telegram. Попробуй отправить голосовое еще раз.")
	default:
		slog.Error("ошибка при обработке ответа", "user_id", c.Sender().ID, "error", err)
		return c.Send("⚠️ Произошла ошибка при оценке ответа. Пожалуйста, попробуй позже.")
	}
}
