package upstream

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/doggydogworld/gobalancer/forwarder/health"
	"github.com/stretchr/testify/assert"
	"golang.org/x/net/nettest"
)

func newTestHeartbeat(addr string) *BackendHeartbeat {
	return &BackendHeartbeat{
		UpstreamName: "test",
		Addr:         addr,
		Checker: &health.TCP{
			Addr: addr,
		},
		Period:  5 * time.Millisecond,
		Timeout: time.Millisecond,
		logger:  slog.Default(),
	}
}

func TestWithHealthy(t *testing.T) {
	l1, err := nettest.NewLocalListener("tcp")
	assert.NoError(t, err)
	l2, err := nettest.NewLocalListener("tcp")
	assert.NoError(t, err)
	defer l1.Close()
	defer l2.Close()

	ctx := context.Background()
	out := make(chan backendStatEvent, 1)

	h := &UpstreamHeartbeats{
		UpstreamName: "test",
		stoppers:     map[*BackendHeartbeat]chan struct{}{},
		mu:           sync.Mutex{},
		logger:       slog.Default(),
	}

	hb1 := newTestHeartbeat(l1.Addr().String())
	hb2 := newTestHeartbeat(l2.Addr().String())

	h.StartHeartbeat(ctx, hb1, out)
	h.StartHeartbeat(ctx, hb2, out)
	// Expect only two events
	assert.Equal(t, HEALTHY, (<-out).stat)
	assert.Equal(t, HEALTHY, (<-out).stat)
	assert.Len(t, out, 0)

	// Cleanup
	h.StopAll()
}

func TestWithUnhealthy(t *testing.T) {
	// Same test as above but we close l2 to watch it be untracked
	l1, err := nettest.NewLocalListener("tcp")
	assert.NoError(t, err)
	l2, err := nettest.NewLocalListener("tcp")
	assert.NoError(t, err)

	ctx := context.Background()
	out := make(chan backendStatEvent, 1)

	h := &UpstreamHeartbeats{
		UpstreamName: "test",
		stoppers:     map[*BackendHeartbeat]chan struct{}{},
		mu:           sync.Mutex{},
		logger:       slog.Default(),
	}

	hb1 := newTestHeartbeat(l1.Addr().String())
	hb2 := newTestHeartbeat(l2.Addr().String())

	h.StartHeartbeat(ctx, hb1, out)
	h.StartHeartbeat(ctx, hb2, out)
	// Expect only two events
	assert.Equal(t, HEALTHY, (<-out).stat)
	assert.Equal(t, HEALTHY, (<-out).stat)
	assert.Len(t, out, 0)

	l2.Close()
	event := <-out
	assert.Equal(t, l2.Addr().String(), event.addr)
	assert.Equal(t, UNHEALTHY, event.stat)
	l1.Close()
	event = <-out
	assert.Equal(t, l1.Addr().String(), event.addr)
	assert.Equal(t, UNHEALTHY, event.stat)

	// Cleanup
	h.StopAll()
}
