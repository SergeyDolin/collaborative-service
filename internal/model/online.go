package model

import "time"

// OnlineSession — активная онлайн-сессия клиента реального времени
type OnlineSession struct {
	ID                int64     `json:"id"`
	UserLogin         string    `json:"userLogin"`
	PublicIP          string    `json:"publicIP"`
	OutputPort        int       `json:"outputPort"`
	SolutionPort      int       `json:"solutionPort"`      // порт SolutionServer (portTCP text + real SDs)
	RoughenedLat      float64   `json:"roughenedLat"`
	RoughenedLon      float64   `json:"roughenedLon"`
	RoughenedHeight   float64   `json:"roughenedHeight"`
	ConvergenceStatus int       `json:"convergenceStatus"` // 0=в процессе, 1=сошлось
	IsMoving          bool      `json:"isMoving"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// CandidateInfo — информация о кандидате для инжекции позиции
type CandidateInfo struct {
	IP           string `json:"ip"`
	Port         int    `json:"port"`
	SolutionPort int    `json:"solutionPort"` // порт SolutionServer кандидата
	IsMoving     bool   `json:"isMoving"`
}

// RegisterOnlineRequest — запрос регистрации онлайн-сессии
type RegisterOnlineRequest struct {
	PublicIP        string  `json:"publicIP"`
	OutputPort      int     `json:"outputPort"`
	SolutionPort    int     `json:"solutionPort"`
	RoughenedLat    float64 `json:"roughenedLat"`
	RoughenedLon    float64 `json:"roughenedLon"`
	RoughenedHeight float64 `json:"roughenedHeight"`
}

// RegisterOnlineResponse — ответ с адресами EPH/SSR потоков
type RegisterOnlineResponse struct {
	SessionID int    `json:"sessionId"`
	EPHHost   string `json:"ephHost"`
	EPHPort   int    `json:"ephPort"`
	SSRHost   string `json:"ssrHost"`
	SSRPort   int    `json:"ssrPort"`
}

// UpdateOnlineStatusRequest — обновление статуса позиционирования
type UpdateOnlineStatusRequest struct {
	ConvergenceStatus int     `json:"convergenceStatus"`
	IsMoving          bool    `json:"isMoving"`
	RoughenedLat      float64 `json:"roughenedLat"`
	RoughenedLon      float64 `json:"roughenedLon"`
}

// UpdateOnlineStatusResponse — ответ с кандидатом, если найден
type UpdateOnlineStatusResponse struct {
	Candidate *CandidateInfo `json:"candidate,omitempty"`
}
