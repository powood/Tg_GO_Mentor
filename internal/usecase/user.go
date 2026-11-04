package usecase

import (
	"context"
	"interview-bot/internal/domain"
)

type userUsecase struct {
	userRepo UserRepository
}

// NewUserUsecase создает юзкейс для работы с пользователями
func NewUserUsecase(repo UserRepository) UserUsecase {
	return &userUsecase{
		userRepo: repo,
	}
}

// RegisterUser регистрирует нового пользователя или обновляет данные существующего
func (u *userUsecase) RegisterUser(ctx context.Context, user domain.User) error {
	return u.userRepo.UpsertUser(ctx, user)
}

// GetUser возвращает профиль пользователя по его ID
func (u *userUsecase) GetUser(ctx context.Context, id int64) (*domain.User, error) {
	return u.userRepo.GetUserByID(ctx, id)
}

// SetGrade обновляет целевой грейд пользователя (junior, middle, senior, all)
func (u *userUsecase) SetGrade(ctx context.Context, id int64, grade string) error {
	return u.userRepo.SetUserGrade(ctx, id, grade)
}
