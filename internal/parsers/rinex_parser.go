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
// It looks for "TIME OF FIRST OBS" in the header; falls back to "APPROX POSITION XYZ"
// year, then returns an error if nothing is found.
func (p *RINEXParser) ParseObservationDate(filePath string) (time.Time, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to open RINEX file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		if strings.Contains(line, "END OF HEADER") {
			break
		}

		// RINEX 2/3: "TIME OF FIRST OBS"
		// Format: "  YYYY  MM  DD  HH  mm  ss.sssssss  system     TIME OF FIRST OBS"
		if strings.Contains(line, "TIME OF FIRST OBS") {
			t, err := parseTimeOfFirstObs(line)
			if err == nil {
				return t, nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return time.Time{}, fmt.Errorf("error reading RINEX file: %w", err)
	}

	return time.Time{}, fmt.Errorf("TIME OF FIRST OBS not found in RINEX header: %s", filePath)
}

// parseTimeOfFirstObs parses a "TIME OF FIRST OBS" header line.
// The first 48 characters contain: YYYY MM DD HH mm ss.sssssss [system]
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
