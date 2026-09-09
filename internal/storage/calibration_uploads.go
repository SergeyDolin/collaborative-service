package storage

import (
	"collaborative/internal/model"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Upload and session metadata commit together, serialized with Submit on the task row.
func (s *TaskStorage) SaveCalibrationUpload(taskID, login, uploadID, filename string, data []byte, session *model.CalibrationSession) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var status string
	if err = tx.QueryRow(ctx, `SELECT status FROM calibration_tasks WHERE id=$1 AND user_login=$2 AND expires_at>NOW() FOR UPDATE`, taskID, login).Scan(&status); err != nil {
		return err
	}
	if status != "pending" {
		return fmt.Errorf("задача уже запущена")
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM calibration_sessions WHERE task_id=$1`, taskID).Scan(&count); err != nil {
		return err
	}
	if session != nil && count >= 32 {
		return fmt.Errorf("не более 32 сеансов")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO calibration_uploads(task_id,upload_id,filename,data) VALUES($1,$2,$3,$4) ON CONFLICT(task_id,upload_id) DO UPDATE SET filename=EXCLUDED.filename,data=EXCLUDED.data`, taskID, uploadID, filename, data); err != nil {
		return err
	}
	if session != nil {
		geometry, e := json.Marshal(session.Geometry)
		if e != nil {
			return e
		}
		_, err = tx.Exec(ctx, `INSERT INTO calibration_sessions(id,task_id,filename,position,orientation,status,geometry_json) VALUES($1,$2,$3,$4,$5,'pending',$6)`, session.ID, taskID, filename, session.Position, session.Orientation, geometry)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *TaskStorage) CalibrationUpload(taskID, uploadID string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var data []byte
	var filename string
	err := s.pool.QueryRow(ctx, `SELECT u.data,u.filename FROM calibration_uploads u JOIN calibration_tasks t ON t.id=u.task_id WHERE u.task_id=$1 AND u.upload_id=$2 AND t.expires_at>NOW()`, taskID, uploadID).Scan(&data, &filename)
	return data, filename, err
}
func (s *TaskStorage) ClaimCalibration(taskID, login string) (bool, error) {
	tag, err := s.pool.Exec(context.Background(), `UPDATE calibration_tasks SET status='processing',started_at=NOW() WHERE id=$1 AND user_login=$2 AND status='pending' AND expires_at>NOW()`, taskID, login)
	return tag.RowsAffected() == 1, err
}
func (s *TaskStorage) ClearCalibrationUploads(taskID string) error {
	_, err := s.pool.Exec(context.Background(), `DELETE FROM calibration_uploads WHERE task_id=$1`, taskID)
	return err
}
func (s *TaskStorage) CleanExpiredCalibrations() error {
	if _, err := s.pool.Exec(context.Background(), `WITH failed AS (
	UPDATE calibration_tasks SET status='failed',completed_at=NOW(),error_msg='Обработка прервана или превысила 4 часа'
	WHERE status='processing' AND COALESCE(started_at,created_at)<NOW()-INTERVAL '4 hours' RETURNING id
	) DELETE FROM calibration_uploads WHERE task_id IN(SELECT id FROM failed)`); err != nil {
		return err
	}
	_, err := s.pool.Exec(context.Background(), `DELETE FROM calibration_tasks WHERE expires_at<=NOW()`)
	return err
}
