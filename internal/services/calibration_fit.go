package services

import (
	"collaborative/internal/model"
	"fmt"
	"math"
)

func calPtr(v float64) *float64 { return &v }

// Equal-weight least squares of session means. Formal standard errors describe
// internal fit only; reference, setup and temporal correlation are not included.
func computeCalibration(t *model.CalibrationTask) (*model.CalibrationResult, error) {
	if err := ValidateCalibration(t); err != nil {
		return nil, err
	}
	noRef := t.RefType == model.CalibRefNone
	dimensions, parameters := 3, 3
	if noRef {
		dimensions, parameters = 2, 4
	}
	design := func(s model.CalibrationSession, axis int) []float64 {
		unit := [3]float64{}
		unit[axis] = 1
		r, sc, up := enuToBody(unit[0], unit[1], unit[2], s.Position, s.Orientation)
		if noRef {
			row := []float64{0, 0, -r, -sc}
			row[axis] = 1
			return row
		}
		return []float64{-r, -sc, -up}
	}
	var rows [][]float64
	var values []float64
	result := &model.CalibrationResult{Method: "relative-static", Scope: "mean-offset", UncertaintyKind: "internal-fit-standard-error", Sessions: []model.SessionDetail{}}
	if t.Mode == model.CalibModeQuick {
		result.Scope = "single-setup-offset"
	}
	for _, s := range t.Sessions {
		if s.Status != "completed" {
			return nil, fmt.Errorf("не все сеансы успешно обработаны")
		}
		result.Sessions = append(result.Sessions, model.SessionDetail{Position: s.Position, Orientation: s.Orientation, DeltaE: s.DeltaE, DeltaN: s.DeltaN, DeltaU: s.DeltaU, FixRate: s.FixRate, Control: s.Geometry.Control, FixedEpochs: s.Geometry.FixedEpochs})
		if s.Geometry.Control {
			continue
		}
		result.TrainingSessions++
		v := [3]float64{s.DeltaE, s.DeltaN, s.DeltaU}
		for axis := 0; axis < dimensions; axis++ {
			rows = append(rows, design(s, axis))
			values = append(values, v[axis])
		}
	}
	beta, inverse, err := calLeastSquares(rows, values, parameters)
	if err != nil {
		return nil, err
	}
	first := 0
	if noRef {
		first = 2
	}
	result.OffsetLeft = beta[first]
	result.OffsetDepth = beta[first+1]
	if !noRef {
		result.OffsetDown = calPtr(beta[2])
	}
	if len(rows) > parameters {
		var ss float64
		for i, row := range rows {
			pred := 0.
			for j, a := range row {
				pred += a * beta[j]
			}
			ss += math.Pow(values[i]-pred, 2)
		}
		variance := ss / float64(len(rows)-parameters)
		result.SigmaLeft = calPtr(math.Sqrt(math.Max(0, variance*inverse[first][first])))
		result.SigmaDepth = calPtr(math.Sqrt(math.Max(0, variance*inverse[first+1][first+1])))
		if !noRef {
			result.SigmaDown = calPtr(math.Sqrt(math.Max(0, variance*inverse[2][2])))
		}
	}
	check := &model.CalibrationValidation{}
	before, after := [3]float64{}, [3]float64{}
	for _, s := range t.Sessions {
		if !s.Geometry.Control {
			continue
		}
		check.Sessions++
		v := [3]float64{s.DeltaE, s.DeltaN, s.DeltaU}
		for axis := 0; axis < dimensions; axis++ {
			row := design(s, axis)
			pred := 0.
			for j, a := range row {
				pred += a * beta[j]
			}
			baseline := 0.
			if noRef {
				baseline = beta[axis]
			}
			before[axis] += math.Pow(v[axis]-baseline, 2)
			after[axis] += math.Pow(v[axis]-pred, 2)
		}
	}
	for axis := 0; axis < dimensions; axis++ {
		check.Before[axis] = calPtr(math.Sqrt(before[axis] / float64(check.Sessions)))
		check.After[axis] = calPtr(math.Sqrt(after[axis] / float64(check.Sessions)))
	}
	result.Validation = check
	return result, nil
}
func calLeastSquares(a [][]float64, y []float64, n int) ([]float64, [][]float64, error) {
	if len(a) != len(y) || len(a) < n {
		return nil, nil, fmt.Errorf("недостаточно независимых наблюдений")
	}
	normal := make([][]float64, n)
	for i := range normal {
		normal[i] = make([]float64, 2*n+1)
		normal[i][n+i] = 1
	}
	for k, row := range a {
		if len(row) != n || !finiteCal(y[k]) {
			return nil, nil, fmt.Errorf("неверные наблюдения")
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				normal[i][j] += row[i] * row[j]
			}
			normal[i][2*n] += row[i] * y[k]
		}
	}
	for i := 0; i < n; i++ {
		pivot := i
		for j := i + 1; j < n; j++ {
			if math.Abs(normal[j][i]) > math.Abs(normal[pivot][i]) {
				pivot = j
			}
		}
		if math.Abs(normal[pivot][i]) < 1e-10 {
			return nil, nil, fmt.Errorf("геометрия не позволяет определить параметры")
		}
		normal[i], normal[pivot] = normal[pivot], normal[i]
		d := normal[i][i]
		for j := range normal[i] {
			normal[i][j] /= d
		}
		for k := 0; k < n; k++ {
			if k == i {
				continue
			}
			f := normal[k][i]
			for j := range normal[k] {
				normal[k][j] -= f * normal[i][j]
			}
		}
	}
	beta := make([]float64, n)
	inv := make([][]float64, n)
	for i := range beta {
		beta[i] = normal[i][2*n]
		inv[i] = append([]float64(nil), normal[i][n:2*n]...)
	}
	return beta, inv, nil
}
