package services

import (
	"context"
	"fmt"
	"math"
	"time"

	"collaborative/internal/model"
	"collaborative/internal/storage"

	"go.uber.org/zap"
)

// CalibrationService определяет средний фазовый центр антенны смартфона.
//
// Алгоритм основан на принципе сверхкороткой базы (USBL):
//   - Для каждого сеанса запускается PPP, результат редуцируется к марке.
//   - Разность PPP − (марка + редуцирование) проецируется из географических осей
//     ENU в оси тела смартфона.
//   - Усреднение по всем ориентациям даёт смещение фазового центра.
//
// Оси тела смартфона (в вертикальном положении, экран к наблюдателю):
//
//	right : вправо (от пользователя)
//	up    : вверх (к верхней грани = ARP)
//	screen: от тела к экрану (от задней крышки к пользователю)
//
// Результат записывается как смещение фазового центра относительно ARP:
//
//	OffsetLeft  > 0  → фазовый центр смещён влево от оси экрана
//	OffsetDepth > 0  → фазовый центр смещён вглубь корпуса (от экрана к крышке)
//	OffsetDown  > 0  → фазовый центр смещён вниз от верхней грани
type CalibrationService struct {
	taskStorage *storage.TaskStorage
	measSvc     *MeasurementService
	logger      *zap.SugaredLogger
}

func NewCalibrationService(
	taskStorage *storage.TaskStorage,
	measSvc *MeasurementService,
	logger *zap.SugaredLogger,
) *CalibrationService {
	return &CalibrationService{
		taskStorage: taskStorage,
		measSvc:     measSvc,
		logger:      logger,
	}
}

// RunCalibration запускает все PPP-сеансы и вычисляет фазовый центр.
// Вызывается асинхронно после того как все файлы загружены.
func (s *CalibrationService) RunCalibration(ctx context.Context, taskID string) {
	s.logger.Infof("[calib:%s] start", taskID)

	if err := s.taskStorage.UpdateCalibrationTaskStatus(taskID, "processing", "", nil); err != nil {
		s.logger.Errorf("[calib:%s] status update: %v", taskID, err)
		return
	}

	task, err := s.taskStorage.GetCalibrationTask(taskID)
	if err != nil {
		s.logger.Errorf("[calib:%s] get task: %v", taskID, err)
		_ = s.taskStorage.UpdateCalibrationTaskStatus(taskID, "failed", err.Error(), nil)
		return
	}

	// --- шаг 1: если refType=receiver, ждём/берём PPP результат приёмника ---
	if task.RefType == model.CalibRefReceiver && task.ReceiverTaskID != "" {
		lat, lon, h, err := s.waitAndGetPPPResult(ctx, task.ReceiverTaskID)
		if err != nil {
			s.fail(taskID, fmt.Sprintf("receiver PPP failed: %v", err))
			return
		}
		task.RefLat, task.RefLon, task.RefH = lat, lon, h
		s.logger.Infof("[calib:%s] receiver ref: %.8f %.8f %.4f", taskID, lat, lon, h)
	}

	// --- шаг 2: PPP для каждого сеанса смартфона ---
	for i := range task.Sessions {
		sess := &task.Sessions[i]
		if sess.PPPTaskID == "" {
			s.logger.Warnf("[calib:%s] session %s has no ppp_task_id, skip", taskID, sess.ID)
			continue
		}
		lat, lon, h, fixRate, err := s.waitAndGetPPPResultFull(ctx, sess.PPPTaskID)
		if err != nil {
			sess.Status = "failed"
			_ = s.taskStorage.UpdateCalibrationSession(sess)
			s.logger.Warnf("[calib:%s] session %s PPP failed: %v", taskID, sess.ID, err)
			continue
		}
		sess.FixRate = fixRate

		// --- шаг 3: редуцирование PPP → ARP → марка ---
		// Позиция ARP = марка + редуцирование (в ENU)
		// ΔX = PPP − ARP_geographic = PPP − (марка + reduction_in_ENU)
		dE, dN, dU := s.applyReduction(lat, lon, h, task)
		sess.DeltaE = dE
		sess.DeltaN = dN
		sess.DeltaU = dU
		sess.Status = "completed"
		_ = s.taskStorage.UpdateCalibrationSession(sess)

		s.logger.Infof("[calib:%s] session %s (%s %s): ΔE=%.4f ΔN=%.4f ΔU=%.4f fix=%.1f%%",
			taskID, sess.ID, sess.Position, sess.Orientation, dE, dN, dU, fixRate)
	}

	// --- шаг 4: вычислить фазовый центр ---
	result, err := s.computePhaseCenter(task)
	if err != nil {
		s.fail(taskID, err.Error())
		return
	}

	// Быстрая калибровка: ставим срок валидности 12 ч
	if task.Mode == model.CalibModeQuick {
		t := time.Now().Add(12 * time.Hour)
		result.ValidUntil = &t
	}

	if err := s.taskStorage.UpdateCalibrationTaskStatus(taskID, "completed", "", result); err != nil {
		s.logger.Errorf("[calib:%s] save result: %v", taskID, err)
		return
	}
	s.logger.Infof("[calib:%s] done: left=%.4f depth=%.4f down=%.4f",
		taskID, result.OffsetLeft, result.OffsetDepth, result.OffsetDown)
}

// applyReduction вычисляет вектор ΔE, ΔN, ΔU от ARP до марки в метрах.
//
// Измеренная PPP-позиция относится к фазовому центру антенны.
// Мы хотим разность: (PPP-позиция) − (положение ARP в пространстве).
// Положение ARP = (марка) + (редуцирование от марки к ARP):
//
//	ARP_lat/lon/h ≈ марка + (ReduceN, ReduceE, ReduceH) в метрах
//
// Поэтому: Δ = PPP − ARP = PPP − марка − reduction
func (s *CalibrationService) applyReduction(lat, lon, h float64, task *model.CalibrationTask) (dE, dN, dU float64) {
	if task.RefType == model.CalibRefNone {
		// Без опоры: абсолютное значение не вычитается.
		// Горизонтальные компоненты будут определены из симметрии по ориентациям.
		// Здесь сохраняем «сырые» PPP ENU относительно произвольного начала (первая сессия).
		// Обрабатывается в computePhaseCenter отдельно.
		dE = lon * 111320 * math.Cos(lat*math.Pi/180) // приближение, м
		dN = lat * 111320
		dU = h
		return
	}
	// Приближённый перевод разности координат в метры ENU
	refLat := task.RefLat * math.Pi / 180
	dN = (lat - task.RefLat) * 111320                    // м на север
	dE = (lon - task.RefLon) * 111320 * math.Cos(refLat) // м на восток
	dU = h - task.RefH                                   // м вверх

	// Вычесть редуцирование (от марки к ARP): ΔX = PPP − (марка + reduction)
	dE -= task.ReduceE
	dN -= task.ReduceN
	dU -= task.ReduceH
	return
}

// computePhaseCenter переводит ENU-смещения из каждого сеанса в оси тела смартфона
// и усредняет. Возвращает CalibrationResult.
func (s *CalibrationService) computePhaseCenter(task *model.CalibrationTask) (*model.CalibrationResult, error) {
	var sessions []model.CalibrationSession
	for _, sess := range task.Sessions {
		if sess.Status == "completed" {
			sessions = append(sessions, sess)
		}
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("все сеансы завершились с ошибкой")
	}

	if task.RefType == model.CalibRefNone {
		return s.computeNoRef(task, sessions)
	}
	return s.computeWithRef(task, sessions)
}

// computeWithRef: есть опорная точка — вычисляем тело-оси напрямую из ΔE/ΔN/ΔU.
func (s *CalibrationService) computeWithRef(_ *model.CalibrationTask, sessions []model.CalibrationSession) (*model.CalibrationResult, error) {
	type bodyVec struct{ right, up, screenOut float64 }

	var vecs []bodyVec
	var details []model.SessionDetail

	for _, sess := range sessions {
		r, sc, up := enuToBody(sess.DeltaE, sess.DeltaN, sess.DeltaU, sess.Position, sess.Orientation)
		vecs = append(vecs, bodyVec{r, up, sc})
		details = append(details, model.SessionDetail{
			Position:    sess.Position,
			Orientation: sess.Orientation,
			DeltaE:      sess.DeltaE,
			DeltaN:      sess.DeltaN,
			DeltaU:      sess.DeltaU,
			FixRate:     sess.FixRate,
		})
	}

	// Среднее
	var sumRight, sumUp, sumScreenOut float64
	for _, v := range vecs {
		sumRight += v.right
		sumUp += v.up
		sumScreenOut += v.screenOut
	}
	n := float64(len(vecs))
	meanRight := sumRight / n
	meanUp := sumUp / n
	meanScreenOut := sumScreenOut / n

	// СКП
	var ssR, ssU, ssSO float64
	for _, v := range vecs {
		ssR += (v.right - meanRight) * (v.right - meanRight)
		ssU += (v.up - meanUp) * (v.up - meanUp)
		ssSO += (v.screenOut - meanScreenOut) * (v.screenOut - meanScreenOut)
	}
	sigmaRight := math.Sqrt(ssR / n)
	sigmaUp := math.Sqrt(ssU / n)
	sigmaScreenOut := math.Sqrt(ssSO / n)

	// Соглашение о знаках результата:
	//   OffsetLeft  = -meanRight  (положительное → влево)
	//   OffsetDown  = -meanUp     (положительное → вниз от ARP)
	//   OffsetDepth = -meanScreenOut (положительное → вглубь корпуса)
	return &model.CalibrationResult{
		OffsetLeft:  -meanRight,
		OffsetDepth: -meanScreenOut,
		OffsetDown:  -meanUp,
		SigmaLeft:   sigmaRight,
		SigmaDepth:  sigmaScreenOut,
		SigmaDown:   sigmaUp,
		Sessions:    details,
	}, nil
}

// computeNoRef: нет опорной точки — горизонтальные компоненты из симметрии ориентаций.
// Вертикальная составляющая (OffsetDown) не определяется.
//
// Принцип: среднее ENU по 4 ориентациям = истинная позиция (горизонт. компоненты
// фазового центра компенсируются). Отклонение каждого сеанса от среднего — проекция.
func (s *CalibrationService) computeNoRef(_ *model.CalibrationTask, sessions []model.CalibrationSession) (*model.CalibrationResult, error) {
	// Для определения горизонтальных компонент нужны вертикальные сеансы
	var vertSessions []model.CalibrationSession
	for _, sess := range sessions {
		if sess.Position == model.CalibPosVertical {
			vertSessions = append(vertSessions, sess)
		}
	}
	if len(vertSessions) < 2 {
		return nil, fmt.Errorf("для определения без опоры нужны минимум 2 вертикальных сеанса")
	}

	// Сначала сеансы хранят «абсолютные» приближённые значения (в метрах).
	// Вычитаем среднее по горизонтали → получаем относительные ΔE, ΔN.
	var sumE, sumN float64
	for _, sess := range vertSessions {
		sumE += sess.DeltaE
		sumN += sess.DeltaN
	}
	meanE := sumE / float64(len(vertSessions))
	meanN := sumN / float64(len(vertSessions))

	var details []model.SessionDetail
	type bodyVec struct{ right, screenOut float64 }
	var vecs []bodyVec

	for _, sess := range vertSessions {
		relE := sess.DeltaE - meanE
		relN := sess.DeltaN - meanN
		r, sc, _ := enuToBody(relE, relN, 0, model.CalibPosVertical, sess.Orientation)
		vecs = append(vecs, bodyVec{r, sc})
		details = append(details, model.SessionDetail{
			Position: sess.Position, Orientation: sess.Orientation,
			DeltaE: relE, DeltaN: relN, DeltaU: 0, FixRate: sess.FixRate,
		})
	}

	var sumR, sumSO float64
	for _, v := range vecs {
		sumR += v.right
		sumSO += v.screenOut
	}
	n := float64(len(vecs))
	var ssR, ssSO float64
	for _, v := range vecs {
		ssR += (v.right - sumR/n) * (v.right - sumR/n)
		ssSO += (v.screenOut - sumSO/n) * (v.screenOut - sumSO/n)
	}

	return &model.CalibrationResult{
		OffsetLeft:  -(sumR / n),
		OffsetDepth: -(sumSO / n),
		OffsetDown:  math.NaN(), // не определяется без опоры
		SigmaLeft:   math.Sqrt(ssR / n),
		SigmaDepth:  math.Sqrt(ssSO / n),
		SigmaDown:   math.NaN(),
		Sessions:    details,
	}, nil
}

// enuToBody переводит вектор [dE, dN, dU] в географических осях
// в оси тела смартфона [right, screenOut, up].
//
// Для вертикального положения, камера (задняя панель) смотрит в направлении θ:
//
//	right_hat    = [ cos(θ), -sin(θ), 0 ]
//	screenOut_hat = [-sin(θ), -cos(θ), 0 ]   (экран — противоположно камере)
//	up_hat       = [       0,       0, 1 ]
//
// где θ — азимут камеры (N=0, E=π/2, S=π, W=3π/2).
//
// Для горизонтального положения (телефон лежит экраном вверх):
//
//	up_hat       = [  sin(θ), cos(θ), 0 ]  (верхняя грань указывает в сторону θ)
//	screenOut_hat = [       0,      0, 1 ]  (экран смотрит вверх)
//	right_hat    = up_hat × screenOut_hat
func enuToBody(dE, dN, dU float64, position, orientation string) (right, screenOut, up float64) {
	θ := azimuthRad(orientation)
	cosT, sinT := math.Cos(θ), math.Sin(θ)

	if position == model.CalibPosVertical {
		right = cosT*dE - sinT*dN
		screenOut = -sinT*dE - cosT*dN
		up = dU
	} else {
		// Горизонтальное: верхняя грань лежит в горизонтальной плоскости,
		// экран смотрит вертикально вверх (dU → screenOut).
		// Верхняя грань: up_body = [sin(θ), cos(θ), 0]
		// right_body = up_body × screenOut_body = [sin(θ), cos(θ), 0] × [0,0,1] = [cos(θ), -sin(θ), 0]
		right = cosT*dE - sinT*dN
		up = sinT*dE + cosT*dN
		screenOut = dU
	}
	return
}

// azimuthRad возвращает азимут камеры/верхней грани в радианах (N=0, по часовой).
func azimuthRad(orientation string) float64 {
	switch orientation {
	case model.CalibOrientNorth:
		return 0
	case model.CalibOrientEast:
		return math.Pi / 2
	case model.CalibOrientSouth:
		return math.Pi
	case model.CalibOrientWest:
		return 3 * math.Pi / 2
	default:
		return 0
	}
}

// waitAndGetPPPResult ждёт завершения PPP-задачи и возвращает координаты.
func (s *CalibrationService) waitAndGetPPPResult(ctx context.Context, pppTaskID string) (lat, lon, h float64, err error) {
	lat, lon, h, _, err = s.waitAndGetPPPResultFull(ctx, pppTaskID)
	return
}

func (s *CalibrationService) waitAndGetPPPResultFull(ctx context.Context, pppTaskID string) (lat, lon, h, fixRate float64, err error) {
	deadline := time.Now().Add(2 * time.Hour)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			err = ctx.Err()
			return
		default:
		}
		task, e := s.taskStorage.GetTaskByID(pppTaskID)
		if e != nil {
			err = e
			return
		}
		switch task.Status {
		case model.StatusCompleted:
			result, e := s.taskStorage.GetResultByTaskID(pppTaskID)
			if e != nil || result == nil {
				err = fmt.Errorf("no result for task %s", pppTaskID)
				return
			}
			lat, lon, h = result.Latitude, result.Longitude, result.Height
			fixRate = float64(result.FixRate)
			return
		case model.StatusFailed:
			err = fmt.Errorf("PPP task %s failed: %s", pppTaskID, task.ErrorMessage)
			return
		}
		time.Sleep(10 * time.Second)
	}
	err = fmt.Errorf("timeout waiting for PPP task %s", pppTaskID)
	return
}

func (s *CalibrationService) fail(taskID, msg string) {
	s.logger.Errorf("[calib:%s] failed: %s", taskID, msg)
	_ = s.taskStorage.UpdateCalibrationTaskStatus(taskID, "failed", msg, nil)
}
