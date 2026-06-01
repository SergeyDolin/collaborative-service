// cmd/igsbatch/main.go — batch IGS PPP-AR static processing vs ITRF2020-u2024
package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"flag"
	"fmt"
	"html/template"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"collaborative/internal/services"

	"go.uber.org/zap"
)

// ── paths hardcoded to service layout ────────────────────────────────────────

const (
	serviceRoot = "/Users/sergeidolin/collaborative-service"
	rtklibPath  = serviceRoot + "/cmd/solver/app"
	atxFile     = serviceRoot + "/cmd/solver/src/igs20.atx"
	pppConfFile = serviceRoot + "/cmd/solver/configs/ppp.conf"
	itrf2020URL = "https://itrf.ign.fr/ftp/pub/itrf/itrf2020-u2024/ITRF2020-u2024_GNSS.SSC.txt"
)

// ── data types ────────────────────────────────────────────────────────────────

type ITRFStation struct {
	Code        string
	X, Y, Z    float64
	Vx, Vy, Vz float64
	T0          float64   // reference epoch (2020.0)
	DataStart   time.Time // zero = beginning of time
	DataEnd     time.Time // zero = currently active
}

func (s ITRFStation) PosAt(date time.Time) (x, y, z float64) {
	dt := toDecimalYear(date) - s.T0
	return s.X + s.Vx*dt, s.Y + s.Vy*dt, s.Z + s.Vz*dt
}

type DailyProducts struct {
	SP3, CLK, ERP, DCB, BIA, BRDC string
}

type StationResult struct {
	Date        time.Time
	Code        string
	Lat, Lon, H float64
	Q, NSat     int
	DN, DE, DU  float64
	R3D         float64
	Status      string
	DurationSec float64
}

// ── station filter from accuracy CSV ─────────────────────────────────────────

// loadStationFilter reads stations_accuracy.csv and returns codes satisfying
// grade ∈ {A,B,C,D} AND sigma_3D_mm < maxSigma_mm.
// Expected columns: station_id, domes, site_name, sigma_X_mm, sigma_Y_mm, sigma_Z_mm, sigma_3D_mm, grade, ...
func loadStationFilter(path string, maxSigma_mm float64) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	idx := make(map[string]int)
	for i, h := range header {
		idx[strings.TrimSpace(h)] = i
	}
	colCode := idx["station_id"]
	colSigma := idx["sigma_3D_mm"]
	colGrade := idx["grade"]

	allowed := map[string]bool{"A": true, "B": true, "C": true, "D": true}
	result := make(map[string]bool)
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if len(rec) <= colGrade || len(rec) <= colSigma {
			continue
		}
		grade := strings.TrimSpace(rec[colGrade])
		if !allowed[grade] {
			continue
		}
		sigma, err := strconv.ParseFloat(strings.TrimSpace(rec[colSigma]), 64)
		if err != nil || sigma >= maxSigma_mm {
			continue
		}
		code := strings.TrimSpace(rec[colCode])
		if len(code) == 4 {
			result[strings.ToUpper(code)] = true
		}
	}
	return result, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

var httpClient = &http.Client{Timeout: 3 * time.Minute}

func ftpCurl(remotePath, destPath string) error {
	url := "ftp://gdc.cddis.eosdis.nasa.gov:21" + remotePath
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	cmd := exec.Command("curl", "--ssl-reqd", "-u", "anonymous:anonymous",
		"--silent", "--show-error", "--max-time", "120", "--output", destPath, url)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(destPath)
		return fmt.Errorf("ftp %s: %s", remotePath, strings.TrimSpace(string(out)))
	}
	info, _ := os.Stat(destPath)
	if info == nil || info.Size() == 0 {
		os.Remove(destPath)
		return fmt.Errorf("empty: %s", remotePath)
	}
	return nil
}

func httpGetFile(url, destPath string) error {
	resp, err := httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		return fmt.Errorf("auth required: %s", url)
	}
	os.MkdirAll(filepath.Dir(destPath), 0755)
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		return err
	}
	if n == 0 {
		os.Remove(destPath)
		return fmt.Errorf("empty: %s", url)
	}
	return nil
}

func decompressZ(src, dst string) error {
	cmd := exec.Command("gzip", "-dc", src)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("gzip -dc %s: %w", src, err)
	}
	return os.WriteFile(dst, out, 0644)
}

var bkgObsRe = regexp.MustCompile(`[A-Z0-9]{4}00[A-Z]{3}_R_\d{7}0000_01D_30S_MO\.crx\.gz`)

func bkgFindObs(su string, year, doy int) string {
	url := fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/obs/%d/%03d/", year, doy)
	resp, err := httpClient.Get(url)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	suffix := fmt.Sprintf("_R_%d%03d0000_01D_30S_MO.crx.gz", year, doy)
	prefix := su + "00"
	for _, m := range bkgObsRe.FindAllString(string(body), -1) {
		if strings.HasPrefix(m, prefix) && strings.HasSuffix(m, suffix) {
			return url + m
		}
	}
	return ""
}

func downloadObs(code string, date time.Time, taskDir string, conv *services.ConverterService) (string, error) {
	year, doy := date.Year(), date.YearDay()
	yy := year % 100
	sl := strings.ToLower(code)
	su := strings.ToUpper(code)

	ftpPath := fmt.Sprintf("/gnss/data/daily/%d/%03d/%02dd/%s%03d0.%02dd.Z", year, doy, yy, sl, doy, yy)
	rawZ := filepath.Join(taskDir, fmt.Sprintf("obs.%02dd.Z", yy))
	rawD := filepath.Join(taskDir, fmt.Sprintf("obs.%02dd", yy))
	if err := ftpCurl(ftpPath, rawZ); err == nil {
		if err := decompressZ(rawZ, rawD); err == nil {
			os.Remove(rawZ)
			out, err := conv.ConvertFile(rawD, taskDir)
			if err == nil {
				return out, nil
			}
		}
		os.Remove(rawZ)
		os.Remove(rawD)
	}

	bkgURL := bkgFindObs(su, year, doy)
	if bkgURL == "" {
		return "", fmt.Errorf("no obs found for %s on %s", code, date.Format("2006-01-02"))
	}
	rawGZ := filepath.Join(taskDir, "obs.crx.gz")
	if err := httpGetFile(bkgURL, rawGZ); err != nil {
		return "", fmt.Errorf("BKG download: %w", err)
	}
	out, err := conv.ConvertFile(rawGZ, taskDir)
	if err != nil {
		return "", fmt.Errorf("BKG convert: %w", err)
	}
	return out, nil
}

func extractAntenna(rinexPath string) (antType string, dH, dE, dN float64) {
	antType = "NONE"
	f, err := os.Open(rinexPath)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "END OF HEADER") {
			break
		}
		if strings.Contains(line, "ANT # / TYPE") && len(line) >= 60 {
			t := strings.TrimSpace(line[20:60])
			if t != "" {
				antType = t
			}
		}
		if strings.Contains(line, "ANTENNA: DELTA H/E/N") && len(line) >= 42 {
			fmt.Sscanf(strings.TrimSpace(line[:14]), "%f", &dH)
			fmt.Sscanf(strings.TrimSpace(line[14:28]), "%f", &dE)
			fmt.Sscanf(strings.TrimSpace(line[28:42]), "%f", &dN)
		}
	}
	return
}

func overrideLine(content, prefix, newLine string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeft(line, " "), prefix) {
			lines[i] = newLine
			return strings.Join(lines, "\n")
		}
	}
	return content
}

func writePPPConfig(
	taskDir, taskID, antType string,
	antH, antE, antN float64,
	posX, posY, posZ float64,
	biaFile, erpFile, blqFile string,
) (string, error) {
	raw, err := os.ReadFile(pppConfFile)
	if err != nil {
		return "", fmt.Errorf("read ppp.conf: %w", err)
	}
	var stripped strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		if idx := strings.Index(line, " #"); idx >= 0 {
			line = strings.TrimRight(line[:idx], " \t")
		}
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		stripped.WriteString(line)
		stripped.WriteByte('\n')
	}
	r := strings.NewReplacer(
		"{{POS_MODE}}", "ppp-static",
		"{{SNR_MASK_R}}", "off",
		"{{SNR_MASK_B}}", "off",
		"{{SNR_MASK_L1}}", "",
		"{{SNR_MASK_L2}}", "",
		"{{SNR_MASK_L5}}", "",
		"{{ANT_TYPE}}", antType,
		"{{ANT_DELTA_U}}", strconv.FormatFloat(antH, 'f', 4, 64),
		"{{ANT_DELTA_E}}", strconv.FormatFloat(antE, 'f', 4, 64),
		"{{ANT_DELTA_N}}", strconv.FormatFloat(antN, 'f', 4, 64),
		"{{BIA_FILE}}", biaFile,
		"{{DCB_FILE}}", "",
		serviceRoot+"/{{ERP_FILE}}", erpFile,
		serviceRoot+"/{{BLQ_FILE}}", blqFile,
	)
	content := r.Replace(stripped.String())
	content = overrideLine(content, "ant1-pos1", fmt.Sprintf("ant1-pos1          =%.4f", posX))
	content = overrideLine(content, "ant1-pos2", fmt.Sprintf("ant1-pos2          =%.4f", posY))
	content = overrideLine(content, "ant1-pos3", fmt.Sprintf("ant1-pos3          =%.4f", posZ))
	content = overrideLine(content, "file-tempdir", "file-tempdir       ="+taskDir)
	if blqFile == "" {
		content = overrideLine(content, "pos1-tidecorr", "pos1-tidecorr      =on")
	}
	content = overrideLine(content, "file-solstatfile", "file-solstatfile   =")
	content = overrideLine(content, "file-tracefile", "file-tracefile     =")
	configPath := filepath.Join(taskDir, taskID+"_ppp.conf")
	return configPath, os.WriteFile(configPath, []byte(content), 0644)
}

func parsePPPOutput(posFile string) (lat, lon, h float64, q, nsat int, ok bool) {
	data, err := os.ReadFile(posFile)
	if err != nil {
		return
	}
	type sol struct {
		lat, lon, h float64
		q, nsat     int
	}
	var solutions []sol
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '%' || line[0] == '#' {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 7 {
			continue
		}
		var s sol
		fmt.Sscanf(f[2], "%f", &s.lat)
		fmt.Sscanf(f[3], "%f", &s.lon)
		fmt.Sscanf(f[4], "%f", &s.h)
		fmt.Sscanf(f[5], "%d", &s.q)
		fmt.Sscanf(f[6], "%d", &s.nsat)
		solutions = append(solutions, s)
	}
	if len(solutions) == 0 {
		return
	}
	for i := len(solutions) - 1; i >= 0; i-- {
		if solutions[i].q == 1 {
			s := solutions[i]
			return s.lat, s.lon, s.h, s.q, s.nsat, true
		}
	}
	for i := len(solutions) - 1; i >= 0; i-- {
		if solutions[i].q == 6 {
			s := solutions[i]
			return s.lat, s.lon, s.h, s.q, s.nsat, true
		}
	}
	s := solutions[len(solutions)-1]
	return s.lat, s.lon, s.h, s.q, s.nsat, true
}

// ── geodesy ───────────────────────────────────────────────────────────────────

const (
	wgs84A  = 6378137.0
	wgs84F  = 1.0 / 298.257223563
	wgs84E2 = 2*wgs84F - wgs84F*wgs84F
)

func xyzToLLH(x, y, z float64) (latDeg, lonDeg, h float64) {
	lonDeg = math.Atan2(y, x) * 180 / math.Pi
	p := math.Sqrt(x*x + y*y)
	lat := math.Atan2(z, p*(1-wgs84E2))
	for i := 0; i < 10; i++ {
		sinLat := math.Sin(lat)
		N := wgs84A / math.Sqrt(1-wgs84E2*sinLat*sinLat)
		next := math.Atan2(z+wgs84E2*N*sinLat, p)
		if math.Abs(next-lat) < 1e-12 {
			lat = next
			break
		}
		lat = next
	}
	sinLat := math.Sin(lat)
	N := wgs84A / math.Sqrt(1-wgs84E2*sinLat*sinLat)
	h = p/math.Cos(lat) - N
	latDeg = lat * 180 / math.Pi
	return
}

func llhToXYZ(latDeg, lonDeg, h float64) (x, y, z float64) {
	latR := latDeg * math.Pi / 180
	lonR := lonDeg * math.Pi / 180
	sinLat, cosLat := math.Sin(latR), math.Cos(latR)
	N := wgs84A / math.Sqrt(1-wgs84E2*sinLat*sinLat)
	x = (N + h) * cosLat * math.Cos(lonR)
	y = (N + h) * cosLat * math.Sin(lonR)
	z = (N*(1-wgs84E2) + h) * sinLat
	return
}

func xyzDiffToNEU(refLatDeg, refLonDeg, dx, dy, dz float64) (dN, dE, dU float64) {
	latR := refLatDeg * math.Pi / 180
	lonR := refLonDeg * math.Pi / 180
	sinLat, cosLat := math.Sin(latR), math.Cos(latR)
	sinLon, cosLon := math.Sin(lonR), math.Cos(lonR)
	dN = -sinLat*cosLon*dx - sinLat*sinLon*dy + cosLat*dz
	dE = -sinLon*dx + cosLon*dy
	dU = cosLat*cosLon*dx + cosLat*sinLon*dy + sinLat*dz
	return
}

func toDecimalYear(t time.Time) float64 {
	y := t.Year()
	startOfYear := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
	startOfNext := time.Date(y+1, 1, 1, 0, 0, 0, 0, time.UTC)
	daysInYear := startOfNext.Sub(startOfYear).Hours() / 24
	dayOfYear := t.Sub(startOfYear).Hours() / 24
	return float64(y) + dayOfYear/daysInYear
}

// ── ITRF2020-u2024 plain-text SSC parser ─────────────────────────────────────

func parseSSCTime(s string) time.Time {
	if s == "00:000:00000" {
		return time.Time{}
	}
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return time.Time{}
	}
	yy, _ := strconv.Atoi(parts[0])
	doy, _ := strconv.Atoi(parts[1])
	sod, _ := strconv.Atoi(parts[2])
	year := 2000 + yy
	if yy >= 80 {
		year = 1900 + yy
	}
	return time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, doy-1).
		Add(time.Duration(sod) * time.Second)
}

func stationCoversDate(st ITRFStation, date time.Time) bool {
	if !st.DataStart.IsZero() && date.Before(st.DataStart) {
		return false
	}
	if !st.DataEnd.IsZero() && !date.Before(st.DataEnd) {
		return false
	}
	return true
}

func stationsForDate(all []ITRFStation, date time.Time) []ITRFStation {
	byCode := make(map[string]ITRFStation)
	for _, st := range all {
		if !stationCoversDate(st, date) {
			continue
		}
		existing, ok := byCode[st.Code]
		if !ok || st.DataStart.After(existing.DataStart) {
			byCode[st.Code] = st
		}
	}
	result := make([]ITRFStation, 0, len(byCode))
	for _, st := range byCode {
		result = append(result, st)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Code < result[j].Code })
	return result
}

func loadITRF2020(cacheFile string) ([]ITRFStation, error) {
	var data []byte
	if cacheFile != "" {
		data, _ = os.ReadFile(cacheFile)
	}
	if len(data) == 0 {
		zap.S().Infof("Loading %s ...", itrf2020URL)
		resp, e := httpClient.Get(itrf2020URL)
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		var err error
		data, err = io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		if cacheFile != "" {
			_ = os.WriteFile(cacheFile, data, 0644)
		}
	}

	var stations []ITRFStation
	var pending *ITRFStation

	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		if f[0] == "DOMES" || strings.Contains(line, "STATION POSITIONS") ||
			strings.Contains(line, "---") || strings.Contains(line, "m/m/y") {
			continue
		}
		if _, ferr := strconv.ParseFloat(f[1], 64); ferr == nil {
			if pending != nil && len(f) >= 4 {
				pending.Vx, _ = strconv.ParseFloat(f[1], 64)
				pending.Vy, _ = strconv.ParseFloat(f[2], 64)
				pending.Vz, _ = strconv.ParseFloat(f[3], 64)
				stations = append(stations, *pending)
				pending = nil
			}
			continue
		}
		techIdx := -1
		for i, tok := range f {
			if tok == "GNSS" || tok == "SLR" || tok == "VLBI" || tok == "DORIS" {
				techIdx = i
				break
			}
		}
		if techIdx < 0 || techIdx+7 >= len(f) {
			continue
		}
		code := f[techIdx+1]
		if len(code) != 4 {
			continue
		}
		x, _ := strconv.ParseFloat(f[techIdx+2], 64)
		y, _ := strconv.ParseFloat(f[techIdx+3], 64)
		z, _ := strconv.ParseFloat(f[techIdx+4], 64)
		var dataStart, dataEnd time.Time
		if techIdx+10 < len(f) {
			dataStart = parseSSCTime(f[techIdx+9])
			dataEnd = parseSSCTime(f[techIdx+10])
		}
		pending = &ITRFStation{
			Code: code, X: x, Y: y, Z: z, T0: 2020.0,
			DataStart: dataStart, DataEnd: dataEnd,
		}
	}

	sort.Slice(stations, func(i, j int) bool { return stations[i].Code < stations[j].Code })
	return stations, nil
}

// ── daily products ────────────────────────────────────────────────────────────

func downloadDailyProducts(fd *services.FileDownloader, logger *zap.SugaredLogger, date time.Time, workDir string) (DailyProducts, error) {
	taskID := "daily_" + date.Format("20060102")
	var prods DailyProducts

	sp3, err := fd.DownloadPreciseEphemeris(date, taskID)
	if err != nil {
		return prods, fmt.Errorf("SP3: %w", err)
	}
	prods.SP3 = sp3

	clk, err := fd.DownloadPreciseClock(date, taskID)
	if err != nil {
		return prods, fmt.Errorf("CLK: %w", err)
	}
	prods.CLK = clk

	erp, err := fd.DownloadERP(date, taskID)
	if err != nil {
		logger.Warnf("ERP not available for %s: %v", date.Format("2006-01-02"), err)
	}
	prods.ERP = erp

	dcb, err := fd.DownloadDCB(date, taskID)
	if err != nil {
		logger.Warnf("DCB not available for %s: %v", date.Format("2006-01-02"), err)
	}
	prods.DCB = dcb

	bia, err := fd.DownloadBIA(date, taskID)
	if err != nil {
		logger.Warnf("BIA not available for %s: %v", date.Format("2006-01-02"), err)
	}
	prods.BIA = bia

	brdc, err := fd.DownloadBroadcastEphemeris(date, taskID)
	if err != nil {
		logger.Warnf("BRDC not available for %s: %v", date.Format("2006-01-02"), err)
	}
	prods.BRDC = brdc

	mark := ""
	for _, p := range []struct{ name, val string }{
		{"SP3", prods.SP3}, {"CLK", prods.CLK}, {"ERP", prods.ERP},
		{"DCB", prods.DCB}, {"BIA", prods.BIA}, {"BRDC", prods.BRDC},
	} {
		if p.val != "" {
			mark += p.name + "=✓ "
		}
	}
	logger.Infof("  %s", strings.TrimSpace(mark))
	return prods, nil
}

// ── per-station processing ────────────────────────────────────────────────────

func processStation(
	st ITRFStation, date time.Time, prods DailyProducts,
	rtk *services.RTKService, conv *services.ConverterService,
	getBLQ func(ITRFStation, time.Time, string) string,
	logger *zap.SugaredLogger, workDir string, keepTmp bool,
) StationResult {
	start := time.Now()
	res := StationResult{Date: date, Code: st.Code}

	taskID := fmt.Sprintf("batch_%s_%s", st.Code, date.Format("20060102"))
	taskDir := filepath.Join(workDir, taskID)
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		res.Status = "setup_failed"
		return res
	}
	if !keepTmp {
		defer os.RemoveAll(taskDir)
	}

	obsPath, err := downloadObs(st.Code, date, taskDir, conv)
	if err != nil {
		logger.Debugf("[%s] no obs: %v", taskID, err)
		res.Status = "no_obs"
		res.DurationSec = time.Since(start).Seconds()
		return res
	}

	blqFile := getBLQ(st, date, taskDir)
	antType, antH, antE, antN := extractAntenna(obsPath)
	refX, refY, refZ := st.PosAt(date)

	configPath, err := writePPPConfig(taskDir, taskID, antType, antH, antE, antN,
		refX, refY, refZ, prods.BIA, prods.ERP, blqFile)
	if err != nil {
		res.Status = "config_failed"
		res.DurationSec = time.Since(start).Seconds()
		return res
	}

	posFile, err := rtk.ProcessPPP(obsPath, prods.BRDC, prods.SP3, prods.CLK, configPath, taskID)
	if err != nil {
		logger.Debugf("[%s] PPP failed: %v", taskID, err)
		res.Status = "ppp_failed"
		res.DurationSec = time.Since(start).Seconds()
		return res
	}

	lat, lon, h, q, nsat, ok := parsePPPOutput(posFile)
	if !ok {
		res.Status = "parse_failed"
		res.DurationSec = time.Since(start).Seconds()
		return res
	}
	res.Lat, res.Lon, res.H = lat, lon, h
	res.Q, res.NSat = q, nsat

	refLat, refLon, _ := xyzToLLH(refX, refY, refZ)
	pppX, pppY, pppZ := llhToXYZ(lat, lon, h)
	dX, dY, dZ := pppX-refX, pppY-refY, pppZ-refZ
	res.DN, res.DE, res.DU = xyzDiffToNEU(refLat, refLon, dX, dY, dZ)
	res.R3D = math.Sqrt(dX*dX + dY*dY + dZ*dZ)

	res.Status = "ok"
	res.DurationSec = time.Since(start).Seconds()
	return res
}

// ── statistics ────────────────────────────────────────────────────────────────

func rms(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(vals)))
}

// PeriodStats holds aggregated accuracy metrics for a date / month / station.
type PeriodStats struct {
	Label  string  // date string, "YYYY-MM", station code, or "Overall"
	N      int     // number of ok solutions
	FixPct float64 // % Q=1 among ok solutions
	RMS_N  float64 // metres
	RMS_E  float64
	RMS_U  float64
	RMS_3D float64
}

type DetailedStats struct {
	Days     []PeriodStats // one per processed date (chronological)
	Months   []PeriodStats // one per calendar month
	Stations []PeriodStats // one per station code (sorted by RMS_3D asc)
	Overall  PeriodStats
}

func computeDetailedStats(csvPath string) DetailedStats {
	f, err := os.Open(csvPath)
	if err != nil {
		return DetailedStats{}
	}
	defer f.Close()

	type row struct {
		date, station, month string
		dN, dE, dU, r3d      float64
		q                    int
	}

	r := csv.NewReader(f)
	r.Read() // header

	var rows []row
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if len(rec) < 12 || rec[11] != "ok" {
			continue
		}
		date := rec[0]
		month := ""
		if len(date) >= 7 {
			month = date[:7]
		}
		dN, _ := strconv.ParseFloat(rec[7], 64)
		dE, _ := strconv.ParseFloat(rec[8], 64)
		dU, _ := strconv.ParseFloat(rec[9], 64)
		r3d, _ := strconv.ParseFloat(rec[10], 64)
		q, _ := strconv.Atoi(rec[5])
		rows = append(rows, row{
			date: date, station: rec[1], month: month,
			dN: dN, dE: dE, dU: dU, r3d: r3d, q: q,
		})
	}

	aggregate := func(rs []row, label string) PeriodStats {
		ps := PeriodStats{Label: label, N: len(rs)}
		if len(rs) == 0 {
			return ps
		}
		var ns, es, us, r3s []float64
		fix := 0
		for _, rw := range rs {
			ns = append(ns, rw.dN)
			es = append(es, rw.dE)
			us = append(us, rw.dU)
			r3s = append(r3s, rw.r3d)
			if rw.q == 1 {
				fix++
			}
		}
		ps.RMS_N = rms(ns)
		ps.RMS_E = rms(es)
		ps.RMS_U = rms(us)
		ps.RMS_3D = rms(r3s)
		ps.FixPct = float64(fix) / float64(len(rs)) * 100
		return ps
	}

	byDate := make(map[string][]row)
	byMonth := make(map[string][]row)
	byStation := make(map[string][]row)
	for _, rw := range rows {
		byDate[rw.date] = append(byDate[rw.date], rw)
		byMonth[rw.month] = append(byMonth[rw.month], rw)
		byStation[rw.station] = append(byStation[rw.station], rw)
	}

	keys := func(m map[string][]row) []string {
		ks := make([]string, 0, len(m))
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}

	days := make([]PeriodStats, 0)
	for _, d := range keys(byDate) {
		days = append(days, aggregate(byDate[d], d))
	}

	monthStats := make([]PeriodStats, 0)
	for _, m := range keys(byMonth) {
		monthStats = append(monthStats, aggregate(byMonth[m], m))
	}

	stStats := make([]PeriodStats, 0)
	for _, s := range keys(byStation) {
		stStats = append(stStats, aggregate(byStation[s], s))
	}
	sort.Slice(stStats, func(i, j int) bool { return stStats[i].RMS_3D < stStats[j].RMS_3D })

	return DetailedStats{
		Days:     days,
		Months:   monthStats,
		Stations: stStats,
		Overall:  aggregate(rows, "Overall"),
	}
}

// ── HTML report ───────────────────────────────────────────────────────────────

const htmlTpl = `<!DOCTYPE html>
<html><head><meta charset="utf-8">
<title>IGS PPP-AR Batch Report</title>
<style>
body{font-family:sans-serif;margin:2em;color:#222}
h1{border-bottom:2px solid #336;padding-bottom:0.3em}
h2{color:#336;margin-top:2em}
table{border-collapse:collapse;width:100%;margin-bottom:1.5em}
th,td{border:1px solid #ccc;padding:5px 11px;text-align:right;font-size:0.88em}
th{background:#e8eaf0;text-align:center}
td.label{text-align:left;font-family:monospace;font-weight:bold}
tr.total{font-weight:bold;background:#d8e8ff}
tr.good{background:#f0fff4}
tr.warn{background:#fffde7}
tr.bad {background:#fff0f0}
.summary{background:#f5f5ff;border:1px solid #aac;padding:1em 1.5em;
         border-radius:6px;margin-bottom:1.5em;display:flex;flex-wrap:wrap;gap:1.2em}
.kv{display:flex;flex-direction:column;align-items:center}
.kv .val{font-size:1.4em;font-weight:bold;color:#336}
.kv .key{font-size:0.75em;color:#666}
</style></head><body>
<h1>IGS PPP-AR Batch Report</h1>
<p style="color:#666">Сгенерирован: {{.Generated}}</p>

<div class="summary">
  <div class="kv"><span class="val">{{.Overall.N}}</span><span class="key">Решений (ok)</span></div>
  <div class="kv"><span class="val">{{printf "%.1f" .Overall.FixPct}}%</span><span class="key">Fix (Q=1)</span></div>
  <div class="kv"><span class="val">{{printf "%.2f" (cm .Overall.RMS_N)}}</span><span class="key">RMS N (cm)</span></div>
  <div class="kv"><span class="val">{{printf "%.2f" (cm .Overall.RMS_E)}}</span><span class="key">RMS E (cm)</span></div>
  <div class="kv"><span class="val">{{printf "%.2f" (cm .Overall.RMS_U)}}</span><span class="key">RMS U (cm)</span></div>
  <div class="kv"><span class="val">{{printf "%.2f" (cm .Overall.RMS_3D)}}</span><span class="key">RMS 3D (cm)</span></div>
</div>

<h2>Точность по дням</h2>
<table>
<tr><th>Дата</th><th>N</th><th>Fix%</th><th>RMS N (cm)</th><th>RMS E (cm)</th><th>RMS U (cm)</th><th>RMS 3D (cm)</th></tr>
{{range .Days}}
<tr class="{{rowclass .RMS_3D}}">
  <td class="label">{{.Label}}</td><td>{{.N}}</td>
  <td>{{printf "%.1f" .FixPct}}</td>
  <td>{{printf "%.2f" (cm .RMS_N)}}</td><td>{{printf "%.2f" (cm .RMS_E)}}</td>
  <td>{{printf "%.2f" (cm .RMS_U)}}</td><td>{{printf "%.2f" (cm .RMS_3D)}}</td>
</tr>{{end}}
<tr class="total">
  <td class="label">Итого</td><td>{{.Overall.N}}</td>
  <td>{{printf "%.1f" .Overall.FixPct}}</td>
  <td>{{printf "%.2f" (cm .Overall.RMS_N)}}</td><td>{{printf "%.2f" (cm .Overall.RMS_E)}}</td>
  <td>{{printf "%.2f" (cm .Overall.RMS_U)}}</td><td>{{printf "%.2f" (cm .Overall.RMS_3D)}}</td>
</tr>
</table>

{{if gt (len .Months) 1}}
<h2>Кумулятивная точность по месяцам</h2>
<table>
<tr><th>Месяц</th><th>N</th><th>Fix%</th><th>RMS N (cm)</th><th>RMS E (cm)</th><th>RMS U (cm)</th><th>RMS 3D (cm)</th></tr>
{{range .Months}}
<tr class="{{rowclass .RMS_3D}}">
  <td class="label">{{.Label}}</td><td>{{.N}}</td>
  <td>{{printf "%.1f" .FixPct}}</td>
  <td>{{printf "%.2f" (cm .RMS_N)}}</td><td>{{printf "%.2f" (cm .RMS_E)}}</td>
  <td>{{printf "%.2f" (cm .RMS_U)}}</td><td>{{printf "%.2f" (cm .RMS_3D)}}</td>
</tr>{{end}}
<tr class="total">
  <td class="label">Итого</td><td>{{.Overall.N}}</td>
  <td>{{printf "%.1f" .Overall.FixPct}}</td>
  <td>{{printf "%.2f" (cm .Overall.RMS_N)}}</td><td>{{printf "%.2f" (cm .Overall.RMS_E)}}</td>
  <td>{{printf "%.2f" (cm .Overall.RMS_U)}}</td><td>{{printf "%.2f" (cm .Overall.RMS_3D)}}</td>
</tr>
</table>
{{end}}

<h2>Точность по станциям (сортировка по RMS 3D ↑)</h2>
<table>
<tr><th>Станция</th><th>N дней</th><th>Fix%</th><th>RMS N (cm)</th><th>RMS E (cm)</th><th>RMS U (cm)</th><th>RMS 3D (cm)</th></tr>
{{range .Stations}}
<tr class="{{rowclass .RMS_3D}}">
  <td class="label">{{.Label}}</td><td>{{.N}}</td>
  <td>{{printf "%.1f" .FixPct}}</td>
  <td>{{printf "%.2f" (cm .RMS_N)}}</td><td>{{printf "%.2f" (cm .RMS_E)}}</td>
  <td>{{printf "%.2f" (cm .RMS_U)}}</td><td>{{printf "%.2f" (cm .RMS_3D)}}</td>
</tr>{{end}}
</table>
</body></html>`

func writeHTMLReport(outPath, csvPath string) error {
	stats := computeDetailedStats(csvPath)
	funcMap := template.FuncMap{
		"cm": func(m float64) float64 { return m * 100 },
		"rowclass": func(r3d float64) string {
			switch {
			case r3d*100 < 1.0:
				return "good"
			case r3d*100 < 2.0:
				return "warn"
			default:
				return "bad"
			}
		},
	}
	tmpl, err := template.New("r").Funcs(funcMap).Parse(htmlTpl)
	if err != nil {
		return err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, map[string]interface{}{
		"Generated": time.Now().Format("2006-01-02 15:04:05"),
		"Overall":   stats.Overall,
		"Days":      stats.Days,
		"Months":    stats.Months,
		"Stations":  stats.Stations,
	})
}

// ── date range builder ────────────────────────────────────────────────────────

func buildDates(start, end time.Time, step int) []time.Time {
	var dates []time.Time
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if (d.YearDay()-1)%step == 0 {
			dates = append(dates, d)
		}
	}
	return dates
}

func loadDone(csvPath string) map[string]bool {
	done := make(map[string]bool)
	f, err := os.Open(csvPath)
	if err != nil {
		return done
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Read()
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if len(rec) >= 2 {
			done[rec[0]+"_"+rec[1]] = true
		}
	}
	return done
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	startDateStr := flag.String("start-date", "", "Start date YYYY-MM-DD")
	endDateStr := flag.String("end-date", "", "End date YYYY-MM-DD")
	startYear := flag.Int("start-year", 2020, "Start year (overridden by --start-date)")
	endYear := flag.Int("end-year", 2025, "End year (overridden by --end-date)")
	sampleStep := flag.Int("sample-step", 7, "Day step (1=every day)")
	workers := flag.Int("workers", 4, "Parallel workers per day")
	workDir := flag.String("work-dir", "/tmp/igsbatch", "Temp working directory")
	outCSV := flag.String("out-csv", "igs_batch_report.csv", "Output CSV path")
	outHTML := flag.String("out-html", "igs_batch_report.html", "Output HTML report path")
	itrfCache := flag.String("itrf-cache", "/tmp/ITRF2020-u2024.SSC", "ITRF2020-u2024 cache file")
	noBLQ := flag.Bool("no-blq", false, "Disable OTL/BLQ correction")
	resume := flag.Bool("resume", false, "Skip already-processed station-days")
	keepTmp := flag.Bool("keep-tmp", false, "Keep per-station temp dirs")
	stationCodes := flag.String("station-codes", "", "Comma-separated station codes (override)")
	stationsCSV := flag.String("stations-csv", "", "stations_accuracy.csv — process grade A–D, sigma_3D < max-sigma")
	maxSigma := flag.Float64("max-sigma", 10.0, "Max sigma_3D in mm when using --stations-csv")
	flag.Parse()

	zapCfg := zap.NewDevelopmentConfig()
	zapCfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	logger, _ := zapCfg.Build()
	sugar := logger.Sugar()
	zap.ReplaceGlobals(logger)
	defer logger.Sync()

	var startDate, endDate time.Time
	if *startDateStr != "" {
		startDate, _ = time.Parse("2006-01-02", *startDateStr)
	}
	if *endDateStr != "" {
		endDate, _ = time.Parse("2006-01-02", *endDateStr)
	}
	if startDate.IsZero() {
		startDate = time.Date(*startYear, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if endDate.IsZero() {
		endDate = time.Date(*endYear, 12, 31, 0, 0, 0, 0, time.UTC)
	}

	// Station filter
	var codeFilter map[string]bool
	if *stationsCSV != "" {
		var err error
		codeFilter, err = loadStationFilter(*stationsCSV, *maxSigma)
		if err != nil {
			sugar.Fatalf("Cannot load stations CSV: %v", err)
		}
		sugar.Infof("Фильтр: %d пунктов (grade A–D, sigma_3D < %.0f мм) из %s",
			len(codeFilter), *maxSigma, *stationsCSV)
	} else if *stationCodes != "" {
		codeFilter = make(map[string]bool)
		for _, c := range strings.Split(*stationCodes, ",") {
			codeFilter[strings.TrimSpace(strings.ToUpper(c))] = true
		}
	}

	allEntries, err := loadITRF2020(*itrfCache)
	if err != nil {
		sugar.Fatalf("Failed to load ITRF2020: %v", err)
	}

	dates := buildDates(startDate, endDate, *sampleStep)

	firstDay := stationsForDate(allEntries, startDate)
	if codeFilter != nil {
		var flt []ITRFStation
		for _, st := range firstDay {
			if codeFilter[st.Code] {
				flt = append(flt, st)
			}
		}
		firstDay = flt
	}
	total := len(dates) * len(firstDay)
	sugar.Infof("ITRF2020: %d решений", len(allEntries))
	sugar.Infof("Задач: %d дней × ~%d станций = ~%d", len(dates), len(firstDay), total)

	if err := os.MkdirAll(*workDir, 0755); err != nil {
		sugar.Fatalf("Cannot create work dir: %v", err)
	}

	fd := services.NewFileDownloader(*workDir, sugar)
	rtk := services.NewRTKService(rtklibPath, *workDir, sugar)
	conv := services.NewConverterService(rtklibPath, sugar)

	var blqSvc *services.BLQService
	if !*noBLQ {
		if pyFES := os.Getenv("PYFES_CONFIG"); pyFES != "" {
			blqScript := filepath.Join(serviceRoot, "scripts/generate_blq.py")
			blqSvc = services.NewBLQService(blqScript, pyFES, *workDir, sugar)
		}
	}

	type blqEntry struct {
		once sync.Once
		path string
	}
	var blqMu sync.Mutex
	blqCache := make(map[string]*blqEntry)

	getBLQ := func(st ITRFStation, date time.Time, _ string) string {
		if blqSvc == nil {
			return ""
		}
		blqMu.Lock()
		entry, ok := blqCache[st.Code]
		if !ok {
			entry = &blqEntry{}
			blqCache[st.Code] = entry
		}
		blqMu.Unlock()
		entry.once.Do(func() {
			dir := filepath.Join(*workDir, "blq_"+st.Code)
			os.MkdirAll(dir, 0755)
			lat, lon, _ := xyzToLLH(st.PosAt(date))
			entry.path, _ = blqSvc.GenerateBLQ(st.Code, lat, lon, "blq_"+st.Code)
		})
		return entry.path
	}

	csvFileExists := false
	if info, err := os.Stat(*outCSV); err == nil && info.Size() > 0 {
		csvFileExists = true
	}
	csvFile, err := os.OpenFile(*outCSV, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		sugar.Fatalf("Cannot open CSV: %v", err)
	}
	defer csvFile.Close()
	csvWriter := csv.NewWriter(csvFile)
	defer csvWriter.Flush()

	if !csvFileExists {
		csvWriter.Write([]string{"date", "station", "lat", "lon", "h_m",
			"q", "nsat", "dN_m", "dE_m", "dU_m", "r3d_m", "status", "duration_s"})
		csvWriter.Flush()
	}

	done := make(map[string]bool)
	if *resume {
		done = loadDone(*outCSV)
		sugar.Infof("Resume: %d entries already done", len(done))
	}

	var mu sync.Mutex
	var processed, succeeded int
	totalStart := time.Now()

	for _, date := range dates {
		sugar.Infof("День %s: загрузка продуктов...", date.Format("2006-01-02"))
		prods, err := downloadDailyProducts(fd, sugar, date, *workDir)
		if err != nil {
			sugar.Warnf("Пропуск %s: %v", date.Format("2006-01-02"), err)
			continue
		}

		stations := stationsForDate(allEntries, date)
		if codeFilter != nil {
			var flt []ITRFStation
			for _, st := range stations {
				if codeFilter[st.Code] {
					flt = append(flt, st)
				}
			}
			stations = flt
		}

		sem := make(chan struct{}, *workers)
		var dayWG sync.WaitGroup

		for _, st := range stations {
			key := date.Format("2006-01-02") + "_" + st.Code
			if done[key] {
				continue
			}
			st := st
			dayWG.Add(1)
			sem <- struct{}{}
			go func() {
				defer dayWG.Done()
				defer func() { <-sem }()

				res := processStation(st, date, prods, rtk, conv, getBLQ, sugar, *workDir, *keepTmp)

				row := []string{
					res.Date.Format("2006-01-02"), res.Code,
					strconv.FormatFloat(res.Lat, 'f', 8, 64),
					strconv.FormatFloat(res.Lon, 'f', 8, 64),
					strconv.FormatFloat(res.H, 'f', 4, 64),
					strconv.Itoa(res.Q), strconv.Itoa(res.NSat),
					strconv.FormatFloat(res.DN, 'f', 4, 64),
					strconv.FormatFloat(res.DE, 'f', 4, 64),
					strconv.FormatFloat(res.DU, 'f', 4, 64),
					strconv.FormatFloat(res.R3D, 'f', 4, 64),
					res.Status,
					strconv.FormatFloat(res.DurationSec, 'f', 1, 64),
				}

				mu.Lock()
				csvWriter.Write(row)
				processed++
				if res.Status == "ok" {
					succeeded++
				}
				if processed%50 == 0 {
					elapsed := time.Since(totalStart).Seconds()
					eta := float64(total-processed) / (float64(processed) / elapsed)
					sugar.Infof("Прогресс: %d/%d (%.1f%%), ok=%d, ETA=%.0fs",
						processed, total, float64(processed)/float64(total)*100, succeeded, eta)
					csvWriter.Flush()
				}
				mu.Unlock()
			}()
		}
		dayWG.Wait()
		csvWriter.Flush()
	}

	sugar.Infof("Генерация отчёта...")
	if err := writeHTMLReport(*outHTML, *outCSV); err != nil {
		sugar.Warnf("HTML report: %v", err)
	} else {
		sugar.Infof("Отчёт: %s", *outHTML)
	}

	stats := computeDetailedStats(*outCSV)
	all := stats.Overall
	sep := strings.Repeat("─", 65)
	fmt.Printf("\n%s\n  IGS PPP Batch  %s – %s\n%s\n",
		sep, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"), sep)
	fmt.Printf("  Решений: %d  |  Fix: %.1f%%  |  RMS N/E/U/3D: %.2f/%.2f/%.2f/%.2f cm\n",
		all.N, all.FixPct, all.RMS_N*100, all.RMS_E*100, all.RMS_U*100, all.RMS_3D*100)
	fmt.Printf("%s\n  %-12s  %5s  %5s  %5s  %5s  %5s  %5s\n%s\n",
		sep, "Дата", "N", "Fix%", "N,cm", "E,cm", "U,cm", "3D,cm", sep)
	for _, d := range stats.Days {
		fmt.Printf("  %-12s  %5d  %4.1f%%  %5.2f  %5.2f  %5.2f  %5.2f\n",
			d.Label, d.N, d.FixPct, d.RMS_N*100, d.RMS_E*100, d.RMS_U*100, d.RMS_3D*100)
	}
	if len(stats.Months) > 1 {
		fmt.Printf("%s\n  %-12s  %5s  %5s  %5s  %5s  %5s  %5s\n%s\n",
			sep, "Месяц", "N", "Fix%", "N,cm", "E,cm", "U,cm", "3D,cm", sep)
		for _, m := range stats.Months {
			fmt.Printf("  %-12s  %5d  %4.1f%%  %5.2f  %5.2f  %5.2f  %5.2f\n",
				m.Label, m.N, m.FixPct, m.RMS_N*100, m.RMS_E*100, m.RMS_U*100, m.RMS_3D*100)
		}
	}
	fmt.Println(sep)
}
