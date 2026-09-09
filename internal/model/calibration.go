package model

import "time"

// Режимы калибровки
const (
	CalibModeFullCalib      = "full"            // 4 вертик. + 4 гориз. + опора
	CalibModeHorizontalOnly = "horizontal_only" // 4 вертик., без опоры
	CalibModeQuick          = "quick"           // поправка для одной установки + контроль
)

// Тип опорной точки
const (
	CalibRefGeodetic = "geodetic" // координаты марки введены вручную
	CalibRefReceiver = "receiver" // RINEX от приёмника, PPP даст координаты
	CalibRefNone     = "none"     // без опоры — только горизонтальные компоненты
)

// Положение смартфона
const (
	CalibPosVertical   = "vertical"
	CalibPosHorizontal = "horizontal"
)

// Ориентация (камера / верхняя грань)
const (
	CalibOrientNorth = "north"
	CalibOrientSouth = "south"
	CalibOrientEast  = "east"
	CalibOrientWest  = "west"
)

// CalibrationTask — задача определения фазового центра смартфона.
type CalibrationTask struct {
	ID          string             `json:"id"`
	UserLogin   string             `json:"userLogin"`
	DeviceID    int                `json:"deviceId"`
	DeviceModel string             `json:"deviceModel"`
	Mode        string             `json:"mode"`
	Status      string             `json:"status"` // pending | processing | completed | failed
	ErrorMsg    string             `json:"errorMessage,omitempty"`
	CreatedAt   time.Time          `json:"createdAt"`
	CompletedAt *time.Time         `json:"completedAt,omitempty"`
	ExpiresAt   time.Time          `json:"expiresAt"`
	Options     CalibrationOptions `json:"options"`
	HasReceiver bool               `json:"hasReceiver"`

	// Опорная точка
	RefType string  `json:"refType"`
	RefLat  float64 `json:"refLat"`
	RefLon  float64 `json:"refLon"`
	RefH    float64 `json:"refH"`
	// Если refType=receiver — task_id PPP-обработки файла приёмника
	ReceiverTaskID string `json:"receiverTaskId,omitempty"`

	// Элементы редуцирования (от ARP = верхняя грань до марки)
	ReduceH float64 `json:"reduceH"` // вертикаль ARP → марка (м), > 0
	ReduceE float64 `json:"reduceE"` // смещение ARP на восток от марки (м)
	ReduceN float64 `json:"reduceN"` // смещение ARP на север от марки (м)

	Sessions []CalibrationSession `json:"sessions,omitempty"`
	Result   *CalibrationResult   `json:"result,omitempty"`
}

// CalibrationSession — один сеанс наблюдений (один RINEX-файл).
type CalibrationSession struct {
	ID          string `json:"id"`
	TaskID      string `json:"taskId"`
	Filename    string `json:"filename"`
	Position    string `json:"position"`    // vertical | horizontal
	Orientation string `json:"orientation"` // north | south | east | west
	PPPTaskID   string `json:"pppTaskId,omitempty"`
	Status      string `json:"status"` // pending | processing | completed | failed
	// Средние относительного решения после редуцирования, ENU.
	DeltaE   float64             `json:"deltaE,omitempty"`
	DeltaN   float64             `json:"deltaN,omitempty"`
	DeltaU   float64             `json:"deltaU,omitempty"`
	FixRate  float64             `json:"fixRate,omitempty"`
	Geometry CalibrationGeometry `json:"geometry"`
}

// All coordinates refer to the same reference frame and epoch. Base coordinates
// refer to its RINEX marker; DELTA H/E/N and ANTEX correct the reference antenna.
type CalibrationOptions struct {
	BaseLat        float64 `json:"baseLat"`
	BaseLon        float64 `json:"baseLon"`
	BaseH          float64 `json:"baseH"`
	ReferenceFrame string  `json:"referenceFrame"`
	Frequency      string  `json:"frequency"`
}
type CalibrationGeometry struct {
	ReduceE     float64 `json:"reduceE"`
	ReduceN     float64 `json:"reduceN"`
	ReduceH     float64 `json:"reduceH"`
	Control     bool    `json:"control"`
	Epochs      int     `json:"epochs"`
	FixedEpochs int     `json:"fixedEpochs"`
	First       string  `json:"first,omitempty"`
	Last        string  `json:"last,omitempty"`
}

// CalibrationResult — итоговые смещения фазового центра в осях тела смартфона.
type CalibrationResult struct {
	// Оси тела (все в метрах, знак: + = влево, + = в тело (от экрана), + = вниз от верхней грани)
	OffsetLeft  float64  `json:"offsetLeft"`  // влево от центра экрана
	OffsetDepth float64  `json:"offsetDepth"` // вглубь корпуса (от экрана к задней крышке)
	OffsetDown  *float64 `json:"offsetDown"`  // nil: не определяется

	SigmaLeft        *float64               `json:"sigmaLeft"`
	SigmaDepth       *float64               `json:"sigmaDepth"`
	SigmaDown        *float64               `json:"sigmaDown"`
	Method           string                 `json:"method"`
	Scope            string                 `json:"scope"`
	Validation       *CalibrationValidation `json:"validation"`
	TrainingSessions int                    `json:"trainingSessions"`
	UncertaintyKind  string                 `json:"uncertaintyKind"`

	// Только для быстрой калибровки
	ValidUntil *time.Time `json:"validUntil,omitempty"`

	// Детали по сеансам для отчёта
	Sessions []SessionDetail `json:"sessions"`
}

// SessionDetail — строка таблицы результатов по сеансу (как Tables 2–3 в статье).
type SessionDetail struct {
	Position    string  `json:"position"`
	Orientation string  `json:"orientation"`
	DeltaE      float64 `json:"deltaE"`
	DeltaN      float64 `json:"deltaN"`
	DeltaU      float64 `json:"deltaU"`
	FixRate     float64 `json:"fixRate"`
	Control     bool    `json:"control"`
	FixedEpochs int     `json:"fixedEpochs"`
}

type CalibrationValidation struct {
	Sessions int         `json:"sessions"`
	Before   [3]*float64 `json:"before"`
	After    [3]*float64 `json:"after"`
}
