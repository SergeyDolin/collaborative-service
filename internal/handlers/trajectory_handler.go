package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"collaborative/internal/middlewares"
	"collaborative/internal/storage"

	"go.uber.org/zap"
)

type TrajectoryHandler struct {
	taskStorage *storage.TaskStorage
	logger      *zap.SugaredLogger
}

func NewTrajectoryHandler(taskStorage *storage.TaskStorage, logger *zap.SugaredLogger) *TrajectoryHandler {
	return &TrajectoryHandler{taskStorage: taskStorage, logger: logger}
}

type TrajectoryPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
	H   float64 `json:"h"`
	Q   int     `json:"q"`
}

type TrajectoryResponse struct {
	Points []TrajectoryPoint `json:"points"`
	MinLat float64           `json:"minLat"`
	MaxLat float64           `json:"maxLat"`
	MinLon float64           `json:"minLon"`
	MaxLon float64           `json:"maxLon"`
}

// GetTrajectory парсит raw_output результата и возвращает массив точек траектории.
// GET /api/measurements/trajectory?id=<taskID>
func (h *TrajectoryHandler) GetTrajectory(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("id")
	if taskID == "" {
		SendJSONError(w, "Task ID required", http.StatusBadRequest, h.logger)
		return
	}

	login, ok := middlewares.GetUserFromContext(r.Context())
	if !ok {
		SendJSONError(w, "Unauthorized", http.StatusUnauthorized, h.logger)
		return
	}

	raw, found, err := h.taskStorage.GetRawOutput(taskID, login)
	if err != nil {
		SendJSONError(w, "Failed to load result", http.StatusInternalServerError, h.logger)
		return
	}
	if !found || raw == "" {
		SendJSONError(w, "No trajectory data", http.StatusNotFound, h.logger)
		return
	}

	points := parseTrajectoryPoints(raw)
	if len(points) == 0 {
		SendJSONError(w, "No trajectory data", http.StatusNotFound, h.logger)
		return
	}

	resp := TrajectoryResponse{Points: points}
	resp.MinLat, resp.MaxLat = points[0].Lat, points[0].Lat
	resp.MinLon, resp.MaxLon = points[0].Lon, points[0].Lon
	for _, p := range points {
		if p.Lat < resp.MinLat { resp.MinLat = p.Lat }
		if p.Lat > resp.MaxLat { resp.MaxLat = p.Lat }
		if p.Lon < resp.MinLon { resp.MinLon = p.Lon }
		if p.Lon > resp.MaxLon { resp.MaxLon = p.Lon }
	}

	SendJSONResponse(w, http.StatusOK, resp, h.logger)
}

// parseTrajectoryPoints извлекает точки из вывода RTKLIB .pos
// Формат: date time lat lon h Q ns sdn sde sdu ...
func parseTrajectoryPoints(rawOutput string) []TrajectoryPoint {
	var points []TrajectoryPoint
	for _, line := range strings.Split(rawOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "%") || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		lat, err1 := strconv.ParseFloat(fields[2], 64)
		lon, err2 := strconv.ParseFloat(fields[3], 64)
		hgt, err3 := strconv.ParseFloat(fields[4], 64)
		q,   err4 := strconv.Atoi(fields[5])
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
			continue
		}
		points = append(points, TrajectoryPoint{Lat: lat, Lon: lon, H: hgt, Q: q})
	}
	return points
}