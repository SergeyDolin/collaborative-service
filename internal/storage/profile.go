package storage

import (
	"collaborative/internal/model"
	"context"
	"fmt"
	"time"
)

// InitProfileSchema добавляет колонки профиля и таблицу устройств
func (stor *DBStorage) InitProfileSchema() error {
	_, err := stor.pool.Exec(context.Background(), `
		ALTER TABLE users ADD COLUMN IF NOT EXISTS full_name  VARCHAR(255);
		ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar     BYTEA;
		ALTER TABLE users ADD COLUMN IF NOT EXISTS created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP;
	`)
	if err != nil {
		return fmt.Errorf("alter users table: %w", err)
	}

	_, err = stor.pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS user_devices (
			id                      SERIAL PRIMARY KEY,
			user_login              VARCHAR(255) NOT NULL REFERENCES users(login) ON DELETE CASCADE,
			name                    VARCHAR(255) NOT NULL,
			device_type             VARCHAR(50)  NOT NULL,
			mount_type              VARCHAR(50)  NOT NULL,
			description             TEXT,
			antenna_name            VARCHAR(255) NOT NULL DEFAULT '',
			antenna_e               DOUBLE PRECISION NOT NULL DEFAULT 0,
			antenna_n               DOUBLE PRECISION NOT NULL DEFAULT 0,
			antenna_u               DOUBLE PRECISION NOT NULL DEFAULT 0,
			phase_center_method     VARCHAR(20)  NOT NULL DEFAULT '',
			phase_center_valid_until TIMESTAMP,
			created_at              TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_devices_user ON user_devices(user_login);
	`)
	if err != nil {
		return fmt.Errorf("create user_devices table: %w", err)
	}

	// Добавляем новые колонки для существующих таблиц (идемпотентно)
	_, err = stor.pool.Exec(context.Background(), `
		ALTER TABLE user_devices ADD COLUMN IF NOT EXISTS antenna_name             VARCHAR(255) NOT NULL DEFAULT '';
		ALTER TABLE user_devices ADD COLUMN IF NOT EXISTS antenna_e                DOUBLE PRECISION NOT NULL DEFAULT 0;
		ALTER TABLE user_devices ADD COLUMN IF NOT EXISTS antenna_n                DOUBLE PRECISION NOT NULL DEFAULT 0;
		ALTER TABLE user_devices ADD COLUMN IF NOT EXISTS antenna_u                DOUBLE PRECISION NOT NULL DEFAULT 0;
		ALTER TABLE user_devices ADD COLUMN IF NOT EXISTS phase_center_method      VARCHAR(20) NOT NULL DEFAULT '';
		ALTER TABLE user_devices ADD COLUMN IF NOT EXISTS phase_center_valid_until TIMESTAMP;
	`)
	if err != nil {
		return fmt.Errorf("alter user_devices table: %w", err)
	}

	return nil
}

// UpdateUserProfile обновляет full_name и/или avatar
func (stor *DBStorage) UpdateUserProfile(login, fullName string, avatar []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if avatar != nil {
		_, err := stor.pool.Exec(ctx,
			`UPDATE users SET full_name = $1, avatar = $2 WHERE login = $3`,
			fullName, avatar, login)
		return err
	}
	_, err := stor.pool.Exec(ctx,
		`UPDATE users SET full_name = $1 WHERE login = $2`,
		fullName, login)
	return err
}

// GetUserProfile возвращает full_name и avatar пользователя
func (stor *DBStorage) GetUserProfile(login string) (fullName string, avatar []byte, createdAt time.Time, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	row := stor.pool.QueryRow(ctx,
		`SELECT COALESCE(full_name,''), avatar, COALESCE(created_at, NOW()) FROM users WHERE login = $1`,
		login)
	err = row.Scan(&fullName, &avatar, &createdAt)
	return
}

// CreateDevice создаёт новое устройство пользователя
func (stor *DBStorage) CreateDevice(d *model.UserDevice) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return stor.pool.QueryRow(ctx, `
		INSERT INTO user_devices (
			user_login, name, device_type, mount_type, description,
			antenna_name, antenna_e, antenna_n, antenna_u,
			phase_center_method, phase_center_valid_until
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at`,
		d.UserLogin, d.Name, d.DeviceType, d.MountType, d.Description,
		d.AntennaName, d.AntennaE, d.AntennaN, d.AntennaU,
		d.PhaseCenterMethod, d.PhaseCenterValidUntil,
	).Scan(&d.ID, &d.CreatedAt)
}

// GetUserDevices возвращает все устройства пользователя
func (stor *DBStorage) GetUserDevices(login string) ([]model.UserDevice, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := stor.pool.Query(ctx, `
		SELECT id, user_login, name, device_type, mount_type,
		       COALESCE(description,''),
		       COALESCE(antenna_name,''), antenna_e, antenna_n, antenna_u,
		       COALESCE(phase_center_method,''), phase_center_valid_until,
		       created_at
		FROM user_devices WHERE user_login = $1 ORDER BY created_at DESC`,
		login)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []model.UserDevice
	for rows.Next() {
		var d model.UserDevice
		if err := rows.Scan(
			&d.ID, &d.UserLogin, &d.Name, &d.DeviceType, &d.MountType, &d.Description,
			&d.AntennaName, &d.AntennaE, &d.AntennaN, &d.AntennaU,
			&d.PhaseCenterMethod, &d.PhaseCenterValidUntil,
			&d.CreatedAt,
		); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, nil
}

// DeleteDevice удаляет устройство (только владельца)
func (stor *DBStorage) DeleteDevice(id int64, login string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := stor.pool.Exec(ctx,
		`DELETE FROM user_devices WHERE id = $1 AND user_login = $2`, id, login)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("device not found or access denied")
	}
	return nil
}
