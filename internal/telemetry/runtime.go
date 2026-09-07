// Package telemetry holds only the current stage of running tasks in memory.
package telemetry

import (
	"sync"
	"time"
)

type Stage struct {
	Name      string    `json:"name"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type Runtime struct {
	mu     sync.RWMutex
	stages map[string]Stage
}

func New() *Runtime { return &Runtime{stages: make(map[string]Stage)} }

var Default = New()

func (r *Runtime) SetStage(id, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	stage, ok := r.stages[id]
	if !ok {
		stage.StartedAt = now
	}
	stage.Name, stage.UpdatedAt = name, now
	r.stages[id] = stage
}
func (r *Runtime) Stage(id string) (Stage, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.stages[id]
	return s, ok
}
func (r *Runtime) Forget(id string) { r.mu.Lock(); defer r.mu.Unlock(); delete(r.stages, id) }
