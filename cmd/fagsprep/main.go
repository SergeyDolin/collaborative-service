// cmd/fagsprep/main.go — скачивает все суточные продукты CODE/IGS для последующей
// обработки пунктов ФАГС. Не требует VPN/доступа к API РГС.
//
// Использование:
//
//	fagsprep --start-date 2025-01-01 --end-date 2025-01-31 --work-dir /data/fagsbatch
//
// После завершения запустить fagsbatch с тем же --work-dir.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"collaborative/internal/services"

	"go.uber.org/zap"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func buildDates(start, end time.Time, step int) []time.Time {
	var dates []time.Time
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if (d.YearDay()-1)%step == 0 {
			dates = append(dates, d)
		}
	}
	return dates
}

func fileReady(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	startDateStr := flag.String("start-date", "2025-01-01", "Начало периода YYYY-MM-DD")
	endDateStr := flag.String("end-date", "2025-01-31", "Конец периода YYYY-MM-DD")
	sampleStep := flag.Int("sample-step", 1, "Шаг выборки (1 = каждый день)")
	workers := flag.Int("workers", 4, "Параллельных загрузок (дней)")
	defaultWorkDir := os.Getenv("HOME") + "/fagsbatch_data"
	workDir := flag.String("work-dir", defaultWorkDir, "Рабочая директория (та же, что у fagsbatch)")
	flag.Parse()

	zapCfg := zap.NewDevelopmentConfig()
	zapCfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	logger, _ := zapCfg.Build()
	sugar := logger.Sugar()
	zap.ReplaceGlobals(logger)
	defer logger.Sync()

	startDate, _ := time.Parse("2006-01-02", *startDateStr)
	endDate, _ := time.Parse("2006-01-02", *endDateStr)
	if startDate.IsZero() || endDate.IsZero() {
		sugar.Fatal("Неверный формат дат (ожидается YYYY-MM-DD)")
	}

	if err := os.MkdirAll(*workDir, 0755); err != nil {
		sugar.Fatalf("Не удалось создать work-dir: %v", err)
	}

	fd := services.NewFileDownloader(*workDir, sugar)
	dates := buildDates(startDate, endDate, *sampleStep)

	sugar.Infof("Период: %s – %s (%d дней)",
		startDate.Format("2006-01-02"), endDate.Format("2006-01-02"), len(dates))
	sugar.Infof("Рабочая директория: %s", *workDir)
	sugar.Infof("Параллельных загрузок: %d", *workers)

	sem := make(chan struct{}, *workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var okCount, failCount, skipCount int

	totalStart := time.Now()

	for _, date := range dates {
		d := date
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			taskID := "daily_" + d.Format("20060102")
			sp3 := filepath.Join(*workDir, taskID, taskID+"_sp3.sp3")
			clk := filepath.Join(*workDir, taskID, taskID+"_clk.clk")

			// Пропускаем, если оба ключевых файла уже есть
			if fileReady(sp3) && fileReady(clk) {
				sugar.Infof("%s: уже скачано, пропускаем", d.Format("2006-01-02"))
				mu.Lock()
				skipCount++
				mu.Unlock()
				return
			}

			type product struct{ name, path string }
			var downloaded []product
			var errs []string

			if sp3Path, err := fd.DownloadPreciseEphemeris(d, taskID); err != nil {
				errs = append(errs, "SP3:"+err.Error())
			} else {
				downloaded = append(downloaded, product{"SP3", sp3Path})
			}

			if clkPath, err := fd.DownloadPreciseClock(d, taskID); err != nil {
				errs = append(errs, "CLK:"+err.Error())
			} else {
				downloaded = append(downloaded, product{"CLK", clkPath})
			}

			if erpPath, err := fd.DownloadERP(d, taskID); err != nil {
				sugar.Debugf("%s ERP: %v", d.Format("2006-01-02"), err)
			} else {
				downloaded = append(downloaded, product{"ERP", erpPath})
			}

			if biaPath, err := fd.DownloadBIA(d, taskID); err != nil {
				sugar.Debugf("%s BIA: %v", d.Format("2006-01-02"), err)
			} else {
				downloaded = append(downloaded, product{"BIA", biaPath})
			}

			if dcbPath, err := fd.DownloadDCB(d, taskID); err != nil {
				sugar.Debugf("%s DCB: %v", d.Format("2006-01-02"), err)
			} else {
				downloaded = append(downloaded, product{"DCB", dcbPath})
			}

			if brdcPath, err := fd.DownloadBroadcastEphemeris(d, taskID); err != nil {
				sugar.Debugf("%s BRDC: %v", d.Format("2006-01-02"), err)
			} else {
				downloaded = append(downloaded, product{"BRDC", brdcPath})
			}

			mu.Lock()
			defer mu.Unlock()

			if len(errs) > 0 {
				sugar.Warnf("%s ОШИБКА: %s", d.Format("2006-01-02"), strings.Join(errs, "; "))
				failCount++
				return
			}

			names := make([]string, 0, len(downloaded))
			for _, p := range downloaded {
				names = append(names, p.name)
			}
			sugar.Infof("%s: %s", d.Format("2006-01-02"), strings.Join(names, " "))
			okCount++
		}()
	}

	wg.Wait()

	elapsed := time.Since(totalStart)
	sep := strings.Repeat("─", 60)
	fmt.Printf("\n%s\n  fagsprep завершён за %.0f сек\n", sep, elapsed.Seconds())
	fmt.Printf("  Скачано: %d дней  |  Пропущено: %d  |  Ошибок: %d\n", okCount, skipCount, failCount)
	fmt.Printf("  Данные в: %s\n%s\n", *workDir, sep)
	fmt.Printf("\nЗапустите fagsbatch с тем же --work-dir:\n")
	fmt.Printf("  ./fagsbatch --work-dir %s --start-date %s --end-date %s\n\n",
		*workDir, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
}
