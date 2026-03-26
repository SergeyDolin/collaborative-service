package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DBStorage struct {
	pool *pgxpool.Pool
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
	_, err := stor.pool.Exec(context.Background(), query, login, password)
	if err != nil {
		return fmt.Errorf("Create user %s: %w", login, err)
	}

	return nil
}

func (stor *DBStorage) UserExists(login string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM users WHERE login = $1)`
	var exists bool
	err := stor.pool.QueryRow(context.Background(), query, login).Scan(&exists)
	return exists, err
}
