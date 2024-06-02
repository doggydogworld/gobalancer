package upstream

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/doggydogworld/gobalancer/forwarder/health"
)

type BackendStatus int

const (
	INIT BackendStatus = iota
	HEALTHY
	UNHEALTHY
)

type backendStatEvent struct {
	upstream string
	addr     string
	stat     BackendStatus
	err      error
}

type BackendHeartbeat struct {
	UpstreamName string
	Addr         string
	Checker      health.HealthChecker
	Period       time.Duration
	Timeout      time.Duration

	logger *slog.Logger
}

// UpstreamHeartbeats provides an API for adding/removing heartbeats for a single upstream.
type UpstreamHeartbeats struct {
	UpstreamName string

	stoppers map[*BackendHeartbeat]chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	logger   *slog.Logger
}

func (b *BackendHeartbeat) beat(ctx context.Context, out chan<- backendStatEvent) error {
	ctx, cancel := context.WithTimeout(ctx, b.Timeout)
	defer cancel()
	event := backendStatEvent{
		upstream: b.UpstreamName,
		addr:     b.Addr,
	}
	check, changed, err := b.Checker.Check(ctx)
	if err != nil {
		return err
	}
	if changed {
		event.stat = UNHEALTHY
		if check == health.SUCCESS {
			event.stat = HEALTHY
		}
		out <- event
	}
	return nil
}

func (b *BackendHeartbeat) newErrEvent(err error) backendStatEvent {
	return backendStatEvent{
		upstream: b.UpstreamName,
		addr:     b.Addr,
		stat:     UNHEALTHY,
		err:      err,
	}
}

// Run starts the heartbeat and will start sending out events to be captured.
func (b *BackendHeartbeat) Run(ctx context.Context, stop <-chan struct{}) <-chan backendStatEvent {
	b.logger.Info("HeartbeatRunning", "upstream", b.UpstreamName, "backend", b.Addr)
	out := make(chan backendStatEvent)
	go func() {
		defer b.logger.Info("HeartbeatStopped", "upstream", b.UpstreamName, "backend", b.Addr)
		t := time.NewTicker(b.Period)
		ctx, cancel := context.WithCancel(ctx)
		// Ensuring proper cleanup
		defer cancel()
		defer close(out)
		defer t.Stop()

		if err := b.beat(ctx, out); err != nil {
			out <- b.newErrEvent(err)
		}
		// Main heartbeat loop
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				out <- b.newErrEvent(ctx.Err())
				return
			case <-t.C:
				if err := b.beat(ctx, out); err != nil {
					out <- b.newErrEvent(err)
				}
			}
		}
	}()
	return out
}

func (u *UpstreamHeartbeats) storeStopper(h *BackendHeartbeat, stop chan struct{}) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.stoppers[h] = stop
}

func (u *UpstreamHeartbeats) loadAndDeleteStopper(h *BackendHeartbeat) (stop chan struct{}, deleted bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if val, ok := u.stoppers[h]; ok {
		stop = val
		delete(u.stoppers, h)
		deleted = true
		return
	}
	deleted = false
	return
}

func (u *UpstreamHeartbeats) StartHeartbeat(ctx context.Context, h *BackendHeartbeat, out chan<- backendStatEvent) {
	stop := make(chan struct{})
	u.storeStopper(h, stop)
	u.wg.Add(1)
	go func() {
		defer u.wg.Done()
		// TODO: Figure out why this defer is executing immediately
		//defer u.StopHeartbeat(h)
		for e := range h.Run(ctx, stop) {
			out <- e
		}
	}()
}

func (u *UpstreamHeartbeats) StopHeartbeat(h *BackendHeartbeat) {
	if stop, deleted := u.loadAndDeleteStopper(h); deleted {
		close(stop)
	}
}

func (u *UpstreamHeartbeats) StopAll() {
	u.mu.Lock()
	defer u.mu.Unlock()

	for _, v := range u.stoppers {
		close(v)
	}
	clear(u.stoppers)

	u.wg.Wait()
}
