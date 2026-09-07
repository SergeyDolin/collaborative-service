package model

import (
	"time"
)

type User struct {
	Login     string    `json:"login" db:"login"`
	Password  string    `json:"-" db:"password"`
	Email     string    `json:"email,omitempty" db:"email"`
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt time.Time `json:"updatedAt" db:"updated_at"`
}

type UserProfile struct {
	Login     string    `json:"login"`
	Email     string    `json:"email,omitempty"`
	CreatedAt time.Time `json:"registeredAt"`

	TotalProcessed int `json:"totalProcessed"`
	SuccessCount   int `json:"successCount"`
	FailedCount    int `json:"failedCount"`
}

type UserStats struct {
	TotalTasks       int     `json:"totalTasks"`
	CompletedTasks   int     `json:"completedTasks"`
	FailedTasks      int     `json:"failedTasks"`
	PendingTasks     int     `json:"pendingTasks"`
	AvgProcessingSec float64 `json:"avgProcessingSec"`
}
