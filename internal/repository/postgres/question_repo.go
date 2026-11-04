package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"interview-bot/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type QuestionRepository struct {
	pool *pgxpool.Pool
}

func NewQuestionRepository(pool *pgxpool.Pool) *QuestionRepository {
	return &QuestionRepository{pool: pool}
}

func (r *QuestionRepository) GetTopics(ctx context.Context) ([]domain.Topic, error) {
	query := `SELECT id, title, description FROM topics ORDER BY id ASC`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("ошибка запроса тем: %w", err)
	}
	defer rows.Close()

	var topics []domain.Topic
	for rows.Next() {
		var t domain.Topic
		if err := rows.Scan(&t.ID, &t.Title, &t.Description); err != nil {
			return nil, fmt.Errorf("ошибка сканирования темы: %w", err)
		}
		topics = append(topics, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации тем: %w", err)
	}
	return topics, nil
}

func (r *QuestionRepository) GetQuestionByTopic(ctx context.Context, topicID string, grade string) ([]domain.Question, error) {
	return r.GetQuestionsByTopics(ctx, []string{topicID}, grade)
}

func (r *QuestionRepository) GetQuestionByID(ctx context.Context, id int) (*domain.Question, error) {
	query := `SELECT id, topic_id, difficulty, text, answer, key_points FROM questions WHERE id = $1;`
	var q domain.Question
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&q.ID,
		&q.TopicID,
		&q.Difficulty,
		&q.Text,
		&q.Answer,
		&q.KeyPoints,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrQuestionNotFound
		}
		return nil, fmt.Errorf("ошибка получения вопроса id %d: %w", id, err)
	}
	return &q, nil
}

func (r *QuestionRepository) GetRandomQuestion(ctx context.Context, grade string) (*domain.Question, error) {
	var diffs []string
	switch strings.ToLower(strings.TrimSpace(grade)) {
	case "intern":
		diffs = []string{"intern"}
	case "junior":
		diffs = []string{"intern", "junior"}
	case "middle":
		diffs = []string{"middle"}
	case "senior":
		diffs = []string{"senior"}
	}

	var query string
	var args []any
	if len(diffs) > 0 {
		query = `SELECT id, topic_id, difficulty, text, answer, key_points FROM questions WHERE difficulty = ANY($1) ORDER BY RANDOM() LIMIT 1;`
		args = []any{diffs}
	} else {
		query = `SELECT id, topic_id, difficulty, text, answer, key_points FROM questions ORDER BY RANDOM() LIMIT 1;`
	}

	var q domain.Question
	err := r.pool.QueryRow(ctx, query, args...).Scan(
		&q.ID,
		&q.TopicID,
		&q.Difficulty,
		&q.Text,
		&q.Answer,
		&q.KeyPoints,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if len(diffs) > 0 {
				return r.GetRandomQuestion(ctx, "all")
			}
			return nil, domain.ErrQuestionNotFound
		}
		return nil, fmt.Errorf("ошибка получения случайного вопроса: %w", err)
	}
	return &q, nil
}

// GetTopicQuestionCounts возвращает количество вопросов по каждой теме и общее число вопросов в базе
func (r *QuestionRepository) GetTopicQuestionCounts(ctx context.Context) (map[string]int, int, error) {
	query := `SELECT topic_id, COUNT(*) FROM questions GROUP BY topic_id;`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("ошибка подсчета вопросов: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	total := 0
	for rows.Next() {
		var topicID string
		var count int
		if err := rows.Scan(&topicID, &count); err != nil {
			return nil, 0, fmt.Errorf("ошибка сканирования количества вопросов: %w", err)
		}
		counts[topicID] = count
		total += count
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("ошибка итерации подсчета вопросов: %w", err)
	}
	return counts, total, nil
}

// GetWeakQuestions возвращает вопросы, где пользователь получил балл ниже 6
func (r *QuestionRepository) GetWeakQuestions(ctx context.Context, userID int64, limit int) ([]domain.Question, error) {
	query := `
		SELECT q.id, q.topic_id, q.difficulty, q.text, q.answer, q.key_points
		FROM questions q
		JOIN user_progress up ON q.id = up.question_id
		WHERE up.user_id = $1 AND up.last_score < 6 AND up.attempts > 0
		ORDER BY up.last_score ASC, up.last_answer_at ASC
		LIMIT $2;
	`
	rows, err := r.pool.Query(ctx, query, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("ошибка запроса слабых вопросов: %w", err)
	}
	defer rows.Close()

	var questions []domain.Question
	for rows.Next() {
		var q domain.Question
		if err := rows.Scan(&q.ID, &q.TopicID, &q.Difficulty, &q.Text, &q.Answer, &q.KeyPoints); err != nil {
			return nil, fmt.Errorf("ошибка сканирования слабого вопроса: %w", err)
		}
		questions = append(questions, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации слабых вопросов: %w", err)
	}
	return questions, nil
}

// GetQuestionsByTopics возвращает вопросы по нескольким темам с фильтрацией по грейду
func (r *QuestionRepository) GetQuestionsByTopics(ctx context.Context, topicIDs []string, grade string) ([]domain.Question, error) {
	if len(topicIDs) == 0 {
		return nil, domain.ErrQuestionNotFound
	}

	normGrade := strings.ToLower(strings.TrimSpace(grade))
	var diffs []string
	switch normGrade {
	case "intern":
		diffs = []string{"intern"}
	case "junior":
		diffs = []string{"intern", "junior"}
	case "middle":
		diffs = []string{"middle"}
	case "senior":
		diffs = []string{"senior"}
	}

	var query string
	var args []any
	if len(diffs) > 0 {
		query = `SELECT id, topic_id, difficulty, text, answer, key_points FROM questions WHERE topic_id = ANY($1) AND difficulty = ANY($2) ORDER BY RANDOM()`
		args = []any{topicIDs, diffs}
	} else {
		query = `SELECT id, topic_id, difficulty, text, answer, key_points FROM questions WHERE topic_id = ANY($1) ORDER BY RANDOM()`
		args = []any{topicIDs}
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("ошибка запроса вопросов по темам: %w", err)
	}
	defer rows.Close()

	var questions []domain.Question
	for rows.Next() {
		var q domain.Question
		if err := rows.Scan(&q.ID, &q.TopicID, &q.Difficulty, &q.Text, &q.Answer, &q.KeyPoints); err != nil {
			return nil, fmt.Errorf("ошибка сканирования вопроса: %w", err)
		}
		questions = append(questions, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ошибка итерации вопросов: %w", err)
	}

	// Страховка: если под строгий грейд не нашлось вопросов
	if len(questions) == 0 && len(diffs) > 0 {
		if normGrade == "intern" {
			// Для intern пробуем junior, но не проваливаемся в middle/senior
			var junDiffs = []string{"intern", "junior"}
			junRows, err := r.pool.Query(ctx, `SELECT id, topic_id, difficulty, text, answer, key_points FROM questions WHERE topic_id = ANY($1) AND difficulty = ANY($2) ORDER BY RANDOM()`, topicIDs, junDiffs)
			if err == nil {
				defer junRows.Close()
				for junRows.Next() {
					var q domain.Question
					if scanErr := junRows.Scan(&q.ID, &q.TopicID, &q.Difficulty, &q.Text, &q.Answer, &q.KeyPoints); scanErr == nil {
						questions = append(questions, q)
					}
				}
				if len(questions) > 0 {
					return questions, nil
				}
			}
			return nil, domain.ErrQuestionNotFound
		}
		return r.GetQuestionsByTopics(ctx, topicIDs, "all")
	}

	if len(questions) == 0 {
		return nil, domain.ErrQuestionNotFound
	}
	return questions, nil
}

// GetTopicQuestionCountsByGrade считает вопросы только в рамках сложностей целевого грейда
func (r *QuestionRepository) GetTopicQuestionCountsByGrade(ctx context.Context, diffs []string) (map[string]int, int, error) {
	query := `SELECT topic_id, COUNT(*) FROM questions WHERE difficulty = ANY($1) GROUP BY topic_id;`
	rows, err := r.pool.Query(ctx, query, diffs)
	if err != nil {
		return nil, 0, fmt.Errorf("ошибка подсчета вопросов по грейду: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	total := 0
	for rows.Next() {
		var topicID string
		var count int
		if err := rows.Scan(&topicID, &count); err != nil {
			return nil, 0, err
		}
		counts[topicID] = count
		total += count
	}
	return counts, total, nil
}

// GetWeakTopics возвращает список тем, где есть неисправленные ошибки (балл < 6)
func (r *QuestionRepository) GetWeakTopics(ctx context.Context, userID int64) ([]domain.WeakTopic, int, error) {
	query := `
		SELECT q.topic_id, t.title, COUNT(*) as weak_count
		FROM questions q
		JOIN user_progress up ON q.id = up.question_id
		JOIN topics t ON q.topic_id = t.id
		WHERE up.user_id = $1 AND up.last_score < 6 AND up.attempts > 0
		GROUP BY q.topic_id, t.title
		ORDER BY weak_count DESC;
	`
	rows, err := r.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("ошибка запроса тем с ошибками: %w", err)
	}
	defer rows.Close()

	var result []domain.WeakTopic
	total := 0
	for rows.Next() {
		var wt domain.WeakTopic
		if err := rows.Scan(&wt.TopicID, &wt.Title, &wt.WeakCount); err != nil {
			return nil, 0, err
		}
		result = append(result, wt)
		total += wt.WeakCount
	}
	return result, total, nil
}

// GetWeakQuestionsByTopic возвращает слабые вопросы конкретной темы
func (r *QuestionRepository) GetWeakQuestionsByTopic(ctx context.Context, userID int64, topicID string, limit int) ([]domain.Question, error) {
	query := `
		SELECT q.id, q.topic_id, q.difficulty, q.text, q.answer, q.key_points
		FROM questions q
		JOIN user_progress up ON q.id = up.question_id
		WHERE up.user_id = $1 AND q.topic_id = $2 AND up.last_score < 6 AND up.attempts > 0
		ORDER BY up.last_score ASC, up.last_answer_at ASC
		LIMIT $3;
	`
	rows, err := r.pool.Query(ctx, query, userID, topicID, limit)
	if err != nil {
		return nil, fmt.Errorf("ошибка запроса слабых вопросов по теме %s: %w", topicID, err)
	}
	defer rows.Close()

	var questions []domain.Question
	for rows.Next() {
		var q domain.Question
		if err := rows.Scan(&q.ID, &q.TopicID, &q.Difficulty, &q.Text, &q.Answer, &q.KeyPoints); err != nil {
			return nil, fmt.Errorf("ошибка сканирования слабого вопроса: %w", err)
		}
		questions = append(questions, q)
	}
	return questions, nil
}
