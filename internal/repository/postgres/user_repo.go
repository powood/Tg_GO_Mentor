package postgres

import (
	"context"
	"errors"
	"fmt"
	"interview-bot/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func (r *UserRepository) UpsertUser(ctx context.Context, u domain.User) error {
	query := `
		INSERT INTO users (id, username, first_name, created_at, updated_at) 
		VALUES ($1, $2, $3, NOW(), NOW()) 
		ON CONFLICT (id) DO UPDATE SET 
			username = EXCLUDED.username, 
			first_name = EXCLUDED.first_name, 
			updated_at = NOW()
	`
	_, err := r.pool.Exec(ctx, query, u.ID, u.Username, u.FirstName)
	if err != nil {
		return fmt.Errorf("ошибка upsert пользователя %d: %w", u.ID, err)
	}
	return nil
}

func (r *UserRepository) GetUserByID(ctx context.Context, id int64) (*domain.User, error) {
	query := `SELECT id, username, first_name, COALESCE(target_grade, 'all'), created_at, updated_at FROM users WHERE id = $1`
	var u domain.User
	err := r.pool.QueryRow(ctx, query, id).Scan(&u.ID, &u.Username, &u.FirstName, &u.TargetGrade, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrUserNotFound
		}
		return nil, fmt.Errorf("ошибка получения пользователя %d: %w", id, err)
	}
	return &u, nil
}

func (r *UserRepository) SetUserGrade(ctx context.Context, id int64, grade string) error {
	query := `UPDATE users SET target_grade = $2, updated_at = NOW() WHERE id = $1`
	_, err := r.pool.Exec(ctx, query, id, grade)
	if err != nil {
		return fmt.Errorf("ошибка обновления грейда пользователя %d: %w", id, err)
	}
	return nil
}
