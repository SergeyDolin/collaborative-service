package services

import (
	"bufio"
	"bytes"
	"collaborative/internal/model"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type calibrationMean struct {
	Mean         [3]float64
	Total, Fixed int
	First, Last  string
	Start, End   time.Time // GPST calendar values, not UTC instants.
}

func fixedCalibrationMean(raw []byte, start, end time.Time) (calibrationMean, error) {
	var out calibrationMean
	scan := bufio.NewScanner(bytes.NewReader(raw))
	var previous time.Time
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 7 {
			return out, fmt.Errorf("неверная строка решения")
		}
		tm, e := time.Parse("2006/01/02 15:04:05", f[0]+" "+f[1])
		if e != nil {
			return out, fmt.Errorf("неверная метка времени результата")
		}
		if tm.Before(start.Add(-time.Millisecond)) || tm.After(end.Add(time.Millisecond)) {
			return out, fmt.Errorf("решение вне совместного интервала наблюдений")
		}
		if !previous.IsZero() && !tm.After(previous) {
			return out, fmt.Errorf("повторные или обратные эпохи решения")
		}
		previous = tm
		var v [3]float64
		for i := range v {
			v[i], e = strconv.ParseFloat(f[i+2], 64)
			if e != nil || !finiteCal(v[i]) {
				return out, fmt.Errorf("неверные координаты решения")
			}
		}
		if v[0] < -90 || v[0] > 90 || v[1] < -180 || v[1] > 180 {
			return out, fmt.Errorf("неверные B/L")
		}
		q, e := strconv.Atoi(f[5])
		if e != nil {
			return out, e
		}
		out.Total++
		if q != 1 {
			continue
		}
		out.Fixed++
		xyz := geoXYZ(v[0], v[1], v[2])
		for i := range xyz {
			out.Mean[i] += (xyz[i] - out.Mean[i]) / float64(out.Fixed)
		}
		if out.First == "" {
			out.First = tm.Format("2006-01-02 15:04:05.000")
		}
		out.Last = tm.Format("2006-01-02 15:04:05.000")
	}
	if err := scan.Err(); err != nil {
		return out, err
	}
	if out.Fixed < 2 {
		return out, fmt.Errorf("недостаточно FIX-эпох: %d; нужны как минимум две разные эпохи", out.Fixed)
	}
	out.Start, out.End = start, end
	return out, nil
}

// Converted observation file must be RINEX 3. Epoch span comes from records,
// never from APPROX POSITION or a guessed current date.
func calibrationSpan(path string) (time.Time, time.Time, error) {
	f, e := os.Open(path)
	if e != nil {
		return time.Time{}, time.Time{}, e
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	var first, last time.Time
	header, phase := true, false
	timeSystem := ""
	system := byte(' ')
	for scan.Scan() {
		line := scan.Text()
		if header {
			if len(line) < 60 {
				continue
			}
			label := strings.TrimSpace(line[60:])
			if label == "TIME OF FIRST OBS" {
				timeSystem = strings.TrimSpace(line[48:51])
			}
			if label == "SYS / # / OBS TYPES" {
				if line[0] != ' ' {
					system = line[0]
				}
				if system == 'G' || system == 'E' {
					for _, code := range strings.Fields(line[7:60]) {
						if len(code) == 3 && code[0] == 'L' && code[1] == '1' {
							phase = true
						}
					}
				}
			}
			if strings.Contains(line, "END OF HEADER") {
				header = false
			}
			continue
		}
		if !strings.HasPrefix(line, ">") {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(line, ">"))
		if len(fields) < 8 {
			continue
		}
		if fields[6] != "0" && fields[6] != "1" {
			continue
		}
		v := make([]float64, 6)
		valid := true
		for i := range v {
			v[i], e = strconv.ParseFloat(fields[i], 64)
			if e != nil || !finiteCal(v[i]) {
				valid = false
			}
		}
		if !valid {
			return first, last, fmt.Errorf("неверная эпоха RINEX")
		}
		tm := time.Date(int(v[0]), time.Month(v[1]), int(v[2]), int(v[3]), int(v[4]), int(v[5]), int((v[5]-float64(int(v[5])))*1e9), time.UTC)
		if tm.Year() != int(v[0]) || int(tm.Month()) != int(v[1]) || tm.Day() != int(v[2]) || tm.Hour() != int(v[3]) || tm.Minute() != int(v[4]) || v[5] < 0 || v[5] >= 60 {
			return first, last, fmt.Errorf("неверная календарная дата RINEX")
		}
		if !last.IsZero() && !tm.After(last) {
			return first, last, fmt.Errorf("эпохи RINEX должны возрастать")
		}
		if first.IsZero() {
			first = tm
		}
		last = tm
	}
	if e = scan.Err(); e != nil {
		return first, last, e
	}
	if timeSystem != "GPS" {
		return first, last, fmt.Errorf("в TIME OF FIRST OBS должна быть явно указана шкала GPS")
	}
	if header || !phase || first.IsZero() || !last.After(first) {
		return first, last, fmt.Errorf("нужен RINEX 3 с фазовыми наблюдениями L1/E1 и несколькими эпохами")
	}
	if last.Sub(first) > 48*time.Hour {
		return first, last, fmt.Errorf("сеанс длиннее 48 часов")
	}
	return first, last, nil
}

func (s *MeasurementService) processCalibrationSession(ctx context.Context, t *model.CalibrationTask, session *model.CalibrationSession, rover []byte, roverName string, base []byte, baseName string) (calibrationMean, error) {
	var empty calibrationMean
	id := session.ID
	dir := filepath.Join(s.workDir, id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return empty, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, ".calibration"), nil, 0600); err != nil {
		return empty, err
	}
	prepare := func(kind, name string, data []byte) (string, error) {
		d := filepath.Join(dir, kind)
		if err := os.MkdirAll(d, 0700); err != nil {
			return "", err
		}
		p := filepath.Join(d, filepath.Base(name))
		if err := os.WriteFile(p, data, 0600); err != nil {
			return "", err
		}
		// The endpoint accepts plain RINEX 3, so conversion and filename logging
		// are unnecessary here.
		return p, nil
	}
	rp, err := prepare("rover", roverName, rover)
	if err != nil {
		return empty, err
	}
	bp, err := prepare("base", baseName, base)
	if err != nil {
		return empty, err
	}
	start, end, err := calibrationSpan(rp)
	if err != nil {
		return empty, err
	}
	bs, be, err := calibrationSpan(bp)
	if err != nil {
		return empty, fmt.Errorf("база: %w", err)
	}
	if bs.After(start) {
		start = bs
	}
	if be.Before(end) {
		end = be
	}
	if !end.After(start) {
		return empty, fmt.Errorf("нет совместного интервала базы и смартфона")
	}
	ant, err := calibrationBaseAntenna(base)
	if err != nil {
		return empty, err
	}
	atx := absFilePath(filepath.Join(s.configGen.templateDir, "..", "src", "igs20.atx"))
	atxData, err := os.ReadFile(atx)
	if err != nil {
		return empty, fmt.Errorf("недоступен ANTEX опорной антенны")
	}
	if !calibrationAntennaPresent(atxData, ant.Type) {
		return empty, fmt.Errorf("тип антенны базы из RINEX не найден в ANTEX")
	}
	if strings.ContainsAny(ant.Type, "\r\n=#") {
		return empty, fmt.Errorf("неверный тип антенны базы")
	}
	navs := []string{}
	for day := start.Truncate(24 * time.Hour); !day.After(end); day = day.Add(24 * time.Hour) {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		nav, e := s.downloader.DownloadBroadcastEphemeris(day, id)
		if e != nil || nav == "" {
			return empty, fmt.Errorf("эфемериды за %s недоступны", day.Format("2006-01-02"))
		}
		navs = append(navs, absPath(nav))
	}
	config := calibrationSolverConfig(t.Options, ant, atx)
	configPath := filepath.Join(dir, "calibration.conf")
	if err = os.WriteFile(configPath, []byte(config), 0600); err != nil {
		return empty, err
	}
	output := absPath(filepath.Join(dir, "calibration.pos"))
	args := []string{"-k", absPath(configPath), "-o", output, "-ts", start.Format("2006/01/02"), start.Format("15:04:05.000"), "-te", end.Format("2006/01/02"), end.Format("15:04:05.000"), absPath(rp), absPath(bp)}
	args = append(args, navs...)
	binary := s.rtk.solverBinary("mobile")
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = filepath.Dir(binary)
	if err = cmd.Run(); err != nil {
		return empty, fmt.Errorf("относительная обработка не выполнена: %w", err)
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		return empty, fmt.Errorf("нет файла решения")
	}
	mean, err := fixedCalibrationMean(raw, start, end)
	if err != nil {
		return empty, err
	}
	bxyz := geoXYZ(t.Options.BaseLat, t.Options.BaseLon, t.Options.BaseH)
	distance := math.Sqrt(math.Pow(mean.Mean[0]-bxyz[0], 2) + math.Pow(mean.Mean[1]-bxyz[1], 2) + math.Pow(mean.Mean[2]-bxyz[2], 2))
	if distance > 1000 {
		return empty, fmt.Errorf("длина базы превышает 1 км; этот режим рассчитан на короткую базу")
	}
	return mean, nil
}
func calibrationSolverConfig(o model.CalibrationOptions, ant AntennaInfo, atx string) string {
	return fmt.Sprintf(`pos1-posmode = static
pos1-frequency = %s
pos1-soltype = forward
pos1-elmask = 10
pos1-ionoopt = off
pos1-tropopt = saas
pos1-sateph = brdc
pos1-navsys = 9
pos1-posopt1 = on
pos1-posopt2 = on
pos2-armode = continuous
pos2-arthres = 3
out-solformat = llh
out-outhead = on
out-outopt = on
out-timesys = gpst
out-timeform = hms
out-timendec = 3
out-degform = deg
out-height = ellipsoidal
out-solstatic = all
ant1-anttype =
ant1-antdele = 0
ant1-antdeln = 0
ant1-antdelu = 0
ant2-postype = llh
ant2-pos1 = %.10f
ant2-pos2 = %.10f
ant2-pos3 = %.4f
ant2-anttype = %s
ant2-antdele = %.6f
ant2-antdeln = %.6f
ant2-antdelu = %.6f
file-satantfile = %s
file-rcvantfile = %s
`, o.Frequency, o.BaseLat, o.BaseLon, o.BaseH, ant.Type, ant.DeltaE, ant.DeltaN, ant.DeltaH, atx, atx)
}

// Require an explicit reference-antenna type and measured marker-to-ARP vector.
// Do not send antenna headers or geometry to application logs.
func calibrationBaseAntenna(raw []byte) (AntennaInfo, error) {
	var ant AntennaInfo
	found := false
	scan := bufio.NewScanner(bytes.NewReader(raw))
	for scan.Scan() {
		line := scan.Text()
		if len(line) < 60 {
			continue
		}
		switch strings.TrimSpace(line[60:]) {
		case "ANT # / TYPE":
			ant.Type = strings.TrimSpace(line[20:40])
		case "ANTENNA: DELTA H/E/N":
			fields := strings.Fields(line[:60])
			if len(fields) != 3 {
				return ant, fmt.Errorf("неверный вектор H/E/N антенны базы")
			}
			values := [3]float64{}
			for i := range values {
				v, e := strconv.ParseFloat(fields[i], 64)
				if e != nil || !finiteCal(v) || math.Abs(v) > 100 {
					return ant, fmt.Errorf("неверный вектор H/E/N антенны базы")
				}
				values[i] = v
			}
			ant.DeltaH, ant.DeltaE, ant.DeltaN = values[0], values[1], values[2]
			found = true
		case "END OF HEADER":
			if !found || ant.Type == "" || ant.Type == "NONE" || strings.ContainsAny(ant.Type, "\r\n=#") {
				return ant, fmt.Errorf("в RINEX базы нужны ANT # / TYPE и ANTENNA: DELTA H/E/N")
			}
			return ant, nil
		}
	}
	return ant, fmt.Errorf("неполный заголовок антенны базы")
}

func calibrationAntennaPresent(raw []byte, name string) bool {
	active := false
	frequencies := map[string]bool{}
	scan := bufio.NewScanner(bytes.NewReader(raw))
	for scan.Scan() {
		line := scan.Text()
		if len(line) < 60 {
			continue
		}
		switch strings.TrimSpace(line[60:]) {
		case "TYPE / SERIAL NO":
			active = strings.TrimSpace(line[:20]) == name
			frequencies = map[string]bool{}
		case "START OF FREQUENCY":
			if active {
				frequencies[strings.TrimSpace(line[:10])] = true
			}
		case "END OF ANTENNA":
			if active && frequencies["G01"] && frequencies["E01"] {
				return true
			}
			active = false
		}
	}
	return false
}
