package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// staticCRS — системы координат с фиксированной эпохой (не зависят от даты наблюдения).
// Для них эпоха всегда определена самим определением системы.
var staticCRS = map[string]string{
	"ГСК-2011":     "2011-01-01",
	"СК-42":        "1942-01-01",
	"СК-95":        "1995-01-01",
	"WGS84(G1150)": "2010-01-01",
}

// isStaticCRS возвращает true если СК статическая (фиксированная эпоха).
func isStaticCRS(crs string) bool {
	_, ok := staticCRS[crs]
	return ok
}

// resolveEpoch возвращает эпоху для СК:
// для статических — фиксированную, для динамических — переданную пользователем.
func resolveEpoch(crs, userEpoch string) string {
	if epoch, ok := staticCRS[crs]; ok {
		return epoch
	}
	if userEpoch == "" {
		return time.Now().Format("2006-01-02")
	}
	return normalizeDate(userEpoch)
}

type TransformHandler struct {
	logger *zap.SugaredLogger
	client *http.Client
}

func NewTransformHandler(logger *zap.SugaredLogger) *TransformHandler {
	return &TransformHandler{
		logger: logger,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (h *TransformHandler) TransformCoordinates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		SendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed, h.logger)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		SendJSONError(w, "Failed to read request", http.StatusBadRequest, h.logger)
		return
	}
	defer r.Body.Close()

	var geojson map[string]interface{}
	if err := json.Unmarshal(body, &geojson); err != nil {
		SendJSONError(w, "Invalid GeoJSON", http.StatusBadRequest, h.logger)
		return
	}

	sourceCRS := r.URL.Query().Get("source_crs")
	targetCRS := r.URL.Query().Get("target_crs")
	sourceCoordType := r.URL.Query().Get("source_coord_type")
	targetCoordType := r.URL.Query().Get("target_coord_type")
	heightSurface := r.URL.Query().Get("height_surface")
	sourceEpochRaw := r.URL.Query().Get("source_epoch")
	targetEpochRaw := r.URL.Query().Get("target_epoch")

	if sourceCRS == "" {
		sourceCRS = "ITRF2020"
	}
	if targetCRS == "" {
		targetCRS = "ГСК-2011"
	}
	if sourceCoordType == "" {
		sourceCoordType = "BLH"
	}
	if targetCoordType == "" {
		targetCoordType = "BLH"
	}

	// Для статических СК эпоха фиксирована и не зависит от ввода пользователя
	sourceEpoch := resolveEpoch(sourceCRS, sourceEpochRaw)
	targetEpoch := resolveEpoch(targetCRS, targetEpochRaw)

	h.logger.Infof("Transform: %s(%s)@%s -> %s(%s)@%s height=%s",
		sourceCRS, sourceCoordType, sourceEpoch,
		targetCRS, targetCoordType, targetEpoch,
		heightSurface)

	var coordinates []float64
	if features, ok := geojson["features"].([]interface{}); ok && len(features) > 0 {
		if feature, ok := features[0].(map[string]interface{}); ok {
			if geometry, ok := feature["geometry"].(map[string]interface{}); ok {
				if coords, ok := geometry["coordinates"].([]interface{}); ok {
					for _, c := range coords {
						if f, ok := c.(float64); ok {
							coordinates = append(coordinates, f)
						}
					}
				}
			}
		}
	}

	if len(coordinates) < 2 {
		SendJSONError(w, "Invalid coordinates", http.StatusBadRequest, h.logger)
		return
	}

	operationCode := h.encodeOperation(sourceCRS, targetCRS, sourceCoordType, targetCoordType, heightSurface, sourceEpoch, targetEpoch)
	if operationCode == "" {
		SendJSONError(w, "Failed to encode operation", http.StatusBadGateway, h.logger)
		return
	}

	sourceDataset := map[string]interface{}{
		"type": "FeatureCollection",
		"features": []interface{}{
			map[string]interface{}{
				"type": "Feature",
				"geometry": map[string]interface{}{
					"type":        "Point",
					"coordinates": coordinates,
				},
				"properties": map[string]interface{}{"id": "1"},
			},
		},
	}
	sourceDatasetJSON, _ := json.Marshal(sourceDataset)

	transformReq := map[string]interface{}{
		"operation_code": operationCode,
		"source_dataset": string(sourceDatasetJSON),
		"source_epoch":   sourceEpoch,
		"target_epoch":   targetEpoch,
	}
	reqBody, _ := json.Marshal(transformReq)

	resp, err := h.client.Post(
		"https://geocentric.xyz/api/operation/from_code/geojson",
		"application/json",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		h.logger.Errorf("Transform request failed: %v", err)
		SendJSONError(w, "Transform failed", http.StatusBadGateway, h.logger)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		SendJSONError(w, fmt.Sprintf("Transform failed: %s", string(respBody)), resp.StatusCode, h.logger)
		return
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	var targetCoords []float64
	if targetDataset, ok := result["target_dataset"]; ok && targetDataset != nil {
		var dataset map[string]interface{}
		if str, ok := targetDataset.(string); ok {
			json.Unmarshal([]byte(str), &dataset)
		} else if obj, ok := targetDataset.(map[string]interface{}); ok {
			dataset = obj
		}
		if dataset != nil {
			if features, ok := dataset["features"].([]interface{}); ok && len(features) > 0 {
				if feature, ok := features[0].(map[string]interface{}); ok {
					if geometry, ok := feature["geometry"].(map[string]interface{}); ok {
						if coords, ok := geometry["coordinates"].([]interface{}); ok {
							for _, c := range coords {
								if f, ok := c.(float64); ok {
									targetCoords = append(targetCoords, f)
								}
							}
						}
					}
				}
			}
		}
	}

	h.logger.Infof("Transform result: %v -> %v", coordinates, targetCoords)

	response := map[string]interface{}{
		"success":            true,
		"source_coordinates": coordinates,
		"target_coordinates": targetCoords,
		"operation_code":     operationCode,
		"source_epoch":       sourceEpoch,
		"target_epoch":       targetEpoch,
		"source_crs_static":  isStaticCRS(sourceCRS),
		"target_crs_static":  isStaticCRS(targetCRS),
		"full_response":      result,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *TransformHandler) encodeOperation(sourceCRS, targetCRS, sourceCoordType, targetCoordType, heightSurface, sourceEpoch, targetEpoch string) string {
	encodeReq := map[string]interface{}{
		"source_metadata": map[string]interface{}{
			"crs": map[string]interface{}{
				"referenceFrameID":   getReferenceFrameID(sourceCRS),
				"deformated":         false,
				"representationType": sourceCoordType,
			},
			"epoch":      map[string]interface{}{"date": sourceEpoch},
			"coord_type": sourceCoordType,
		},
		"target_metadata": map[string]interface{}{
			"crs": map[string]interface{}{
				"referenceFrameID":   getReferenceFrameID(targetCRS),
				"deformated":         false,
				"representationType": targetCoordType,
			},
			"epoch":      map[string]interface{}{"date": targetEpoch},
			"coord_type": targetCoordType,
		},
		"height_surface": heightSurface,
	}

	reqBody, _ := json.Marshal(encodeReq)
	h.logger.Debugf("encodeOperation body: %s", string(reqBody))

	resp, err := h.client.Post(
		"https://geocentric.xyz/api/operation/encode",
		"application/json",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		h.logger.Errorf("encodeOperation failed: %v", err)
		return ""
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		h.logger.Errorf("encodeOperation status %d: %s", resp.StatusCode, string(respBody))
		return ""
	}

	code := strings.Trim(string(respBody), "\"\n ")
	h.logger.Infof("encodeOperation result: %s", code)
	return code
}

func normalizeDate(date string) string {
	if date == "" {
		return time.Now().Format("2006-01-02")
	}
	for _, f := range []string{"2006-01-02", "02.01.2006", "01/02/2006", "2006/01/02"} {
		if t, err := time.Parse(f, date); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return date
}

func getReferenceFrameID(crsName string) string {
	mapping := map[string]string{
		"WGS84(G1150)": "WGS84G1150GOST",
		"WGS84":        "WGS84G1150GOST",
		"ГСК-2011":     "GSK2011",
		"ПЗ-90.11":     "PZ9011",
		"ПЗ-90.02":     "PZ9002",
		"ПЗ-90":        "PZ90",
		"СК-42":        "SK42",
		"СК-95":        "SK95",
		"ITRF2020":     "ITRF2020",
		"ITRF2014":     "ITRF2014",
		"ITRF2008":     "ITRF2008",
		"ITRF2005":     "ITRF2005",
		"ITRF2000":     "ITRF2000",
	}
	if code, ok := mapping[crsName]; ok {
		return code
	}
	return "ITRF2020"
}

func (h *TransformHandler) TransformStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok","message":"Transform service ready"}`))
}
