package main

import (
	"collaborative/internal/auth"
	"collaborative/internal/config"
	"collaborative/internal/handlers"
	"collaborative/internal/middlewares"
	"collaborative/internal/storage"
	"context"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"go.uber.org/zap"
)

func main() {
	cfg := config.LoadConfig()
	// init logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		logger.Fatal("cannot initialize zap")
	}
	defer logger.Sync()

	sugar := logger.Sugar()

	// init DB
	var dbStor *storage.DBStorage

	if cfg.DSN != "" {
		sugar.Infof("Initializing PostgresSQL storage with DSN: %s", cfg.DSN)

		dbStor, err = storage.NewDBStorage(cfg.DSN)
		if err != nil {
			sugar.Fatalf("Failed to save metrics on exit: %v", err)
		}
		defer func() {
			if err := dbStor.Close(); err != nil {
				sugar.Errorf("Failed to close DB connection: %v", err)
			}
		}()
	}

	// init JWT services
	jwtService := auth.NewJWTService(cfg.JWTSecret, cfg.TokenExpiry)
	sugar.Infof("JWT service initialized with expiry: %d hours", cfg.TokenExpiry)
	// init router
	router := chi.NewRouter()

	// default router methods
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

	// public route
	router.Group(func(r chi.Router) {
		r.Get("/", handlers.IndexHandler(sugar))
		r.Get("/login", handlers.LoginPageHandler(sugar))
		r.Get("/register", handlers.RegisterPageHandler(sugar))

		r.Post("/register", handlers.RegisterHandler(dbStor, sugar))
		r.Post("/login", handlers.LoginHandler(dbStor, jwtService, sugar))
	})

	// private route
	router.Group(func(r chi.Router) {
		r.Use(middlewares.AuthMiddleware(jwtService, sugar))
		r.Get("/profile", handlers.ProfilePageHandler(sugar))
		r.Get("/api/profile", handlers.ProfileHandler(sugar))
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer stop()

	// Start the HTTP server in a goroutine
	srv := &http.Server{
		Addr:         cfg.RunAddr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	go func() {
		sugar.Infof("Running server on %s", cfg.RunAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			sugar.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Block until a signal is received (context is canceled)
	<-ctx.Done()
	sugar.Info("Shutdown signal received")

	// Create a context with timeout for graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown the server gracefully
	if err := srv.Shutdown(shutdownCtx); err != nil {
		sugar.Errorf("Server shutdown error: %v", err)
	} else {
		sugar.Info("Server shutdown complete")
	}
}
