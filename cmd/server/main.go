package main

import (
	"collaborative/internal/auth"
	"collaborative/internal/config"
	"collaborative/internal/handlers"
	"collaborative/internal/middlewares"
	"collaborative/internal/storage"
	"collaborative/internal/workers"
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"go.uber.org/zap"
)

// Constants для magic numbers
const (
	ShutdownTimeout     = 5 * time.Second
	ReadTimeout         = 15 * time.Minute
	WriteTimeout        = 15 * time.Minute
	IdleTimeout         = 60 * time.Second
	CleanupInterval     = 1 * time.Hour
	ResultExpirationTTL = 24 * time.Hour
	DefaultWorkDir      = "./tmp"
	ConfigDir           = "./cmd/solver/app"
	MaxUploadSize       = 1 << 30
)

// Application представляет приложение с управлением жизненным циклом
type Application struct {
	server      *http.Server
	logger      *zap.SugaredLogger
	dbStorage   *storage.DBStorage
	taskStorage *storage.TaskStorage
	workerMgr   *workers.Manager
	ctx         context.Context
	cancel      context.CancelFunc
}

func main() {
	cfg := config.LoadConfig()

	logger, err := zap.NewDevelopment()
	if err != nil {
		panic("cannot initialize zap")
	}
	defer logger.Sync()
	sugar := logger.Sugar()

	app, err := NewApplication(cfg, sugar)
	if err != nil {
		sugar.Fatalf("Failed to initialize application: %v", err)
	}

	if err := app.Run(); err != nil {
		sugar.Fatalf("Application error: %v", err)
	}
}

// NewApplication создает и инициализирует приложение
func NewApplication(cfg *config.Config, logger *zap.SugaredLogger) (*Application, error) {
	ctx, cancel := context.WithCancel(context.Background())

	app := &Application{
		logger: logger,
		ctx:    ctx,
		cancel: cancel,
	}

	// Инициализация хранилища
	if err := app.initStorage(cfg); err != nil {
		return nil, err
	}

	// Инициализация сервиса вспомогательных функций
	app.workerMgr = workers.NewManager(
		app.logger,
		app.taskStorage,
		DefaultWorkDir,
	)

	// Инициализация HTTP маршрутов
	router := app.setupRoutes(cfg)

	app.server = &http.Server{
		Addr:         cfg.RunAddr,
		Handler:      router,
		ReadTimeout:  ReadTimeout,
		WriteTimeout: WriteTimeout,
		IdleTimeout:  IdleTimeout,
	}

	return app, nil
}

// initStorage инициализирует хранилище
func (app *Application) initStorage(cfg *config.Config) error {
	if cfg.DSN == "" {
		app.logger.Warn("No DSN provided, running without database")
		return nil
	}

	dbStor, err := storage.NewDBStorage(cfg.DSN)
	if err != nil {
		return err
	}
	app.logger.Infof("Connected to database: %s", cfg.DSN)

	app.dbStorage = dbStor
	app.taskStorage = storage.NewTaskStorage(dbStor.Pool())

	if err := app.taskStorage.InitTaskSchema(); err != nil {
		return err
	}
	app.logger.Info("Task schema initialized")

	return nil
}

// setupRoutes настраивает все маршруты приложения
func (app *Application) setupRoutes(cfg *config.Config) *chi.Mux {
	router := chi.NewRouter()

	// Middleware
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.StripSlashes)
	router.Use(middlewares.LogMiddleware(app.logger))
	router.Use(middlewares.MaxUploadSizeMiddleware(app.logger))

	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Invalid path format", http.StatusNotFound)
	})

	// Инициализация JWT сервиса
	jwtService := auth.NewJWTService(cfg.JWTSecret, cfg.TokenExpiry)
	app.logger.Infof("JWT service initialized with expiry: %d hours", cfg.TokenExpiry)

	// Public routes
	router.Get("/", handlers.IndexHandler(app.logger))
	router.Get("/login", handlers.LoginPageHandler(app.logger))
	router.Get("/register", handlers.RegisterPageHandler(app.logger))
	router.Get("/profile", handlers.ProfilePageHandler(app.logger))
	router.Get("/measurements", handlers.MeasurementsPageHandler(app.logger))

	// Public API routes
	router.Post("/api/register", handlers.RegisterHandler(app.dbStorage, app.logger))
	router.Post("/api/login", handlers.LoginHandler(app.dbStorage, jwtService, app.logger))

	// Measurement handler инициализация
	if app.taskStorage != nil {
		measurementHandler := handlers.NewMeasurementHandler(
			app.dbStorage,
			app.taskStorage,
			app.logger,
		)
		transformHandler := handlers.NewTransformHandler(app.logger)
		observationHandler := handlers.NewObservationHandler(app.taskStorage, app.logger)
		router.Get("/api/stats", measurementHandler.GetSystemStatsHandler)

		// Protected routes
		router.Group(func(r chi.Router) {
			r.Use(middlewares.AuthMiddleware(jwtService, app.logger))
			r.Get("/api/profile", handlers.ProfileHandler(app.logger))
			r.Post("/api/measurements/process", measurementHandler.ProcessMeasurementHandler)
			r.Get("/api/measurements/history", measurementHandler.GetHistoryHandler)
			r.Get("/api/measurements/status", measurementHandler.GetTaskStatusHandler)
			r.Get("/api/measurements/download", measurementHandler.DownloadResultHandler)
			r.Post("/api/transform/geojson", transformHandler.TransformCoordinates)
			r.Get("/api/transform/status", transformHandler.TransformStatus)
			r.Get("/api/measurements/observation-date", observationHandler.GetObservationDate)

			r.Delete("/api/measurements/delete", measurementHandler.DeleteTaskHandler)
			r.Delete("/api/measurements/delete-all", measurementHandler.DeleteAllTasksHandler)
		})
	}

	return router
}

// Run запускает приложение и управляет его жизненным циклом
func (app *Application) Run() error {
	// Запуск фоновых рабочих процессов
	app.workerMgr.Start(app.ctx)
	app.logger.Info("Background workers started")

	// Запуск сервера
	go func() {
		app.logger.Infof("Running server on %s", app.server.Addr)
		if err := app.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			app.logger.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Ожидание сигнала завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	<-sigChan
	app.logger.Info("Shutdown signal received")

	// Graceful shutdown
	return app.Shutdown()
}

// Shutdown корректно завершает приложение
func (app *Application) Shutdown() error {
	// Отмена контекста для рабочих процессов
	app.cancel()
	app.logger.Info("Background workers stopped")

	// Завершение HTTP сервера
	ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout)
	defer cancel()

	if err := app.server.Shutdown(ctx); err != nil {
		app.logger.Errorf("Server shutdown error: %v", err)
		return err
	}
	app.logger.Info("Server shutdown complete")

	// Закрытие хранилища
	if app.dbStorage != nil {
		if err := app.dbStorage.Close(); err != nil {
			app.logger.Errorf("Failed to close DB connection: %v", err)
			return err
		}
	}

	app.logger.Info("Application shutdown complete")
	return nil
}
