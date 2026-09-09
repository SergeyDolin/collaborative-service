package model

type SatelliteObservation struct {
	T      float64  `json:"t"` // Calendar seconds in GPST, not UTC.
	ID     string   `json:"id"`
	Signal string   `json:"signal"` // Exact RINEX S observation code.
	CNo    *float64 `json:"cno"`
	Az     *float64 `json:"az"`
	El     *float64 `json:"el"`
}
type SatelliteReport struct {
	Rows      []SatelliteObservation `json:"rows"`
	Note      string                 `json:"note"`
	Truncated bool                   `json:"truncated"`
}
