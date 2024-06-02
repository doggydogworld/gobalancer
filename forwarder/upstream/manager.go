package upstream

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/doggydogworld/gobalancer/config"
	"github.com/doggydogworld/gobalancer/forwarder/health"
)

type Manager struct {
	Upstreams     sync.Map
	BackendStatus sync.Map

	healthEvents chan backendStatEvent
	stop         chan struct{}
	logger       *slog.Logger
}

func NewManager() *Manager {
	return &Manager{
		Upstreams:     sync.Map{},
		BackendStatus: sync.Map{},
		healthEvents:  make(chan backendStatEvent),
		stop:          make(chan struct{}),
		logger:        slog.Default(),
	}
}

func (m *Manager) handleHealthy(upstream string, backend string) {
	m.logger.Info("BackendHealthy", "upstream", upstream, "backend", backend)
	up, err := m.GetUpstream(upstream)
	if err != nil {
		m.logger.Error("MissingUpstream", "msg", err)
		return
	}
	up.TrackBackend(backend)
	m.BackendStatus.Store(backend, HEALTHY)
	up.Status.Store(int32(HEALTHY))
}

func (m *Manager) handleUnhealthy(upstream string, backend string) {
	m.logger.Info("BackendUnhealthy", "upstream", upstream, "backend", backend)
	up, err := m.GetUpstream(upstream)
	if err != nil {
		m.logger.Error("MissingUpstream", "msg", err)
		return
	}
	up.UntrackBackend(backend, ErrBackendUnhealthy)
	m.BackendStatus.Store(backend, UNHEALTHY)
}

func (m *Manager) healthReceiver() {
	for e := range m.healthEvents {
		switch e.stat {
		case HEALTHY:
			m.handleHealthy(e.upstream, e.addr)
		case UNHEALTHY:
			if e.err != nil {
				m.logger.Error("BackendError", "msg", e.err)
			}
			m.handleUnhealthy(e.upstream, e.addr)
		}
	}
}

// LoadUpstreamFromConfig will setup an upstream based on the configuration.
func (m *Manager) LoadUpstreamFromConfig(cfg *config.Upstream) {
	var up *Upstream
	if val, err := m.GetUpstream(cfg.Name); err != nil {
		up = NewUpstream(cfg.Name)
		m.Upstreams.Store(cfg.Name, up)
	} else {
		up = val
	}
	for _, back := range cfg.Backends {
		hb := &BackendHeartbeat{
			UpstreamName: cfg.Name,
			Addr:         back,
			Checker: &health.TCP{
				Addr: back,
			},
			Period:  2 * time.Second,
			Timeout: time.Second,
			logger:  slog.Default(),
		}
		up.StartHeartbeat(context.Background(), hb, m.healthEvents)
	}
}

func (m *Manager) GetUpstream(name string) (*Upstream, error) {
	var up *Upstream
	if val, ok := m.Upstreams.Load(name); ok {
		up = val.(*Upstream)
	} else {
		return up, fmt.Errorf("upstream was not found")
	}
	return up, nil
}

func (m *Manager) Start() error {
	go m.healthReceiver()

	<-m.stop
	close(m.healthEvents)
	m.Upstreams.Range(func(key any, value any) bool {
		up := value.(*Upstream)
		up.StopAll()
		return true
	})
	return nil
}

func (m *Manager) Stop() {
	m.stop <- struct{}{}
}
