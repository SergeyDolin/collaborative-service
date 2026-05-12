package storage

import (
	"collaborative/internal/model"
	"context"
	"fmt"
	"time"
)

// InitCollaborativeSchema создаёт таблицы для коллаборативного позиционирования
func (s *DBStorage) InitCollaborativeSchema() error {
	_, err := s.pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS collaborative_sessions (
			id                SERIAL PRIMARY KEY,
			user_login        VARCHAR(255) NOT NULL REFERENCES users(login) ON DELETE CASCADE,
			device_id         BIGINT NOT NULL REFERENCES user_devices(id) ON DELETE CASCADE,
			device_name       VARCHAR(255) NOT NULL DEFAULT '',
			connection_type   VARCHAR(10) NOT NULL,
			tcp_host          VARCHAR(255) NOT NULL DEFAULT '',
			tcp_port          INT NOT NULL DEFAULT 0,
			ntrip_host        VARCHAR(255) NOT NULL DEFAULT '',
			ntrip_port        INT NOT NULL DEFAULT 0,
			ntrip_mountpoint  VARCHAR(255) NOT NULL DEFAULT '',
			ntrip_user        VARCHAR(255) NOT NULL DEFAULT '',
			ntrip_pass        VARCHAR(255) NOT NULL DEFAULT '',
			assigned_port     INT NOT NULL,
			enable_positioning BOOLEAN NOT NULL DEFAULT FALSE,
			str2str_cmd       TEXT NOT NULL DEFAULT '',
			created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_collab_sessions_port ON collaborative_sessions(assigned_port);
		CREATE INDEX IF NOT EXISTS idx_collab_sessions_user ON collaborative_sessions(user_login);

		CREATE TABLE IF NOT EXISTS collaborative_positions (
			id         SERIAL PRIMARY KEY,
			session_id BIGINT NOT NULL REFERENCES collaborative_sessions(id) ON DELETE CASCADE,
			lat        DOUBLE PRECISION NOT NULL,
			lon        DOUBLE PRECISION NOT NULL,
			height     DOUBLE PRECISION NOT NULL,
			pdop       DOUBLE PRECISION NOT NULL DEFAULT 0,
			nsat       INT NOT NULL DEFAULT 0,
			quality    INT NOT NULL DEFAULT 0,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (session_id)
		);
	`)
	return err
}

// AllocatePort выделяет свободный порт из диапазона 7000-9000
func (s *DBStorage) AllocatePort() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var maxPort int
	if err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(assigned_port), 6999) FROM collaborative_sessions`).Scan(&maxPort); err != nil {
		return 0, err
	}

	next := maxPort + 1
	if next >= 7000 && next <= 9000 {
		return next, nil
	}

	// Диапазон переполнен — ищем первый свободный порт
	rows, err := s.pool.Query(ctx, `SELECT assigned_port FROM collaborative_sessions ORDER BY assigned_port`)
	if err != nil {
		return 0, fmt.Errorf("allocate port: %w", err)
	}
	defer rows.Close()

	used := make(map[int]bool)
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err == nil {
			used[p] = true
		}
	}
	for p := 7000; p <= 9000; p++ {
		if !used[p] {
			return p, nil
		}
	}
	return 0, fmt.Errorf("нет свободных портов в диапазоне 7000-9000")
}

// CreateCollaborativeSession создаёт новую сессию и возвращает её с заполненными id и created_at
func (s *DBStorage) CreateCollaborativeSession(sess *model.CollaborativeSession) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.pool.QueryRow(ctx, `
		INSERT INTO collaborative_sessions (
			user_login, device_id, device_name, connection_type,
			tcp_host, tcp_port, ntrip_host, ntrip_port, ntrip_mountpoint,
			ntrip_user, ntrip_pass, assigned_port, enable_positioning, str2str_cmd
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id, created_at`,
		sess.UserLogin, sess.DeviceID, sess.DeviceName, sess.ConnectionType,
		sess.TCPHost, sess.TCPPort, sess.NTRIPHost, sess.NTRIPPort, sess.NTRIPMountpoint,
		sess.NTRIPUser, sess.NTRIPPass, sess.AssignedPort, sess.EnablePositioning, sess.Str2strCmd,
	).Scan(&sess.ID, &sess.CreatedAt)
}

// GetCollaborativeSessions возвращает сессии пользователя с последними позициями
func (s *DBStorage) GetCollaborativeSessions(login string) ([]model.CollaborativeSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := s.pool.Query(ctx, `
		SELECT id, user_login, device_id, device_name, connection_type,
		       tcp_host, tcp_port, ntrip_host, ntrip_port, ntrip_mountpoint,
		       ntrip_user, ntrip_pass, assigned_port, enable_positioning, str2str_cmd, created_at
		FROM collaborative_sessions WHERE user_login = $1 ORDER BY created_at DESC`, login)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []model.CollaborativeSession
	for rows.Next() {
		var sess model.CollaborativeSession
		if err := rows.Scan(
			&sess.ID, &sess.UserLogin, &sess.DeviceID, &sess.DeviceName, &sess.ConnectionType,
			&sess.TCPHost, &sess.TCPPort, &sess.NTRIPHost, &sess.NTRIPPort, &sess.NTRIPMountpoint,
			&sess.NTRIPUser, &sess.NTRIPPass, &sess.AssignedPort, &sess.EnablePositioning, &sess.Str2strCmd,
			&sess.CreatedAt,
		); err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}

	// Подтягиваем последние позиции
	for i := range sessions {
		pos, err := s.GetLatestPosition(sessions[i].ID)
		if err == nil {
			sessions[i].LatestPosition = pos
		}
	}

	return sessions, nil
}

// DeleteCollaborativeSession удаляет сессию (только владельца)
func (s *DBStorage) DeleteCollaborativeSession(id int64, login string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := s.pool.Exec(ctx,
		`DELETE FROM collaborative_sessions WHERE id=$1 AND user_login=$2`, id, login)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("сессия не найдена или нет доступа")
	}
	return nil
}

// UpdateSessionPositioning включает/отключает серверное позиционирование для сессии
func (s *DBStorage) UpdateSessionPositioning(id int64, login string, enabled bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := s.pool.Exec(ctx,
		`UPDATE collaborative_sessions SET enable_positioning=$1 WHERE id=$2 AND user_login=$3`,
		enabled, id, login)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("сессия не найдена или нет доступа")
	}
	return nil
}

// UpsertCollaborativePosition сохраняет/обновляет последнюю позицию сессии
func (s *DBStorage) UpsertCollaborativePosition(pos *model.CollaborativePosition) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.pool.Exec(ctx, `
		INSERT INTO collaborative_positions (session_id, lat, lon, height, pdop, nsat, quality, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())
		ON CONFLICT (session_id) DO UPDATE SET
			lat=EXCLUDED.lat, lon=EXCLUDED.lon, height=EXCLUDED.height,
			pdop=EXCLUDED.pdop, nsat=EXCLUDED.nsat, quality=EXCLUDED.quality,
			updated_at=EXCLUDED.updated_at`,
		pos.SessionID, pos.Lat, pos.Lon, pos.Height, pos.PDOP, pos.NSat, pos.Quality,
	)
	return err
}

// GetLatestPosition возвращает последнюю позицию сессии
func (s *DBStorage) GetLatestPosition(sessionID int64) (*model.CollaborativePosition, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var pos model.CollaborativePosition
	err := s.pool.QueryRow(ctx, `
		SELECT id, session_id, lat, lon, height, pdop, nsat, quality, updated_at
		FROM collaborative_positions WHERE session_id = $1`, sessionID).Scan(
		&pos.ID, &pos.SessionID, &pos.Lat, &pos.Lon, &pos.Height,
		&pos.PDOP, &pos.NSat, &pos.Quality, &pos.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &pos, nil
}

// GetSessionsWithPositioning возвращает все сессии с включённым позиционированием
func (s *DBStorage) GetSessionsWithPositioning() ([]model.CollaborativeSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := s.pool.Query(ctx, `
		SELECT id, user_login, device_id, device_name, connection_type,
		       tcp_host, tcp_port, ntrip_host, ntrip_port, ntrip_mountpoint,
		       ntrip_user, ntrip_pass, assigned_port, enable_positioning, str2str_cmd, created_at
		FROM collaborative_sessions WHERE enable_positioning = true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []model.CollaborativeSession
	for rows.Next() {
		var sess model.CollaborativeSession
		if err := rows.Scan(
			&sess.ID, &sess.UserLogin, &sess.DeviceID, &sess.DeviceName, &sess.ConnectionType,
			&sess.TCPHost, &sess.TCPPort, &sess.NTRIPHost, &sess.NTRIPPort, &sess.NTRIPMountpoint,
			&sess.NTRIPUser, &sess.NTRIPPass, &sess.AssignedPort, &sess.EnablePositioning, &sess.Str2strCmd,
			&sess.CreatedAt,
		); err != nil {
			return nil, err
		}
		sessions = append(sessions, sess)
	}
	return sessions, nil
}
