package services

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
)

type FileDownloader struct {
	workDir string
	client  *http.Client
	logger  *zap.SugaredLogger
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

// DownloadBroadcastEphemeris скачивает широковещательные эфемериды (BRDC).
// Структура: /IGS/BRDC/YYYY/DDD/ — по году и DOY (не GPS неделя!)
func (d *FileDownloader) DownloadBroadcastEphemeris(date time.Time, taskID string) (string, error) {
	year, doy := getYearDay(date)

	localFile := filepath.Join(d.workDir, fmt.Sprintf("%s_brdc.rnx.gz", taskID))
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
// Имя файла содержит YYYY+DOY, но директория — 4-значная GPS неделя.
func (d *FileDownloader) DownloadPreciseEphemeris(date time.Time, taskID string) (string, error) {
	week, dow := getGPSWeekAndDOW(date)
	year, doy := getYearDay(date)

	localFile := filepath.Join(d.workDir, fmt.Sprintf("%s_sp3.sp3.gz", taskID))

	candidates := []struct {
		label string
		url   string
	}{
		{"FINAL", fmt.Sprintf(
			"https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSFIN_%d%03d0000_01D_15M_ORB.SP3.gz",
			week, year, doy)},
		{"RAPID", fmt.Sprintf(
			"https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSRAP_%d%03d0000_01D_15M_ORB.SP3.gz",
			week, year, doy)},
		{"ULTRA", fmt.Sprintf(
			"https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSULT_%d%03d0000_02D_15M_ORB.SP3.gz",
			week, year, doy)},
		// Fallback: старый формат имён (до реформы IGS3, GPS неделя+DOW в имени)
		{"RAPID_OLD", fmt.Sprintf(
			"https://igs.bkg.bund.de/root_ftp/IGS/products/%d/igr%d%d.sp3.gz",
			week, week, dow)},
	}

	var lastErr error
	for _, c := range candidates {
		if err := d.downloadFile(c.url, localFile); err != nil {
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

	unpacked := localFile[:len(localFile)-3]
	if err := d.gunzipFile(localFile, unpacked); err != nil {
		return "", fmt.Errorf("failed to unpack SP3: %w", err)
	}

	d.logger.Infof("Downloaded precise ephemeris: %s", unpacked)
	os.Remove(localFile)
	return unpacked, nil
}

// DownloadPreciseClock скачивает точные часы CLK для PPP.
// Структура та же: /IGS/products/WWWW/
func (d *FileDownloader) DownloadPreciseClock(date time.Time, taskID string) (string, error) {
	week, dow := getGPSWeekAndDOW(date)
	year, doy := getYearDay(date)

	localFile := filepath.Join(d.workDir, fmt.Sprintf("%s_clk.clk.gz", taskID))

	candidates := []struct {
		label string
		url   string
	}{
		{"FINAL", fmt.Sprintf(
			"https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSFIN_%d%03d0000_01D_30S_CLK.CLK.gz",
			week, year, doy)},
		{"RAPID", fmt.Sprintf(
			"https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSRAP_%d%03d0000_01D_05M_CLK.CLK.gz",
			week, year, doy)},
		{"ULTRA", fmt.Sprintf(
			"https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSULT_%d%03d0000_02D_05M_CLK.CLK.gz",
			week, year, doy)},
		{"RAPID_OLD", fmt.Sprintf(
			"https://igs.bkg.bund.de/root_ftp/IGS/products/%d/igr%d%d.clk.gz",
			week, week, dow)},
	}

	var lastErr error
	for _, c := range candidates {
		if err := d.downloadFile(c.url, localFile); err != nil {
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

	unpacked := localFile[:len(localFile)-3]
	if err := d.gunzipFile(localFile, unpacked); err != nil {
		return "", fmt.Errorf("failed to unpack CLK: %w", err)
	}

	d.logger.Infof("Downloaded precise clock: %s", unpacked)
	os.Remove(localFile)
	return unpacked, nil
}

// DownloadERP скачивает параметры вращения Земли (ERP).
// ERP — недельный файл, лежит в папке по GPS неделе.
func (d *FileDownloader) DownloadERP(date time.Time, taskID string) (string, error) {
	week, _ := getGPSWeekAndDOW(date)
	year, doy := getYearDay(date)

	localFile := filepath.Join(d.workDir, fmt.Sprintf("%s_erp.erp.gz", taskID))

	candidates := []struct {
		label string
		url   string
	}{
		{"FINAL", fmt.Sprintf(
			"https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSFIN_%d%03d0000_07D_01D_ERP.ERP.gz",
			week, year, doy)},
		{"RAPID", fmt.Sprintf(
			"https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSRAP_%d%03d0000_01D_01D_ERP.ERP.gz",
			week, year, doy)},
		{"ULTRA", fmt.Sprintf(
			"https://igs.bkg.bund.de/root_ftp/IGS/products/%d/IGS0OPSULT_%d%03d0000_02D_01D_ERP.ERP.gz",
			week, year, doy)},
		// Старый формат: igs{week}7.erp (недельный файл всегда имеет суффикс 7)
		{"OLD", fmt.Sprintf(
			"https://igs.bkg.bund.de/root_ftp/IGS/products/%d/igs%d7.erp.gz",
			week, week)},
	}

	var lastErr error
	for _, c := range candidates {
		if err := d.downloadFile(c.url, localFile); err != nil {
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

	unpacked := localFile[:len(localFile)-3]
	if err := d.gunzipFile(localFile, unpacked); err != nil {
		return "", fmt.Errorf("failed to unpack ERP: %w", err)
	}

	d.logger.Infof("Downloaded ERP: %s", unpacked)
	os.Remove(localFile)
	return unpacked, nil
}

// DownloadDCB скачивает Differential Code Bias.
// CDDIS требует регистрацию на Earthdata — используем открытый CAS/GIPP.
func (d *FileDownloader) DownloadDCB(date time.Time, taskID string) (string, error) {
	year, doy := getYearDay(date)

	gzFile := filepath.Join(d.workDir, fmt.Sprintf("%s_dcb.bsx.gz", taskID))
	outFile := filepath.Join(d.workDir, fmt.Sprintf("%s_dcb.bsx", taskID))

	url := fmt.Sprintf("https://ftp.gipp.org.cn/product/dcb/mgex/%d/CAS0MGXRAP_%d%03d0000_01D_01D_DCB.BSX.gz",
		year, year, doy)

	if err := d.downloadFile(url, gzFile); err != nil {
		d.logger.Warnf("Failed to download DCB from CAS: %v", err)
		return "", fmt.Errorf("failed to download DCB: %w", err)
	}

	if err := d.gunzipFile(gzFile, outFile); err != nil {
		return "", fmt.Errorf("failed to unpack DCB: %w", err)
	}

	os.Remove(gzFile)
	d.logger.Infof("Downloaded DCB: %s", outFile)
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

	filename := filepath.Join(d.workDir, fmt.Sprintf("%s_base", taskID))
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
