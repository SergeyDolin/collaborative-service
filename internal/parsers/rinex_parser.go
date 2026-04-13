package parsers

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// RINEXParser parses RINEX observation files
type RINEXParser struct{}

func NewRINEXParser() *RINEXParser {
	return &RINEXParser{}
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

// isNumeric проверяет, состоит ли строка только из цифр
func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
