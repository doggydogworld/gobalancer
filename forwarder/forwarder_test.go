package forwarder

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/doggydogworld/gobalancer/config"
	"go.uber.org/goleak"
	"golang.org/x/sync/errgroup"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func newHTTPServers(msg string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, msg)
	}))
}
func setupServersAndConfig() (*config.Config, map[string][]*httptest.Server, error) {
	web := []*httptest.Server{
		newHTTPServers("web"),
		newHTTPServers("web"),
		newHTTPServers("web"),
	}
	db := []*httptest.Server{
		newHTTPServers("db"),
		newHTTPServers("db"),
		newHTTPServers("db"),
	}
	telemetry := []*httptest.Server{
		newHTTPServers("telemetry"),
		newHTTPServers("telemetry"),
		newHTTPServers("telemetry"),
	}
	addrs := map[string][]*httptest.Server{}
	addrs["web"] = web
	addrs["db"] = db
	addrs["telemetry"] = telemetry
	return &config.Config{
		RateLimit: &config.RateLimit{
			// Setting to maxfloat64 disables the rate limiter for testing
			TokenRefillPerSecond: math.MaxFloat64,
			MaxTokens:            0,
		},
		Upstreams: []*config.Upstream{
			{
				Name: "web",
				Tags: []string{},
				Backends: []string{
					web[0].Listener.Addr().String(),
					web[1].Listener.Addr().String(),
					web[2].Listener.Addr().String(),
				},
			},
			{
				Name: "db",
				Tags: []string{},
				Backends: []string{
					db[0].Listener.Addr().String(),
					db[1].Listener.Addr().String(),
					db[2].Listener.Addr().String(),
				},
			},
			{
				Name: "telemetry",
				Tags: []string{},
				Backends: []string{
					telemetry[0].Listener.Addr().String(),
					telemetry[1].Listener.Addr().String(),
					telemetry[2].Listener.Addr().String(),
				},
			},
		},
	}, addrs, nil
}

// listen on a random port or fail the test
func mustListen(t testing.TB) net.Listener {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start listener: %v", err)
	}
	return l
}

func acceptAndFwd(fwdr *LeastConnections, upstream string, l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return nil
		}
		err = fwdr.Forward(context.Background(), FwdInfo{
			Upstream:       upstream,
			Conn:           conn,
			RateLimiterKey: "user",
		})
		if err != nil {
			return err
		}
	}
}

func doRequests(count int, addr string, expect string) error {
	for i := 0; i < count; i += 1 {
		client := http.Client{}
		resp, err := client.Get("http://" + addr)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if string(body) != (expect + "\n") {
			return fmt.Errorf("expected web got %s", string(body))
		}
		// If we don't close idle connections the client will default to reusing the connection
		// A reused connection just means we end up with 1 connection ever going through the lb
		client.CloseIdleConnections()
		resp.Body.Close()
	}
	return nil
}

func TestForwarder(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start HTTP servers
	cfg, servers, err := setupServersAndConfig()
	if err != nil {
		t.Fatalf("could not start test servers")
	}
	defer func() {
		for _, l := range servers {
			for _, s := range l {
				s.Close()
			}
		}
	}()
	fwdr, err := NewLeastConnectionsFromConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("could not start forwarder")
	}

	// Start listeners to forward to HTTP servers
	webListener := mustListen(t)
	dbListener := mustListen(t)
	telListener := mustListen(t)
	defer webListener.Close()
	defer dbListener.Close()
	defer telListener.Close()
	go func() {
		if err := acceptAndFwd(fwdr, "web", webListener); err != nil {
			t.Errorf("web listener has failed %v", err)
		}
	}()
	go func() {
		if err := acceptAndFwd(fwdr, "db", dbListener); err != nil {
			t.Errorf("db listener has failed %v", err)
		}
	}()
	go func() {
		if err := acceptAndFwd(fwdr, "telemetry", telListener); err != nil {
			t.Errorf("telemetry listener has failed %v", err)
		}
	}()

	eg := errgroup.Group{}
	eg.Go(func() error {
		return doRequests(100, webListener.Addr().String(), "web")
	})
	eg.Go(func() error {
		return doRequests(100, dbListener.Addr().String(), "db")
	})
	eg.Go(func() error {
		return doRequests(100, telListener.Addr().String(), "telemetry")
	})

	if err := eg.Wait(); err != nil {
		t.Error(err)
	}
}

func BenchmarkForwarder(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg, _, err := setupServersAndConfig()
	if err != nil {
		b.Fatalf("could not start test servers")
	}
	fwdr, err := NewLeastConnectionsFromConfig(ctx, cfg)
	fwdr.ratelimit.tokenRefillPerSecond = math.MaxFloat64
	if err != nil {
		b.Fatalf("could not start forwarder")
	}
	webListener := mustListen(b)
	dbListener := mustListen(b)
	telListener := mustListen(b)
	go func() {
		if err := acceptAndFwd(fwdr, "web", webListener); err != nil {
			b.Errorf("web listener has failed %b", err)
		}
	}()
	go func() {
		if err := acceptAndFwd(fwdr, "db", dbListener); err != nil {
			b.Errorf("db listener has failed %b", err)
		}
	}()
	go func() {
		if err := acceptAndFwd(fwdr, "telemetry", telListener); err != nil {
			b.Errorf("telemetry listener has failed %b", err)
		}
	}()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := doRequests(i, webListener.Addr().String(), "web"); err != nil {
				b.Error(err)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := doRequests(i, dbListener.Addr().String(), "db"); err != nil {
				b.Error(err)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := doRequests(i, telListener.Addr().String(), "telemetry"); err != nil {
				b.Error(err)
			}
		}()
		wg.Wait()
	}
}
