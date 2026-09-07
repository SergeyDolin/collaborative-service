package parsers

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
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

// ComputeSNRMaskFromObservations сканирует тело RINEX 3 файла и вычисляет пороги SNR
// для полос L1 и L5 (плюс L2, если присутствует), когда SNR-маппинг отсутствует в заголовке.
//
// Возвращает строки-маски вида "T,T,T,T,T,T,T,T,T" (9 значений на 9 углов возвышения),
// пригодные для параметров RTKLIB pos1-snrmask_L1/L2/L5.
//
// Порог берётся как 5-й перцентиль минус запас 2 dBHz, чтобы отсечь только явный шумовой
// хвост распределения (мультипас/срыв слежения), сохранив основную массу слабых, но валидных
// наблюдений — на смартфонах их количество критично для решения.
//
// ok=false, если формат не RINEX 3 или в теле не набралось достаточно значений (< 30 на полосу).
func (p *RINEXParser) ComputeSNRMaskFromObservations(filePath string) (l1Mask, l2Mask, l5Mask string, ok bool) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", "", "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// В теле файла строка satRecord может быть длинной; поднимаем лимит.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Индексы позиций (обзвон 16-символьных полей) для колонок S1x/S2x/S5x по системам.
	// Ключ — символ системы ('G','R','E','J','C','I','S'); значение — индексы полей.
	snr1 := make(map[byte][]int)
	snr2 := make(map[byte][]int)
	snr5 := make(map[byte][]int)

	var (
		currentSys        byte
		remainingObsTypes int
		obsIdx            int
		inHeader          = true
		version3          bool
	)

	// Проход по заголовку: собираем индексы S1x/S2x/S5x per system.
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "RINEX VERSION / TYPE") {
			// Первая колонка — версия. RINEX 3 если начинается с "3".
			ver := strings.TrimSpace(line[:9])
			if strings.HasPrefix(ver, "3") {
				version3 = true
			}
		}
		if strings.Contains(line, "SYS / # / OBS TYPES") {
			if !version3 {
				continue
			}
			// Начало или продолжение блока для системы.
			if remainingObsTypes == 0 {
				if len(line) < 6 {
					continue
				}
				currentSys = line[0]
				n, _ := strconv.Atoi(strings.TrimSpace(line[3:6]))
				remainingObsTypes = n
				obsIdx = 0
			}
			// Токены типов наблюдений: с колонки 7, поля по 4 символа (3 символа + пробел).
			// Проще — Fields по пробелам, пропуская первый токен-заголовок системы для первой строки.
			fields := strings.Fields(line[:60])
			start := 0
			// Первое поле первой строки блока — символ системы или число: определим по длине.
			// Если поле длиной 1 (например "G") или это число — пропускаем два первых токена (sys, n).
			// Для строк-продолжений поля начинаются сразу с типов наблюдений.
			if len(fields) > 0 && (fields[0] == string(currentSys) || (obsIdx == 0 && len(fields) > 1)) {
				// Это первая строка блока: пропускаем "sys" и "n".
				if _, err := strconv.Atoi(fields[0]); err == nil {
					// Формат без sys в начале (редко) — пропускаем только число.
					start = 1
				} else {
					start = 2
				}
			}
			for i := start; i < len(fields) && remainingObsTypes > 0; i++ {
				t := fields[i]
				if len(t) >= 2 && t[0] == 'S' {
					switch t[1] {
					case '1':
						snr1[currentSys] = append(snr1[currentSys], obsIdx)
					case '2':
						snr2[currentSys] = append(snr2[currentSys], obsIdx)
					case '5':
						snr5[currentSys] = append(snr5[currentSys], obsIdx)
					}
				}
				obsIdx++
				remainingObsTypes--
			}
			continue
		}
		if strings.Contains(line, "END OF HEADER") {
			inHeader = false
			break
		}
	}

	if inHeader || !version3 {
		return "", "", "", false
	}
	if len(snr1) == 0 && len(snr5) == 0 {
		return "", "", "", false
	}

	// Собираем значения SNR из тела.
	var vals1, vals2, vals5 []float64
	// Общее количество полей наблюдений per system (для парсинга строки спутника).
	// Мы уже знаем obsIdx на конец каждого блока, но перезаписали — восстановим из мап.
	// Проще: при чтении строки спутника достаточно извлечь 16-символьные поля по нужным индексам,
	// не зная общего числа полей — если поле выходит за длину строки, пропускаем.

	for scanner.Scan() {
		line := scanner.Text()
		if len(line) == 0 {
			continue
		}
		// Заголовок эпохи в RINEX 3 начинается с '>'.
		if line[0] == '>' {
			continue
		}
		// Строка спутника: колонки 0-2 — идентификатор ('G01', 'E13', ...).
		if len(line) < 4 {
			continue
		}
		sys := line[0]
		body := line[3:] // наблюдения идут с 4-го символа

		extract := func(indices []int, dst *[]float64) {
			for _, idx := range indices {
				start := idx * 16
				end := start + 14 // 14.3 float; последние 2 символа — LLI и SNR-флаг
				if end > len(body) {
					continue
				}
				s := strings.TrimSpace(body[start:end])
				if s == "" {
					continue
				}
				v, err := strconv.ParseFloat(s, 64)
				if err != nil || v <= 0 {
					continue
				}
				*dst = append(*dst, v)
			}
		}
		extract(snr1[sys], &vals1)
		extract(snr2[sys], &vals2)
		extract(snr5[sys], &vals5)
	}

	// Маска RTKLIB: 9 значений на углы возвышения 5°,15°,25°,35°,45°,55°,65°,75°,85°.
	// Плоская маска: одинаковый порог для всех углов. Базовый порог — 3-й перцентиль
	// минус запас 3 dBHz, зажат в [minT, maxT]. Отсекает только явный шумовой хвост.
	toMask := func(vals []float64, minT, maxT int) (string, bool) {
		if len(vals) < 30 {
			return "", false
		}
		sort.Float64s(vals)
		idx := int(math.Floor(0.03 * float64(len(vals)-1)))
		threshold := int(math.Floor(vals[idx])) - 3
		if threshold < minT {
			threshold = minT
		}
		if threshold > maxT {
			threshold = maxT
		}
		parts := make([]string, 9)
		for i := range parts {
			parts[i] = strconv.Itoa(threshold)
		}
		return strings.Join(parts, ","), true
	}

	l1, ok1 := toMask(vals1, 22, 28)
	l5, ok5 := toMask(vals5, 25, 30)
	l2, _ := toMask(vals2, 22, 28) // L2 опционален

	if !ok1 && !ok5 {
		return "", "", "", false
	}
	if !ok1 {
		l1 = "0,0,0,0,0,0,0,0,0"
	}
	if !ok5 {
		l5 = "0,0,0,0,0,0,0,0,0"
	}
	if l2 == "" {
		l2 = "0,0,0,0,0,0,0,0,0"
	}
	return l1, l2, l5, true
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
			// Обрезаем метку (последние 20 символов — лейбл), берём числовую часть.
			// Поддерживаем как RINEX 2 (фиксированные колонки), так и RINEX 3 (свободный формат).
			numPart := line
			if idx := strings.Index(line, "APPROX POSITION XYZ"); idx >= 0 {
				numPart = line[:idx]
			}
			fields := strings.Fields(numPart)
			if len(fields) < 3 {
				continue
			}
			x, err1 := strconv.ParseFloat(fields[0], 64)
			y, err2 := strconv.ParseFloat(fields[1], 64)
			z, err3 := strconv.ParseFloat(fields[2], 64)
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
