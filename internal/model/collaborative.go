package model

import "time"

type ConnectionType string

const (
	ConnectionTypeTCP   ConnectionType = "tcp"
	ConnectionTypeNTRIP ConnectionType = "ntrip"
)

// CollaborativeSession — сессия устройства в коллаборативной сети
type CollaborativeSession struct {
	ID              int64          `json:"id" db:"id"`
	UserLogin       string         `json:"userLogin" db:"user_login"`
	DeviceID        int64          `json:"deviceId" db:"device_id"`
	DeviceName      string         `json:"deviceName" db:"device_name"`
	ConnectionType  ConnectionType `json:"connectionType" db:"connection_type"`
	TCPHost         string         `json:"tcpHost,omitempty" db:"tcp_host"`
	TCPPort         int            `json:"tcpPort,omitempty" db:"tcp_port"`
	NTRIPHost       string         `json:"ntripHost,omitempty" db:"ntrip_host"`
	NTRIPPort       int            `json:"ntripPort,omitempty" db:"ntrip_port"`
	NTRIPMountpoint string         `json:"ntripMountpoint,omitempty" db:"ntrip_mountpoint"`
	NTRIPUser       string         `json:"ntripUser,omitempty" db:"ntrip_user"`
	NTRIPPass       string         `json:"ntripPass,omitempty" db:"ntrip_pass"`
	AssignedPort    int            `json:"assignedPort" db:"assigned_port"`
	// EnablePositioning — включить определение местоположения на сервере через rtkrcv
	EnablePositioning bool      `json:"enablePositioning" db:"enable_positioning"`
	Str2strCmd        string    `json:"str2strCmd" db:"str2str_cmd"`
	CreatedAt         time.Time `json:"createdAt" db:"created_at"`
	// LatestPosition подтягивается JOIN-ом, не хранится в таблице сессий
	LatestPosition *CollaborativePosition `json:"latestPosition,omitempty" db:"-"`
}

// CollaborativePosition — последнее вычисленное положение по сессии
type CollaborativePosition struct {
	ID        int64     `json:"id"`
	SessionID int64     `json:"sessionId"`
	Lat       float64   `json:"lat"`
	Lon       float64   `json:"lon"`
	Height    float64   `json:"height"`
	PDOP      float64   `json:"pdop"`
	NSat      int       `json:"nsat"`
	Quality   int       `json:"quality"` // 1=fix,2=float,5=single
	UpdatedAt time.Time `json:"updatedAt"`
}
