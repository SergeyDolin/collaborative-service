package handlers

import (
	"collaborative/internal/middlewares"
	"collaborative/internal/model"
	"collaborative/internal/storage"
	"collaborative/internal/telemetry"
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStatusStagesAreOnlyVisibleToOwner(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is required")
	}
	ctx := context.Background()
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("workflow_handlers_%d", time.Now().UnixNano())
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		root.Close()
		t.Fatal(err)
	}
	defer func() { _, _ = root.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); root.Close() }()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := storage.NewDBStorage(u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tasks := storage.NewTaskStorage(db.Pool())
	if err = tasks.InitTaskSchema(); err != nil {
		t.Fatal(err)
	}
	id := "00000000-0000-0000-0000-000000000001"
	if err = tasks.CreateTask(&model.ProcessingTask{ID: id, UserLogin: "alice", Filename: "test.obs", Config: model.DefaultConfig(model.MethodPPP), Status: model.StatusProcessing, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	telemetry.Default.SetStage(id, "calculating")
	defer telemetry.Default.Forget(id)
	h := NewMeasurementHandler(db, tasks, zap.NewNop().Sugar())
	for _, tc := range []struct {
		login  string
		status int
	}{{"alice", 200}, {"bob", 404}, {"", 401}} {
		req := httptest.NewRequest("GET", "/api/measurements/status?id="+id, nil)
		if tc.login != "" {
			req = req.WithContext(context.WithValue(req.Context(), middlewares.UserContextKey, tc.login))
		}
		rec := httptest.NewRecorder()
		h.GetTaskStatusHandler(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.login, rec.Code, rec.Body.String())
		}
		if tc.login == "alice" {
			var result map[string]interface{}
			if err = json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result["stage"] != "calculating" || result["stageUpdatedAt"] == nil || result["startedAt"] == nil {
				t.Fatal(result)
			}
		} else if strings.Contains(rec.Body.String(), "calculating") {
			t.Fatal("stage leaked to non-owner")
		}
	}
}

func TestDisabledPositioningCannotBeStarted(t *testing.T) {
	h := NewCollaborativeHandler(nil, zap.NewNop().Sugar(), func(model.CollaborativeSession) model.SessionDiagnostics {
		return model.SessionDiagnostics{WorkerEnabled: false}
	})
	router := chi.NewRouter()
	router.Post("/sessions/{id}/positioning", h.SetPositioning)
	req := httptest.NewRequest(http.MethodPost, "/sessions/1/positioning", strings.NewReader(`{"enabled":true}`))
	req = req.WithContext(context.WithValue(req.Context(), middlewares.UserContextKey, "alice"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d: %s", rec.Code, rec.Body.String())
	}
}
