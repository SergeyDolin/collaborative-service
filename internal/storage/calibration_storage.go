package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"collaborative/internal/model"
)

// InitCalibrationSchema создаёт таблицы калибровки.
func (s *TaskStorage) InitCalibrationSchema() error {
	_, err := s.pool.Exec(context.Background(), `
		CREATE TABLE IF NOT EXISTS calibration_tasks (
			id            VARCHAR(36) PRIMARY KEY,
			user_login    VARCHAR(255) NOT NULL,
			device_id     INTEGER,
			device_model  VARCHAR(255),
			mode          VARCHAR(32) NOT NULL,
			status        VARCHAR(32) NOT NULL DEFAULT 'pending',
			error_msg     TEXT,
			ref_type      VARCHAR(32),
			ref_lat       DOUBLE PRECISION,
			ref_lon       DOUBLE PRECISION,
			ref_h         DOUBLE PRECISION,
			receiver_task_id VARCHAR(36),
			reduce_h      DOUBLE PRECISION NOT NULL DEFAULT 0,
			reduce_e      DOUBLE PRECISION NOT NULL DEFAULT 0,
			reduce_n      DOUBLE PRECISION NOT NULL DEFAULT 0,
			result_json   TEXT,
			created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			completed_at  TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS calibration_sessions (
			id           VARCHAR(36) PRIMARY KEY,
			task_id      VARCHAR(36) NOT NULL REFERENCES calibration_tasks(id) ON DELETE CASCADE,
			filename     VARCHAR(255) NOT NULL,
			position     VARCHAR(16) NOT NULL,
			orientation  VARCHAR(8)  NOT NULL,
			ppp_task_id  VARCHAR(36),
			status       VARCHAR(32) NOT NULL DEFAULT 'pending',
			delta_e      DOUBLE PRECISION,
			delta_n      DOUBLE PRECISION,
			delta_u      DOUBLE PRECISION,
			fix_rate     REAL
		);
		CREATE INDEX IF NOT EXISTS idx_calib_tasks_user ON calibration_tasks(user_login);
		CREATE INDEX IF NOT EXISTS idx_calib_sessions_task ON calibration_sessions(task_id);
	`)
	return err
}

// CreateCalibrationTask сохраняет новую задачу калибровки.
func (s *TaskStorage) CreateCalibrationTask(t *model.CalibrationTask) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO calibration_tasks
			(id, user_login, device_id, device_model, mode, status,
			 ref_type, ref_lat, ref_lon, ref_h, receiver_task_id,
			 reduce_h, reduce_e, reduce_n, created_at)
		VALUES ($1,$2,$3,$4,$5,'pending',$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		t.ID, t.UserLogin, t.DeviceID, t.DeviceModel, t.Mode,
		t.RefType, t.RefLat, t.RefLon, t.RefH, nullStr(t.ReceiverTaskID),
		t.ReduceH, t.ReduceE, t.ReduceN, t.CreatedAt,
	)
	return err
}

// AddCalibrationSession добавляет сеанс к задаче.
func (s *TaskStorage) AddCalibrationSession(sess *model.CalibrationSession) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO calibration_sessions
			(id, task_id, filename, position, orientation, status)
		VALUES ($1,$2,$3,$4,$5,'pending')`,
		sess.ID, sess.TaskID, sess.Filename, sess.Position, sess.Orientation,
	)
	return err
}

// UpdateCalibrationSession обновляет ppp_task_id и статус сеанса.
func (s *TaskStorage) UpdateCalibrationSession(sess *model.CalibrationSession) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.pool.Exec(ctx, `
		UPDATE calibration_sessions
		SET ppp_task_id=$1, status=$2, delta_e=$3, delta_n=$4, delta_u=$5, fix_rate=$6
		WHERE id=$7`,
		nullStr(sess.PPPTaskID), sess.Status,
		nullFloat(sess.DeltaE), nullFloat(sess.DeltaN), nullFloat(sess.DeltaU),
		nullFloat(sess.FixRate), sess.ID,
	)
	return err
}

// UpdateCalibrationTaskStatus обновляет статус и опционально результат.
func (s *TaskStorage) UpdateCalibrationTaskStatus(id, status, errMsg string, result *model.CalibrationResult) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var resultJSON []byte
	if result != nil {
		resultJSON, _ = json.Marshal(result)
	}
	var completedAt *time.Time
	if status == "completed" || status == "failed" {
		t := time.Now()
		completedAt = &t
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE calibration_tasks
		SET status=$1, error_msg=$2, result_json=$3, completed_at=$4
		WHERE id=$5`,
		status, nullStr(errMsg), nullBytes(resultJSON), completedAt, id,
	)
	return err
}

// GetCalibrationTask возвращает задачу с сеансами.
func (s *TaskStorage) GetCalibrationTask(id string) (*model.CalibrationTask, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t := &model.CalibrationTask{}
	var resultJSON []byte
	var errMsg, receiverTaskID, deviceModel *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_login, device_id, device_model, mode, status, error_msg,
		       ref_type, ref_lat, ref_lon, ref_h, receiver_task_id,
		       reduce_h, reduce_e, reduce_n, result_json, created_at, completed_at
		FROM calibration_tasks WHERE id=$1`, id).Scan(
		&t.ID, &t.UserLogin, &t.DeviceID, &deviceModel, &t.Mode, &t.Status, &errMsg,
		&t.RefType, &t.RefLat, &t.RefLon, &t.RefH, &receiverTaskID,
		&t.ReduceH, &t.ReduceE, &t.ReduceN, &resultJSON, &t.CreatedAt, &t.CompletedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get calibration task: %w", err)
	}
	if errMsg != nil {
		t.ErrorMsg = *errMsg
	}
	if receiverTaskID != nil {
		t.ReceiverTaskID = *receiverTaskID
	}
	if deviceModel != nil {
		t.DeviceModel = *deviceModel
	}
	if resultJSON != nil {
		_ = json.Unmarshal(resultJSON, &t.Result)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, task_id, filename, position, orientation,
		       ppp_task_id, status, delta_e, delta_n, delta_u, fix_rate
		FROM calibration_sessions WHERE task_id=$1 ORDER BY id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sess model.CalibrationSession
		var pppTaskID *string
		var dE, dN, dU *float64
		var fixRate *float64
		if err := rows.Scan(&sess.ID, &sess.TaskID, &sess.Filename, &sess.Position, &sess.Orientation,
			&pppTaskID, &sess.Status, &dE, &dN, &dU, &fixRate); err != nil {
			return nil, err
		}
		if pppTaskID != nil {
			sess.PPPTaskID = *pppTaskID
		}
		if dE != nil {
			sess.DeltaE = *dE
		}
		if dN != nil {
			sess.DeltaN = *dN
		}
		if dU != nil {
			sess.DeltaU = *dU
		}
		if fixRate != nil {
			sess.FixRate = *fixRate
		}
		t.Sessions = append(t.Sessions, sess)
	}
	return t, nil
}

// ListCalibrationTasks возвращает задачи пользователя (без сеансов).
func (s *TaskStorage) ListCalibrationTasks(userLogin string) ([]*model.CalibrationTask, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_login, device_id, device_model, mode, status, error_msg,
		       ref_type, reduce_h, reduce_e, reduce_n, result_json, created_at, completed_at
		FROM calibration_tasks WHERE user_login=$1 ORDER BY created_at DESC`, userLogin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []*model.CalibrationTask
	for rows.Next() {
		t := &model.CalibrationTask{}
		var resultJSON []byte
		var errMsg, deviceModel *string
		if err := rows.Scan(&t.ID, &t.UserLogin, &t.DeviceID, &deviceModel, &t.Mode, &t.Status, &errMsg,
			&t.RefType, &t.ReduceH, &t.ReduceE, &t.ReduceN, &resultJSON, &t.CreatedAt, &t.CompletedAt); err != nil {
			return nil, err
		}
		if errMsg != nil {
			t.ErrorMsg = *errMsg
		}
		if deviceModel != nil {
			t.DeviceModel = *deviceModel
		}
		if resultJSON != nil {
			_ = json.Unmarshal(resultJSON, &t.Result)
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

// SetCalibrationReceiverTask сохраняет task_id PPP приёмника в задаче калибровки.
func (s *TaskStorage) SetCalibrationReceiverTask(calibTaskID, pppTaskID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.pool.Exec(ctx,
		`UPDATE calibration_tasks SET receiver_task_id=$1 WHERE id=$2`,
		pppTaskID, calibTaskID,
	)
	return err
}

// helpers
func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
func nullFloat(f float64) interface{} {
	return f
}
func nullBytes(b []byte) interface{} {
	if len(b) == 0 {
		return nil
	}
	return b
}
