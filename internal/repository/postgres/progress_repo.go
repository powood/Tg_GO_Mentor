package postgres

import (
	"context"
	"fmt"
	"interview-bot/internal/domain"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ProgressRepository struct {
	pool *pgxpool.Pool
}

func NewProgressRepository(pool *pgxpool.Pool) *ProgressRepository {
	return &ProgressRepository{pool: pool}
}

func (r *ProgressRepository) GetUserProgressByTopic(ctx context.Context, userID int64, topicID string) (map[int]domain.QuestionProgress, error) {
	query := `SELECT question_id, attempts, success_count, last_score, last_answer_at FROM user_progress WHERE user_id = $1 AND topic_id = $2`
	rows, err := r.pool.Query(ctx, query, userID, topicID)
	if err != nil {
		return nil, fmt.Errorf("ошибка запроса прогресса по теме %s: %w", topicID, err)
	}
	defer rows.Close()
	result := make(map[int]domain.QuestionProgress)
	for rows.Next() {
		var p domain.QuestionProgress
		p.UserID = userID
		if err := rows.Scan(
			&p.QuestionID,
			&p.Attempts,
			&p.SuccessCount,
			&p.LastScore,
			&p.LastAnswerAt,
		); err != nil {
			return nil, fmt.Errorf("ошибка сканирования прогресса: %w", err)
		}
		result[p.QuestionID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации прогресса: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации прогресса: %w", err)
	}
	return result, nil
}

func (r *ProgressRepository) SaveAttemptAndProgress(ctx context.Context, attempt domain.AnswerAttempt, topicID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ошибка начала транзакции: %w", err)
	}
	defer tx.Rollback(ctx)

	// Страховка внешнего ключа (H-7): если юзера еще нет в таблице users, создаем запись на лету
	_, err = tx.Exec(
		ctx,
		`INSERT INTO users (id, first_name) VALUES ($1, 'User') ON CONFLICT (id) DO NOTHING;`,
		attempt.UserID,
	)
	if err != nil {
		return fmt.Errorf("ошибка проверки пользователя перед сохранением: %w", err)
	}

	missingPoints := attempt.Eval.MissingPoints
	if missingPoints == nil {
		missingPoints = []string{}
	}

	queryAttempt := `INSERT INTO answer_attempts (
			user_id, question_id, user_answer, is_voice,
			score, is_correct, feedback, missing_points, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8::text[], ARRAY[]::text[]), NOW());
	`
	_, err = tx.Exec(
		ctx,
		queryAttempt,
		attempt.UserID,
		attempt.QuestionID,
		attempt.UserAnswer,
		attempt.IsVoice,
		attempt.Eval.Score,
		attempt.Eval.IsCorrect,
		attempt.Eval.Feedback,
		missingPoints,
	)
	if err != nil {
		return fmt.Errorf("ошибка записи answer_attempt: %w", err)
	}

	queryProgress := `
		INSERT INTO user_progress (
			user_id, question_id, topic_id, attempts, success_count, last_score, last_answer_at
		) VALUES (
			$1, $2, $3, 1, CASE WHEN $4 >= 6 THEN 1 ELSE 0 END, $4, NOW()
		)
		ON CONFLICT (user_id, question_id) DO UPDATE SET
			attempts = user_progress.attempts + 1,
			success_count = user_progress.success_count + CASE WHEN EXCLUDED.last_score >= 6 THEN 1 ELSE 0 END,
			last_score = EXCLUDED.last_score,
			last_answer_at = NOW();
	`
	_, err = tx.Exec(
		ctx,
		queryProgress,
		attempt.UserID,
		attempt.QuestionID,
		topicID,
		attempt.Eval.Score,
	)
	if err != nil {
		return fmt.Errorf("ошибка обновления user_progress: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ошибка коммита транзакции: %w", err)
	}
	return nil
}

// GetUserOverallProgress возвращает весь срез прогресса пользователя по всем темам
func (r *ProgressRepository) GetUserOverallProgress(ctx context.Context, userID int64) ([]domain.QuestionProgress, error) {
	query := `SELECT question_id, topic_id, attempts, success_count, last_score, last_answer_at FROM user_progress WHERE user_id = $1`
	rows, err := r.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("ошибка запроса общего прогресса пользователя: %w", err)
	}
	defer rows.Close()

	var result []domain.QuestionProgress
	for rows.Next() {
		var p domain.QuestionProgress
		p.UserID = userID
		if err := rows.Scan(
			&p.QuestionID,
			&p.TopicID,
			&p.Attempts,
			&p.SuccessCount,
			&p.LastScore,
			&p.LastAnswerAt,
		); err != nil {
			return nil, fmt.Errorf("ошибка сканирования прогресса: %w", err)
		}
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации общего прогресса: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации прогресса: %w", err)
	}
	return result, nil
}

// GetUserGradeProgress возвращает прогресс пользователя только по вопросам входящим в программу целевого грейда
func (r *ProgressRepository) GetUserGradeProgress(ctx context.Context, userID int64, diffs []string) ([]domain.QuestionProgress, error) {
	query := `
		SELECT up.question_id, up.topic_id, up.attempts, up.success_count, up.last_score, up.last_answer_at
		FROM user_progress up
		JOIN questions q ON up.question_id = q.id
		WHERE up.user_id = $1 AND q.difficulty = ANY($2);
	`
	rows, err := r.pool.Query(ctx, query, userID, diffs)
	if err != nil {
		return nil, fmt.Errorf("ошибка запроса прогресса по грейду: %w", err)
	}
	defer rows.Close()

	var result []domain.QuestionProgress
	for rows.Next() {
		var p domain.QuestionProgress
		p.UserID = userID
		if err := rows.Scan(
			&p.QuestionID,
			&p.TopicID,
			&p.Attempts,
			&p.SuccessCount,
			&p.LastScore,
			&p.LastAnswerAt,
		); err != nil {
			return nil, fmt.Errorf("ошибка сканирования прогресса: %w", err)
		}
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации прогресса: %w", err)
	}
	return result, nil
}
