package forwarder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/doggydogworld/gobalancer/config"
	"github.com/doggydogworld/gobalancer/forwarder/upstream"
	"golang.org/x/time/rate"
)

type FwdInfo struct {
	Upstream       string
	Conn           net.Conn
	RateLimiterKey string
}

type LeastConnections struct {
	ratelimit *perClientRateLimiter
	d         net.Dialer
	manager   *upstream.Manager
}

func NewLeastConnectionsFromConfig(ctx context.Context, cfg *config.Config) (*LeastConnections, error) {
	m := upstream.NewManager()
	go m.Start()
	go func() {
		<-ctx.Done()
		m.Stop()
	}()
	for _, up := range cfg.Upstreams {
		m.LoadUpstreamFromConfig(up)
	}
	return &LeastConnections{
		manager: m,
		ratelimit: &perClientRateLimiter{
			maxTokens:            cfg.RateLimit.MaxTokens,
			tokenRefillPerSecond: cfg.RateLimit.TokenRefillPerSecond,
			clientRL:             make(map[string]*rate.Limiter),
		},
	}, nil
}

// fwd forwards a connection that was inflight completing its journey
func (l *LeastConnections) fwd(ctx context.Context, in FwdInfo, backend string) error {
	errc := make(chan error)
	upConn, err := l.d.DialContext(ctx, "tcp", backend)
	if err != nil {
		return err
	}

	// Connect both connections by copying in both connections
	go func() {
		defer upConn.Close()
		defer in.Conn.Close()
		_, err := io.Copy(in.Conn, upConn)
		errc <- err
	}()
	go func() {
		defer upConn.Close()
		defer in.Conn.Close()
		_, err := io.Copy(upConn, in.Conn)
		errc <- err
	}()

	err = <-errc
	errors.Join(err, <-errc)
	if err != nil {
		err = fmt.Errorf("failed to forward connection: %w", err)
	}
	return err
}

func (l *LeastConnections) Forward(ctx context.Context, info FwdInfo) error {
	if err := l.ratelimit.rateLimit(info.RateLimiterKey); err != nil {
		return err
	}
	fmt.Println("Getting upstream")
	up, err := l.manager.GetUpstream(info.Upstream)
	if err != nil {
		return err
	}
	up.WaitForReady(time.Second)
	fmt.Println("Getting ctx")
	backend, ctx, cancel, err := up.NextWithContext(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	fmt.Println("Forwarding")
	return l.fwd(ctx, info, backend)
}
