package parsers

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RINEXParser parses RINEX observation files
type RINEXParser struct{}

func NewRINEXParser() *RINEXParser {
	return &RINEXParser{}
}

// SNRRange представляет диапазон SNR значений
type SNRRange struct {
	Min float64
	Max float64
	Val int
}

// SNRInfo содержит информацию о SNR маппинге из RINEX заголовка
type SNRInfo struct {
	Present bool
	Ranges  []SNRRange
}

// ParseSNRMapping извлекает SNR маппинг из заголовка RINEX
func (p *RINEXParser) ParseSNRMapping(filePath string) *SNRInfo {
	f, err := os.Open(filePath)
	if err != nil {
		return &SNRInfo{Present: false}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var snrLines []string

	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "END OF HEADER") {
			break
		}
		// Ищем строки с SNR mapping
		if strings.Contains(line, "SNR is mapped to RINEX snr flag value") ||
			strings.Contains(line, "dBHz ->") {
			snrLines = append(snrLines, line)
		}
	}

	if len(snrLines) == 0 {
		return &SNRInfo{Present: false}
	}

	// Парсим все строки вместе
	fullText := strings.Join(snrLines, " ")
	info := &SNRInfo{Present: true}

	// Известные стандартные диапазоны
	knownRanges := []SNRRange{
		{Min: 0, Max: 12, Val: 1},
		{Min: 12, Max: 17, Val: 2},
		{Min: 18, Max: 23, Val: 3},
		{Min: 24, Max: 29, Val: 4},
		{Min: 30, Max: 35, Val: 5},
		{Min: 36, Max: 41, Val: 6},
		{Min: 42, Max: 47, Val: 7},
		{Min: 48, Max: 53, Val: 8},
		{Min: 54, Max: 999, Val: 9},
	}

	// Проверяем, соответствует ли текст стандартному формату
	if strings.Contains(fullText, "< 12dBHz -> 1") {
		info.Ranges = knownRanges
		return info
	}

	// Если формат нестандартный, пытаемся распарсить с помощью regexp
	re := regexp.MustCompile(`([<>]=?\s*\d+|\d+\s*-\s*\d+)\s*dBHz\s*->\s*(\d)`)
	matches := re.FindAllStringSubmatch(fullText, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			rangeStr := match[1]
			val, _ := strconv.Atoi(match[2])

			var min, max float64
			if strings.HasPrefix(rangeStr, "<") {
				maxStr := strings.TrimPrefix(rangeStr, "<")
				max, _ = strconv.ParseFloat(strings.TrimSpace(maxStr), 64)
				min = 0
			} else if strings.HasPrefix(rangeStr, ">=") {
				minStr := strings.TrimPrefix(rangeStr, ">=")
				min, _ = strconv.ParseFloat(strings.TrimSpace(minStr), 64)
				max = 999
			} else if strings.Contains(rangeStr, "-") {
				parts := strings.Split(rangeStr, "-")
				min, _ = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
				max, _ = strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
			}

			info.Ranges = append(info.Ranges, SNRRange{
				Min: min,
				Max: max,
				Val: val,
			})
		}
	}

	return info
}

// GetSNRMaskValues возвращает массив значений SNR маски для RTKLIB конфига
// на основе распарсенного SNR маппинга. Используется верхняя граница диапазона.
func (p *RINEXParser) GetSNRMaskValues(info *SNRInfo) []int {
	if !info.Present || len(info.Ranges) == 0 {
		return nil
	}

	// Для каждого значения 1-9 находим порог (верхнюю границу диапазона)
	thresholds := make([]int, 9)
	for i := range thresholds {
		thresholds[i] = 0
	}

	for _, r := range info.Ranges {
		if r.Val >= 1 && r.Val <= 9 {
			// Используем верхнюю границу диапазона как порог
			threshold := int(r.Max)
			// Для последнего диапазона (>=54) Max=999, но порог должен быть 54
			if r.Max > 100 {
				threshold = int(r.Min)
			}
			if threshold > thresholds[r.Val-1] {
				thresholds[r.Val-1] = threshold
			}
		}
	}

	return thresholds
}

// ParseObservationDate extracts the start date from a RINEX observation file header.
func (p *RINEXParser) ParseObservationDate(filePath string) (time.Time, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to open RINEX file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	// Переменные для хранения данных из разных частей заголовка
	var (
		pgmDate           time.Time
		timeOfFirstObs    time.Time
		hasTimeOfFirstObs bool
	)

	for scanner.Scan() {
		line := scanner.Text()

		if strings.Contains(line, "END OF HEADER") {
			break
		}

		// PGM / RUN BY / DATE - содержит дату создания/конвертации
		if strings.Contains(line, "PGM / RUN BY / DATE") {
			t, err := parsePGMDate(line)
			if err == nil {
				pgmDate = t
			}
		}

		// TIME OF FIRST OBS - приоритетный источник
		if strings.Contains(line, "TIME OF FIRST OBS") {
			t, err := parseTimeOfFirstObs(line)
			if err == nil {
				timeOfFirstObs = t
				hasTimeOfFirstObs = true
			}
		}

		// RINEX 2.11: DATE OF FIRST OBS (альтернативное название)
		if strings.Contains(line, "DATE OF FIRST OBS") {
			t, err := parseDateOfFirstObs(line)
			if err == nil {
				timeOfFirstObs = t
				hasTimeOfFirstObs = true
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return time.Time{}, fmt.Errorf("error reading RINEX file: %w", err)
	}

	// Приоритет: TIME OF FIRST OBS > PGM DATE > ошибка
	if hasTimeOfFirstObs {
		return timeOfFirstObs, nil
	}

	if !pgmDate.IsZero() {
		return pgmDate, nil
	}

	return time.Time{}, fmt.Errorf("no date found in RINEX header: %s", filePath)
}

func parsePGMDate(line string) (time.Time, error) {
	// Ищем паттерн YYYYMMDD
	fields := strings.Fields(line)
	for i, field := range fields {
		// Пропускаем короткие поля
		if len(field) < 8 {
			continue
		}

		// Проверяем, является ли поле датой в формате YYYYMMDD
		if len(field) == 8 || (len(field) > 8 && isNumeric(field[:8])) {
			dateStr := field
			if len(dateStr) > 8 {
				dateStr = dateStr[:8]
			}

			year, err1 := strconv.Atoi(dateStr[0:4])
			month, err2 := strconv.Atoi(dateStr[4:6])
			day, err3 := strconv.Atoi(dateStr[6:8])

			if err1 == nil && err2 == nil && err3 == nil {
				if year >= 1980 && year <= 2100 && month >= 1 && month <= 12 && day >= 1 && day <= 31 {
					// Ищем время (HHMMSS) в следующем поле
					hour, minute, second := 0, 0, 0
					if i+1 < len(fields) && len(fields[i+1]) >= 6 {
						timeField := fields[i+1]
						if len(timeField) >= 2 {
							hour, _ = strconv.Atoi(timeField[0:2])
						}
						if len(timeField) >= 4 {
							minute, _ = strconv.Atoi(timeField[2:4])
						}
						if len(timeField) >= 6 {
							second, _ = strconv.Atoi(timeField[4:6])
						}
					}
					return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC), nil
				}
			}
		}
	}

	return time.Time{}, fmt.Errorf("no valid date found in PGM line: %s", line)
}

// parseDateOfFirstObs парсит DATE OF FIRST OBS (RINEX 2.11)
func parseDateOfFirstObs(line string) (time.Time, error) {
	fields := strings.Fields(line)
	for i, field := range fields {
		// Ищем YYYY MM DD HH MM SS
		if len(field) == 4 && isNumeric(field) {
			year, _ := strconv.Atoi(field)
			if year >= 1980 && year <= 2100 && i+5 < len(fields) {
				month, _ := strconv.Atoi(fields[i+1])
				day, _ := strconv.Atoi(fields[i+2])
				hour, _ := strconv.Atoi(fields[i+3])
				minute, _ := strconv.Atoi(fields[i+4])
				second := 0
				if f, err := strconv.ParseFloat(fields[i+5], 64); err == nil {
					second = int(f)
				}

				if month >= 1 && month <= 12 && day >= 1 && day <= 31 {
					return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC), nil
				}
			}
		}
	}
	return time.Time{}, fmt.Errorf("failed to parse DATE OF FIRST OBS")
}

// parseTimeOfFirstObs parses a "TIME OF FIRST OBS" header line.
func parseTimeOfFirstObs(line string) (time.Time, error) {
	if len(line) < 43 {
		return time.Time{}, fmt.Errorf("line too short")
	}

	fields := strings.Fields(line[:48])
	if len(fields) < 3 {
		return time.Time{}, fmt.Errorf("not enough fields")
	}

	year, err := strconv.Atoi(fields[0])
	if err != nil {
		return time.Time{}, err
	}
	month, err := strconv.Atoi(fields[1])
	if err != nil {
		return time.Time{}, err
	}
	day, err := strconv.Atoi(fields[2])
	if err != nil {
		return time.Time{}, err
	}

	hour, minute, second := 0, 0, 0
	if len(fields) >= 4 {
		hour, _ = strconv.Atoi(fields[3])
	}
	if len(fields) >= 5 {
		minute, _ = strconv.Atoi(fields[4])
	}
	if len(fields) >= 6 {
		f, _ := strconv.ParseFloat(fields[5], 64)
		second = int(f)
	}

	if year < 1980 || year > 2100 || month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, fmt.Errorf("invalid date: %d-%d-%d", year, month, day)
	}

	return time.Date(year, time.Month(month), day, hour, minute, second, 0, time.UTC), nil
}

// ParseApproxPosition извлекает APPROX POSITION XYZ из заголовка RINEX,
// конвертирует ECEF → геодезические координаты WGS84.
// Возвращает (lat, lon) в градусах и found=true при успехе.
func (p *RINEXParser) ParseApproxPosition(filePath string) (lat, lon float64, found bool) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "END OF HEADER") {
			break
		}
		if strings.Contains(line, "APPROX POSITION XYZ") {
			// RINEX фиксированные колонки: X[0:14], Y[14:28], Z[28:42]
			if len(line) < 42 {
				continue
			}
			x, err1 := strconv.ParseFloat(strings.TrimSpace(line[0:14]), 64)
			y, err2 := strconv.ParseFloat(strings.TrimSpace(line[14:28]), 64)
			z, err3 := strconv.ParseFloat(strings.TrimSpace(line[28:42]), 64)
			if err1 != nil || err2 != nil || err3 != nil {
				continue
			}
			if x == 0 && y == 0 && z == 0 {
				continue // неинициализированная позиция
			}
			lat, lon = ecefToGeodetic(x, y, z)
			return lat, lon, true
		}
	}
	return 0, 0, false
}

// ParseMarkerName возвращает MARKER NAME из заголовка RINEX.
func (p *RINEXParser) ParseMarkerName(filePath string) string {
	f, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "END OF HEADER") {
			break
		}
		if strings.Contains(line, "MARKER NAME") {
			end := len(line)
			if idx := strings.Index(line, "MARKER NAME"); idx > 0 {
				end = idx
			}
			if name := strings.TrimSpace(line[:end]); name != "" {
				return name
			}
		}
	}
	return ""
}

// ecefToGeodetic конвертирует ECEF-координаты в геодезические (WGS84).
// Использует итерационный алгоритм Боуринга.
func ecefToGeodetic(x, y, z float64) (lat, lon float64) {
	const (
		a  = 6378137.0           // большая полуось WGS84 (м)
		f  = 1.0 / 298.257223563 // сжатие WGS84
		e2 = 2*f - f*f           // квадрат первого эксцентриситета
	)

	lon = math.Atan2(y, x) * (180.0 / math.Pi)

	p := math.Sqrt(x*x + y*y)
	latRad := math.Atan2(z, p*(1-e2))

	for i := 0; i < 10; i++ {
		sinLat := math.Sin(latRad)
		N := a / math.Sqrt(1-e2*sinLat*sinLat)
		next := math.Atan2(z+e2*N*sinLat, p)
		if math.Abs(next-latRad) < 1e-12 {
			latRad = next
			break
		}
		latRad = next
	}

	lat = latRad * (180.0 / math.Pi)
	return
}

// isNumeric проверяет, состоит ли строка только из цифр
func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
