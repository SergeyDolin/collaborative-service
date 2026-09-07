package model

// BaseStation information about base station
type BaseStation struct {
	ID        string  `json:"id"`   // 4-symbols code IGS
	Name      string  `json:"name"` // full name of station
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Height    float64 `json:"height"`
	Country   string  `json:"country"`
	Network   string  `json:"network"` // IGS, EUREF, etc.
}

// NearestStationResult nearest station of network
type NearestStationResult struct {
	Station    BaseStation `json:"station"`
	DistanceKM float64     `json:"distanceKm"`
	AzimuthDeg float64     `json:"azimuthDeg"`
}
