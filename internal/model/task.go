package model

import (
	"time"
)

// TaskStatus статус задачи обработки
type TaskStatus string

const (
	StatusPending    TaskStatus = "pending"
	StatusProcessing TaskStatus = "processing"
	StatusCompleted  TaskStatus = "completed"
	StatusFailed     TaskStatus = "failed"
)

// ProcessingTask задача на обработку
type ProcessingTask struct {
	ID            string               `json:"id" db:"id"`
	UserLogin     string               `json:"userLogin" db:"user_login"`
	Config        UserProcessingConfig `json:"config"`
	Filename      string               `json:"filename" db:"filename"`
	OriginalPath  string               `json:"originalPath" db:"original_path"`
	RinexPath     string               `json:"rinexPath,omitempty" db:"rinex_path"`
	OutputPath    string               `json:"outputPath,omitempty" db:"output_path"`
	Status        TaskStatus           `json:"status" db:"status"`
	ErrorMessage  string               `json:"errorMessage,omitempty" db:"error_message"`
	CreatedAt     time.Time            `json:"createdAt" db:"created_at"`
	StartedAt     *time.Time           `json:"startedAt,omitempty" db:"started_at"`
	CompletedAt   *time.Time           `json:"completedAt,omitempty" db:"completed_at"`
	ProcessingSec float64              `json:"processingSec,omitempty" db:"processing_sec"`
}

// ProcessingResult результат обработки
type ProcessingResult struct {
	ID               int64     `json:"id" db:"id"`
	TaskID           string    `json:"taskId" db:"task_id"`
	UserLogin        string    `json:"userLogin" db:"user_login"`
	X                float64   `json:"x" db:"x"`
	Y                float64   `json:"y" db:"y"`
	Z                float64   `json:"z" db:"z"`
	Latitude         float64   `json:"latitude,omitempty" db:"latitude"`
	Longitude        float64   `json:"longitude,omitempty" db:"longitude"`
	Height           float64   `json:"height,omitempty" db:"height"`
	Q                int       `json:"q" db:"q"`
	NSat             int       `json:"nSat" db:"n_sat"`
	SDX              float32   `json:"sdx" db:"sdx"`
	SDY              float32   `json:"sdy" db:"sdy"`
	SDZ              float32   `json:"sdz" db:"sdz"`
	LastSolutionLine string    `json:"lastSolutionLine,omitempty" db:"last_solution_line"`
	FullResultFile   []byte    `json:"-" db:"full_result_file"`
	FileType         string    `json:"fileType,omitempty" db:"file_type"`
	RawOutput        string    `json:"rawOutput,omitempty" db:"raw_output"`
	CreatedAt        time.Time `json:"createdAt" db:"created_at"`
	ExpiresAt        time.Time `json:"expiresAt" db:"expires_at"`
}
