package services

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	cddisHost = "gdc.cddis.eosdis.nasa.gov"
	ftpUser   = "anonymous"
	ftpPass   = "anonymous"
)

type FileDownloader struct {
	workDir string
	client  *http.Client
	logger  *zap.SugaredLogger
}

func (d *FileDownloader) downloadFTP(remotePath, destPath string) error {
	url := fmt.Sprintf("ftp://%s:21%s", cddisHost, remotePath)
	d.logger.Infof("FTP download: %s", url)

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	cmd := exec.Command("curl",
		"--ssl-reqd",
		"-u", ftpUser+":"+ftpPass,
		"--silent",
		"--show-error",
		"--output", destPath,
		url,
	)

	if out, err := cmd.CombinedOutput(); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("curl FTP %s: %w: %s", url, err, strings.TrimSpace(string(out)))
	}

	info, err := os.Stat(destPath)
	if err != nil {
		return fmt.Errorf("FTP stat failed %s: %w", destPath, err)
	}
	if info.Size() == 0 {
		os.Remove(destPath)
		return fmt.Errorf("FTP: empty file at %s", remotePath)
	}

	d.logger.Infof("FTP downloaded %s (%d bytes)", destPath, info.Size())
	return nil
}

func NewFileDownloader(workDir string, logger *zap.SugaredLogger) *FileDownloader {
	return &FileDownloader{
		workDir: workDir,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
		logger: logger,
	}
}

// getYearDay возвращает год и день года (DOY)
func getYearDay(date time.Time) (year int, doy int) {
	year = date.Year()
	doy = date.YearDay()
	return
}

// getGPSWeekAndDOW возвращает GPS неделю и день недели (0=вс ... 6=сб).
// Продукты IGS на BKG хранятся в папках по GPS неделе: /IGS/products/WWWW/
// Это КРИТИЧЕСКИ отличается от структуры BRDC (/IGS/BRDC/YYYY/DDD/).
func getGPSWeekAndDOW(date time.Time) (week int, dow int) {
	gpsEpoch := time.Date(1980, 1, 6, 0, 0, 0, 0, time.UTC)
	days := int(date.UTC().Sub(gpsEpoch).Hours() / 24)
	week = days / 7
	dow = days % 7
	return
}

// ultraRapidIssue описывает выпуск Ultra-Rapid продукта.
// Ultra-Rapid обновляется 4 раза в сутки (00/06/12/18 UTC) и покрывает 48 часов
// (первые ~24ч — наблюдённая часть, вторые ~24ч — прогноз). Для обработки
// PPP/PPP-AR день-в-день нужен файл, чей 48-часовой интервал перекрывает
// целевой день; такой файл — это выпуск текущего дня в 00 UTC либо любой
// выпуск предыдущего дня.
type ultraRapidIssue struct {
	year int // год выпуска
	doy  int // DOY выпуска
	hour int // час выпуска (00, 06, 12, 18)
}

// ultraRapidIssues возвращает выпуски Ultra-Rapid для целевой даты date,
// упорядоченные от самого свежего к самому старому. Первым идёт выпуск,
// изданный в самое позднее допустимое время — он содержит максимальный
// объём фактических (не прогнозных) данных для даты наблюдения.
func ultraRapidIssues(date time.Time) []ultraRapidIssue {
	d0 := date.UTC().Truncate(24 * time.Hour)
	dPrev := d0.AddDate(0, 0, -1)
	hours := []int{18, 12, 6, 0}

	issues := make([]ultraRapidIssue, 0, 8)
	// Выпуск текущего дня в 00 UTC — единственный выпуск дня наблюдения,
	// у которого весь целевой день попадает в наблюдённую половину окна.
	y0, doy0 := d0.Year(), d0.YearDay()
	issues = append(issues, ultraRapidIssue{year: y0, doy: doy0, hour: 0})
	// Затем выпуски предыдущего дня — сначала самый свежий (18 UTC).
	yP, doyP := dPrev.Year(), dPrev.YearDay()
	for _, h := range hours {
		issues = append(issues, ultraRapidIssue{year: yP, doy: doyP, hour: h})
	}
	return issues
}

// getWeekSunday возвращает год и DOY воскресенья (начала GPS-недели).
// Файлы ERP нового формата (IGS0OPSFIN/IGS0OPSRAP) используют DOY воскресенья,
// а не DOY дня наблюдения. Воскресенье может быть в другом году (например,
// наблюдение 2025-01-01 → воскресенье 2024-12-29, DOY=364).
func getWeekSunday(date time.Time) (year int, doy int) {
	gpsEpoch := time.Date(1980, 1, 6, 0, 0, 0, 0, time.UTC)
	days := int(date.UTC().Sub(gpsEpoch).Hours() / 24)
	dow := days % 7
	sunday := date.UTC().AddDate(0, 0, -dow).Truncate(24 * time.Hour)
	return sunday.Year(), sunday.YearDay()
}

// DownloadBroadcastEphemeris скачивает широковещательные эфемериды (BRDC).
// Структура: /IGS/BRDC/YYYY/DDD/ — по году и DOY (не GPS неделя!)
func (d *FileDownloader) DownloadBroadcastEphemeris(date time.Time, taskID string) (string, error) {
	year, doy := getYearDay(date)

	localFile := filepath.Join(d.workDir, taskID, fmt.Sprintf("%s_brdc.rnx.gz", taskID))
	url := fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/BRDC/%d/%03d/BRDC00WRD_R_%d%03d0000_01D_MN.rnx.gz",
		year, doy, year, doy)

	if err := d.downloadFile(url, localFile); err != nil {
		d.logger.Warnf("Failed to download BRDC from BKG: %v, trying CDDIS...", err)
		url = fmt.Sprintf("https://cddis.nasa.gov/archive/gnss/data/daily/%d/brdc/brdc%03d0.%02dn.gz",
			year, doy, year%100)
		if err := d.downloadFile(url, localFile); err != nil {
			return "", fmt.Errorf("failed to download broadcast ephemeris: %w", err)
		}
	}

	unpacked := localFile[:len(localFile)-3]
	if err := d.gunzipFile(localFile, unpacked); err != nil {
		return "", fmt.Errorf("failed to unpack broadcast ephemeris: %w", err)
	}

	d.logger.Infof("Downloaded broadcast ephemeris: %s", unpacked)
	os.Remove(localFile)
	return unpacked, nil
}

// DownloadPreciseEphemeris скачивает точные эфемериды SP3 для PPP.
// Структура: /IGS/products/WWWW/ — по GPS неделе (не год/DOY!)
// Новый формат имён (с ~недели 2238, ноябрь 2022): IGS0OPSFIN_YYYYDDD...
// Старый формат (до недели 2238): igs{week}{dow}.sp3.gz / igr{week}{dow}.sp3.gz
func (d *FileDownloader) DownloadPreciseEphemeris(date time.Time, taskID string) (string, error) {
	week, dow := getGPSWeekAndDOW(date)
	year, doy := getYearDay(date)

	localFile := filepath.Join(d.workDir, taskID, fmt.Sprintf("%s_sp3.sp3.gz", taskID))

	type candidate struct {
		label  string
		ftpDir string // пустая строка — использовать HTTP
		url    string
	}

	ftpWeekDir := fmt.Sprintf("/gnss/products/%d", week)

	candidates := []candidate{
		// ── Новый формат (после ~2022) ──────────────────────────────────────
		{"CDDIS_COD_FINAL", ftpWeekDir,
			fmt.Sprintf("COD0OPSFIN_%d%03d0000_01D_05M_ORB.SP3.gz", year, doy)},
		{"CDDIS_COD_RAPID", ftpWeekDir,
			fmt.Sprintf("COD0OPSRAP_%d%03d0000_01D_05M_ORB.SP3.gz", year, doy)},
		{"CDDIS_IGS_FINAL", ftpWeekDir,
			fmt.Sprintf("IGS0OPSFIN_%d%03d0000_01D_15M_ORB.SP3.gz", year, doy)},
		{"CDDIS_IGS_RAPID", ftpWeekDir,
			fmt.Sprintf("IGS0OPSRAP_%d%03d0000_01D_15M_ORB.SP3.gz", year, doy)},
		{"BKG_IGS_FINAL", "",
			fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSFIN_%d%03d0000_01D_15M_ORB.SP3.gz", week, year, doy)},
		{"BKG_IGS_RAPID", "",
			fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSRAP_%d%03d0000_01D_15M_ORB.SP3.gz", week, year, doy)},
	}

	// ── Ultra-Rapid: для обработки день-в-день, когда rapid ещё не вышел ──
	// Пробуем свежие выпуски (текущего дня 00 UTC, затем предыдущего 18/12/06/00).
	// Файл покрывает 48ч от момента выпуска; целевой день должен попадать
	// в наблюдённую половину. Три центра поддерживают AR из Ultra продуктов:
	//   IGS0OPSULT — базовый IGS Ultra (GPS+GLO)
	//   WUM0MGXULT — Wuhan MGEX Ultra (multi-GNSS + фазовые смещения)
	//   GFZ0OPSULT — GFZ Ultra (GPS+GLO+GAL+BDS)
	for _, u := range ultraRapidIssues(date) {
		uWeek, _ := getGPSWeekAndDOW(time.Date(u.year, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, u.doy-1))
		uWeekDir := fmt.Sprintf("/gnss/products/%d", uWeek)
		candidates = append(candidates,
			candidate{fmt.Sprintf("CDDIS_IGS_ULTRA_%02d", u.hour), uWeekDir,
				fmt.Sprintf("IGS0OPSULT_%d%03d%02d00_02D_15M_ORB.SP3.gz", u.year, u.doy, u.hour)},
			candidate{fmt.Sprintf("BKG_IGS_ULTRA_%02d", u.hour), "",
				fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSULT_%d%03d%02d00_02D_15M_ORB.SP3.gz",
					uWeek, u.year, u.doy, u.hour)},
			candidate{fmt.Sprintf("CDDIS_WUM_ULTRA_%02d", u.hour), fmt.Sprintf("/gnss/products/mgex/%d", uWeek),
				fmt.Sprintf("WUM0MGXULT_%d%03d%02d00_01D_05M_ORB.SP3.gz", u.year, u.doy, u.hour)},
			candidate{fmt.Sprintf("CDDIS_GFZ_ULTRA_%02d", u.hour), uWeekDir,
				fmt.Sprintf("GFZ0OPSULT_%d%03d%02d00_02D_05M_ORB.SP3.gz", u.year, u.doy, u.hour)},
		)
	}

	candidates = append(candidates, []candidate{
		// ── CODE MGEX (AIUB Bern) — организованы по году, охватывают 2012–наст. ──
		// Используются для данных до ~2022, когда IGS ещё не перешёл на новый формат.
		{"AIUB_CODE_MGEX_FINAL", "",
			fmt.Sprintf("http://ftp.aiub.unibe.ch/CODE_MGEX/CODE/%d/COD0MGXFIN_%d%03d0000_01D_05M_ORB.SP3.gz",
				year, year, doy)},
		{"AIUB_CODE_MGEX_RAPID", "",
			fmt.Sprintf("http://ftp.aiub.unibe.ch/CODE_MGEX/CODE/%d/COD0MGXRAP_%d%03d0000_01D_05M_ORB.SP3.gz",
				year, year, doy)},
		// ── Старый формат (до ~2022, например неделя 2086) ──────────────────
		// igs = final (~2 нед. задержка), igr = rapid (~17 ч задержка)
		{"CDDIS_OLD_FINAL", ftpWeekDir,
			fmt.Sprintf("igs%d%d.sp3.Z", week, dow)},
		{"CDDIS_OLD_RAPID", ftpWeekDir,
			fmt.Sprintf("igr%d%d.sp3.Z", week, dow)},
		{"BKG_OLD_FINAL", "",
			fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d/igs%d%d.sp3.gz", week, week, dow)},
		{"BKG_OLD_RAPID", "",
			fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d/igr%d%d.sp3.gz", week, week, dow)},
	}...)

	var lastErr error
	for _, c := range candidates {
		var err error
		if c.ftpDir != "" {
			err = d.downloadFTP(c.ftpDir+"/"+c.url, localFile)
		} else {
			err = d.downloadFile(c.url, localFile)
		}
		if err != nil {
			d.logger.Warnf("Failed to download %s SP3: %v", c.label, err)
			lastErr = err
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return "", fmt.Errorf("failed to download precise ephemeris: %w", lastErr)
	}

	// Убираем суффикс сжатия (.gz или .Z) — имя файла всегда .sp3.gz,
	// но скачанный контент может быть как gzip так и Unix compress (.Z).
	unpacked := strings.TrimSuffix(strings.TrimSuffix(localFile, ".gz"), ".Z")
	if err := d.decompressFile(localFile, unpacked); err != nil {
		return "", fmt.Errorf("failed to unpack SP3: %w", err)
	}

	d.logger.Infof("Downloaded precise ephemeris: %s", unpacked)
	os.Remove(localFile)
	return unpacked, nil
}

// DownloadPreciseClock скачивает точные часы CLK для PPP.
// Структура та же: /IGS/products/WWWW/
// Новый формат (с ~недели 2238): IGS0OPSFIN_YYYYDDD..._CLK.CLK.gz
// Старый формат (до ~2238): igs{week}{dow}.clk.gz / igr{week}{dow}.clk.gz
func (d *FileDownloader) DownloadPreciseClock(date time.Time, taskID string) (string, error) {
	week, dow := getGPSWeekAndDOW(date)
	year, doy := getYearDay(date)

	localFile := filepath.Join(d.workDir, taskID, fmt.Sprintf("%s_clk.clk.gz", taskID))

	type candidate struct {
		label  string
		ftpDir string
		url    string
	}

	ftpWeekDir := fmt.Sprintf("/gnss/products/%d", week)

	candidates := []candidate{
		// ── Новый формат (после ~2022) ──────────────────────────────────────
		{"CDDIS_COD_FINAL", ftpWeekDir,
			fmt.Sprintf("COD0OPSFIN_%d%03d0000_01D_30S_CLK.CLK.gz", year, doy)},
		{"CDDIS_COD_RAPID", ftpWeekDir,
			fmt.Sprintf("COD0OPSRAP_%d%03d0000_01D_05M_CLK.CLK.gz", year, doy)},
		{"CDDIS_IGS_FINAL", ftpWeekDir,
			fmt.Sprintf("IGS0OPSFIN_%d%03d0000_01D_30S_CLK.CLK.gz", year, doy)},
		{"CDDIS_IGS_RAPID", ftpWeekDir,
			fmt.Sprintf("IGS0OPSRAP_%d%03d0000_01D_05M_CLK.CLK.gz", year, doy)},
		{"BKG_IGS_FINAL", "",
			fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSFIN_%d%03d0000_01D_30S_CLK.CLK.gz", week, year, doy)},
		{"BKG_IGS_RAPID", "",
			fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSRAP_%d%03d0000_01D_05M_CLK.CLK.gz", week, year, doy)},
	}

	// ── Ultra-Rapid CLK: свежие часы для обработки день-в-день ──
	// IGS Ultra публикует часы 15-мин; WUM/GFZ MGEX Ultra — 5-мин и
	// поддерживают PPP-AR (совместимы с фазовыми смещениями того же центра).
	for _, u := range ultraRapidIssues(date) {
		uWeek, _ := getGPSWeekAndDOW(time.Date(u.year, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, u.doy-1))
		uWeekDir := fmt.Sprintf("/gnss/products/%d", uWeek)
		candidates = append(candidates,
			candidate{fmt.Sprintf("CDDIS_IGS_ULTRA_%02d", u.hour), uWeekDir,
				fmt.Sprintf("IGS0OPSULT_%d%03d%02d00_02D_15M_CLK.CLK.gz", u.year, u.doy, u.hour)},
			candidate{fmt.Sprintf("BKG_IGS_ULTRA_%02d", u.hour), "",
				fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSULT_%d%03d%02d00_02D_15M_CLK.CLK.gz",
					uWeek, u.year, u.doy, u.hour)},
			candidate{fmt.Sprintf("CDDIS_WUM_ULTRA_%02d", u.hour), fmt.Sprintf("/gnss/products/mgex/%d", uWeek),
				fmt.Sprintf("WUM0MGXULT_%d%03d%02d00_01D_05M_CLK.CLK.gz", u.year, u.doy, u.hour)},
			candidate{fmt.Sprintf("CDDIS_GFZ_ULTRA_%02d", u.hour), uWeekDir,
				fmt.Sprintf("GFZ0OPSULT_%d%03d%02d00_02D_05M_CLK.CLK.gz", u.year, u.doy, u.hour)},
		)
	}

	candidates = append(candidates, []candidate{
		// ── CODE MGEX (AIUB Bern) — организованы по году, охватывают 2012–наст. ──
		{"AIUB_CODE_MGEX_FINAL", "",
			fmt.Sprintf("http://ftp.aiub.unibe.ch/CODE_MGEX/CODE/%d/COD0MGXFIN_%d%03d0000_01D_30S_CLK.CLK.gz",
				year, year, doy)},
		{"AIUB_CODE_MGEX_RAPID", "",
			fmt.Sprintf("http://ftp.aiub.unibe.ch/CODE_MGEX/CODE/%d/COD0MGXRAP_%d%03d0000_01D_30S_CLK.CLK.gz",
				year, year, doy)},
		// ── Старый формат (до ~2022, например неделя 2086) ──────────────────
		{"CDDIS_OLD_FINAL", ftpWeekDir,
			fmt.Sprintf("igs%d%d.clk_30s.Z", week, dow)},
		{"CDDIS_OLD_RAPID", ftpWeekDir,
			fmt.Sprintf("igr%d%d.clk.Z", week, dow)},
		{"BKG_OLD_FINAL", "",
			fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d/igs%d%d.clk.gz", week, week, dow)},
		{"BKG_OLD_RAPID", "",
			fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d/igr%d%d.clk.gz", week, week, dow)},
	}...)

	var lastErr error
	for _, c := range candidates {
		var err error
		if c.ftpDir != "" {
			err = d.downloadFTP(c.ftpDir+"/"+c.url, localFile)
		} else {
			err = d.downloadFile(c.url, localFile)
		}
		if err != nil {
			d.logger.Warnf("Failed to download %s CLK: %v", c.label, err)
			lastErr = err
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return "", fmt.Errorf("failed to download precise clock: %w", lastErr)
	}

	unpacked := strings.TrimSuffix(strings.TrimSuffix(localFile, ".gz"), ".Z")
	if err := d.decompressFile(localFile, unpacked); err != nil {
		return "", fmt.Errorf("failed to unpack CLK: %w", err)
	}

	d.logger.Infof("Downloaded precise clock: %s", unpacked)
	os.Remove(localFile)
	return unpacked, nil
}

// DownloadERP скачивает параметры вращения Земли (ERP).
// Новый формат (IGS3, после ~2022): файлы индексируются по DOY воскресенья
// GPS-недели (не дня наблюдения!). Воскресенье может быть в другом году.
// Старый формат (до ~2022): COD{WWWW}{D}.ERP.Z на AIUB (HTTP, без авторизации).
func (d *FileDownloader) DownloadERP(date time.Time, taskID string) (string, error) {
	week, dow := getGPSWeekAndDOW(date)
	sunYear, sunDOY := getWeekSunday(date)

	localFile := filepath.Join(d.workDir, taskID, fmt.Sprintf("%s_erp.erp.gz", taskID))

	ftpWeekDir := fmt.Sprintf("/gnss/products/%d", week)

	type erpCandidate struct {
		label  string
		ftpDir string
		url    string
	}

	year, doy := getYearDay(date)

	candidates := []erpCandidate{
		// ── AIUB CODE MGEX FINAL: суточные, DOY наблюдения — самый надёжный ──
		{"AIUB_COD_MGEX_FINAL", "",
			fmt.Sprintf("http://ftp.aiub.unibe.ch/CODE_MGEX/CODE/%d/COD0MGXFIN_%d%03d0000_01D_12H_ERP.ERP.gz",
				year, year, doy)},
		// ── BKG IGS FINAL: недельный, DOY воскресенья ────────────────────────
		{"BKG_IGS_FINAL", "",
			fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSFIN_%d%03d0000_07D_01D_ERP.ERP.gz",
				week, sunYear, sunDOY)},
		{"CDDIS_IGS_FINAL", ftpWeekDir,
			fmt.Sprintf("IGS0OPSFIN_%d%03d0000_07D_01D_ERP.ERP.gz", sunYear, sunDOY)},
		// ── BKG IGS RAPID: суточные, DOY наблюдения ──────────────────────────
		{"BKG_IGS_RAPID", "",
			fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSRAP_%d%03d0000_01D_01D_ERP.ERP.gz",
				week, year, doy)},
		{"CDDIS_IGS_RAPID", ftpWeekDir,
			fmt.Sprintf("IGS0OPSRAP_%d%03d0000_01D_01D_ERP.ERP.gz", year, doy)},
		// ── Старый формат (до ~2022): COD{WWWW}{D}.ERP.Z на AIUB ─────────────
		{"AIUB_COD_OLD", "",
			fmt.Sprintf("http://ftp.aiub.unibe.ch/CODE/%d/COD%d%d.ERP.Z",
				date.Year(), week, dow)},
	}

	var lastErr error
	for _, c := range candidates {
		var err error
		if c.ftpDir != "" {
			err = d.downloadFTP(c.ftpDir+"/"+c.url, localFile)
		} else {
			err = d.downloadFile(c.url, localFile)
		}
		if err != nil {
			d.logger.Warnf("Failed to download %s ERP: %v", c.label, err)
			lastErr = err
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return "", fmt.Errorf("failed to download ERP: %w", lastErr)
	}

	unpacked := strings.TrimSuffix(strings.TrimSuffix(localFile, ".gz"), ".Z")
	if err := d.decompressFile(localFile, unpacked); err != nil {
		return "", fmt.Errorf("failed to unpack ERP: %w", err)
	}

	d.logger.Infof("Downloaded ERP: %s", unpacked)
	os.Remove(localFile)
	return unpacked, nil
}

// DownloadDCB скачивает Differential Code Bias.
// Новый формат (после ~2020): CAS0OPSRAP_YYYYDDD..._DCB.BIA.gz на BKG
// в папке /root_ftp/IGS/products/{week}/ — без авторизации.
// Старый формат (до ~2020): COM{WWWW}{D}.DCB.Z на AIUB — по GPS-неделе и DOW.
func (d *FileDownloader) DownloadDCB(date time.Time, taskID string) (string, error) {
	year, doy := getYearDay(date)

	gzFile := filepath.Join(d.workDir, taskID, fmt.Sprintf("%s_dcb.bsx.gz", taskID))
	outFile := filepath.Join(d.workDir, taskID, fmt.Sprintf("%s_dcb.bsx", taskID))

	ftpDir := fmt.Sprintf("/gnss/products/bias/%d", year)

	type candidate struct {
		label   string
		ftpPath string
		httpURL string
	}

	candidates := []candidate{
		{"CDDIS_CAS",
			ftpDir + fmt.Sprintf("/CAS0OPSRAP_%d%03d0000_01D_01D_DCB.BIA.gz", year, doy),
			""},
		{"CDDIS_CODE",
			ftpDir + fmt.Sprintf("/COD0OPSFIN_%d%03d0000_01D_01D_DCB.BIA.gz", year, doy),
			""},
		{"CAS_HTTP",
			"",
			fmt.Sprintf("https://ftp.gipp.org.cn/product/dcb/mgex/%d/CAS0OPSRAP_%d%03d0000_01D_01D_DCB.BIA.gz",
				year, year, doy)},
	}

	var lastErr error
	for _, c := range candidates {
		var err error
		if c.ftpPath != "" {
			err = d.downloadFTP(c.ftpPath, gzFile)
		} else {
			err = d.downloadFile(c.httpURL, gzFile)
		}
		if err != nil {
			d.logger.Warnf("Failed to download %s DCB: %v", c.label, err)
			lastErr = err
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return "", fmt.Errorf("failed to download DCB: %w", lastErr)
	}

	if err := d.gunzipFile(gzFile, outFile); err != nil {
		return "", fmt.Errorf("failed to unpack DCB: %w", err)
	}

	os.Remove(gzFile)
	d.logger.Infof("Downloaded DCB: %s", outFile)
	return outFile, nil
}

// DownloadBIA скачивает файл фазовых смещений (BIA/OSB) для PPP-AR.
// Источник: CDDIS FTP (gdc.cddis.eosdis.nasa.gov:21, anonymous).
func (d *FileDownloader) DownloadBIA(date time.Time, taskID string) (string, error) {
	week, _ := getGPSWeekAndDOW(date)
	year, doy := getYearDay(date)

	gzFile := filepath.Join(d.workDir, taskID, fmt.Sprintf("%s_bia.bia.gz", taskID))
	outFile := filepath.Join(d.workDir, taskID, fmt.Sprintf("%s_bia.bia", taskID))

	// MGEX-продукты (COD0MGX*, GRG0MGX*, WUM0MGX*) на CDDIS лежат в
	// /gnss/products/mgex/{week}/; стандартные IGS-продукты (COD0OPS*)
	// — в /gnss/products/{week}/.
	opsDir := fmt.Sprintf("/gnss/products/%d", week)
	mgexDir := fmt.Sprintf("/gnss/products/mgex/%d", week)

	type biaCandidate struct {
		label   string
		ftpPath string // если не пусто — FTP CDDIS
		httpURL string
	}

	candidates := []biaCandidate{
		// ── Финальный CODE MGEX (задержка ~2 недели) ──
		{label: "CODE_MGX_FIN",
			ftpPath: fmt.Sprintf("%s/COD0MGXFIN_%d%03d0000_01D_01D_OSB.BIA.gz", mgexDir, year, doy)},
		// ── Rapid/Ultra источники для PPP-AR день-в-день ──
		// CODE rapid OSB (~1 сутки задержки)
		{label: "CODE_OPS_RAP",
			ftpPath: fmt.Sprintf("%s/COD0OPSRAP_%d%03d0000_01D_01D_OSB.BIA.gz", opsDir, year, doy)},
		{label: "CODE_MGX_RAP",
			ftpPath: fmt.Sprintf("%s/COD0MGXRAP_%d%03d0000_01D_01D_OSB.BIA.gz", mgexDir, year, doy)},
		{label: "AIUB_CODE_RAP",
			httpURL: fmt.Sprintf("http://ftp.aiub.unibe.ch/CODE_MGEX/CODE/%d/COD0MGXRAP_%d%03d0000_01D_01D_OSB.BIA.gz",
				year, year, doy)},
		// CNES/CLS GRG rapid — целевой продукт для PPP-AR (integer WL/NL biases)
		{label: "GRG_RAP",
			ftpPath: fmt.Sprintf("%s/GRG0MGXRAP_%d%03d0000_01D_01D_OSB.BIA.gz", mgexDir, year, doy)},
	}

	// WUM (Wuhan) Ultra BIA — свежайшие фазовые смещения, публикуются вместе
	// с WUM Ultra SP3/CLK, поддерживают PPP-AR по multi-GNSS.
	for _, u := range ultraRapidIssues(date) {
		uWeek, _ := getGPSWeekAndDOW(time.Date(u.year, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, u.doy-1))
		candidates = append(candidates, biaCandidate{
			label: fmt.Sprintf("WUM_ULTRA_%02d", u.hour),
			ftpPath: fmt.Sprintf("/gnss/products/mgex/%d/WUM0MGXULT_%d%03d%02d00_01D_01D_OSB.BIA.gz",
				uWeek, u.year, u.doy, u.hour),
		})
	}

	var lastErr error
	for _, c := range candidates {
		var err error
		if c.ftpPath != "" {
			err = d.downloadFTP(c.ftpPath, gzFile)
		} else {
			err = d.downloadFile(c.httpURL, gzFile)
		}
		if err != nil {
			d.logger.Warnf("Failed to download %s BIA: %v", c.label, err)
			lastErr = err
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return "", fmt.Errorf("failed to download BIA: %w", lastErr)
	}

	if err := d.gunzipFile(gzFile, outFile); err != nil {
		return "", fmt.Errorf("failed to unpack BIA: %w", err)
	}

	os.Remove(gzFile)
	d.logger.Infof("Downloaded BIA: %s", outFile)
	return outFile, nil
}

// downloadFile скачивает файл по URL.
// Проверяет Content-Type — CDDIS без авторизации возвращает text/html при HTTP 200.
func (d *FileDownloader) downloadFile(url, destPath string) error {
	d.logger.Infof("Downloading: %s", url)

	resp, err := d.client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/html") {
		return fmt.Errorf("server returned HTML (auth required?): %s", url)
	}

	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	written, err := io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	if written == 0 {
		os.Remove(destPath)
		return fmt.Errorf("empty response from: %s", url)
	}

	d.logger.Infof("Downloaded %s (%d bytes)", destPath, written)
	return nil
}

// gunzipFile распаковывает gzip файл.
// При невалидном gzip (например HTML вместо архива) удаляет исходный файл.
func (d *FileDownloader) gunzipFile(src, dst string) error {
	reader, err := os.Open(src)
	if err != nil {
		return err
	}
	defer reader.Close()

	gzReader, err := gzip.NewReader(reader)
	if err != nil {
		os.Remove(src)
		return fmt.Errorf("not a valid gzip %s: %w", src, err)
	}
	defer gzReader.Close()

	writer, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer writer.Close()

	_, err = io.Copy(writer, gzReader)
	if err != nil {
		return err
	}

	d.logger.Infof("Unpacked: %s -> %s", src, dst)
	return nil
}

// uncompressZ распаковывает файл в формате Unix compress (.Z) через системный uncompress/gunzip.
// Используется для старых продуктов IGS (DCB до ~2018).
func (d *FileDownloader) uncompressZ(src, dst string) error {
	// Пробуем uncompress (стандартный инструмент Unix compress)
	cmd := exec.Command("uncompress", "-c", src)
	out, err := cmd.Output()
	if err != nil {
		// Fallback: некоторые системы распаковывают .Z через gzip -d
		cmd2 := exec.Command("gzip", "-dc", src)
		out, err = cmd2.Output()
		if err != nil {
			return fmt.Errorf("cannot decompress .Z file %s: %w", src, err)
		}
	}
	if err := os.WriteFile(dst, out, 0644); err != nil {
		return fmt.Errorf("write decompressed DCB: %w", err)
	}
	d.logger.Infof("Unpacked .Z: %s -> %s (%d bytes)", src, dst, len(out))
	return nil
}

// decompressFile автоматически определяет формат сжатия по магическим байтам
// и распаковывает файл: 0x1f 0x8b → gzip, 0x1f 0x9d → Unix compress (.Z).
func (d *FileDownloader) decompressFile(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open for magic detection %s: %w", src, err)
	}
	magic := make([]byte, 2)
	_, err = f.Read(magic)
	f.Close()
	if err != nil {
		return fmt.Errorf("read magic bytes %s: %w", src, err)
	}

	switch {
	case magic[0] == 0x1f && magic[1] == 0x8b:
		return d.gunzipFile(src, dst)
	case magic[0] == 0x1f && magic[1] == 0x9d:
		return d.uncompressZ(src, dst)
	default:
		return fmt.Errorf("unknown compression format (magic %02x %02x) for %s", magic[0], magic[1], src)
	}
}

// DownloadBaseStation скачивает наблюдения с базовой станции
// Автоматически конвертирует CRX в RINEX если необходимо
func (d *FileDownloader) DownloadBaseStation(stationID string, date time.Time, taskID string, converter *ConverterService) (string, error) {
	year, doy := getYearDay(date)
	stationID = strings.ToUpper(stationID)

	// Пробуем RINEX 2.x формат .o
	urlRinex2 := fmt.Sprintf("https://cddis.nasa.gov/archive/gnss/data/daily/%d/%03d/%s%03d0.%02do.gz",
		year, doy, strings.ToLower(stationID), doy, year%100)

	// Пробуем Hatanaka сжатый формат .crx
	urlCrx := fmt.Sprintf("https://cddis.nasa.gov/archive/gnss/data/daily/%d/%03d/%s%03d0.%02dcrx.gz",
		year, doy, strings.ToLower(stationID), doy, year%100)

	filename := filepath.Join(d.workDir, taskID, fmt.Sprintf("%s_base", taskID))
	gzFile := filename + ".gz"

	// Сначала пробуем скачать .crx.gz
	err := d.downloadFile(urlCrx, gzFile)
	if err == nil {
		d.logger.Infof("Downloaded CRX file: %s", gzFile)

		// Распаковываем gz
		crxFile := filename + ".crx"
		if err := d.gunzipFile(gzFile, crxFile); err != nil {
			return "", err
		}
		os.Remove(gzFile)

		// Конвертируем CRX в RNX
		rnxFile := filename + ".obs"
		if converter != nil {
			if err := converter.ConvertCRX2RNX(crxFile, rnxFile); err != nil {
				d.logger.Warnf("CRX conversion failed: %v, trying RINEX2 fallback", err)
				os.Remove(crxFile)
				// Пробуем RINEX2 формат
				return d.downloadBaseStationRinex2(urlRinex2, filename, taskID)
			}
		}
		os.Remove(crxFile)
		d.logger.Infof("Converted to RINEX: %s", rnxFile)
		return rnxFile, nil
	}

	// Fallback на RINEX 2.x
	return d.downloadBaseStationRinex2(urlRinex2, filename, taskID)
}

func (d *FileDownloader) downloadBaseStationRinex2(url, filename, taskID string) (string, error) {
	gzFile := filename + ".gz"

	if err := d.downloadFile(url, gzFile); err != nil {
		return "", fmt.Errorf("failed to download base station data: %w", err)
	}

	unpacked := filename + ".obs"
	if err := d.gunzipFile(gzFile, unpacked); err != nil {
		return "", err
	}
	os.Remove(gzFile)

	d.logger.Infof("Downloaded base station RINEX: %s", unpacked)
	return unpacked, nil
}
