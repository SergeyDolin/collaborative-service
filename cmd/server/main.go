package main

import (
	"collaborative/internal/auth"
	"collaborative/internal/config"
	"collaborative/internal/handlers"
	"collaborative/internal/middlewares"
	"collaborative/internal/services"
	"collaborative/internal/storage"
	"context"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"go.uber.org/zap"
)

func main() {
	cfg := config.LoadConfig()

	logger, err := zap.NewDevelopment()
	if err != nil {
		panic("cannot initialize zap")
	}
	defer logger.Sync()
	sugar := logger.Sugar()

	// init DB
	var dbStor *storage.DBStorage
	var taskStorage *storage.TaskStorage

	if dbStor != nil {
		taskStorage = storage.NewTaskStorage(dbStor.Pool())
		if err := taskStorage.InitTaskSchema(); err != nil {
			sugar.Errorf("Failed to init task schema: %v", err)
		}
		sugar.Info("Task storage initialized")
	}

	if cfg.DSN != "" {
		sugar.Infof("Initializing PostgresSQL storage with DSN: %s", cfg.DSN)
		dbStor, err = storage.NewDBStorage(cfg.DSN)
		if err != nil {
			sugar.Fatalf("Failed to connect to database: %v", err)
		}
		defer func() {
			if err := dbStor.Close(); err != nil {
				sugar.Errorf("Failed to close DB connection: %v", err)
			}
		}()

		// Инициализируем storage для задач
		taskStorage = storage.NewTaskStorage(dbStor.Pool())
		if err := taskStorage.InitTaskSchema(); err != nil {
			sugar.Fatalf("Failed to init task schema: %v", err)
		}
	}
	if taskStorage != nil {
		// Запускаем фоновую очистку старых записей каждый час
		go func() {
			ticker := time.NewTicker(1 * time.Hour)
			defer ticker.Stop()

			for range ticker.C {
				if err := taskStorage.CleanExpiredResults(); err != nil {
					sugar.Errorf("Failed to clean expired results: %v", err)
				} else {
					sugar.Debug("Cleaned expired results")
				}
			}
		}()
	}

	// init JWT services
	jwtService := auth.NewJWTService(cfg.JWTSecret, cfg.TokenExpiry)
	sugar.Infof("JWT service initialized with expiry: %d hours", cfg.TokenExpiry)

	// Initialize services for measurement processing
	workDir := "./tmp"
	configDir := "./cmd/solver/app"

	if _, err := os.Stat(configDir + "/rnx2rtkp"); os.IsNotExist(err) {
		sugar.Warnf("rnx2rtkp not found at %s/rnx2rtkp", configDir)
		sugar.Warnf("Please ensure RTKLIB is installed at: %s", configDir)
	} else {
		sugar.Infof("Found rnx2rtkp at: %s/rnx2rtkp", configDir)
	}

	os.MkdirAll(workDir, 0755)
	os.MkdirAll(configDir, 0755)

	configGenerator := services.NewConfigGenerator(configDir, workDir, sugar)
	downloader := services.NewFileDownloader(workDir, sugar)
	rtkService := services.NewRTKService("./cmd/solver/app", workDir, sugar)

	measurementHandler := handlers.NewMeasurementHandler(
		dbStor, taskStorage, configGenerator, downloader, rtkService, workDir, sugar,
	)

	// В main.go добавьте периодическую очистку старых временных файлов
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			// Удаляем папки старше 24 часов
			entries, _ := os.ReadDir(workDir)
			for _, entry := range entries {
				if entry.IsDir() {
					info, _ := entry.Info()
					if info != nil && time.Since(info.ModTime()) > 24*time.Hour {
						os.RemoveAll(filepath.Join(workDir, entry.Name()))
					}
				}
			}
		}
	}()

	router := chi.NewRouter()

	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(middleware.Recoverer)
	router.Use(middleware.StripSlashes)
	router.Use(middlewares.LogMiddleware(sugar))

	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Invalid path format", http.StatusNotFound)
	})

	// Public HTML routes
	router.Get("/", handlers.IndexHandler(sugar))
	router.Get("/login", handlers.LoginPageHandler(sugar))
	router.Get("/register", handlers.RegisterPageHandler(sugar))
	router.Get("/profile", handlers.ProfilePageHandler(sugar))
	router.Get("/measurements", handlers.MeasurementsPageHandler(sugar))

	// Public API routes
	router.Post("/api/register", handlers.RegisterHandler(dbStor, sugar))
	router.Post("/api/login", handlers.LoginHandler(dbStor, jwtService, sugar))

	// Protected API routes
	router.Group(func(r chi.Router) {
		r.Use(middlewares.AuthMiddleware(jwtService, sugar))
		r.Get("/api/profile", handlers.ProfileHandler(sugar))
		r.Post("/api/measurements/process", measurementHandler.ProcessMeasurementHandler)
		r.Get("/api/measurements/history", measurementHandler.GetHistoryHandler)
		r.Get("/api/measurements/status", measurementHandler.GetTaskStatusHandler)
		r.Get("/api/measurements/download", measurementHandler.DownloadResultHandler)
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	srv := &http.Server{
		Addr:         cfg.RunAddr,
		Handler:      router,
		ReadTimeout:  5 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		sugar.Infof("Running server on %s", cfg.RunAddr)
		sugar.Infof("Available routes:")
		sugar.Infof("  GET  /             - Main page")
		sugar.Infof("  GET  /login        - Login page")
		sugar.Infof("  GET  /register     - Register page")
		sugar.Infof("  GET  /profile      - Profile page")
		sugar.Infof("  GET  /measurements - Measurements page")
		sugar.Infof("  POST /api/register - Register API")
		sugar.Infof("  POST /api/login    - Login API")
		sugar.Infof("  POST /api/measurements/process - Process measurements")
		sugar.Infof("  GET  /api/measurements/history - Get history (cached)")
		sugar.Infof("  GET  /api/measurements/status  - Get task status")
		sugar.Infof("  GET  /api/measurements/download - Download result")

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			sugar.Fatalf("Server failed to start: %v", err)
		}
	}()

	<-ctx.Done()
	sugar.Info("Shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		sugar.Errorf("Server shutdown error: %v", err)
	} else {
		sugar.Info("Server shutdown complete")
	}
}
