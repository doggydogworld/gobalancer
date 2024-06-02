package srv

import (
	"errors"
	"log/slog"
	"sync"

	"github.com/doggydogworld/gobalancer/config"
)

type policyEnforcer struct {
	upstreamTags map[string][]string
	logger       *slog.Logger
	mu           sync.RWMutex
}

type policyQuery struct {
	user     string
	ou       string
	upstream string
}

func newPolicyEnforcerFromConfig(cfg *config.Config) *policyEnforcer {
	m := map[string][]string{}
	logger := slog.Default().WithGroup("audit")
	for _, v := range cfg.Upstreams {
		m[v.Name] = v.Tags
	}
	return &policyEnforcer{
		upstreamTags: m,
		logger:       logger,
	}
}

func (p *policyEnforcer) query(q policyQuery) (bool, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	tags, ok := p.upstreamTags[q.upstream]
	if !ok {
		return false, errors.New("upstream wasn't found in config")
	}

	for _, t := range tags {
		// Attempt to find ou in tags
		if t == q.ou {
			return true, nil
		}
	}

	p.logger.Info("access_denied", "user", q.user, "upstream", q.upstream)
	// Deny by default
	return false, nil
}
