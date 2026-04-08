package repositories

import (
	"collaborative/internal/model"
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserRepositoryImpl реализация UserRepository
type UserRepositoryImpl struct {
	pool *pgxpool.Pool
}

// NewUserRepositoryImpl создает новый репозиторий пользователей
func NewUserRepositoryImpl(pool *pgxpool.Pool) *UserRepositoryImpl {
	return &UserRepositoryImpl{pool: pool}
}

// CreateUser создает нового пользователя
func (r *UserRepositoryImpl) CreateUser(login, password string) error {
	query := `INSERT INTO users (login, password) VALUES ($1, $2) ON CONFLICT (login) DO NOTHING`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := r.pool.Exec(ctx, query, login, password)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}

	if result.RowsAffected() == 0 {
		return fmt.Errorf("user %s already exists", login)
	}

	return nil
}

// GetUser получает пользователя по логину
func (r *UserRepositoryImpl) GetUser(login string) (*model.User, error) {
	query := `SELECT login, password FROM users WHERE login = $1`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var user model.User
	err := r.pool.QueryRow(ctx, query, login).Scan(&user.Login, &user.Password)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("user not found")
		}
		return nil, fmt.Errorf("get user: %w", err)
	}

	return &user, nil
}

// UserExists проверяет наличие пользователя
func (r *UserRepositoryImpl) UserExists(login string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM users WHERE login = $1)`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var exists bool
	err := r.pool.QueryRow(ctx, query, login).Scan(&exists)
	return exists, err
}

// UpdateUserPassword обновляет пароль
func (r *UserRepositoryImpl) UpdateUserPassword(login, newPassword string) error {
	query := `UPDATE users SET password = $1 WHERE login = $2`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := r.pool.Exec(ctx, query, newPassword, login)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}

	return nil
}
