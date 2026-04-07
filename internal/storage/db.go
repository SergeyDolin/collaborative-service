package storage

import (
	"collaborative/internal/model"
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DBStorage struct {
	pool *pgxpool.Pool
}

func (stor *DBStorage) Pool() *pgxpool.Pool {
	return stor.pool
}

func (stor *DBStorage) initSchema() error {
	_, err := stor.pool.Exec(context.Background(), `
	CREATE TABLE IF NOT EXISTS users (
		login VARCHAR(255) PRIMARY KEY,
		password VARCHAR(255) NOT NULL
		);
	`)
	if err != nil {
		return fmt.Errorf("Create users table: %w", err)
	}
	return nil
}

func NewDBStorage(dsn string) (*DBStorage, error) {
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, fmt.Errorf("Failed to connect to DB: %w", err)
	}

	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("Failed to ping DB:%w", err)
	}

	stor := &DBStorage{
		pool: pool,
	}

	if err := stor.initSchema(); err != nil {
		pool.Close()
		return nil, fmt.Errorf("Init schema: %w", err)
	}

	return stor, nil
}

func (stor *DBStorage) Close() error {
	stor.pool.Close()
	return nil
}

func (stor *DBStorage) CreateUser(login, password string) error {
	query := `INSERT INTO users (login, password) VALUES ($1, $2) ON CONFLICT (login) DO NOTHING`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := stor.pool.Exec(ctx, query, login, password)
	if err != nil {
		return fmt.Errorf("Create user %s: %w", login, err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("user %s already exists", login)
	}

	return nil
}

func (stor *DBStorage) UserExists(login string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM users WHERE login = $1)`
	var exists bool
	err := stor.pool.QueryRow(context.Background(), query, login).Scan(&exists)
	return exists, err
}

func (stor *DBStorage) GetUser(login string) (*model.User, error) {
	query := `SELECT login, password FROM users WHERE login = $1`
	var user model.User
	err := stor.pool.QueryRow(context.Background(), query, login).Scan(&user.Login, &user.Password)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("User %s not found", login)
		}
		return nil, fmt.Errorf("Get user %s: %w", login, err)
	}
	return &user, nil
}

func (stor *DBStorage) UpdateUserPassword(login, newPassword string) error {
	query := `UPDATE users SET password = $1 WHERE login = $2`
	_, err := stor.pool.Exec(context.Background(), query, newPassword, login)
	if err != nil {
		return fmt.Errorf("Update password for %s: %w", login, err)
	}
	return nil
}
