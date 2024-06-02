package upstream

import (
	"context"
	"log/slog"
	"math"
	"sync"
)

// activeConns tracks contexts used for ongoing connections.
// Taking a length of activeConns will give you the number of active connections.
// The reason for using context is because they are expected to be request scoped for all
// incoming connections and allow us to propagate any cancellation signals e.g. in the event of a
// config change.
type activeConns map[context.Context]struct{}

// backendCtx holds the status of the backend as well as context information
// that can be used to associate contexts with the status of the backend.
type backendCtx struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
}

// Tracker manages tracking of connections to healthy hosts by relying on request scoped contexts.
type Tracker struct {
	UpstreamName string
	Cancel       context.CancelCauseFunc
	Ctx          context.Context
	// healthyBackends is a mapping of healthy backends by address to a mapping of contexts.
	// Only healthy addresses will be in this map and therefore this map will be searched
	// when deciding on least connections.
	// You can find the number of active connections for a backend with
	//	len(healthyBackends["127.0.0.1:0"])
	healthyBackends map[string]activeConns

	backendCanceler map[string]*backendCtx

	logger *slog.Logger
	mu     sync.Mutex
}

func NewTracker(parent context.Context, upstream string) *Tracker {
	ctx, cancel := context.WithCancelCause(parent)
	return &Tracker{
		UpstreamName:    upstream,
		Cancel:          cancel,
		Ctx:             ctx,
		healthyBackends: map[string]activeConns{},
		backendCanceler: map[string]*backendCtx{},
		logger:          slog.Default(),
		mu:              sync.Mutex{},
	}
}

func (t *Tracker) removeTrackedConn(ctx context.Context, addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.healthyBackends[addr], ctx)
}

// trackCtx will create a new derived context that listens to cancellation signals from two parent contexts:
// the input context and the context tied to the backend.
func (t *Tracker) trackCtx(outer context.Context, backend context.Context, addr string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(outer)
	// if backend is cancelled a goroutine is created that propagates the cancel
	afterBackendCancelled := context.AfterFunc(backend, func() {
		cancel(context.Cause(backend))
	})

	context.AfterFunc(ctx, func() {
		t.removeTrackedConn(outer, addr)
	})

	return ctx, func() {
		afterBackendCancelled()
		cancel(context.Canceled)
	}
}

func (t *Tracker) BackendActiveConns(addr string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.healthyBackends[addr])
}

// AddBackend will add backend by address to be tracked
func (t *Tracker) TrackBackend(addr string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// If doesn't exist add otherwise no-op
	if _, ok := t.healthyBackends[addr]; !ok {
		t.logger.Info("tracking backend", "upstream", t.UpstreamName, "addr", addr)
		ctx, cancel := context.WithCancelCause(t.Ctx)
		t.healthyBackends[addr] = activeConns{}
		t.backendCanceler[addr] = &backendCtx{
			ctx:    ctx,
			cancel: cancel,
		}
	}
}

// leastConnections chooses the least active backend.
// This does not lock so make sure to wrap this in a mu.Lock()
func (t *Tracker) leastConnections() string {
	var choice string
	min := math.MaxInt32
	for b, activeConns := range t.healthyBackends {
		if len(activeConns) < min {
			min = len(activeConns)
			choice = b
		}
	}
	return choice
}

// UntrackBackend will remove backend by address and send the error down as cancellation cause
func (t *Tracker) UntrackBackend(addr string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// With a fast enough heartbeat this could be called too often
	// Probably won't cause issues in actual use but in testing at 1ms period it caused issues
	// no-op on repeated use
	if c, ok := t.backendCanceler[addr]; ok {
		t.logger.Info("untracking backend", "upstream", t.UpstreamName, "addr", addr, "reason", err.Error())
		c.cancel(err)
		delete(t.backendCanceler, addr)
		delete(t.healthyBackends, addr)
	}
}

func (t *Tracker) NextWithContext(parent context.Context) (addr string, ctx context.Context, cancelFunc context.CancelFunc, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.healthyBackends) == 0 {
		err = ErrUpstreamNotReady
		return
	}
	addr = t.leastConnections()
	t.healthyBackends[addr][parent] = struct{}{}
	ctx, cancelFunc = t.trackCtx(parent, t.backendCanceler[addr].ctx, addr)
	return
}
