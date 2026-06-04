package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"collaborative/internal/middlewares"
	"collaborative/internal/storage"

	"go.uber.org/zap"
)

type StatsHandler struct {
	taskStorage *storage.TaskStorage
	logger      *zap.SugaredLogger
}

func NewStatsHandler(taskStorage *storage.TaskStorage, logger *zap.SugaredLogger) *StatsHandler {
	return &StatsHandler{taskStorage: taskStorage, logger: logger}
}

// SatEpoch — одна запись $SAT из .stat файла
type SatEpoch struct {
	TOW  float64 `json:"tow"`
	Sat  string  `json:"sat"`
	Az   float64 `json:"az"`
	El   float64 `json:"el"`
	Resp float64 `json:"resp"` // pseudorange residual (м)
	Resc float64 `json:"resc"` // carrier-phase residual (м)
	SNR  float64 `json:"snr"`
	Fix  int     `json:"fix"`  // 0=float,1=fix,2=hold
}

// TrpEpoch — одна запись $TRP
type TrpEpoch struct {
	TOW  float64 `json:"tow"`
	Trp  float64 `json:"trp"`
	Std  float64 `json:"std"`
}

// ClkEpoch — одна запись $CLK
type ClkEpoch struct {
	TOW  float64 `json:"tow"`
	Bias float64 `json:"bias"` // наносекунды
	Std  float64 `json:"std"`
}

type StatsResponse struct {
	Satellites []SatEpoch `json:"satellites"`
	Tropo      []TrpEpoch `json:"tropo"`
	Clock      []ClkEpoch `json:"clock"`
}

// GetStats возвращает распарсенные данные из .stat файла RTKLIB
// GET /api/measurements/stats?id=<taskID>
func (h *StatsHandler) GetStats(w http.ResponseWriter, r *http.Request) {
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

	stat, found, err := h.taskStorage.GetStatOutput(taskID, login)
	if err != nil {
		SendJSONError(w, "Failed to load stats", http.StatusInternalServerError, h.logger)
		return
	}
	if !found || stat == "" {
		SendJSONError(w, "Stats not available", http.StatusNotFound, h.logger)
		return
	}

	resp := parseStatFile(stat)
	SendJSONResponse(w, http.StatusOK, resp, h.logger)
}

// parseStatFile парсит .stat файл RTKLIB формата:
// $SAT,week,tow,sat,frq,az,el,resp,resc,vsat,snr,fix,slip,lock,...
// $TRP,week,tow,rcvr,tropo,std,...
// $CLK,week,tow,rcvr,bias,drift,std,...
func parseStatFile(content string) StatsResponse {
	var resp StatsResponse
	// Для дедупликации спутников по tow+sat берём последнюю запись (frq=0 — суммарная)
	satMap := map[string]SatEpoch{}

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		f := strings.Split(line, ",")

		switch {
		case strings.HasPrefix(line, "$SAT") && len(f) >= 12:
			tow, _ := strconv.ParseFloat(strings.TrimSpace(f[2]), 64)
			sat := strings.TrimSpace(f[3])
			frq, _ := strconv.Atoi(strings.TrimSpace(f[4]))
			if frq != 0 { // берём только frq=0 (объединённая запись)
				continue
			}
			az, _   := strconv.ParseFloat(strings.TrimSpace(f[5]), 64)
			el, _   := strconv.ParseFloat(strings.TrimSpace(f[6]), 64)
			resp_, _ := strconv.ParseFloat(strings.TrimSpace(f[7]), 64)
			resc, _  := strconv.ParseFloat(strings.TrimSpace(f[8]), 64)
			snr, _   := strconv.ParseFloat(strings.TrimSpace(f[10]), 64)
			fix, _   := strconv.Atoi(strings.TrimSpace(f[11]))
			key := strings.TrimSpace(f[2]) + "_" + sat
			satMap[key] = SatEpoch{
				TOW: tow, Sat: sat,
				Az: az, El: el,
				Resp: resp_, Resc: resc,
				SNR: snr, Fix: fix,
			}

		case strings.HasPrefix(line, "$TRP") && len(f) >= 6:
			tow, _ := strconv.ParseFloat(strings.TrimSpace(f[2]), 64)
			trp, _ := strconv.ParseFloat(strings.TrimSpace(f[4]), 64)
			std, _ := strconv.ParseFloat(strings.TrimSpace(f[5]), 64)
			resp.Tropo = append(resp.Tropo, TrpEpoch{TOW: tow, Trp: trp, Std: std})

		case strings.HasPrefix(line, "$CLK") && len(f) >= 6:
			tow, _  := strconv.ParseFloat(strings.TrimSpace(f[2]), 64)
			bias, _ := strconv.ParseFloat(strings.TrimSpace(f[4]), 64)
			std, _  := strconv.ParseFloat(strings.TrimSpace(f[6]), 64)
			// bias в метрах → наносекунды: /0.299792458
			resp.Clock = append(resp.Clock, ClkEpoch{TOW: tow, Bias: bias / 0.299792458, Std: std / 0.299792458})
		}
	}

	for _, s := range satMap {
		resp.Satellites = append(resp.Satellites, s)
	}
	return resp
}
