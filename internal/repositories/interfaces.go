package repositories

import "collaborative/internal/model"

// TaskRepository интерфейс для работы с задачами
type TaskRepository interface {
	CreateTask(task *model.ProcessingTask) error
	UpdateTask(task *model.ProcessingTask) error
	GetUserTasks(userLogin string, limit, offset int) ([]model.ProcessingTask, error)
	GetTaskByID(taskID string) (*model.ProcessingTask, error)
	GetUserTasksWithResults(userLogin string, limit, offset int) ([]map[string]interface{}, error)
	SaveResult(result *model.ProcessingResult) error
	GetResultByTaskID(taskID string) (*model.ProcessingResult, error)
	CleanExpiredResults() error
	GetSystemStats() (map[string]interface{}, error)
}

// UserRepository интерфейс для работы с пользователями
type UserRepository interface {
	CreateUser(login, password string) error
	GetUser(login string) (*model.User, error)
	UserExists(login string) (bool, error)
	UpdateUserPassword(login, newPassword string) error
}
