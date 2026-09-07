package model

import "time"

// AuthRequest request authentication
type AuthRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

// AuthResponse response authentication
type AuthResponse struct {
	Message string `json:"message,omitempty"`
	Login   string `json:"login,omitempty"`
	Token   string `json:"token,omitempty"`
}

// ErrorResponse response authentication
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

// ProcessRequest request for adjustment
type ProcessRequest struct {
	Config   UserProcessingConfig `json:"config"`
	Filename string               `json:"filename"`
}

// ProcessResponse response for adjustment
type ProcessResponse struct {
	TaskID  string `json:"taskId"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// TaskStatusResponse response with status of task
type TaskStatusResponse struct {
	TaskID        string            `json:"taskId"`
	Status        TaskStatus        `json:"status"`
	ErrorMessage  string            `json:"errorMessage,omitempty"`
	CreatedAt     time.Time         `json:"createdAt"`
	StartedAt     *time.Time        `json:"startedAt,omitempty"`
	CompletedAt   *time.Time        `json:"completedAt,omitempty"`
	ProcessingSec float64           `json:"processingSec,omitempty"`
	Result        *ProcessingResult `json:"result,omitempty"`
}
