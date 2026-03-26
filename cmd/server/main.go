package main

import (
	"collaborative/internal/handlers"
	"collaborative/internal/storage"
	"net/http"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"go.uber.org/zap"
)

func main() {
	parseFlags()
	// init logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		logger.Fatal("cannot initialize zap")
	}
	defer logger.Sync()

	sugar := logger.Sugar()

	// init DB
	var dbStor *storage.DBStorage

	if flagDSN != "" {
		sugar.Infof("Initializing PostgresSQL storage with DSN: %s", flagDSN)

		dbStor, err = storage.NewDBStorage(flagDSN)
		if err != nil {
			sugar.Fatalf("Failed to save metrics on exit: %v", err)
		}
		defer func() {
			if err := dbStor.Close(); err != nil {
				sugar.Errorf("Failed to close DB connection: %v", err)
			}
		}()
	}
	// init router
	router := chi.NewRouter()

	// default router methods
	router.Use(middleware.StripSlashes)
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Invalid path format", http.StatusNotFound)
	})

	// public route
	router.Group(func(r chi.Router) {
		r.Get("/", handlers.IndexHandler)
		r.Post("/register", handlers.RegisterHandler(dbStor, sugar))
	})

	sugar.Infof("Running server on %s", flagRunAddr)
	sugar.Fatal(http.ListenAndServe(flagRunAddr, router))
}
