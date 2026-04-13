package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"collaborative/internal/model"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TaskStorage struct {
	pool *pgxpool.Pool
}

func NewTaskStorage(pool *pgxpool.Pool) *TaskStorage {
	return &TaskStorage{pool: pool}
}

func (s *TaskStorage) Pool() *pgxpool.Pool {
	return s.pool
}

// UpdateTaskObservationDate обновляет дату наблюдения
func (s *TaskStorage) UpdateTaskObservationDate(taskID string, date time.Time) error {
	query := `UPDATE processing_tasks SET observation_date = $1 WHERE id = $2`
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := s.pool.Exec(ctx, query, date, taskID)
	if err != nil {
		return fmt.Errorf("update observation date: %w", err)
	}
	return nil
}

// InitTaskSchema создает таблицы для задач и результатов
func (s *TaskStorage) InitTaskSchema() error {
	_, err := s.pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS processing_tasks (
			id VARCHAR(36) PRIMARY KEY,
			user_login VARCHAR(255) NOT NULL,
			config JSONB NOT NULL,
			filename VARCHAR(255) NOT NULL,
			original_path TEXT,
			rinex_path TEXT,
			output_path TEXT,
			status VARCHAR(50) NOT NULL,
			error_message TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			started_at TIMESTAMP,
			completed_at TIMESTAMP,
			processing_sec FLOAT,
			observation_date TIMESTAMP
		);
	`)
	if err != nil {
		return fmt.Errorf("create tasks table: %w", err)
	}

	_, err = s.pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS processing_results (
			id SERIAL PRIMARY KEY,
			task_id VARCHAR(36) NOT NULL UNIQUE,
			user_login VARCHAR(255) NOT NULL,
			x DOUBLE PRECISION,
			y DOUBLE PRECISION,
			z DOUBLE PRECISION,
			latitude DOUBLE PRECISION,
			longitude DOUBLE PRECISION,
			height DOUBLE PRECISION,
			q INTEGER,
			n_sat INTEGER,
			rms DOUBLE PRECISION,
			sdx REAL,
			sdy REAL,
			sdz REAL,
			fix_rate REAL,
			last_solution_line TEXT,
			full_result_file BYTEA,
			file_type VARCHAR(20),
			raw_output TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			expires_at TIMESTAMP DEFAULT (CURRENT_TIMESTAMP + INTERVAL '24 hours')
		);
	`)
	if err != nil {
		return fmt.Errorf("create results table: %w", err)
	}

	_, err = s.pool.Exec(context.Background(), `
		CREATE INDEX IF NOT EXISTS idx_tasks_user ON processing_tasks(user_login);
		CREATE INDEX IF NOT EXISTS idx_tasks_status ON processing_tasks(status);
		CREATE INDEX IF NOT EXISTS idx_tasks_created ON processing_tasks(created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_results_task ON processing_results(task_id);
		CREATE INDEX IF NOT EXISTS idx_results_user ON processing_results(user_login);
		CREATE INDEX IF NOT EXISTS idx_results_expires ON processing_results(expires_at);
	`)
	if err != nil {
		return fmt.Errorf("create indexes: %w", err)
	}

	_, err = s.pool.Exec(context.Background(), `
		CREATE OR REPLACE FUNCTION delete_expired_results()
		RETURNS TRIGGER AS $$
		BEGIN
			DELETE FROM processing_results WHERE expires_at < NOW();
			RETURN NULL;
		END;
		$$ LANGUAGE plpgsql;
		
		DROP TRIGGER IF EXISTS trigger_delete_expired ON processing_results;
		CREATE TRIGGER trigger_delete_expired
		AFTER INSERT ON processing_results
		EXECUTE FUNCTION delete_expired_results();
	`)
	if err != nil {
		fmt.Printf("Trigger creation warning: %v\n", err)
	}
	_, err = s.pool.Exec(context.Background(), `
    ALTER TABLE processing_tasks 
    ADD COLUMN IF NOT EXISTS observation_date TIMESTAMP;`)
	if err != nil {
		fmt.Printf("Warning: Failed to add observation_date column: %v\n", err)
	}

	_, err = s.pool.Exec(context.Background(), `
		ALTER TABLE processing_results 
		ADD COLUMN IF NOT EXISTS fix_rate REAL;
	`)
	if err != nil {
		fmt.Printf("Warning: Failed to add fix_rate column: %v\n", err)
	}

	return nil
}

// CreateTask создает новую задачу
func (s *TaskStorage) CreateTask(task *model.ProcessingTask) error {
	configJSON, err := json.Marshal(task.Config)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	query := `
		INSERT INTO processing_tasks (
			id, user_login, config, filename, original_path, 
			rinex_path, output_path, status, error_message, 
			created_at, started_at, completed_at, processing_sec
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`

	_, err = s.pool.Exec(context.Background(), query,
		task.ID, task.UserLogin, configJSON, task.Filename,
		task.OriginalPath, task.RinexPath, task.OutputPath,
		task.Status, task.ErrorMessage, task.CreatedAt,
		task.StartedAt, task.CompletedAt, task.ProcessingSec,
	)

	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	return nil
}

// UpdateTask обновляет задачу
func (s *TaskStorage) UpdateTask(task *model.ProcessingTask) error {
	query := `
		UPDATE processing_tasks 
		SET status = $1, error_message = $2, started_at = $3, 
		    completed_at = $4, processing_sec = $5, output_path = $6
		WHERE id = $7
	`

	_, err := s.pool.Exec(context.Background(), query,
		task.Status, task.ErrorMessage, task.StartedAt,
		task.CompletedAt, task.ProcessingSec, task.OutputPath, task.ID,
	)

	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}

	return nil
}

func (s *TaskStorage) SaveResult(result *model.ProcessingResult) error {
	if result.ExpiresAt.IsZero() {
		result.ExpiresAt = time.Now().Add(24 * time.Hour)
	}

	query := `
		INSERT INTO processing_results (
			task_id, user_login, x, y, z, latitude, longitude, height,
			q, n_sat, sdx, sdy, sdz, fix_rate, last_solution_line, 
			full_result_file, file_type, raw_output, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		ON CONFLICT (task_id) DO UPDATE SET
			x = EXCLUDED.x, y = EXCLUDED.y, z = EXCLUDED.z,
			latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude,
			height = EXCLUDED.height, q = EXCLUDED.q, n_sat = EXCLUDED.n_sat,
			sdx = EXCLUDED.sdx, sdy = EXCLUDED.sdy, sdz = EXCLUDED.sdz,
			fix_rate = EXCLUDED.fix_rate,
			last_solution_line = EXCLUDED.last_solution_line,
			full_result_file = EXCLUDED.full_result_file, file_type = EXCLUDED.file_type,
			raw_output = EXCLUDED.raw_output, expires_at = EXCLUDED.expires_at
	`

	_, err := s.pool.Exec(context.Background(), query,
		result.TaskID, result.UserLogin, result.X, result.Y, result.Z,
		result.Latitude, result.Longitude, result.Height, result.Q,
		result.NSat, result.SDX, result.SDY, result.SDZ, result.FixRate,
		result.LastSolutionLine, result.FullResultFile, result.FileType,
		result.RawOutput, result.CreatedAt, result.ExpiresAt,
	)

	if err != nil {
		return fmt.Errorf("save result: %w", err)
	}

	return nil
}

// GetUserTasks возвращает задачи пользователя
func (s *TaskStorage) GetUserTasks(userLogin string, limit, offset int) ([]model.ProcessingTask, error) {
	query := `
		SELECT id, user_login, config, filename, original_path, 
		       rinex_path, output_path, status, error_message,
		       created_at, started_at, completed_at, processing_sec
		FROM processing_tasks
		WHERE user_login = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := s.pool.Query(context.Background(), query, userLogin, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	var tasks []model.ProcessingTask
	for rows.Next() {
		var task model.ProcessingTask
		var configJSON []byte

		err := rows.Scan(
			&task.ID, &task.UserLogin, &configJSON, &task.Filename,
			&task.OriginalPath, &task.RinexPath, &task.OutputPath,
			&task.Status, &task.ErrorMessage, &task.CreatedAt,
			&task.StartedAt, &task.CompletedAt, &task.ProcessingSec,
		)
		if err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}

		if err := json.Unmarshal(configJSON, &task.Config); err != nil {
			return nil, fmt.Errorf("unmarshal config: %w", err)
		}

		tasks = append(tasks, task)
	}

	return tasks, nil
}

// GetTaskByID возвращает задачу по ID
func (s *TaskStorage) GetTaskByID(taskID string) (*model.ProcessingTask, error) {
	query := `
		SELECT id, user_login, config, filename, original_path,
		       rinex_path, output_path, status, error_message,
		       created_at, started_at, completed_at, processing_sec,
		       observation_date
		FROM processing_tasks
		WHERE id = $1
	`

	var task model.ProcessingTask
	var configJSON []byte

	err := s.pool.QueryRow(context.Background(), query, taskID).Scan(
		&task.ID, &task.UserLogin, &configJSON, &task.Filename,
		&task.OriginalPath, &task.RinexPath, &task.OutputPath,
		&task.Status, &task.ErrorMessage, &task.CreatedAt,
		&task.StartedAt, &task.CompletedAt, &task.ProcessingSec,
		&task.ObservationDate,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get task: %w", err)
	}

	if err := json.Unmarshal(configJSON, &task.Config); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &task, nil
}

// GetResultByTaskID возвращает результат по ID задачи
func (s *TaskStorage) GetResultByTaskID(taskID string) (*model.ProcessingResult, error) {
	query := `
		SELECT id, task_id, user_login, x, y, z, latitude, longitude, height,
		       q, n_sat, sdx, sdy, sdz, last_solution_line, 
		       full_result_file, file_type, raw_output, created_at, expires_at
		FROM processing_results
		WHERE task_id = $1
	`

	var result model.ProcessingResult

	err := s.pool.QueryRow(context.Background(), query, taskID).Scan(
		&result.ID, &result.TaskID, &result.UserLogin, &result.X, &result.Y, &result.Z,
		&result.Latitude, &result.Longitude, &result.Height, &result.Q, &result.NSat,
		&result.SDX, &result.SDY, &result.SDZ, &result.LastSolutionLine,
		&result.FullResultFile, &result.FileType, &result.RawOutput, &result.CreatedAt, &result.ExpiresAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("get result: %w", err)
	}

	return &result, nil
}

// GetUserTasksWithResults возвращает задачи пользователя с результатами
func (s *TaskStorage) GetUserTasksWithResults(userLogin string, limit, offset int) ([]map[string]interface{}, error) {
	query := `
		SELECT t.id, t.user_login, t.config, t.filename, t.status, 
		       COALESCE(t.error_message, ''), t.created_at, t.completed_at, t.processing_sec,
		       COALESCE(r.x, 0), COALESCE(r.y, 0), COALESCE(r.z, 0), 
		       COALESCE(r.latitude, 0), COALESCE(r.longitude, 0), COALESCE(r.height, 0), 
		       COALESCE(r.q, 0), COALESCE(r.n_sat, 0),
		       COALESCE(r.sdx, 0), COALESCE(r.sdy, 0), COALESCE(r.sdz, 0),
		       COALESCE(r.fix_rate, 0),
		       COALESCE(r.last_solution_line, ''), COALESCE(r.file_type, '')
		FROM processing_tasks t
		LEFT JOIN processing_results r ON t.id = r.task_id
		WHERE t.user_login = $1 AND (r.expires_at IS NULL OR r.expires_at > NOW())
		ORDER BY t.created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := s.pool.Query(context.Background(), query, userLogin, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query tasks with results: %w", err)
	}
	defer rows.Close()

	var tasks []map[string]interface{}
	for rows.Next() {
		var (
			id, userLogin, filename, status, errorMessage, lastSolutionLine, fileType string
			configJSON                                                                []byte
			createdAt                                                                 time.Time
			completedAt                                                               *time.Time
			processingSec                                                             *float64
			x, y, z, lat, lon, height                                                 float64
			q, nSat                                                                   int
			sdx, sdy, sdz, fixRate                                                    float32
		)

		err := rows.Scan(
			&id, &userLogin, &configJSON, &filename, &status,
			&errorMessage, &createdAt, &completedAt, &processingSec,
			&x, &y, &z, &lat, &lon, &height, &q, &nSat,
			&sdx, &sdy, &sdz, &fixRate,
			&lastSolutionLine, &fileType,
		)
		if err != nil {
			return nil, fmt.Errorf("scan task result: %w", err)
		}

		var config model.UserProcessingConfig
		if err := json.Unmarshal(configJSON, &config); err != nil {
			config = model.UserProcessingConfig{Method: model.MethodSingle}
		}

		task := map[string]interface{}{
			"id":        id,
			"userLogin": userLogin,
			"config":    config,
			"filename":  filename,
			"status":    status,
			"createdAt": createdAt,
			"fileType":  fileType,
		}

		if completedAt != nil {
			task["completedAt"] = completedAt
		}

		if errorMessage != "" {
			task["errorMessage"] = errorMessage
		}
		if processingSec != nil {
			task["processingSec"] = *processingSec
		}

		result := map[string]interface{}{
			"x": x, "y": y, "z": z,
			"latitude": lat, "longitude": lon, "height": height,
			"q": q, "nSat": nSat,
			"sdx": sdx, "sdy": sdy, "sdz": sdz,
			"fixRate": fixRate,
		}

		if fileType == "static" && lastSolutionLine != "" {
			result["lastSolutionLine"] = lastSolutionLine
		}

		task["result"] = result
		tasks = append(tasks, task)
	}

	return tasks, nil
}

// GetSystemStats возвращает общую статистику системы
func (s *TaskStorage) GetSystemStats() (map[string]interface{}, error) {
	query := `
		SELECT 
			(SELECT COUNT(DISTINCT user_login) FROM processing_tasks) as total_users,
			(SELECT COUNT(*) FROM processing_tasks WHERE DATE(created_at) = CURRENT_DATE) as today_tasks
	`

	var totalUsers int
	var todayTasks int

	err := s.pool.QueryRow(context.Background(), query).Scan(&totalUsers, &todayTasks)
	if err != nil {
		return nil, fmt.Errorf("get stats: %w", err)
	}

	return map[string]interface{}{
		"activeUsers":       totalUsers,
		"measurementsToday": todayTasks,
	}, nil
}

// CleanExpiredResults удаляет устаревшие результаты
func (s *TaskStorage) CleanExpiredResults() error {
	_, err := s.pool.Exec(context.Background(), `
		DELETE FROM processing_results WHERE expires_at < NOW()
	`)
	return err
}

// FailStalledTasks marks tasks stuck in "processing" for longer than timeout as failed.
func (s *TaskStorage) FailStalledTasks(timeout time.Duration) error {
	cutoff := time.Now().Add(-timeout)
	_, err := s.pool.Exec(context.Background(), `
		UPDATE processing_tasks
		SET status = 'failed',
		    error_message = 'processing timed out'
		WHERE status = 'processing'
		  AND started_at < $1
	`, cutoff)
	return err
}

// DeleteTaskByID удаляет задачу и её результат по ID (только если принадлежит пользователю)
func (s *TaskStorage) DeleteTaskByID(taskID, userLogin string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Удаляем результат (если есть)
	_, err := s.pool.Exec(ctx,
		`DELETE FROM processing_results WHERE task_id = $1 AND user_login = $2`,
		taskID, userLogin,
	)
	if err != nil {
		return fmt.Errorf("delete result: %w", err)
	}

	// Удаляем саму задачу
	result, err := s.pool.Exec(ctx,
		`DELETE FROM processing_tasks WHERE id = $1 AND user_login = $2`,
		taskID, userLogin,
	)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("task not found or access denied")
	}

	return nil
}

// DeleteAllUserTasks удаляет все задачи и результаты пользователя
func (s *TaskStorage) DeleteAllUserTasks(userLogin string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Удаляем результаты
	_, err := s.pool.Exec(ctx,
		`DELETE FROM processing_results WHERE user_login = $1`,
		userLogin,
	)
	if err != nil {
		return 0, fmt.Errorf("delete results: %w", err)
	}

	// Удаляем задачи
	res, err := s.pool.Exec(ctx,
		`DELETE FROM processing_tasks WHERE user_login = $1`,
		userLogin,
	)
	if err != nil {
		return 0, fmt.Errorf("delete tasks: %w", err)
	}

	return res.RowsAffected(), nil
}
