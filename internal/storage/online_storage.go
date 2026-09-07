package storage

import (
	"collaborative/internal/model"
	"context"
	"fmt"
	"math"
	"time"
)

// InitOnlineSchema создаёт таблицу online_sessions
func (s *DBStorage) InitOnlineSchema() error {
	_, err := s.pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS online_sessions (
			id                  SERIAL PRIMARY KEY,
			user_login          VARCHAR(255) NOT NULL REFERENCES users(login) ON DELETE CASCADE,
			public_ip           VARCHAR(50)  NOT NULL DEFAULT '',
			output_port         INT          NOT NULL DEFAULT 0,
			solution_port       INT          NOT NULL DEFAULT 0,
			roughened_lat       DOUBLE PRECISION NOT NULL DEFAULT 0,
			roughened_lon       DOUBLE PRECISION NOT NULL DEFAULT 0,
			roughened_height    DOUBLE PRECISION NOT NULL DEFAULT 0,
			convergence_status  INT          NOT NULL DEFAULT 0,
			is_moving           BOOLEAN      NOT NULL DEFAULT FALSE,
			created_at          TIMESTAMP    DEFAULT CURRENT_TIMESTAMP,
			updated_at          TIMESTAMP    DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (user_login)
		);
		ALTER TABLE online_sessions ADD COLUMN IF NOT EXISTS solution_port INT NOT NULL DEFAULT 0;
		CREATE INDEX IF NOT EXISTS idx_online_convergence ON online_sessions(convergence_status);
	`)
	return err
}

// UpsertOnlineSession создаёт или полностью обновляет онлайн-сессию
func (s *DBStorage) UpsertOnlineSession(sess *model.OnlineSession) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.pool.QueryRow(ctx, `
		INSERT INTO online_sessions
			(user_login, public_ip, output_port, solution_port, roughened_lat, roughened_lon,
			 roughened_height, convergence_status, is_moving, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW())
		ON CONFLICT (user_login) DO UPDATE SET
			public_ip          = EXCLUDED.public_ip,
			output_port        = EXCLUDED.output_port,
			solution_port      = EXCLUDED.solution_port,
			roughened_lat      = EXCLUDED.roughened_lat,
			roughened_lon      = EXCLUDED.roughened_lon,
			roughened_height   = EXCLUDED.roughened_height,
			convergence_status = EXCLUDED.convergence_status,
			is_moving          = EXCLUDED.is_moving,
			updated_at         = NOW()
		RETURNING id, created_at, updated_at`,
		sess.UserLogin, sess.PublicIP, sess.OutputPort, sess.SolutionPort,
		sess.RoughenedLat, sess.RoughenedLon, sess.RoughenedHeight,
		sess.ConvergenceStatus, sess.IsMoving,
	).Scan(&sess.ID, &sess.CreatedAt, &sess.UpdatedAt)
}

// UpdateOnlineStatus обновляет только поля статуса сессии
func (s *DBStorage) UpdateOnlineStatus(login string, convergence int, isMoving bool, lat, lon float64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := s.pool.Exec(ctx, `
		UPDATE online_sessions SET
			convergence_status = $1,
			is_moving          = $2,
			roughened_lat      = $3,
			roughened_lon      = $4,
			updated_at         = NOW()
		WHERE user_login = $5`,
		convergence, isMoving, lat, lon, login,
	)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("online session not found for %s", login)
	}
	return nil
}

// DeleteOnlineSession удаляет онлайн-сессию пользователя
func (s *DBStorage) DeleteOnlineSession(login string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.pool.Exec(ctx, `DELETE FROM online_sessions WHERE user_login = $1`, login)
	return err
}

// CountOnlineSessions возвращает количество активных онлайн-сессий.
// Сессия считается активной если она была создана или обновлена не позднее 10 минут назад.
func (s *DBStorage) CountOnlineSessions() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM online_sessions
		WHERE GREATEST(created_at, updated_at) > NOW() - INTERVAL '10 minutes'
	`).Scan(&count)
	return count, err
}

// FindNearestCandidate ищет ближайшего пользователя со статусом сходимости 1 (кандидат).
// Кандидат должен был обновить свой статус не позднее 2 минут назад.
func (s *DBStorage) FindNearestCandidate(lat, lon float64, excludeLogin string) (*model.OnlineSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := s.pool.Query(ctx, `
		SELECT id, user_login, public_ip, output_port, solution_port,
		       roughened_lat, roughened_lon, roughened_height,
		       convergence_status, is_moving
		FROM online_sessions
		WHERE convergence_status = 1
		  AND user_login != $1
		  AND updated_at > NOW() - INTERVAL '2 minutes'
	`, excludeLogin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nearest *model.OnlineSession
	minDist := math.MaxFloat64

	for rows.Next() {
		var sess model.OnlineSession
		if err := rows.Scan(
			&sess.ID, &sess.UserLogin, &sess.PublicIP, &sess.OutputPort, &sess.SolutionPort,
			&sess.RoughenedLat, &sess.RoughenedLon, &sess.RoughenedHeight,
			&sess.ConvergenceStatus, &sess.IsMoving,
		); err != nil {
			continue
		}
		dist := haversineKm(lat, lon, sess.RoughenedLat, sess.RoughenedLon)
		if dist < minDist {
			minDist = dist
			c := sess
			nearest = &c
		}
	}
	if nearest == nil {
		return nil, fmt.Errorf("кандидат не найден")
	}
	return nearest, nil
}

// haversineKm вычисляет расстояние в км между двумя точками (градусы)
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
