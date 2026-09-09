package handlers

import (
	"math"
	"strconv"
	"strings"
)

// RTKLIB $SAT: GPST week,tow,satellite,frequency slot,azimuth,elevation,
// code/phase residual (m),valid,SNR(dBHz),fix,slip,lock,outage,slips,rejections.
// Frequency is a solver slot, not a RINEX signal identifier.
type SatellitePoint struct {
	T          float64  `json:"t"`
	ID         string   `json:"id"`
	Frequency  int      `json:"frequency"`
	Azimuth    float64  `json:"az"`
	Elevation  float64  `json:"el"`
	Code       *float64 `json:"code"`
	Phase      *float64 `json:"phase"`
	CNo        *float64 `json:"cno"`
	Valid      int      `json:"valid"`
	Fix        int      `json:"fix"`
	Slip       int      `json:"slip"`
	Lock       int      `json:"lock"`
	Outage     int      `json:"outage"`
	Slips      int      `json:"slips"`
	Rejections int      `json:"rejections"`
}

func parseSatellitePoints(raw string) []SatellitePoint {
	var out []SatellitePoint
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "% $SAT,") {
			continue
		}
		f := strings.Split(strings.TrimPrefix(line, "% "), ",")
		if len(f) != 17 {
			continue
		}
		id := f[3]
		if len(id) < 2 || len(id) > 4 || !strings.ContainsRune("GRECIJS", rune(id[0])) {
			continue
		}
		if _, e := strconv.Atoi(id[1:]); e != nil {
			continue
		}
		v := make([]float64, 17)
		valid := true
		for i := 1; i < 17; i++ {
			if i == 3 {
				continue
			}
			n, e := strconv.ParseFloat(f[i], 64)
			if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				valid = false
				break
			}
			v[i] = n
		}
		if !valid || v[1] < 0 || v[2] < 0 || v[2] >= 604800 || v[4] < 1 || v[4] > 9 || v[5] < 0 || v[5] > 360 || v[6] < 0 || v[6] > 90 {
			continue
		}
		for _, i := range []int{1, 4, 9, 11, 12, 13, 14, 15, 16} {
			if math.Trunc(v[i]) != v[i] {
				valid = false
			}
		}
		if !valid || v[9] < 0 || v[9] > 1 {
			continue
		}
		p := SatellitePoint{T: v[1]*604800 + v[2], ID: id, Frequency: int(v[4]), Azimuth: v[5], Elevation: v[6], Valid: int(v[9]), Fix: int(v[11]), Slip: int(v[12]), Lock: int(v[13]), Outage: int(v[14]), Slips: int(v[15]), Rejections: int(v[16])}
		if p.Valid == 1 {
			code, phase := v[7], v[8]
			p.Code = &code
			p.Phase = &phase
		}
		if v[10] > 0 {
			cno := v[10]
			p.CNo = &cno
		}
		out = append(out, p)
	}
	return out
}
