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

// getGPSWeekAndDOW возвращает GPS неделю и день недели для заданной даты
func getGPSWeekAndDOW(date time.Time) (week int, dow int) {
	// GPS эпоха: 6 января 1980 года
	gpsEpoch := time.Date(1980, 1, 6, 0, 0, 0, 0, time.UTC)
	days := int(date.Sub(gpsEpoch).Hours() / 24)
	week = days / 7
	dow = days % 7
	return
}

// getYearDay возвращает год и день года (DOY)
func getYearDay(date time.Time) (year int, doy int) {
	year = date.Year()
	doy = date.YearDay()
	return
}

// DownloadBroadcastEphemeris скачивает широковещательные эфемериды (BRDC)
func (d *FileDownloader) DownloadBroadcastEphemeris(date time.Time, taskID string) (string, error) {
	year, doy := getYearDay(date)

	// Формат: BRDC00IGS_R_YYYYDOY0000_01D_MN.rnx.gz
	filename := fmt.Sprintf("BRDC00WRD_R_%d%03d0000_01D_MN.rnx.gz", year, doy)
	url := fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/BRDC/%d/%03d/%s", year, doy, filename)

	localFile := filepath.Join(d.workDir, fmt.Sprintf("%s_brdc.rnx.gz", taskID))

	if err := d.downloadFile(url, localFile); err != nil {
		d.logger.Warnf("Failed to download from BKG: %v, trying CDDIS...", err)
		// Fallback на CDDIS
		url = fmt.Sprintf("https://cddis.nasa.gov/archive/gnss/data/daily/%d/brdc/brdc%03d0.%02dn.gz",
			year, doy, year%100)
		if err := d.downloadFile(url, localFile); err != nil {
			return "", fmt.Errorf("failed to download broadcast ephemeris: %w", err)
		}
	}

	// Распаковываем
	unpacked := filename[:len(filename)-3]
	if err := d.gunzipFile(filename, unpacked); err != nil {
		return "", err
	}

	d.logger.Infof("Downloaded broadcast ephemeris: %s", unpacked)
	os.Remove(filename)

	return unpacked, nil
}

// DownloadPreciseEphemeris скачивает точные эфемериды (SP3) для PPP
func (d *FileDownloader) DownloadPreciseEphemeris(date time.Time, taskID string) (string, error) {
	year, doy := getYearDay(date)

	// Формат для финальных продуктов (FIN): IGS0OPSFIN_YYYYDOY0000_01D_15M_ORB.SP3.gz
	// Формат для быстрых продуктов (RAP): IGS0OPSRAP_YYYYDOY0000_01D_15M_ORB.SP3.gz

	// Пробуем скачать финальные продукты (доступны с задержкой ~12-14 дней)
	url := fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d%03d/IGS0OPSFIN_%d%03d0000_01D_15M_ORB.SP3.gz",
		year, doy, year, doy)

	localFile := filepath.Join(d.workDir, fmt.Sprintf("%s_sp3.sp3.gz", taskID))

	err := d.downloadFile(url, localFile)
	if err != nil {
		d.logger.Warnf("Failed to download FINAL ephemeris: %v, trying RAPID...", err)

		// Пробуем быстрые продукты (доступны с задержкой ~1 день)
		url = fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d%03d/IGS0OPSRAP_%d%03d0000_01D_15M_ORB.SP3.gz",
			year, doy, year, doy)

		if err := d.downloadFile(url, localFile); err != nil {
			// Пробуем ультра-быстрые продукты
			url = fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d%03d/IGS0OPSULT_%d%03d0000_02D_15M_ORB.SP3.gz",
				year, doy, year, doy)

			if err := d.downloadFile(url, localFile); err != nil {
				return "", fmt.Errorf("failed to download precise ephemeris: %w", err)
			}
		}
	}

	// Распаковываем
	unpacked := localFile[:len(localFile)-3]
	if err := d.gunzipFile(localFile, unpacked); err != nil {
		return "", err
	}

	d.logger.Infof("Downloaded precise ephemeris: %s", unpacked)
	return unpacked, nil
}

// DownloadPreciseClock скачивает точные часы (CLK) для PPP
func (d *FileDownloader) DownloadPreciseClock(date time.Time, taskID string) (string, error) {
	year, doy := getYearDay(date)

	// Пробуем скачать финальные часы (30s интервал)
	url := fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d%03d/IGS0OPSFIN_%d%03d0000_01D_30S_CLK.CLK.gz",
		year, doy, year, doy)

	localFile := filepath.Join(d.workDir, fmt.Sprintf("%s_clk.clk.gz", taskID))

	err := d.downloadFile(url, localFile)
	if err != nil {
		d.logger.Warnf("Failed to download FINAL clock: %v, trying RAPID...", err)

		url = fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d%03d/IGS0OPSRAP_%d%03d0000_01D_05M_CLK.CLK.gz",
			year, doy, year, doy)

		if err := d.downloadFile(url, localFile); err != nil {
			// Пробуем ультра-быстрые часы
			url = fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d%03d/IGS0OPSULT_%d%03d0000_02D_05M_CLK.CLK.gz",
				year, doy, year, doy)

			if err := d.downloadFile(url, localFile); err != nil {
				return "", fmt.Errorf("failed to download precise clock: %w", err)
			}
		}
	}

	// Распаковываем
	unpacked := localFile[:len(localFile)-3]
	if err := d.gunzipFile(localFile, unpacked); err != nil {
		return "", err
	}

	d.logger.Infof("Downloaded precise clock: %s", unpacked)
	return unpacked, nil
}

// DownloadERP скачивает параметры вращения Земли (ERP) для PPP
func (d *FileDownloader) DownloadERP(date time.Time, taskID string) (string, error) {
	year, doy := getYearDay(date)

	// Пробуем скачать финальные ERP
	url := fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d%03d/IGS0OPSFIN_%d%03d0000_07D_01D_ERP.ERP.gz",
		year, doy, year, doy)

	localFile := filepath.Join(d.workDir, fmt.Sprintf("%s_erp.erp.gz", taskID))

	err := d.downloadFile(url, localFile)
	if err != nil {
		d.logger.Warnf("Failed to download FINAL ERP: %v, trying RAPID...", err)

		url = fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d%03d/IGS0OPSRAP_%d%03d0000_01D_01D_ERP.ERP.gz",
			year, doy, year, doy)

		if err := d.downloadFile(url, localFile); err != nil {
			// Пробуем ультра-быстрые ERP
			url = fmt.Sprintf("https://igs.bkg.bund.de/root_ftp/IGS/products/%d%03d/IGS0OPSULT_%d%03d0000_02D_01D_ERP.ERP.gz",
				year, doy, year, doy)

			if err := d.downloadFile(url, localFile); err != nil {
				return "", fmt.Errorf("failed to download ERP file: %w", err)
			}
		}
	}

	// Распаковываем
	unpacked := localFile[:len(localFile)-3]
	if err := d.gunzipFile(localFile, unpacked); err != nil {
		return "", err
	}

	d.logger.Infof("Downloaded ERP file: %s", unpacked)
	return unpacked, nil
}

// DownloadDCB скачивает файлы Differential Code Bias
func (d *FileDownloader) DownloadDCB(date time.Time, taskID string) (string, error) {
	year, doy := getYearDay(date)

	// DCB файлы от CAS
	url := fmt.Sprintf("https://cddis.nasa.gov/archive/gnss/products/bias/%d/p1p2%03d0.dcb", year, doy)

	filename := filepath.Join(d.workDir, fmt.Sprintf("%s_dcb.dcb", taskID))

	if err := d.downloadFile(url, filename); err != nil {
		d.logger.Warnf("Failed to download DCB file: %v", err)
		return "", err
	}

	d.logger.Infof("Downloaded DCB file: %s", filename)
	return filename, nil
}

// DownloadBaseStation скачивает наблюдения с базовой станции
func (d *FileDownloader) DownloadBaseStation(stationID string, date time.Time, taskID string) (string, error) {
	year, doy := getYearDay(date)
	stationID = strings.ToUpper(stationID)

	// Формат RINEX 2.x: ZIMM0010.24o.gz
	url := fmt.Sprintf("https://cddis.nasa.gov/archive/gnss/data/daily/%d/%03d/%s%03d0.%02do.gz",
		year, doy, strings.ToLower(stationID), doy, year%100)

	filename := filepath.Join(d.workDir, fmt.Sprintf("%s_base.obs.gz", taskID))

	if err := d.downloadFile(url, filename); err != nil {
		return "", fmt.Errorf("failed to download base station data: %w", err)
	}

	// Распаковываем
	unpacked := filename[:len(filename)-3]
	if err := d.gunzipFile(filename, unpacked); err != nil {
		return "", err
	}

	d.logger.Infof("Downloaded base station data: %s", unpacked)
	return unpacked, nil
}

// downloadFile скачивает файл по URL
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

	// Создаем директорию если нужно
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	d.logger.Infof("Downloaded %s (%d bytes)", destPath, resp.ContentLength)
	return nil
}

// gunzipFile распаковывает gzip файл
func (d *FileDownloader) gunzipFile(src, dst string) error {
	reader, err := os.Open(src)
	if err != nil {
		return err
	}
	defer reader.Close()

	gzReader, err := gzip.NewReader(reader)
	if err != nil {
		return err
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
