package storage

import (
	"collaborative/internal/model"
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func workflowStorage(t *testing.T) (*DBStorage, *TaskStorage) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN is required for isolated PostgreSQL integration tests")
	}
	ctx := context.Background()
	root, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := pgx.Identifier{"workflow_" + uuid.New().String()}.Sanitize()
	if _, err = root.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		root.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = root.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); root.Close() })
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	db := &DBStorage{pool: pool}
	tasks := NewTaskStorage(pool)
	if err = db.initSchema(); err != nil {
		t.Fatal(err)
	}
	if err = tasks.InitTaskSchema(); err != nil {
		t.Fatal(err)
	}
	return db, tasks
}

func TestHistoryFiltersRespectOwnerAndRetention(t *testing.T) {
	db, s := workflowStorage(t)
	if err := db.CreateUser("alice", "test"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser("bob", "test"); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(24 * time.Hour).Add(12 * time.Hour)
	for _, row := range []struct {
		id, user, file, method string
		expired                bool
	}{
		{"a", "alice", "Point_A.obs", "ppp", false}, {"b", "alice", "100%.obs", "single", false},
		{"c", "bob", "Point_A.obs", "ppp", false}, {"d", "alice", "expired.obs", "ppp", true},
	} {
		if err := s.CreateTask(&model.ProcessingTask{ID: row.id, UserLogin: row.user, Filename: row.file, Config: model.UserProcessingConfig{Method: model.ProcessingMethod(row.method)}, Status: model.StatusPending, CreatedAt: base}); err != nil {
			t.Fatal(err)
		}
		if row.expired {
			if _, err := s.pool.Exec(context.Background(), "UPDATE processing_tasks SET expires_at=NOW()-INTERVAL '1 hour' WHERE id=$1", row.id); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct {
		name                string
		f                   TaskFilter
		limit, offset, want int
	}{
		{"all", TaskFilter{}, 50, 0, 2}, {"case insensitive", TaskFilter{Query: "point", Method: "ppp"}, 50, 0, 1},
		{"literal wildcard", TaskFilter{Query: "%"}, 50, 0, 1}, {"pagination", TaskFilter{}, 1, 1, 1},
		{"status", TaskFilter{Status: "completed"}, 50, 0, 0},
		{"inclusive date", TaskFilter{From: base.Format("2006-01-02"), To: base.Format("2006-01-02")}, 50, 0, 2},
		{"future", TaskFilter{From: base.AddDate(0, 0, 1).Format("2006-01-02")}, 50, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := s.GetUserTasksWithResults("alice", tc.limit, tc.offset, tc.f)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != tc.want {
				t.Fatalf("got %d want %d", len(rows), tc.want)
			}
			for _, r := range rows {
				if r["userLogin"] != "alice" || r["id"] == "d" {
					t.Fatal("owner/expiry filter violated")
				}
			}
		})
	}
	stats, err := s.GetSystemStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats["activeUsers"] != 2 {
		t.Fatal(stats)
	}
	if _, err := s.pool.Exec(context.Background(), "DELETE FROM processing_tasks"); err != nil {
		t.Fatal(err)
	}
	stats, err = s.GetSystemStats()
	if err != nil || stats["activeUsers"] != 2 {
		t.Fatalf("account count depends on tasks: %v %v", stats, err)
	}
}

func TestUserLookupDistinguishesDatabaseFailure(t *testing.T) {
	db, _ := workflowStorage(t)
	if _, err := db.GetUser("missing"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	db.pool.Close()
	if _, err := db.GetUser("missing"); err == nil || errors.Is(err, ErrUserNotFound) {
		t.Fatalf("database failure misclassified: %v", err)
	}
}
