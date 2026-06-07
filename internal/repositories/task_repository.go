package repositories

import (
	"collaborative/internal/model"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TaskRepositoryImpl реализация TaskRepository
type TaskRepositoryImpl struct {
	pool *pgxpool.Pool
}

// NewTaskRepositoryImpl создает новый репозиторий задач
func NewTaskRepositoryImpl(pool *pgxpool.Pool) *TaskRepositoryImpl {
	return &TaskRepositoryImpl{pool: pool}
}

// CreateTask создает новую задачу
func (r *TaskRepositoryImpl) CreateTask(task *model.ProcessingTask) error {
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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = r.pool.Exec(ctx, query,
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
func (r *TaskRepositoryImpl) UpdateTask(task *model.ProcessingTask) error {
	query := `
		UPDATE processing_tasks 
		SET status = $1, error_message = $2, started_at = $3, 
		    completed_at = $4, processing_sec = $5, output_path = $6
		WHERE id = $7
	`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := r.pool.Exec(ctx, query,
		task.Status, task.ErrorMessage, task.StartedAt,
		task.CompletedAt, task.ProcessingSec, task.OutputPath, task.ID,
	)

	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}

	return nil
}

// GetUserTasks получает все задачи пользователя
func (r *TaskRepositoryImpl) GetUserTasks(userLogin string, limit, offset int) ([]model.ProcessingTask, error) {
	// Валидируем параметры
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 || offset > 1000000 {
		offset = 0
	}

	query := `
		SELECT id, user_login, config, filename, original_path, 
		       rinex_path, output_path, status, error_message,
		       created_at, started_at, completed_at, processing_sec
		FROM processing_tasks
		WHERE user_login = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := r.pool.Query(ctx, query, userLogin, limit, offset)
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

// GetTaskByID получает задачу по ID
func (r *TaskRepositoryImpl) GetTaskByID(taskID string) (*model.ProcessingTask, error) {
	query := `
		SELECT id, user_login, config, filename, original_path, 
		       rinex_path, output_path, status, error_message,
		       created_at, started_at, completed_at, processing_sec
		FROM processing_tasks
		WHERE id = $1
	`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var task model.ProcessingTask
	var configJSON []byte

	err := r.pool.QueryRow(ctx, query, taskID).Scan(
		&task.ID, &task.UserLogin, &configJSON, &task.Filename,
		&task.OriginalPath, &task.RinexPath, &task.OutputPath,
		&task.Status, &task.ErrorMessage, &task.CreatedAt,
		&task.StartedAt, &task.CompletedAt, &task.ProcessingSec,
	)

	if err != nil {
		return nil, nil // Задача не найдена, возвращаем nil без ошибки
	}

	if err := json.Unmarshal(configJSON, &task.Config); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &task, nil
}

// GetUserTasksWithResults получает задачи с результатами
func (r *TaskRepositoryImpl) GetUserTasksWithResults(userLogin string, limit, offset int) ([]map[string]interface{}, error) {
	// Валидируем параметры
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if offset < 0 || offset > 1000000 {
		offset = 0
	}

	query := `
		SELECT t.id, t.user_login, t.config, t.filename, t.status, 
		       COALESCE(t.error_message, ''), t.created_at, t.completed_at, t.processing_sec,
		       COALESCE(r.latitude, 0), COALESCE(r.longitude, 0), COALESCE(r.height, 0), 
		       COALESCE(r.q, 0), COALESCE(r.n_sat, 0),
		       COALESCE(r.last_solution_line, ''), COALESCE(r.file_type, '')
		FROM processing_tasks t
		LEFT JOIN processing_results r ON t.id = r.task_id
		WHERE t.user_login = $1 AND (r.expires_at IS NULL OR r.expires_at > NOW())
		ORDER BY t.created_at DESC
		LIMIT $2 OFFSET $3
	`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := r.pool.Query(ctx, query, userLogin, limit, offset)
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
			lat, lon, height                                                          float64
			q, nSat                                                                   int
		)

		err := rows.Scan(
			&id, &userLogin, &configJSON, &filename, &status,
			&errorMessage, &createdAt, &completedAt, &processingSec,
			&lat, &lon, &height, &q, &nSat,
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
			"latitude":  lat,
			"longitude": lon,
			"height":    height,
			"q":         q,
			"nSat":      nSat,
		}

		if fileType == "static" && lastSolutionLine != "" {
			result["lastSolutionLine"] = lastSolutionLine
		}

		task["result"] = result
		tasks = append(tasks, task)
	}

	return tasks, nil
}

// SaveResult сохраняет результат
func (r *TaskRepositoryImpl) SaveResult(result *model.ProcessingResult) error {
	if result.ExpiresAt.IsZero() {
		result.ExpiresAt = time.Now().Add(24 * time.Hour)
	}

	query := `
		INSERT INTO processing_results (
			task_id, user_login, x, y, z, latitude, longitude, height,
			q, n_sat, sdx, sdy, sdz, last_solution_line, 
			full_result_file, file_type, raw_output, created_at, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		ON CONFLICT (task_id) DO UPDATE SET
			x = EXCLUDED.x, y = EXCLUDED.y, z = EXCLUDED.z,
			latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude,
			height = EXCLUDED.height, q = EXCLUDED.q, n_sat = EXCLUDED.n_sat,
			sdx = EXCLUDED.sdx, sdy = EXCLUDED.sdy, sdz = EXCLUDED.sdz,
			last_solution_line = EXCLUDED.last_solution_line,
			full_result_file = EXCLUDED.full_result_file, file_type = EXCLUDED.file_type,
			raw_output = EXCLUDED.raw_output, expires_at = EXCLUDED.expires_at
	`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := r.pool.Exec(ctx, query,
		result.TaskID, result.UserLogin, result.X, result.Y, result.Z,
		result.Latitude, result.Longitude, result.Height, result.Q,
		result.NSat, result.SDX, result.SDY, result.SDZ,
		result.LastSolutionLine, result.FullResultFile, result.FileType,
		result.RawOutput, result.CreatedAt, result.ExpiresAt,
	)

	if err != nil {
		return fmt.Errorf("save result: %w", err)
	}

	return nil
}

// GetResultByTaskID получает результат по ID задачи
func (r *TaskRepositoryImpl) GetResultByTaskID(taskID string) (*model.ProcessingResult, error) {
	query := `
		SELECT id, task_id, user_login, x, y, z, latitude, longitude, height,
		       q, n_sat, sdx, sdy, sdz, last_solution_line, 
		       full_result_file, file_type, raw_output, created_at, expires_at
		FROM processing_results
		WHERE task_id = $1
	`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result model.ProcessingResult

	err := r.pool.QueryRow(ctx, query, taskID).Scan(
		&result.ID, &result.TaskID, &result.UserLogin, &result.X, &result.Y, &result.Z,
		&result.Latitude, &result.Longitude, &result.Height, &result.Q, &result.NSat,
		&result.SDX, &result.SDY, &result.SDZ, &result.LastSolutionLine,
		&result.FullResultFile, &result.FileType, &result.RawOutput,
		&result.CreatedAt, &result.ExpiresAt,
	)

	if err != nil {
		return nil, nil // Результат не найден
	}

	return &result, nil
}

// CleanExpiredResults удаляет устаревшие результаты
func (r *TaskRepositoryImpl) CleanExpiredResults() error {
	query := `DELETE FROM processing_results WHERE expires_at < NOW()`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := r.pool.Exec(ctx, query)
	return err
}

// GetSystemStats получает системную статистику
func (r *TaskRepositoryImpl) GetSystemStats() (map[string]interface{}, error) {
	query := `
		SELECT 
			(SELECT COUNT(DISTINCT user_login) FROM processing_tasks) as total_users,
			(SELECT COUNT(*) FROM processing_tasks WHERE DATE(created_at) = CURRENT_DATE) as today_tasks
	`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var totalUsers int
	var todayTasks int

	err := r.pool.QueryRow(ctx, query).Scan(&totalUsers, &todayTasks)
	if err != nil {
		return nil, fmt.Errorf("get stats: %w", err)
	}

	return map[string]interface{}{
		"activeUsers":       totalUsers,
		"measurementsToday": todayTasks,
	}, nil
}
