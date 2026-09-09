package services

import (
	"collaborative/internal/model"
	"collaborative/internal/storage"
	"context"
	"fmt"
	"math"
	"time"

	"go.uber.org/zap"
)

type CalibrationService struct {
	taskStorage *storage.TaskStorage
	measSvc     *MeasurementService
	logger      *zap.SugaredLogger
}

func NewCalibrationService(st *storage.TaskStorage, m *MeasurementService, l *zap.SugaredLogger) *CalibrationService {
	return &CalibrationService{st, m, l}
}
func (s *CalibrationService) RunCalibration(ctx context.Context, id string) {
	defer func() {
		if err := s.taskStorage.ClearCalibrationUploads(id); err != nil {
			s.logger.Errorw("calibration upload cleanup failed", "task", id)
		}
	}()
	task, err := s.taskStorage.GetCalibrationTask(id)
	if err != nil {
		s.fail(id, err.Error())
		return
	}
	if err = ValidateCalibration(task); err != nil {
		s.fail(id, err.Error())
		return
	}
	deadline := time.Now().Add(4 * time.Hour)
	if task.ExpiresAt.Before(deadline) {
		deadline = task.ExpiresAt
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	base, name, err := s.taskStorage.CalibrationUpload(id, "base")
	if err != nil {
		s.fail(id, "Файл базы недоступен")
		return
	}
	var intervals [][2]time.Time
	for i := range task.Sessions {
		sess := &task.Sessions[i]
		raw, filename, e := s.taskStorage.CalibrationUpload(id, sess.ID)
		if e != nil {
			s.fail(id, "Файл сеанса недоступен")
			return
		}
		sess.Status = "processing"
		if e = s.taskStorage.UpdateCalibrationSession(sess); e != nil {
			s.fail(id, e.Error())
			return
		}
		summary, e := s.measSvc.processCalibrationSession(ctx, task, sess, raw, filename, base, name)
		if e != nil {
			sess.Status = "failed"
			_ = s.taskStorage.UpdateCalibrationSession(sess)
			s.fail(id, fmt.Sprintf("Сеанс %s/%s: %v", sess.Position, sess.Orientation, e))
			return
		}
		for _, span := range intervals {
			if !summary.Start.After(span[1]) && !summary.End.Before(span[0]) {
				s.fail(id, "Интервалы сеансов смартфона пересекаются. Для каждой ориентации и контроля нужны отдельные наблюдения")
				return
			}
		}
		intervals = append(intervals, [2]time.Time{summary.Start, summary.End})
		origin := geoXYZ(task.RefLat, task.RefLon, task.RefH)
		lat, lon := task.RefLat, task.RefLon
		if task.RefType == model.CalibRefNone {
			lat, lon = task.Options.BaseLat, task.Options.BaseLon
			origin = geoXYZ(lat, lon, task.Options.BaseH)
		}
		delta := xyzENU(summary.Mean, origin, lat, lon)
		sess.DeltaE = delta[0] - sess.Geometry.ReduceE
		sess.DeltaN = delta[1] - sess.Geometry.ReduceN
		sess.DeltaU = delta[2] - sess.Geometry.ReduceH
		sess.FixRate = 100 * float64(summary.Fixed) / float64(summary.Total)
		sess.Geometry.Epochs = summary.Total
		sess.Geometry.FixedEpochs = summary.Fixed
		sess.Geometry.First = summary.First
		sess.Geometry.Last = summary.Last
		sess.Status = "completed"
		if e = s.taskStorage.UpdateCalibrationSession(sess); e != nil {
			s.fail(id, e.Error())
			return
		}
	}
	result, err := computeCalibration(task)
	if err != nil {
		s.fail(id, err.Error())
		return
	}
	if err = s.taskStorage.UpdateCalibrationTaskStatus(id, "completed", "", result); err != nil {
		s.fail(id, "Не удалось сохранить результат")
	}
}
func (s *CalibrationService) fail(id, msg string) {
	s.logger.Warnw("calibration failed", "task", id)
	_ = s.taskStorage.UpdateCalibrationTaskStatus(id, "failed", msg, nil)
}
func finiteCal(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func ValidateCalibration(t *model.CalibrationTask) error {
	if t.Mode != model.CalibModeFullCalib && t.Mode != model.CalibModeHorizontalOnly && t.Mode != model.CalibModeQuick {
		return fmt.Errorf("неверный режим")
	}
	if t.RefType != model.CalibRefGeodetic && t.RefType != model.CalibRefNone {
		return fmt.Errorf("укажите координаты марки либо режим без марки")
	}
	if t.RefType == model.CalibRefNone && t.Mode != model.CalibModeHorizontalOnly {
		return fmt.Errorf("для этого режима нужны координаты марки")
	}
	if !t.HasReceiver {
		return fmt.Errorf("загрузите наблюдения опорного приёмника")
	}
	required := map[string]bool{}
	for _, o := range []string{"north", "east", "south", "west"} {
		required["vertical/"+o] = true
		if t.Mode == model.CalibModeFullCalib {
			required["horizontal/"+o] = true
		}
	}
	if t.Mode == model.CalibModeQuick {
		required = map[string]bool{"vertical/north": true}
	}
	controls := 0
	for _, ss := range t.Sessions {
		if err := ValidateCalibrationSession(t, ss); err != nil {
			return err
		}
		if ss.Geometry.Control {
			controls++
			continue
		}
		delete(required, ss.Position+"/"+ss.Orientation)
	}
	if len(required) > 0 {
		return fmt.Errorf("неполный набор калибровочных ориентаций")
	}
	if controls == 0 {
		return fmt.Errorf("добавьте независимый контрольный сеанс")
	}
	return nil
}
func ValidateCalibrationSession(t *model.CalibrationTask, s model.CalibrationSession) error {
	if s.Position != "vertical" && s.Position != "horizontal" {
		return fmt.Errorf("неверное положение")
	}
	if s.Orientation != "north" && s.Orientation != "south" && s.Orientation != "east" && s.Orientation != "west" {
		return fmt.Errorf("неверная ориентация")
	}
	if t.Mode == model.CalibModeHorizontalOnly && s.Position != "vertical" {
		return fmt.Errorf("нужны вертикальные установки")
	}
	if t.Mode == model.CalibModeQuick && (s.Position != "vertical" || s.Orientation != "north") {
		return fmt.Errorf("поправка установки и контроль требуют положения vertical/north")
	}
	for _, v := range []float64{s.Geometry.ReduceE, s.Geometry.ReduceN, s.Geometry.ReduceH} {
		if !finiteCal(v) || math.Abs(v) > 100 {
			return fmt.Errorf("смещение марки → ARP должно быть конечным и не превышать 100 м")
		}
	}
	return nil
}
func geoXYZ(lat, lon, h float64) [3]float64 {
	b, l := lat*math.Pi/180, lon*math.Pi/180
	e2 := 6.6943799901413165e-3
	n := 6378137 / math.Sqrt(1-e2*math.Sin(b)*math.Sin(b))
	return [3]float64{(n + h) * math.Cos(b) * math.Cos(l), (n + h) * math.Cos(b) * math.Sin(l), (n*(1-e2) + h) * math.Sin(b)}
}
func xyzENU(x, origin [3]float64, lat, lon float64) [3]float64 {
	b, l := lat*math.Pi/180, lon*math.Pi/180
	dx, dy, dz := x[0]-origin[0], x[1]-origin[1], x[2]-origin[2]
	return [3]float64{-math.Sin(l)*dx + math.Cos(l)*dy, -math.Sin(b)*math.Cos(l)*dx - math.Sin(b)*math.Sin(l)*dy + math.Cos(b)*dz, math.Cos(b)*math.Cos(l)*dx + math.Cos(b)*math.Sin(l)*dy + math.Sin(b)*dz}
}

// Right, screen outward, top. Vertical: camera azimuth. Horizontal:
// screen up, top edge azimuth. Orientations refer to true north.
func enuToBody(e, n, u float64, position, orientation string) (right, screen, up float64) {
	theta := map[string]float64{"north": 0, "east": math.Pi / 2, "south": math.Pi, "west": 3 * math.Pi / 2}[orientation]
	c, s := math.Cos(theta), math.Sin(theta)
	right = c*e - s*n
	if position == "vertical" {
		screen = -s*e - c*n
		up = u
	} else {
		screen = u
		up = s*e + c*n
	}
	return
}
