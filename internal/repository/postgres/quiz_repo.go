package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"interview-bot/internal/domain"
)

type QuizRepository struct {
	pool *pgxpool.Pool
}

func NewQuizRepository(pool *pgxpool.Pool) *QuizRepository {
	return &QuizRepository{pool: pool}
}

func (r *QuizRepository) GetRandomQuizQuestion(ctx context.Context, grade string, topicIDs []string) (*domain.QuizQuestion, error) {
	var whereClauses []string
	var args []any
	argIdx := 1

	normalizedGrade := strings.ToLower(strings.TrimSpace(grade))
	var diffs []string
	switch normalizedGrade {
	case "intern":
		diffs = []string{"intern"}
	case "junior":
		diffs = []string{"intern", "junior"}
	case "middle":
		diffs = []string{"middle"}
	case "senior":
		diffs = []string{"senior"}
	}

	if len(diffs) > 0 {
		whereClauses = append(whereClauses, fmt.Sprintf("q.difficulty = ANY($%d)", argIdx))
		args = append(args, diffs)
		argIdx++
	}

	if len(topicIDs) > 0 {
		whereClauses = append(whereClauses, fmt.Sprintf("q.topic_id = ANY($%d)", argIdx))
		args = append(args, topicIDs)
		argIdx++
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = "WHERE " + strings.Join(whereClauses, " AND ")
	}

	query := fmt.Sprintf(`
		SELECT 
			q.id, q.topic_id, t.title, q.difficulty, q.question, q.options, q.correct_option, q.explanation
		FROM quiz_questions q
		JOIN topics t ON t.id = q.topic_id
		%s
		ORDER BY RANDOM()
		LIMIT 1
	`, whereSQL)

	q, err := r.scanQuestion(ctx, query, args...)
	if err == nil {
		return q, nil
	}

	// Если с фильтром по сложности ничего не нашлось, сначала пробуем в ТЕХ ЖЕ темах без фильтра сложности!
	if len(whereClauses) > 0 && !errors.Is(err, context.Canceled) {
		if len(topicIDs) > 0 {
			fallbackTopicQuery := `
				SELECT 
					q.id, q.topic_id, t.title, q.difficulty, q.question, q.options, q.correct_option, q.explanation
				FROM quiz_questions q
				JOIN topics t ON t.id = q.topic_id
				WHERE q.topic_id = ANY($1)
				ORDER BY RANDOM()
				LIMIT 1
			`
			if fq, ferr := r.scanQuestion(ctx, fallbackTopicQuery, topicIDs); ferr == nil {
				return fq, nil
			}
		}

		fallbackQuery := `
			SELECT 
				q.id, q.topic_id, t.title, q.difficulty, q.question, q.options, q.correct_option, q.explanation
			FROM quiz_questions q
			JOIN topics t ON t.id = q.topic_id
			ORDER BY RANDOM()
			LIMIT 1
		`
		return r.scanQuestion(ctx, fallbackQuery)
	}

	return nil, err
}

func (r *QuizRepository) GetQuizQuestionByID(ctx context.Context, id int) (*domain.QuizQuestion, error) {
	query := `
		SELECT 
			q.id, q.topic_id, t.title, q.difficulty, q.question, q.options, q.correct_option, q.explanation
		FROM quiz_questions q
		JOIN topics t ON t.id = q.topic_id
		WHERE q.id = $1
		LIMIT 1
	`
	return r.scanQuestion(ctx, query, id)
}

func (r *QuizRepository) scanQuestion(ctx context.Context, query string, args ...any) (*domain.QuizQuestion, error) {
	var q domain.QuizQuestion
	var rawOptions []byte
	var diffStr string

	row := r.pool.QueryRow(ctx, query, args...)
	err := row.Scan(
		&q.ID,
		&q.TopicID,
		&q.TopicTitle,
		&diffStr,
		&q.Question,
		&rawOptions,
		&q.CorrectOption,
		&q.Explanation,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errors.New("вопрос квиза не найден")
		}
		return nil, fmt.Errorf("ошибка сканирования quiz_question: %w", err)
	}

	q.Difficulty = domain.Difficulty(diffStr)

	if len(rawOptions) > 0 {
		if err := json.Unmarshal(rawOptions, &q.Options); err != nil {
			return nil, fmt.Errorf("ошибка десериализации options (id=%d): %w", q.ID, err)
		}
	}

	return &q, nil
}

