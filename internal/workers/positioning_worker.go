package workers

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"collaborative/internal/model"
	"collaborative/internal/storage"

	"go.uber.org/zap"
)

const (
	positioningInterval = 30 * time.Second
	rtkrcvBinary        = "rtkrcv"
	ephemerisNTRIP      = "Dolin:SergeySGGABG12@products.igs-ip.net:2101/BCEP00BKG0"
)

// processEntry описывает запущенный процесс rtkrcv для одной сессии
type processEntry struct {
	cmd     *exec.Cmd
	solFile string
	cancel  context.CancelFunc
}

// PositioningWorker управляет процессами rtkrcv и периодически читает их решения
type PositioningWorker struct {
	db         *storage.DBStorage
	logger     *zap.SugaredLogger
	workDir    string
	configTmpl string // путь к single-rtk.conf
	atxFile    string // путь к igs20.atx
	procs      map[int64]*processEntry
	mu         sync.Mutex
}

// NewPositioningWorker создаёт воркер позиционирования
func NewPositioningWorker(
	db *storage.DBStorage,
	logger *zap.SugaredLogger,
	workDir string,
	configTmpl string,
	atxFile string,
) *PositioningWorker {
	return &PositioningWorker{
		db:         db,
		logger:     logger,
		workDir:    workDir,
		configTmpl: configTmpl,
		atxFile:    atxFile,
		procs:      make(map[int64]*processEntry),
	}
}

// Start запускает воркер; завершается при отмене ctx
func (pw *PositioningWorker) Start(ctx context.Context) {
	go pw.run(ctx)
}

func (pw *PositioningWorker) run(ctx context.Context) {
	ticker := time.NewTicker(positioningInterval)
	defer ticker.Stop()

	// Первый тик сразу при старте
	pw.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			pw.stopAll()
			return
		case <-ticker.C:
			pw.tick(ctx)
		}
	}
}

func (pw *PositioningWorker) tick(ctx context.Context) {
	if pw.db == nil {
		return
	}

	sessions, err := pw.db.GetSessionsWithPositioning()
	if err != nil {
		pw.logger.Warnf("PositioningWorker: failed to fetch sessions: %v", err)
		return
	}

	// Собираем актуальные ID сессий
	active := make(map[int64]bool, len(sessions))
	for _, s := range sessions {
		active[s.ID] = true
	}

	pw.mu.Lock()
	defer pw.mu.Unlock()

	// Останавливаем процессы для отключённых сессий
	for id, entry := range pw.procs {
		if !active[id] {
			pw.stopProcess(id, entry)
		}
	}

	// Запускаем/обновляем процессы для активных сессий
	for _, sess := range sessions {
		if _, running := pw.procs[sess.ID]; !running {
			if err := pw.startProcess(ctx, sess); err != nil {
				pw.logger.Warnf("PositioningWorker: start rtkrcv for session %d: %v", sess.ID, err)
			}
		}
		pw.readAndStorePosition(sess.ID)
	}
}

func (pw *PositioningWorker) startProcess(ctx context.Context, sess model.CollaborativeSession) error {
	configPath, solFile, err := pw.generateConfig(sess)
	if err != nil {
		return fmt.Errorf("generate config: %w", err)
	}

	procCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(procCtx, rtkrcvBinary, "-o", configPath, "-d", "5")
	cmd.Dir = pw.workDir

	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start rtkrcv: %w", err)
	}

	pw.procs[sess.ID] = &processEntry{
		cmd:     cmd,
		solFile: solFile,
		cancel:  cancel,
	}
	pw.logger.Infof("PositioningWorker: started rtkrcv for session %d (port %d)", sess.ID, sess.AssignedPort)
	return nil
}

func (pw *PositioningWorker) stopProcess(id int64, entry *processEntry) {
	entry.cancel()
	if entry.cmd.Process != nil {
		_ = entry.cmd.Process.Kill()
	}
	delete(pw.procs, id)
	pw.logger.Infof("PositioningWorker: stopped rtkrcv for session %d", id)
}

func (pw *PositioningWorker) stopAll() {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	for id, entry := range pw.procs {
		pw.stopProcess(id, entry)
	}
}

// generateConfig создаёт конфигурационный файл rtkrcv для сессии
func (pw *PositioningWorker) generateConfig(sess model.CollaborativeSession) (configPath, solFile string, err error) {
	tmplData, err := os.ReadFile(pw.configTmpl)
	if err != nil {
		return "", "", fmt.Errorf("read template %q: %w", pw.configTmpl, err)
	}

	sessDir := filepath.Join(pw.workDir, fmt.Sprintf("collab_%d", sess.ID))
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		return "", "", fmt.Errorf("mkdir %q: %w", sessDir, err)
	}

	solFile = filepath.Join(sessDir, "solution.pos")
	configPath = filepath.Join(sessDir, "rtkrcv.conf")

	// Определяем тип и путь входного потока rover из настроек подключения сессии
	var roverType, roverPath string
	switch sess.ConnectionType {
	case model.ConnectionTypeTCP:
		roverType = "tcpcli"
		roverPath = fmt.Sprintf("%s:%d", sess.TCPHost, sess.TCPPort)
	case model.ConnectionTypeNTRIP:
		roverType = "ntripcli"
		if sess.NTRIPUser != "" {
			roverPath = fmt.Sprintf("%s:%s@%s:%d/%s",
				sess.NTRIPUser, sess.NTRIPPass,
				sess.NTRIPHost, sess.NTRIPPort, sess.NTRIPMountpoint)
		} else {
			roverPath = fmt.Sprintf("%s:%d/%s", sess.NTRIPHost, sess.NTRIPPort, sess.NTRIPMountpoint)
		}
	default:
		return "", "", fmt.Errorf("неизвестный тип подключения: %s", sess.ConnectionType)
	}

	content := string(tmplData)
	content = strings.ReplaceAll(content, "{{ROVER_TYPE}}", roverType)
	content = strings.ReplaceAll(content, "{{ROVER_PATH}}", roverPath)
	content = strings.ReplaceAll(content, "{{SOL_FILE}}", solFile)
	content = strings.ReplaceAll(content, "{{ATX_FILE}}", pw.atxFile)

	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		return "", "", fmt.Errorf("write config: %w", err)
	}
	return configPath, solFile, nil
}

// readAndStorePosition читает последнюю строку решения из файла и сохраняет позицию в БД
func (pw *PositioningWorker) readAndStorePosition(sessionID int64) {
	entry, ok := pw.procs[sessionID]
	if !ok {
		return
	}

	pos, err := parseLastPosition(entry.solFile, sessionID)
	if err != nil {
		// Файл может быть ещё пустым — не логируем как ошибку
		return
	}

	if err := pw.db.UpsertCollaborativePosition(pos); err != nil {
		pw.logger.Warnf("PositioningWorker: upsert position for session %d: %v", sessionID, err)
	}
}

// parseLastPosition читает последнюю валидную строку LLH-решения из файла rtkrcv
//
// Формат строки LLH (out-solformat=llh):
//
//	yyyy/mm/dd hh:mm:ss.sss   lat(deg)   lon(deg)   height(m)  Q  ns  sdp sdne sdeu sdun age ratio
func parseLastPosition(solFile string, sessionID int64) (*model.CollaborativePosition, error) {
	f, err := os.Open(solFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lastLine string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") {
			continue
		}
		lastLine = line
	}
	if lastLine == "" {
		return nil, fmt.Errorf("no solution lines")
	}

	fields := strings.Fields(lastLine)
	// Минимум: date time lat lon height Q ns
	if len(fields) < 7 {
		return nil, fmt.Errorf("too few fields: %d", len(fields))
	}

	// date=fields[0], time=fields[1], lat=fields[2], lon=fields[3], height=fields[4], Q=fields[5], ns=fields[6]
	lat, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return nil, err
	}
	lon, err := strconv.ParseFloat(fields[3], 64)
	if err != nil {
		return nil, err
	}
	height, err := strconv.ParseFloat(fields[4], 64)
	if err != nil {
		return nil, err
	}
	quality, _ := strconv.Atoi(fields[5])
	nsat, _ := strconv.Atoi(fields[6])

	var pdop float64
	if len(fields) >= 8 {
		pdop, _ = strconv.ParseFloat(fields[7], 64)
	}

	return &model.CollaborativePosition{
		SessionID: sessionID,
		Lat:       lat,
		Lon:       lon,
		Height:    height,
		Quality:   quality,
		NSat:      nsat,
		PDOP:      pdop,
	}, nil
}
