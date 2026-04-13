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

// TransformCoordinates выполняет трансформацию координат
func (h *TransformHandler) TransformCoordinates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		SendJSONError(w, "Method not allowed", http.StatusMethodNotAllowed, h.logger)
		return
	}

	// Читаем тело запроса
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Errorf("Failed to read request body: %v", err)
		SendJSONError(w, "Failed to read request", http.StatusBadRequest, h.logger)
		return
	}
	defer r.Body.Close()

	// Парсим входящий GeoJSON
	var geojson map[string]interface{}
	if err := json.Unmarshal(body, &geojson); err != nil {
		h.logger.Errorf("Failed to parse GeoJSON: %v", err)
		SendJSONError(w, "Invalid GeoJSON", http.StatusBadRequest, h.logger)
		return
	}

	// Получаем параметры трансформации
	sourceCRS := r.URL.Query().Get("source_crs")
	targetCRS := r.URL.Query().Get("target_crs")
	sourceCoordType := r.URL.Query().Get("source_coord_type")
	targetCoordType := r.URL.Query().Get("target_coord_type")
	heightSurface := r.URL.Query().Get("height_surface")
	sourceEpoch := r.URL.Query().Get("source_epoch")
	targetEpoch := r.URL.Query().Get("target_epoch")

	// Устанавливаем значения по умолчанию
	if sourceCRS == "" {
		sourceCRS = "WGS84(G1150)"
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
	today := time.Now().Format("2006-01-02")
	if sourceEpoch == "" {
		sourceEpoch = today
	}
	if targetEpoch == "" {
		targetEpoch = today
	}

	// Извлекаем координаты из GeoJSON
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

	h.logger.Infof("Coordinates to transform: %v", coordinates)

	// Сначала получаем код операции
	operationCode := h.encodeOperation(sourceCRS, targetCRS, sourceCoordType, targetCoordType, heightSurface, sourceEpoch, targetEpoch)
	if operationCode == "" {
		SendJSONError(w, "Failed to encode operation", http.StatusBadGateway, h.logger)
		return
	}

	h.logger.Infof("Got operation code: %s", operationCode)

	// Создаем source_dataset как строку (GeoJSON)
	sourceDataset := map[string]interface{}{
		"type": "FeatureCollection",
		"features": []interface{}{
			map[string]interface{}{
				"type": "Feature",
				"geometry": map[string]interface{}{
					"type":        "Point",
					"coordinates": coordinates,
				},
				"properties": map[string]interface{}{
					"id": "1",
				},
			},
		},
	}

	// Преобразуем в JSON строку
	sourceDatasetJSON, _ := json.Marshal(sourceDataset)

	// Отправляем запрос с source_dataset как строкой и эпохами
	transformReq := map[string]interface{}{
		"operation_code": operationCode,
		"source_dataset": string(sourceDatasetJSON),
		"source_epoch":   sourceEpoch,
		"target_epoch":   targetEpoch,
	}

	reqBody, _ := json.Marshal(transformReq)

	h.logger.Infof("Transform request: operation_code=%s, source_epoch=%s, target_epoch=%s",
		operationCode, sourceEpoch, targetEpoch)
	h.logger.Debugf("Request body: %s", string(reqBody))

	resp, err := h.client.Post(
		"https://geocentric.xyz/api/operation/from_code/geojson",
		"application/json",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		h.logger.Errorf("Transform failed: %v", err)
		SendJSONError(w, "Transform failed", http.StatusBadGateway, h.logger)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	h.logger.Infof("Response status: %d", resp.StatusCode)
	h.logger.Debugf("Response body: %s", string(respBody))

	if resp.StatusCode != http.StatusOK {
		SendJSONError(w, fmt.Sprintf("Transform failed: %s", string(respBody)), resp.StatusCode, h.logger)
		return
	}

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)

	// Извлекаем трансформированные координаты
	var targetCoords []float64

	// Сначала пробуем из target_dataset (может быть строкой или объектом)
	if targetDataset, ok := result["target_dataset"]; ok && targetDataset != nil {
		var dataset map[string]interface{}

		// Если это строка, парсим её
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

	response := map[string]interface{}{
		"success":            true,
		"source_coordinates": coordinates,
		"target_coordinates": targetCoords,
		"operation_code":     operationCode,
		"full_response":      result,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
	h.logger.Info("Transform completed!")
}

// encodeOperation получает код операции
// encodeOperation получает код операции
func (h *TransformHandler) encodeOperation(sourceCRS, targetCRS, sourceCoordType, targetCoordType, heightSurface, sourceEpoch, targetEpoch string) string {
	// Форматируем эпоху как "YYYY-MM-DD"
	// Убеждаемся, что дата в правильном формате
	if sourceEpoch != "" {
		if t, err := time.Parse("2006-01-02", sourceEpoch); err == nil {
			sourceEpoch = t.Format("2006-01-02")
		}
	}
	if targetEpoch != "" {
		if t, err := time.Parse("2006-01-02", targetEpoch); err == nil {
			targetEpoch = t.Format("2006-01-02")
		}
	}

	sourceMetadata := map[string]interface{}{
		"crs": map[string]interface{}{
			"referenceFrameID":   getReferenceFrameID(sourceCRS),
			"deformated":         false,
			"representationType": sourceCoordType,
		},
		"epoch": map[string]interface{}{
			"date": sourceEpoch,
		},
		"coord_type": sourceCoordType,
	}

	targetMetadata := map[string]interface{}{
		"crs": map[string]interface{}{
			"referenceFrameID":   getReferenceFrameID(targetCRS),
			"deformated":         false,
			"representationType": targetCoordType,
		},
		"epoch": map[string]interface{}{
			"date": targetEpoch,
		},
		"coord_type": targetCoordType,
	}

	encodeReq := map[string]interface{}{
		"source_metadata": sourceMetadata,
		"target_metadata": targetMetadata,
		"height_surface":  heightSurface,
	}

	reqBody, _ := json.Marshal(encodeReq)

	h.logger.Infof("Encode request: %s", string(reqBody))

	resp, err := h.client.Post(
		"https://geocentric.xyz/api/operation/encode",
		"application/json",
		bytes.NewReader(reqBody),
	)
	if err != nil {
		h.logger.Errorf("Failed to encode: %v", err)
		return ""
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	operationCode := strings.Trim(string(respBody), "\"\n ")

	h.logger.Infof("Got operation code: %s for source_epoch=%s, target_epoch=%s",
		operationCode, sourceEpoch, targetEpoch)

	return operationCode
}

// getReferenceFrameID возвращает правильный код referenceFrameID
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

	return "WGS84G1150GOST"
}

// TransformStatus проверяет статус сервиса
func (h *TransformHandler) TransformStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok","message":"Transform service ready"}`))
}
