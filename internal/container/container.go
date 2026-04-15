package container

import (
	"collaborative/internal/auth"
	"collaborative/internal/config"
	"collaborative/internal/handlers"
	"collaborative/internal/repositories"
	"collaborative/internal/services"
	"collaborative/internal/storage"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// ServiceContainer содержит все сервисы
type ServiceContainer struct {
	// Хранилище
	DB       *storage.DBStorage
	TaskRepo repositories.TaskRepository
	UserRepo repositories.UserRepository

	// Сервисы
	Measurement  *services.MeasurementService
	File         *services.FileService
	Cache        *services.CacheService
	Notification *services.NotificationService

	// Утилиты
	ConfigGen  *services.ConfigGenerator
	Downloader *services.FileDownloader
	Converter  *services.ConverterService
	RTK        *services.RTKService
	JWTService *auth.JWTService
	Logger     *zap.SugaredLogger
}

// HandlerContainer содержит все обработчики
type HandlerContainer struct {
	Measurement *handlers.MeasurementHandler
	Logger      *zap.SugaredLogger
}

// Container главный контейнер зависимостей
type Container struct {
	Services *ServiceContainer
	Handlers *HandlerContainer
	Logger   *zap.SugaredLogger
}

// NewContainer создает новый контейнер
func NewContainer(cfg *config.Config, logger *zap.SugaredLogger) (*Container, error) {
	var dbStorage *storage.DBStorage
	var taskRepo repositories.TaskRepository
	var userRepo repositories.UserRepository

	// Инициализируем БД если конфигурирована
	if cfg.DSN != "" {
		var err error
		dbStorage, err = storage.NewDBStorage(cfg.DSN)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to database: %w", err)
		}

		taskRepo = repositories.NewTaskRepositoryImpl(dbStorage.Pool())
		userRepo = repositories.NewUserRepositoryImpl(dbStorage.Pool())
	}

	// Инициализируем сервисы
	taskStorage := storage.NewTaskStorage(dbStorage.Pool())
	if err := taskStorage.InitTaskSchema(); err != nil {
		return nil, fmt.Errorf("failed to init task schema: %w", err)
	}

	configGen := services.NewConfigGenerator("./cmd/solver/app", "./tmp", logger)
	downloader := services.NewFileDownloader("./tmp", logger)
	converter := services.NewConverterService("./cmd/solver/app", logger)
	rtk := services.NewRTKService("./cmd/solver/app", "./tmp", logger)
	blqSvc := services.NewBLQService("./cmd/solver/src", "./cmd/solver/src", "./tmp", logger)

	measurementSvc := services.NewMeasurementService(
		taskStorage, configGen, downloader, converter, rtk,
		services.NewFileService("./tmp", logger),
		blqSvc,
		"./tmp", logger,
	)

	fileSvc := services.NewFileService("./tmp", logger)
	cacheSvc := services.NewCacheService(5*time.Minute, logger)
	notificationSvc := services.NewNotificationService(logger)

	jwtService := auth.NewJWTService(cfg.JWTSecret, cfg.TokenExpiry)

	// Создаем контейнеры
	servicesContainer := &ServiceContainer{
		DB:           dbStorage,
		TaskRepo:     taskRepo,
		UserRepo:     userRepo,
		Measurement:  measurementSvc,
		File:         fileSvc,
		Cache:        cacheSvc,
		Notification: notificationSvc,
		ConfigGen:    configGen,
		Downloader:   downloader,
		Converter:    converter,
		RTK:          rtk,
		JWTService:   jwtService,
		Logger:       logger,
	}

	measurementHandler := handlers.NewMeasurementHandler(
		dbStorage, taskStorage, logger,
	)

	handlersContainer := &HandlerContainer{
		Measurement: measurementHandler,
		Logger:      logger,
	}

	return &Container{
		Services: servicesContainer,
		Handlers: handlersContainer,
		Logger:   logger,
	}, nil
}

// Close закрывает ресурсы контейнера
func (c *Container) Close() error {
	if c.Services.DB != nil {
		return c.Services.DB.Close()
	}
	return nil
}
