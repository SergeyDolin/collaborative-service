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
		tm, e := calibrationSolutionTime(f[0], f[1])
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
		if q != 1 && q != 2 {
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
	if out.Fixed < 1 {
		return out, fmt.Errorf("нет годных эпох относительного решения")
	}
	out.Start, out.End = start, end
	return out, nil
}

func calibrationSolutionTime(a, b string) (time.Time, error) {
	if tm, err := time.Parse("2006/01/02 15:04:05", a+" "+b); err == nil {
		return tm, nil
	}
	week, err := strconv.Atoi(a)
	if err != nil {
		return time.Time{}, err
	}
	tow, err := strconv.ParseFloat(b, 64)
	if err != nil || !finiteCal(tow) || tow < 0 {
		return time.Time{}, fmt.Errorf("invalid GPST tow")
	}
	sec, frac := math.Modf(tow)
	return time.Date(1980, 1, 6, 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, week*7).
		Add(time.Duration(sec)*time.Second + time.Duration(frac*1e9)*time.Nanosecond), nil
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
						if calibrationIsL1Phase(code) {
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

func calibrationIsL1Phase(code string) bool {
	return len(code) >= 2 && code[0] == 'L' && code[1] == '1'
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
	roverStart, roverEnd := start, end
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
		return empty, fmt.Errorf("нет совместного интервала базы и смартфона: смартфон %s - %s GPS, база %s - %s GPS",
			roverStart.Format("2006-01-02 15:04:05.000"),
			roverEnd.Format("2006-01-02 15:04:05.000"),
			bs.Format("2006-01-02 15:04:05.000"),
			be.Format("2006-01-02 15:04:05.000"))
	}
	ant, err := calibrationBaseAntenna(base)
	if err != nil {
		return empty, err
	}
	atx := absFilePath(filepath.Join(s.configGen.templateDir, "..", "src", "igs20.atx"))
	atxData, err := os.ReadFile(atx)
	if err != nil {
		s.logger.Warnw("ANTEX not available; continuing without antenna PCV", "path", atx, "error", err)
		ant.Type = ""
		atx = ""
	} else if err := calibrationAntennaAvailable(atxData, ant.Type); err != nil {
		s.logger.Warnw("base antenna calibration not found; continuing without antenna PCV", "antenna", ant.Type, "error", err)
		ant.Type = ""
		atx = ""
	}
	if strings.ContainsAny(ant.Type, "\r\n=#") {
		return empty, fmt.Errorf("неверный тип антенны базы")
	}
	products := []string{}
	for day := start.Truncate(24 * time.Hour); !day.After(end); day = day.Add(24 * time.Hour) {
		if err := ctx.Err(); err != nil {
			return empty, err
		}
		nav, e := s.downloader.DownloadBroadcastEphemeris(day, id)
		if e != nil || nav == "" {
			return empty, fmt.Errorf("эфемериды за %s недоступны", day.Format("2006-01-02"))
		}
		products = append(products, absPath(nav))
		sp3, sp3Err := s.downloader.DownloadPreciseEphemeris(day, id)
		clk, clkErr := s.downloader.DownloadPreciseClock(day, id)
		if sp3Err != nil || clkErr != nil || sp3 == "" || clk == "" {
			return empty, fmt.Errorf("точные эфемериды/часы за %s недоступны для sppostls static LS", day.Format("2006-01-02"))
		}
		products = append(products, absPath(sp3), absPath(clk))
	}
	config := calibrationSolverConfig(t.Options, ant, atx)
	configPath := filepath.Join(dir, "calibration.conf")
	if err = os.WriteFile(configPath, []byte(config), 0600); err != nil {
		return empty, err
	}
	output := absPath(filepath.Join(dir, "calibration.pos"))
	binary := s.rtk.calibrationSolverBinary()
	args := calibrationSolverArgs(binary, configPath, output, start, end, rp, bp, products)
	s.logger.Infow("running calibration solver", "binary", binary, "args", strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = filepath.Dir(binary)
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := calibrationCommandOutput(out)
		if detail != "" {
			return empty, fmt.Errorf("относительная обработка не выполнена: %w: %s", err, detail)
		}
		return empty, fmt.Errorf("относительная обработка не выполнена: %w", err)
	}
	raw, err := calibrationReadSolution(output, dir)
	if err != nil {
		detail := calibrationCommandOutput(out)
		if detail != "" {
			return empty, fmt.Errorf("нет файла решения: %s", detail)
		}
		return empty, fmt.Errorf("нет файла решения")
	}
	mean, err := fixedCalibrationMean(raw, start, end)
	if err != nil {
		return empty, err
	}
	bxyz := geoXYZ(t.Options.BaseLat, t.Options.BaseLon, t.Options.BaseH)
	distance := math.Sqrt(math.Pow(mean.Mean[0]-bxyz[0], 2) + math.Pow(mean.Mean[1]-bxyz[1], 2) + math.Pow(mean.Mean[2]-bxyz[2], 2))
	if distance > 1000 {
		warning := fmt.Sprintf("Длина базы %.1f км превышает 1 км; результат рассчитан, но точность может быть хуже короткобазового режима", distance/1000)
		session.Geometry.Warning = warning
		s.logger.Warnw("calibration baseline exceeds short-baseline recommendation", "task", t.ID, "session", id, "baseline_km", distance/1000)
	}
	return mean, nil
}

func calibrationReadSolution(output, dir string) ([]byte, error) {
	if raw, err := os.ReadFile(output); err == nil {
		return raw, nil
	}
	var fallback string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Clean(path) == filepath.Clean(output) {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".pos") {
			fallback = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if fallback == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(fallback)
}

func calibrationSolverArgs(binary, configPath, output string, start, end time.Time, rover, base string, products []string) []string {
	args := []string{"-k", absPath(configPath), "-o", output}
	if strings.EqualFold(filepath.Base(binary), binSppostls) {
		args = append(args, absPath(rover), absPath(base))
		return append(args, products...)
	}
	args = append(args,
		"-ts", start.Format("2006/01/02"), start.Format("15:04:05.000"),
		"-te", end.Format("2006/01/02"), end.Format("15:04:05.000"),
		absPath(rover), absPath(base),
	)
	return append(args, products...)
}

func calibrationCommandOutput(out []byte) string {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	const limit = 600
	if len(text) <= limit {
		return text
	}
	return "..." + text[len(text)-limit:]
}

func calibrationSolverConfig(o model.CalibrationOptions, ant AntennaInfo, atx string) string {
	return fmt.Sprintf(`pos1-posmode       =static
pos1-frequency     =%s
pos1-soltype       =forward
pos1-elmask        =25
pos1-snrmask_r     =off
pos1-snrmask_b     =off
pos1-ionoopt       =brdc
pos1-tropopt       =saas
pos1-sateph        =precise
pos1-posopt1       =off
pos1-posopt2       =off
pos1-posopt3       =on
pos1-posopt4       =on
pos1-posopt5       =on
pos1-posopt6       =on
pos1-exclsats      =
pos1-navsys        =125

pos2-armode        =instantaneous
pos2-gloarmode     =off
pos2-gpsarmode     =off
pos2-bdsarmode     =off
pos2-arfilter      =off
pos2-maxage        =30
pos2-syncsol       =off
pos2-slipthres     =0.05
pos2-rejionno      =30
pos2-rejgdop       =30
pos2-niter         =1
pos2-baselen       =0
pos2-basesig       =0

out-solformat      =llh
out-outhead        =on
out-outopt         =off
out-outvel         =off
out-timesys        =gpst
out-timeform       =tow
out-timendec       =3
out-degform        =deg
out-fieldsep       =
out-height         =ellipsoidal
out-solstatic      =single
out-outstat        =off

stats-eratio1      =300
stats-eratio2      =300
stats-eratio3      =300
stats-eratio4      =300
stats-eratio5      =300
stats-errphase     =0.006
stats-errphaseel   =0.006
stats-errphasebl   =0
stats-errdoppler   =1
stats-stdbias      =30
stats-stdiono      =0.03
stats-stdtrop      =0.3
stats-prnbias      =0.0001
stats-prniono      =0.001
stats-prntrop      =0.0001
stats-prnpos       =0
stats-clkstab      =5e-12

ant1-postype       =single
ant1-anttype       =*
ant1-antdele       =0
ant1-antdeln       =0
ant1-antdelu       =0

ant2-postype       =llh
ant2-pos1          =%.10f
ant2-pos2          =%.10f
ant2-pos3          =%.4f
ant2-anttype       =*
ant2-antdele       =%.6f
ant2-antdeln       =%.6f
ant2-antdelu       =%.6f
ant2-maxaveep      =0
ant2-initrst       =off

misc-timeinterp    =off
misc-rnxopt1       =
misc-rnxopt2       =
misc-pppopt        =

file-satantfile    =%s
file-rcvantfile    =%s
file-staposfile    =
file-geoidfile     =
file-ionofile      =
file-dcbfile       =
file-eopfile       =
file-blqfile       =
file-tempdir       =
file-geexefile     =
file-solstatfile   =
file-tracefile     =
file-gpt3file      =
	`, o.Frequency, o.BaseLat, o.BaseLon, o.BaseH, ant.DeltaE, ant.DeltaN, ant.DeltaH, atx, atx)
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

func calibrationAntennaAvailable(raw []byte, name string) error {
	active := false
	frequencies := map[string]bool{}
	similar := []string{}
	scan := bufio.NewScanner(bytes.NewReader(raw))
	for scan.Scan() {
		line := scan.Text()
		if len(line) < 60 {
			continue
		}
		switch strings.TrimSpace(line[60:]) {
		case "TYPE / SERIAL NO":
			antenna := strings.TrimSpace(line[:20])
			active = antenna == name
			frequencies = map[string]bool{}
			if !active && calibrationSimilarAntenna(name, antenna) && len(similar) < 5 {
				similar = append(similar, antenna)
			}
		case "START OF FREQUENCY":
			if active {
				frequencies[strings.TrimSpace(line[:10])] = true
			}
		case "END OF ANTENNA":
			if active && frequencies["G01"] && frequencies["E01"] {
				return nil
			}
			if active {
				return fmt.Errorf("в ANTEX для антенны базы %q нет калибровок частот G01/E01; есть %s", name, calibrationFrequencyList(frequencies))
			}
			active = false
		}
	}
	if len(similar) > 0 {
		return fmt.Errorf("тип антенны базы %q из RINEX не найден в ANTEX; похожие записи: %s", name, strings.Join(similar, ", "))
	}
	return fmt.Errorf("тип антенны базы %q из RINEX не найден в ANTEX", name)
}

func calibrationSimilarAntenna(want, got string) bool {
	wantFields, gotFields := strings.Fields(want), strings.Fields(got)
	if len(wantFields) == 0 || len(gotFields) == 0 {
		return false
	}
	if wantFields[0] == gotFields[0] {
		return true
	}
	if len(wantFields[0]) != len(gotFields[0]) {
		return false
	}
	diff := 0
	for i := range wantFields[0] {
		if wantFields[0][i] != gotFields[0][i] {
			diff++
		}
	}
	return diff <= 2
}

func calibrationFrequencyList(frequencies map[string]bool) string {
	if len(frequencies) == 0 {
		return "нет частот"
	}
	order := []string{"G01", "E01", "G02", "E05", "G05", "R01", "R02"}
	out := []string{}
	for _, f := range order {
		if frequencies[f] {
			out = append(out, f)
			delete(frequencies, f)
		}
	}
	for f := range frequencies {
		out = append(out, f)
	}
	return strings.Join(out, ", ")
}
