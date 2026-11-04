package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"interview-bot/internal/config"
)

type RegeneratedQuestion struct {
	ID                int      `json:"id"`
	TopicID           string   `json:"topic_id"`
	TopicTitle        string   `json:"topic_title"`
	Difficulty        string   `json:"difficulty"`
	OriginalQuestion  string   `json:"original_question"`
	OriginalAnswer    string   `json:"original_answer"`
	OriginalKeyPoints []string `json:"original_key_points"`
	IssueTypes        []string `json:"issue_types,omitempty"`
	Critique          string   `json:"critique,omitempty"`
	NewQuestion       string   `json:"new_question"`
	NewAnswer         string   `json:"new_answer"`
	NewKeyPoints      []string `json:"new_key_points"`
	UpdatedAt         string   `json:"updated_at"`
}

type AnswerItem struct {
	TopicID    string `json:"topic_id"`
	Difficulty string `json:"difficulty"`
	Question   string `json:"question"`
}

func main() {
	inputPath := flag.String("input", "regenerated_questions.json", "Путь к файлу regenerated_questions.json")
	answersPath := flag.String("answers", "answers.json", "Путь к файлу answers.json")
	dryRun := flag.Bool("dry-run", false, "Режим предпросмотра без внесения изменений")
	rollback := flag.Bool("rollback", false, "Откатить изменения из бэкапа (questions_backup_audit и answers_backup_audit.json)")
	flag.Parse()

	log.Println("🛠️ Инициализация патчера базы вопросов Go Mentor...")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ Ошибка загрузки конфигурации: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pgPool, err := pgxpool.New(ctx, cfg.Postgres.URL)
	if err != nil {
		log.Fatalf("❌ Не удалось подключиться к PostgreSQL: %v", err)
	}
	defer pgPool.Close()
	log.Println("🐘 Подключение к PostgreSQL успешно установлено")

	// ==========================================
	// РЕЖИМ ОТКАТА (ROLLBACK)
	// ==========================================
	if *rollback {
		log.Println("⏪ Запуск отката изменений из бэкапа...")

		var tableExists bool
		err = pgPool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT FROM information_schema.tables 
				WHERE table_schema = 'public' AND table_name = 'questions_backup_audit'
			)
		`).Scan(&tableExists)

		if err != nil || !tableExists {
			log.Fatalf("❌ Резервная таблица questions_backup_audit не найдена в PostgreSQL: %v", err)
		}

		// Восстанавливаем PostgreSQL
		cmdTag, err := pgPool.Exec(ctx, `
			UPDATE questions q
			SET text = b.text, answer = b.answer, key_points = b.key_points
			FROM questions_backup_audit b
			WHERE q.id = b.id;
		`)
		if err != nil {
			log.Fatalf("❌ Ошибка восстановления таблицы questions в PostgreSQL: %v", err)
		}
		log.Printf("✅ Таблица questions в PostgreSQL успешно восстановлена (%d строк)!", cmdTag.RowsAffected())

		// Восстанавливаем answers.json
		backupAnswersPath := "answers_backup_audit.json"
		if backupData, err := os.ReadFile(backupAnswersPath); err == nil {
			if err := os.WriteFile(*answersPath, backupData, 0644); err != nil {
				log.Printf("⚠️ Ошибка восстановления %s: %v", *answersPath, err)
			} else {
				log.Printf("✅ Файл %s успешно восстановлен из %s!", *answersPath, backupAnswersPath)
			}
		} else {
			log.Printf("ℹ️ Файл бэкапа %s не найден, пропускаем.", backupAnswersPath)
		}

		log.Println("🎉 Откат успешно завершён!")
		return
	}

	// ==========================================
	// ЧТЕНИЕ ФАЙЛА С РЕГЕНЕРИРОВАННЫМИ ВОПРОСАМИ
	// ==========================================
	data, err := os.ReadFile(*inputPath)
	if err != nil {
		log.Fatalf("❌ Не удалось прочитать %s: %v", *inputPath, err)
	}

	var regenerated []RegeneratedQuestion
	if err := json.Unmarshal(data, &regenerated); err != nil {
		log.Fatalf("❌ Ошибка парсинга JSON в %s: %v", *inputPath, err)
	}

	var fixesToApply []RegeneratedQuestion
	for _, q := range regenerated {
		newQ := strings.TrimSpace(q.NewQuestion)
		newAns := strings.TrimSpace(q.NewAnswer)
		if newQ != "" && newAns != "" && len(q.NewKeyPoints) > 0 {
			fixesToApply = append(fixesToApply, q)
		}
	}

	log.Printf("📋 В %s найдено %d вопросов, из них готово к накату: %d",
		*inputPath, len(regenerated), len(fixesToApply))

	if len(fixesToApply) == 0 {
		log.Println("ℹ️ Нет готовых исправлений для применения.")
		return
	}

	if *dryRun {
		log.Println("\n🔍 [DRY-RUN] Режим предпросмотра (изменения НЕ вносятся в базу):")
		for i, f := range fixesToApply {
			if i < 5 {
				fmt.Printf("\n--- [#%d] ID: %d (%s • %s) ---\n", i+1, f.ID, f.TopicID, f.Difficulty)
				fmt.Printf("❌ Было:  %s\n", f.OriginalQuestion)
				fmt.Printf("✨ Стало: %s\n", f.NewQuestion)
				fmt.Printf("🎯 Критерии: %s\n", strings.Join(f.NewKeyPoints, " | "))
			}
		}
		if len(fixesToApply) > 5 {
			fmt.Printf("\n... и ещё %d вопросов будут обновлены (text, answer, key_points).\n", len(fixesToApply)-5)
		}
		log.Printf("\n✨ Для реального применения запусти: go run ./cmd/question_patcher")
		return
	}

	// ==========================================
	// СОЗДАНИЕ РЕЗЕРВНОЙ КОПИИ (БЭКАПА)
	// ==========================================
	log.Println("🛡️ Создание автоматического бэкапа перед накатом...")

	_, err = pgPool.Exec(ctx, `
		DROP TABLE IF EXISTS questions_backup_audit;
		CREATE TABLE questions_backup_audit AS SELECT * FROM questions;
	`)
	if err != nil {
		log.Fatalf("❌ Ошибка создания бэкап-таблицы в PostgreSQL: %v", err)
	}
	log.Println("🐘 Резервная копия таблицы создана: questions_backup_audit")

	answersBytes, err := os.ReadFile(*answersPath)
	if err == nil {
		backupAnswersPath := "answers_backup_audit.json"
		if err := os.WriteFile(backupAnswersPath, answersBytes, 0644); err != nil {
			log.Printf("⚠️ Не удалось создать бэкап %s: %v", backupAnswersPath, err)
		} else {
			log.Printf("📁 Резервная копия файла создана: %s", backupAnswersPath)
		}
	}

	// ==========================================
	// ПРИМЕНЕНИЕ В POSTGRESQL (UPDATE text, answer, key_points)
	// ==========================================
	log.Println("🚀 Применение исправлений (text, answer, key_points) в PostgreSQL...")
	dbUpdated := 0
	for _, f := range fixesToApply {
		cmdTag, err := pgPool.Exec(ctx, `
			UPDATE questions 
			SET text = $1, answer = $2, key_points = $3 
			WHERE id = $4
		`, f.NewQuestion, f.NewAnswer, f.NewKeyPoints, f.ID)

		if err != nil {
			log.Printf("⚠️ Ошибка обновления вопроса ID #%d: %v", f.ID, err)
		} else if cmdTag.RowsAffected() > 0 {
			dbUpdated++
		}
	}
	log.Printf("✅ В PostgreSQL успешно обновлено: %d из %d вопросов (text + answer + key_points)", dbUpdated, len(fixesToApply))

	// ==========================================
	// ПРИМЕНЕНИЕ В ANSWERS.JSON
	// ==========================================
	if len(answersBytes) > 0 {
		var answers []AnswerItem
		if err := json.Unmarshal(answersBytes, &answers); err == nil {
			fixMap := make(map[string]string)
			for _, f := range fixesToApply {
				fixMap[strings.TrimSpace(f.OriginalQuestion)] = strings.TrimSpace(f.NewQuestion)
			}

			jsonUpdated := 0
			for i := range answers {
				curText := strings.TrimSpace(answers[i].Question)
				if newText, ok := fixMap[curText]; ok {
					answers[i].Question = newText
					jsonUpdated++
				}
			}

			if newJSONData, err := json.MarshalIndent(answers, "", "    "); err == nil {
				if err := os.WriteFile(*answersPath, newJSONData, 0644); err == nil {
					log.Printf("✅ В файле %s успешно обновлено: %d вопросов", *answersPath, jsonUpdated)
				} else {
					log.Printf("⚠️ Ошибка записи в %s: %v", *answersPath, err)
				}
			}
		}
	}

	log.Println("\n🎉 Все правки успешно применены!")
	log.Println("💡 Если потребуется откатить изменения, выполни: go run ./cmd/question_patcher -rollback")
}
