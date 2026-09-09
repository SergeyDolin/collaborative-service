package services

import (
	"bufio"
	"collaborative/internal/model"
	"encoding/json"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type orbitPoint struct {
	t   float64
	xyz [3]float64
}

func calendarSeconds(s string) (float64, bool) {
	f := strings.Fields(s)
	if len(f) < 6 {
		return 0, false
	}
	var v [6]float64
	for i := range v {
		n, e := strconv.ParseFloat(f[i], 64)
		if e != nil || !finiteCal(n) {
			return 0, false
		}
		v[i] = n
	}
	d := time.Date(int(v[0]), time.Month(v[1]), int(v[2]), int(v[3]), int(v[4]), int(v[5]), 0, time.UTC)
	if d.Year() != int(v[0]) || int(d.Month()) != int(v[1]) || d.Day() != int(v[2]) || v[3] < 0 || v[3] >= 24 || v[4] < 0 || v[4] >= 60 || v[5] < 0 || v[5] >= 60 {
		return 0, false
	}
	return float64(d.Unix()) + v[5] - math.Floor(v[5]), true
}
func readSkyOrbits(path string) map[string][]orbitPoint {
	out := map[string][]orbitPoint{}
	f, e := os.Open(path)
	if e != nil {
		return out
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	gps := false
	t := 0.0
	for scan.Scan() {
		line := scan.Text()
		if strings.HasPrefix(line, "%c ") && len(line) >= 12 && line[9:12] == "GPS" {
			gps = true
		}
		if strings.HasPrefix(line, "* ") {
			t, _ = calendarSeconds(line[1:])
			continue
		}
		if !gps || t == 0 || len(line) < 46 || line[0] != 'P' {
			continue
		}
		p := orbitPoint{t: t}
		valid := true
		for i := 0; i < 3; i++ {
			v, e := strconv.ParseFloat(strings.TrimSpace(line[4+14*i:18+14*i]), 64)
			if e != nil || !finiteCal(v) || math.Abs(v) >= 999999 {
				valid = false
				break
			}
			p.xyz[i] = v * 1000
		}
		if valid && p.xyz != [3]float64{} {
			out[line[1:4]] = append(out[line[1:4]], p)
		}
	}
	for id, ps := range out {
		sort.Slice(ps, func(i, j int) bool { return ps[i].t < ps[j].t })
		out[id] = ps
	}
	return out
}

// Local Lagrange interpolation for geometric sky angles only. Never extrapolate
// or bridge missing orbit epochs longer than 30 minutes.
func skyOrbit(ps []orbitPoint, t float64) ([3]float64, bool) {
	var xyz [3]float64
	k := sort.Search(len(ps), func(i int) bool { return ps[i].t >= t })
	if k < len(ps) && math.Abs(ps[k].t-t) < .001 {
		return ps[k].xyz, true
	}
	if k == 0 || k == len(ps) || len(ps) < 4 {
		return xyz, false
	}
	start := max(0, min(k-4, len(ps)-min(8, len(ps))))
	window := ps[start:min(start+8, len(ps))]
	for i, p := range window {
		if i > 0 && (p.t-window[i-1].t > 1800 || p.t <= window[i-1].t) {
			return xyz, false
		}
		w := 1.0
		for j, q := range window {
			if i != j {
				w *= (t - q.t) / (p.t - q.t)
			}
		}
		for j := range xyz {
			xyz[j] += w * p.xyz[j]
		}
	}
	return xyz, true
}
func satelliteReport(obs, sp3, nav string, raw []byte, static bool) string {
	report := model.SatelliteReport{Note: "C/N₀ — из RINEX; азимут и угол места — геометрические, из SP3 и координат решения. Это не признак использования спутника решателем."}
	f, e := os.Open(obs)
	if e != nil {
		return ""
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	types := map[byte][]string{}
	system := byte(' ')
	gps := false
	leap := -1
	if nf, err := os.Open(nav); err == nil {
		ns := bufio.NewScanner(nf)
		for ns.Scan() {
			l := ns.Text()
			if len(l) < 60 {
				continue
			}
			if strings.TrimSpace(l[60:]) == "LEAP SECONDS" {
				if n, e := strconv.Atoi(strings.TrimSpace(l[:6])); e == nil && n >= 0 && n <= 100 {
					leap = n
				}
			}
			if strings.TrimSpace(l[60:]) == "END OF HEADER" {
				break
			}
		}
		nf.Close()
	}
	header := true
	orbits := readSkyOrbits(sp3)
	// Positions are matched to observations by epoch, including moving receivers.
	positions := map[int64][3]float64{}
	type llhPoint struct {
		t   float64
		llh [3]float64
	}
	var receivers []llhPoint
	t := 0.0
	keep := false
	next := math.Inf(-1)
	seen := map[string]bool{}
	for scan.Scan() {
		line := scan.Text()
		if header {
			if len(line) < 60 {
				continue
			}
			label := strings.TrimSpace(line[60:])
			if label == "SYS / # / OBS TYPES" {
				if line[0] != ' ' {
					system = line[0]
				}
				types[system] = append(types[system], strings.Fields(line[7:60])...)
			}
			if label == "TIME OF FIRST OBS" {
				gps = strings.TrimSpace(line[48:51]) == "GPS"
			}
			if label == "LEAP SECONDS" {
				if n, e := strconv.Atoi(strings.TrimSpace(line[:6])); e == nil && n >= 0 && n <= 100 {
					leap = n
				}
			}
			if label != "END OF HEADER" {
				continue
			}
			header = false
			if !gps {
				report.Note = "Для спутниковых графиков нужен RINEX 3 с явно указанной шкалой GPS."
				break
			}
			shift := math.NaN()
			for _, l := range strings.Split(string(raw), "\n") {
				if strings.HasPrefix(l, "%") && strings.Contains(l, "latitude") {
					if strings.Contains(l, "GPST") {
						shift = 0
					} else if strings.Contains(l, "UTC") && leap >= 0 {
						shift = float64(leap)
					}
				}
			}
			if !math.IsNaN(shift) {
				for _, l := range strings.Split(string(raw), "\n") {
					fs := strings.Fields(l)
					if len(fs) < 7 || strings.HasPrefix(fs[0], "%") {
						continue
					}
					d, e := time.Parse("2006/01/02 15:04:05", fs[0]+" "+fs[1])
					if e != nil {
						continue
					}
					p := llhPoint{t: float64(d.UnixNano())/1e9 + shift}
					ok := true
					for i := 0; i < 3; i++ {
						v, e := strconv.ParseFloat(fs[i+2], 64)
						if e != nil || !finiteCal(v) {
							ok = false
						}
						p.llh[i] = v
					}
					if ok && math.Abs(p.llh[0]) <= 90 && math.Abs(p.llh[1]) <= 180 {
						receivers = append(receivers, p)
					}
				}
			}
			for _, p := range receivers {
				positions[int64(math.Round(p.t*1000))] = p.llh
			}
			continue
		}
		if strings.HasPrefix(line, ">") {
			fs := strings.Fields(line[1:])
			keep = false
			if len(fs) < 8 || fs[6] != "0" {
				continue
			}
			var ok bool
			t, ok = calendarSeconds(line[1:])
			if !ok || t < next {
				continue
			}
			next = t + 60
			keep = true
			continue
		}
		if !keep || len(line) < 3 {
			continue
		}
		id := line[:3]
		if !strings.ContainsRune("GRECIJS", rune(id[0])) {
			continue
		}
		if _, e := strconv.Atoi(id[1:]); e != nil {
			continue
		}
		var az, el *float64
		pos, ok := positions[int64(math.Round(t*1000))]
		if !ok && static && len(receivers) > 0 {
			pos = receivers[len(receivers)-1].llh
			ok = true
		}
		if ok {
			if xyz, ok := skyOrbit(orbits[id], t); ok {
				enu := xyzENU(xyz, geoXYZ(pos[0], pos[1], pos[2]), pos[0], pos[1])
				a := math.Mod(math.Atan2(enu[0], enu[1])*180/math.Pi+360, 360)
				h := math.Atan2(enu[2], math.Hypot(enu[0], enu[1])) * 180 / math.Pi
				az = &a
				el = &h
			}
		}
		added := false
		for i, code := range types[id[0]] {
			if len(code) != 3 || code[0] != 'S' {
				continue
			}
			start := 3 + 16*i
			if len(line) < start+14 {
				continue
			}
			v, e := strconv.ParseFloat(strings.TrimSpace(line[start:start+14]), 64)
			if e != nil || !finiteCal(v) || v <= 0 {
				continue
			}
			key := id + code + strconv.FormatFloat(t, 'f', 3, 64)
			if seen[key] {
				continue
			}
			seen[key] = true
			report.Rows = append(report.Rows, model.SatelliteObservation{T: t, ID: id, Signal: code, CNo: &v, Az: az, El: el})
			added = true
		}
		if !added {
			report.Rows = append(report.Rows, model.SatelliteObservation{T: t, ID: id, Signal: "", Az: az, El: el})
		}
		if len(report.Rows) >= 100000 {
			report.Truncated = true
			break
		}
	}
	if scan.Err() != nil {
		report.Truncated = true
	}
	if len(positions) == 0 || len(orbits) == 0 {
		report.Note += " Небесная карта недоступна без SP3 и согласованных по времени координат решения (для UTC нужна строка LEAP SECONDS в RINEX наблюдений или навигации)."
	}
	if static {
		report.Note += " Для статической установки при отсутствии решения на эпоху используется последняя координата статического решения."
	}
	report.Note += " Для графиков взято не более одной эпохи в минуту; значения C/N₀ не усреднялись."
	if len(report.Rows) == 0 && report.Note == "" {
		return ""
	}
	b, e := json.Marshal(report)
	if e != nil {
		return ""
	}
	return "\n% SATELLITE_REPORT " + string(b) + "\n"
}
