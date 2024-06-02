package upstream

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type UpstreamStatus int

const (
	NOTREADY UpstreamStatus = iota
	// Upstream is ready to receive connections if it has one healthy backend
	READY
)

var (
	ErrUpstreamNotReady = errors.New("upstream is not ready for requests")
	ErrBackendUnhealthy = errors.New("backend is unhealthy")
	ErrBackendRemoved   = errors.New("backend config has been removed")
)

type Upstream struct {
	Name   string
	Status atomic.Int32

	*Tracker
	*UpstreamHeartbeats
}

func NewUpstream(name string) *Upstream {
	logger := slog.Default()
	t := &Tracker{
		UpstreamName:    name,
		Ctx:             context.Background(),
		healthyBackends: map[string]activeConns{},
		backendCanceler: map[string]*backendCtx{},
		logger:          logger,
		mu:              sync.Mutex{},
	}
	h := &UpstreamHeartbeats{
		UpstreamName: name,
		stoppers:     map[*BackendHeartbeat]chan struct{}{},
		mu:           sync.Mutex{},
		logger:       logger,
	}
	return &Upstream{
		Name:               name,
		Tracker:            t,
		UpstreamHeartbeats: h,
	}
}

// WaitForReady is a convenience function to wait for the upstream to be ready in the duration.
// This is mostly to simplify testing and shouldn't really be used to confirm readiness as it can cause a TOCTOU race.
// In concurrency it's better to ask for forgiveness rather than permission so use NextWithContext for normal use.
func (u *Upstream) WaitForReady(d time.Duration) error {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if u.Status.Load() == int32(READY) {
			return nil
		}
	}
	return ErrUpstreamNotReady
}
