package workers

import (
	"collaborative/internal/model"
	"testing"
	"time"
)

func TestDiagnosticsDoesNotConfuseEnabledWithConnected(t *testing.T) {
	pw := &PositioningWorker{procs: make(map[int64]*processEntry)}
	sess := model.CollaborativeSession{ID: 1, EnablePositioning: true}
	if got := pw.Diagnostics(sess); got.WorkerEnabled || got.ProcessState != "disabled" {
		t.Fatal(got)
	}
	pw.enabled.Store(true)
	if got := pw.Diagnostics(sess); got.ProcessState != "waiting" || got.InputState != "unknown" || got.CorrectionsState != "unknown" {
		t.Fatal(got)
	}
	done := make(chan struct{})
	pw.procs[1] = &processEntry{done: done}
	if got := pw.Diagnostics(sess); got.ProcessState != "running" {
		t.Fatal(got)
	}
	close(done)
	if got := pw.Diagnostics(sess); got.ProcessState != "exited" {
		t.Fatal(got)
	}
	sess.LatestPosition = &model.CollaborativePosition{Quality: 1, UpdatedAt: time.Now().Add(-5 * time.Minute)}
	if got := pw.Diagnostics(sess); got.SolutionState != "stale" {
		t.Fatal(got)
	}
	sess.LatestPosition.UpdatedAt = time.Now().Add(-time.Second)
	if got := pw.Diagnostics(sess); got.SolutionState != "fresh" {
		t.Fatal(got)
	}
}
