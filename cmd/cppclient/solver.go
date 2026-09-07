package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ----------------------------------------------------------------
// Position
// ----------------------------------------------------------------

type Position struct {
	Lat, Lon, Height float64
	Quality          int // 1=fix,2=float,5=single,6=ppp
	NSat             int
	Sdx, Sdy, Sdz    float64 // NEU standard deviations (sdn, sde, sdu) from solution.pos
	Cnvg             int     // rtkrcv internal convergence flag: 0=not converged, 1=converged
}

var qualityLabel = map[int]string{
	1: "Fix", 2: "Float", 3: "SBAS", 4: "DGPS", 5: "Single", 6: "PPP",
}

func (p Position) QualityLabel() string {
	if l, ok := qualityLabel[p.Quality]; ok {
		return l
	}
	return "—"
}

// Converged returns true when rtkrcv's internal cpp-kinematic algorithm
// has flagged the solution as converged (cnvg==1 in solution.pos).
func (p Position) Converged() bool {
	return p.Cnvg == 1
}

// ----------------------------------------------------------------
// Solver modes
// ----------------------------------------------------------------

const (
	ModeCPPKinematic = "cpp-kinematic"
	ModeRTK          = "rtk-collab"
	ModeMB           = "mb-collab"
)

var receiverTypes = map[string]string{
	"tcp":    "tcpcli",
	"ntrip":  "6",
	"serial": "serial",
}

// ----------------------------------------------------------------
// Solver
// ----------------------------------------------------------------

type Solver struct {
	rtkrcvPath  string
	atxFile     string
	workDir     string
	configsDir  string
	outputPort  int // logstr1 tcpsvr — raw RTCM3 from receiver (fed to RTCMProxy)
	injectPort  int // inpstr4 inject relay port; rtkrcv started with -p injectPort

	mu          sync.Mutex
	proc        *exec.Cmd
	stdinW      *os.File // write-end of stdin pipe — kept open so rtkrcv never sees EOF
	solFile     string
	mode        string
	lastPos     *Position
	lastCfgFile string
}

func (s *Solver) LastCfgFile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCfgFile
}

// LastSolFile returns the path to the current solution.pos file, or "" before first start.
func (s *Solver) LastSolFile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.solFile
}

func NewSolver(rtkrcvPath, atxFile, workDir string) *Solver {
	// Configs directory: relative to binary's repo layout
	// cmd/cppclient → two levels up → cmd/solver/configs
	execDir := filepath.Dir(rtkrcvPath)
	cfgDir := filepath.Join(execDir, "..", "..", "cmd", "solver", "configs")
	cfgAbs, err := filepath.Abs(cfgDir)
	if err != nil {
		cfgAbs = cfgDir
	}
	return &Solver{
		rtkrcvPath: rtkrcvPath,
		atxFile:    atxFile,
		workDir:    workDir,
		configsDir: cfgAbs,
	}
}

// SetConfigsDir allows overriding the config templates directory
func (s *Solver) SetConfigsDir(dir string) { s.configsDir = dir }

// SetOutputPort sets the port for CPP's logstr1 raw-RTCM3 stream (fed into RTCMProxy).
func (s *Solver) SetOutputPort(port int) { s.outputPort = port }

// SetInjectPort sets the TCP port where CPP's inpstr4 will connect for base injection.
// rtkrcv will be started with -p <injectPort> so it parses this stream as partner position.
func (s *Solver) SetInjectPort(port int) { s.injectPort = port }

func (s *Solver) ConfigsDir() string { return s.configsDir }

func (s *Solver) Mode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// AntennaCfg — параметры антенны для подстановки в конфиг rtkrcv
type AntennaCfg struct {
	Type string  // название в каталоге IGS (например LEIAR25.R4 или *)
	E    float64 // смещение East, м
	N    float64 // смещение North, м
	U    float64 // смещение Up, м
	// Если BaseLat/Lon != 0 — ant1-postype=llh с CPP-координатами базы
	BaseLat, BaseLon, BaseH float64
}

func (a AntennaCfg) subs() map[string]string {
	t := a.Type
	if t == "" {
		t = "*"
	}
	posType := "single"
	pos1, pos2, pos3 := "0.0", "0.0", "0.0"
	if a.BaseLat != 0 || a.BaseLon != 0 {
		posType = "llh"
		pos1 = strconv.FormatFloat(a.BaseLat, 'f', 9, 64)
		pos2 = strconv.FormatFloat(a.BaseLon, 'f', 9, 64)
		pos3 = strconv.FormatFloat(a.BaseH, 'f', 4, 64)
	}
	return map[string]string{
		"ANT_TYPE":      t,
		"ANT_E":         strconv.FormatFloat(a.E, 'f', 4, 64),
		"ANT_N":         strconv.FormatFloat(a.N, 'f', 4, 64),
		"ANT_U":         strconv.FormatFloat(a.U, 'f', 4, 64),
		"BASE_POS_TYPE": posType,
		"BASE_POS1":     pos1,
		"BASE_POS2":     pos2,
		"BASE_POS3":     pos3,
	}
}

func (s *Solver) StartCPPKinematic(roverType, roverPath, ephHost string, ephPort int, ssrHost string, ssrPort int, ant AntennaCfg) error {
	subs := map[string]string{
		"ROVER_TYPE":  receiverType(roverType),
		"ROVER_PATH":  roverPath,
		"EPH_HOST":    ephHost,
		"EPH_PORT":    strconv.Itoa(ephPort),
		"SSR_HOST":    ssrHost,
		"SSR_PORT":    strconv.Itoa(ssrPort),
		"OUT_PORT":    strconv.Itoa(s.outputPort),
		"INJECT_PORT": strconv.Itoa(s.injectPort),
	}
	for k, v := range ant.subs() {
		subs[k] = v
	}
	return s.start(ModeCPPKinematic, subs)
}

func (s *Solver) StartRTK(roverType, roverPath, baseHost string, basePort int, ephHost string, ephPort int, ssrHost string, ssrPort int, ant AntennaCfg) error {
	subs := map[string]string{
		"ROVER_TYPE": receiverType(roverType),
		"ROVER_PATH": roverPath,
		"BASE_HOST":  baseHost,
		"BASE_PORT":  strconv.Itoa(basePort),
		"EPH_HOST":   ephHost,
		"EPH_PORT":   strconv.Itoa(ephPort),
		"SSR_HOST":   ssrHost,
		"SSR_PORT":   strconv.Itoa(ssrPort),
	}
	for k, v := range ant.subs() {
		subs[k] = v
	}
	return s.start(ModeRTK, subs)
}

func (s *Solver) StartMB(roverType, roverPath, baseHost string, basePort int, ephHost string, ephPort int, ssrHost string, ssrPort int, ant AntennaCfg) error {
	subs := map[string]string{
		"ROVER_TYPE": receiverType(roverType),
		"ROVER_PATH": roverPath,
		"BASE_HOST":  baseHost,
		"BASE_PORT":  strconv.Itoa(basePort),
		"EPH_HOST":   ephHost,
		"EPH_PORT":   strconv.Itoa(ephPort),
		"SSR_HOST":   ssrHost,
		"SSR_PORT":   strconv.Itoa(ssrPort),
	}
	for k, v := range ant.subs() {
		subs[k] = v
	}
	return s.start(ModeMB, subs)
}

func (s *Solver) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

func (s *Solver) stopLocked() {
	if s.stdinW != nil {
		s.stdinW.Close()
		s.stdinW = nil
	}
	if s.proc != nil && s.proc.Process != nil {
		_ = s.proc.Process.Kill()
		_ = s.proc.Wait()
		s.proc = nil
	}
}

func (s *Solver) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.proc != nil && s.proc.ProcessState == nil
}

func (s *Solver) GetLatestPosition() *Position {
	s.mu.Lock()
	solFile := s.solFile
	s.mu.Unlock()
	if solFile == "" {
		return s.lastPos
	}
	pos, err := parseLastPosition(solFile)
	if err == nil && pos != nil {
		s.mu.Lock()
		s.lastPos = pos
		s.mu.Unlock()
		return pos
	}
	return s.lastPos
}

// ----------------------------------------------------------------
// Internal
// ----------------------------------------------------------------

func (s *Solver) start(mode string, subs map[string]string) error {
	s.mu.Lock()
	s.stopLocked()
	s.mu.Unlock()

	tmplPath := filepath.Join(s.configsDir, mode+".conf")
	tmplData, err := os.ReadFile(tmplPath)
	if err != nil {
		return fmt.Errorf("read config template %s: %w", tmplPath, err)
	}

	if err := os.MkdirAll(s.workDir, 0755); err != nil {
		return fmt.Errorf("mkdir workdir: %w", err)
	}
	sessDir := filepath.Join(s.workDir, fmt.Sprintf("%s_%d", mode, time.Now().UnixMilli()))
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		return err
	}

	solFile := filepath.Join(sessDir, "solution.pos")
	cfgFile := filepath.Join(sessDir, "rtkrcv.conf")

	content := string(tmplData)
	for k, v := range subs {
		content = strings.ReplaceAll(content, "{{"+k+"}}", v)
	}
	content = strings.ReplaceAll(content, "{{ATX_FILE}}", s.atxFile)
	content = strings.ReplaceAll(content, "{{SOL_FILE}}", solFile)

	// Проверяем незамененные переменные
	if idx := strings.Index(content, "{{"); idx != -1 {
		end := strings.Index(content[idx:], "}}")
		varName := ""
		if end != -1 {
			varName = content[idx : idx+end+2]
		}
		return fmt.Errorf("незамененная переменная в конфиге: %s (шаблон: %s)", varName, tmplPath)
	}

	if err := os.WriteFile(cfgFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	s.lastCfgFile = cfgFile

	// Удаляем старые сессии, оставляем только последние 3
	cleanOldSessions(s.workDir, mode, 3)

	// Stdin — открытый pipe; rtkrcv читает команды со stdin и завершается при EOF.
	// Держим write-end открытым на всё время жизни процесса.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create stdin pipe: %w", err)
	}

	logFile, _ := os.OpenFile(
		filepath.Join(sessDir, "rtkrcv.log"),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644,
	)

	// -s: автостарт без ожидания команды "start" из stdin
	// -n: без консоли (патч rtkrcv, иначе требует /dev/tty или TCP-порт)
	// -port: порт CPP-inject; rtkrcv парсит inpstr4 как партнёрское XYZ-решение (TEXT-формат)
	args := []string{"-s", "-n", "-o", cfgFile}
	if s.injectPort > 0 {
		args = append(args, "-port", strconv.Itoa(s.injectPort))
	}
	cmd := exec.Command(s.rtkrcvPath, args...)
	cmd.Dir = filepath.Dir(s.rtkrcvPath)
	cmd.Stdin = stdinR
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = detachedProcess()

	if err := cmd.Start(); err != nil {
		stdinR.Close()
		stdinW.Close()
		if logFile != nil {
			logFile.Close()
		}
		return fmt.Errorf("start rtkrcv: %w", err)
	}
	stdinR.Close() // read-end больше не нужен в родительском процессе

	s.mu.Lock()
	s.proc = cmd
	s.stdinW = stdinW
	s.solFile = solFile
	s.mode = mode
	s.lastPos = nil
	s.mu.Unlock()

	go cmd.Wait() //nolint

	return nil
}

// cleanOldSessions удаляет старые директории сессий, оставляя последние keep штук.
func cleanOldSessions(workDir, mode string, keep int) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return
	}
	prefix := mode + "_"
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			dirs = append(dirs, filepath.Join(workDir, e.Name()))
		}
	}
	// Директории названы mode_<timestamp_ms> — лексикографическая сортировка совпадает с хронологической
	if len(dirs) <= keep {
		return
	}
	for _, d := range dirs[:len(dirs)-keep] {
		_ = os.RemoveAll(d)
	}
}

// freePort returns an unused TCP port chosen by the OS.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

func receiverType(t string) string {
	if v, ok := receiverTypes[t]; ok {
		return v
	}
	return t
}

func parseLastPosition(solFile string) (*Position, error) {
	f, err := os.Open(solFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var last string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") {
			continue
		}
		last = line
	}
	if last == "" {
		return nil, fmt.Errorf("no solution lines")
	}

	fields := strings.Fields(last)
	if len(fields) < 7 {
		return nil, fmt.Errorf("too few fields")
	}
	lat, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return nil, err
	}
	lon, _ := strconv.ParseFloat(fields[3], 64)
	height, _ := strconv.ParseFloat(fields[4], 64)
	quality, _ := strconv.Atoi(fields[5])
	nsat, _ := strconv.Atoi(fields[6])
	var sdx, sdy, sdz float64
	if len(fields) > 9 {
		sdx, _ = strconv.ParseFloat(fields[7], 64)
		sdy, _ = strconv.ParseFloat(fields[8], 64)
		sdz, _ = strconv.ParseFloat(fields[9], 64)
	}
	// fields: 0=date 1=time 2=lat 3=lon 4=h 5=Q 6=ns 7=sdn 8=sde 9=sdu
	//         10=sdne 11=sdeu 12=sdun 13=age 14=ratio 15=cnvg
	var cnvg int
	if len(fields) > 15 {
		cnvg, _ = strconv.Atoi(fields[15])
	}
	return &Position{
		Lat: lat, Lon: lon, Height: height,
		Quality: quality, NSat: nsat,
		Sdx: sdx, Sdy: sdy, Sdz: sdz,
		Cnvg: cnvg,
	}, nil
}

// mergeStop returns a channel that closes when EITHER a or b closes.
// Useful for combining a session-level stop with a per-candidate stop.
func mergeStop(a, b <-chan struct{}) <-chan struct{} {
	out := make(chan struct{})
	go func() {
		select {
		case <-a:
		case <-b:
		}
		close(out)
	}()
	return out
}

// ----------------------------------------------------------------
// Coordinate roughening (±500 m, deterministic per user)
// ----------------------------------------------------------------

func Roughen(lat, lon, height float64, seed string) (float64, float64, float64) {
	h := sha256.Sum256([]byte(seed))
	angle := float64(binary.BigEndian.Uint32(h[:4])) / (1 << 32) * 2 * math.Pi
	offsetM := 450.0 + float64(binary.BigEndian.Uint32(h[4:8]))/(1<<32)*100.0
	dLat := (offsetM * math.Cos(angle)) / 111_320.0
	dLon := (offsetM * math.Sin(angle)) / (111_320.0*math.Cos(lat*math.Pi/180) + 1e-9)
	return lat + dLat, lon + dLon, height
}

// ----------------------------------------------------------------
// Moving detection
// ----------------------------------------------------------------

type MovingDetector struct {
	threshold float64 // metres
	window    float64 // seconds
	history   []movSample
}

type movSample struct {
	t        time.Time
	lat, lon float64
}

func NewMovingDetector(thresholdM, windowS float64) *MovingDetector {
	return &MovingDetector{threshold: thresholdM, window: windowS}
}

func (d *MovingDetector) Update(lat, lon float64) bool {
	now := time.Now()
	d.history = append(d.history, movSample{now, lat, lon})
	cutoff := now.Add(-time.Duration(d.window * float64(time.Second)))
	filtered := d.history[:0]
	for _, s := range d.history {
		if s.t.After(cutoff) {
			filtered = append(filtered, s)
		}
	}
	d.history = filtered
	if len(d.history) < 2 {
		return false
	}
	first := d.history[0]
	return distM(first.lat, first.lon, lat, lon) > d.threshold
}

func distM(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6_371_000.0
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return R * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
