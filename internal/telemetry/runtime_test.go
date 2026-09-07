package telemetry

import (
	"sync"
	"testing"
)

func TestStagesAreTransientAndConcurrent(t *testing.T) {
	r := New()
	r.SetStage("task", "checking")
	first, _ := r.Stage("task")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.SetStage("task", "calculating"); r.Stage("task") }()
	}
	wg.Wait()
	current, _ := r.Stage("task")
	if !first.StartedAt.Equal(current.StartedAt) || current.Name != "calculating" {
		t.Fatal(current)
	}
	r.Forget("task")
	if _, ok := r.Stage("task"); ok {
		t.Fatal("completed task retained")
	}
}
