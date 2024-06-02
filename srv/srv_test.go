package srv

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"embed"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/doggydogworld/gobalancer/config"
	"github.com/doggydogworld/gobalancer/forwarder"
)

//go:embed testcerts/*
var CertsFS embed.FS

// LoadStaticConfig loads a static configuration that isn't expected to change
// Be warned changing this could break tests
func LoadStaticConfig() (*config.Config, error) {
	ca, err := CertsFS.ReadFile("testcerts/root.crt")
	if err != nil {
		return &config.Config{}, err
	}
	crt, err := CertsFS.ReadFile("testcerts/server.crt")
	if err != nil {
		return &config.Config{}, err
	}
	key, err := CertsFS.ReadFile("testcerts/server.key")
	if err != nil {
		return &config.Config{}, err
	}
	return &config.Config{
		RootCA:    ca,
		ServerCrt: crt,
		ServerKey: key,
		RateLimit: &config.RateLimit{
			TokenRefillPerSecond: 0,
			MaxTokens:            0,
		},
		Listeners: []*config.Listener{
			{
				Addr:     "127.0.0.1:0",
				Upstream: "web",
			},
			{
				Addr:     "127.0.0.1:0",
				Upstream: "db",
			},
			{
				Addr:     "127.0.0.1:0",
				Upstream: "telemetry",
			},
		},
		Upstreams: []*config.Upstream{
			{
				Name: "web",
				Tags: []string{
					"sre",
					"webdev",
				},
				Backends: []string{},
			},
			{
				Name: "db",
				Tags: []string{
					"sre",
					"dba",
				},
				Backends: []string{},
			},
			{
				Name: "telemetry",
				Tags: []string{
					"sre",
					"webdev",
				},
				Backends: []string{},
			},
		},
	}, nil
}

// dummyForwarder is useful for doing some quick tests
// Responds with an HTTP response with WithMsg
type dummyForwarder struct {
	WithMsg string
}

// Forward just sends a HTTP response back with message
func (d *dummyForwarder) Forward(ctx context.Context, info forwarder.FwdInfo) error {
	defer info.Conn.Close()

	// TODO: This implementation actually breaks HTTP spec a bit. It's possible
	// for a TCP connection to reach here before the HTTP request is sent so this can
	// actually send a response before receiving a request. For a quick hacky fix
	// I added a `fmt.Fscanln` to force a read before sending a response back. This never
	// panic'd until the last minute so didn't have time to fix
	//
	// To see more on the issue: https://github.com/golang/go/issues/31259
	fmt.Fscanln(info.Conn)
	_, err := fmt.Fprintf(info.Conn, "HTTP/1.1 200 OK\n\r\n\r\n%s", d.WithMsg)
	if err != nil {
		return err
	}
	return nil
}

// setup a server and fail the test if server cannot start
func newTestServer(t *testing.T) (*Server, map[string]string) {
	m := map[string]string{}
	cfg, err := LoadStaticConfig()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServerFromCfg(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Configure downstream listeners with stubs and start listening on them
	for _, v := range srv.Downstreams {
		v.fwdr = &mustNotForwarder{t: t}
		m[v.Upstream] = v.listener.Addr().String()
	}
	return srv, m
}

// on an active server insert stubs as forwarders
func injectDummyForwarders(srv *Server) {
	srv.Forwarder = &dummyForwarder{}
	// Configure downstream listeners with stubs and start listening on them
	for _, v := range srv.Downstreams {
		v.fwdr = &dummyForwarder{
			// dummy forwarders will return upstream name
			WithMsg: v.Upstream,
		}
	}
}

// run test server and fail build if it errors
func runTestServer(t *testing.T, srv *Server) {
	if err := srv.ListenAndServe(context.Background()); err != nil {
		t.Error(err)
	}
}

// Setting up a https client for TLS testing
func newUserClient(t *testing.T, crtFile, keyFile string) *http.Client {
	// Create an HTTP client and perform requests
	caCert, err := CertsFS.ReadFile("testcerts/root.crt")
	if err != nil {
		t.Fatal(err)
	}
	userCert, err := CertsFS.ReadFile("testcerts/" + crtFile)
	if err != nil {
		t.Fatal(err)
	}
	userKey, err := CertsFS.ReadFile("testcerts/" + keyFile)
	if err != nil {
		t.Fatal(err)
	}
	crt, err := tls.X509KeyPair(userCert, userKey)
	if err != nil {
		log.Fatalf("error getting user certificate")
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)
	// Clone is important because DefaultTransport is a global pointer
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{
		Certificates: []tls.Certificate{crt},
		RootCAs:      caCertPool,
	}

	return &http.Client{Transport: tr}
}

func TestListenersWithdummy(t *testing.T) {
	srv, upstream := newTestServer(t)
	injectDummyForwarders(srv)
	sreClient := newUserClient(t, "sre.crt", "sre.key")
	webdevClient := newUserClient(t, "webdev.crt", "webdev.key")
	dbaClient := newUserClient(t, "dba.crt", "dba.key")

	go runTestServer(t, srv)

	tests := map[string]struct {
		client       *http.Client
		upstreamName string
		upstreamAddr string
		shouldFail   bool
	}{
		"sre can access web": {
			client:       sreClient,
			upstreamName: "web",
			upstreamAddr: upstream["web"],
		},
		"sre can access db": {
			client:       sreClient,
			upstreamName: "db",
			upstreamAddr: upstream["db"],
		},
		"sre can access telemetry": {
			client:       sreClient,
			upstreamName: "telemetry",
			upstreamAddr: upstream["telemetry"],
		},
		"dba can access db": {
			client:       dbaClient,
			upstreamName: "db",
			upstreamAddr: upstream["db"],
		},
		"webdev can access web": {
			client:       webdevClient,
			upstreamName: "web",
			upstreamAddr: upstream["web"],
		},
		"dba cannot access web": {
			client:       dbaClient,
			upstreamName: "web",
			upstreamAddr: upstream["web"],
			shouldFail:   true,
		},
		"dba cannot access telemetry": {
			client:       dbaClient,
			upstreamName: "telemetry",
			upstreamAddr: upstream["telemetry"],
			shouldFail:   true,
		},
		"webdev cannot access db": {
			client:       webdevClient,
			upstreamName: "db",
			upstreamAddr: upstream["db"],
			shouldFail:   true,
		},
		"webdev cannot access telemetry": {
			client:       webdevClient,
			upstreamName: "telemetry",
			upstreamAddr: upstream["telemetry"],
			shouldFail:   true,
		},
	}
	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Do requests on each listener
			resp, err := test.client.Get("https://" + test.upstreamAddr)
			if err != nil {
				// Catch tests that should fail the above e.g. denied authorization
				if test.shouldFail {
					return
				}
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(body)) != test.upstreamName {
				t.Fatalf("expected '%s' got %s", test.upstreamName, (body))
			}
		})
	}

}

// mustNotForwarder is used to test that unauthenticated/unauthorized client connections are not forwarded
type mustNotForwarder struct {
	t *testing.T
}

func (f *mustNotForwarder) Forward(ctx context.Context, info forwarder.FwdInfo) error {
	f.t.Fatalf("this connection should not have been forwarded")
	return nil
}

func injectMustNotForwarder(t *testing.T, srv *Server) {
	srv.Forwarder = &mustNotForwarder{t: t}
	for _, v := range srv.Downstreams {
		v.fwdr = &mustNotForwarder{t: t}
	}
}

func TestNoOpMustNotForward(t *testing.T) {
	srv, m := newTestServer(t)
	injectMustNotForwarder(t, srv)
	go runTestServer(t, srv)
	// Make sure that incoming tcp connections never attempt forwarding
	// This test might seem odd but an incoming tcp connection that has sent no data yet will create a `*net.Conn`
	// We need to make sure that we validate that connection before forwarding
	for _, addr := range m {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Errorf("server must not have been setup correctly")
		}
		// Write data to the connection
		_, err = fmt.Fprint(conn, "data")
		if err != nil {
			t.Errorf("server must not have been setup correctly")
		}
		err = conn.Close()
		if err != nil {
			t.Errorf("failed to clean up connection")
		}
	}
}

func TestNoCertMustNotForward(t *testing.T) {
	srv, m := newTestServer(t)
	injectMustNotForwarder(t, srv)
	go runTestServer(t, srv)
	for _, addr := range m {
		_, err := http.Get("http://" + addr)
		if err == nil {
			t.Errorf("connection should have failed due to no TLS")
		}
	}
}

func TestBadCertMustNotForward(t *testing.T) {
	srv, m := newTestServer(t)
	injectMustNotForwarder(t, srv)
	go runTestServer(t, srv)
	// User attempting to impersonate sre with self signed certificate
	selfSignedClient := newUserClient(t, "selfsigned.crt", "selfsigned.key")
	for _, addr := range m {
		_, err := selfSignedClient.Get("http://" + addr)
		if err == nil {
			t.Errorf("connection should have failed because a selfsigned cert shouldn't be accepted")
		}
	}
}

func TestAnonymousCertMustNotForward(t *testing.T) {
	srv, m := newTestServer(t)
	injectMustNotForwarder(t, srv)
	go runTestServer(t, srv)
	// User attempting to impersonate sre with self signed certificate
	anonymousSignedClient := newUserClient(t, "anonymous.crt", "anonymous.key")
	for _, addr := range m {
		_, err := anonymousSignedClient.Get("http://" + addr)
		if err == nil {
			t.Errorf("connection should have failed because cert had no CN or OU set")
		}
	}
}
