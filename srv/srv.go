package srv

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/doggydogworld/gobalancer/config"
	"github.com/doggydogworld/gobalancer/forwarder"
	"golang.org/x/sync/errgroup"
)

type Forwarder interface {
	Forward(ctx context.Context, info forwarder.FwdInfo) error
}

// newTLSConfig generates TLS configuration that uses modern best practices from a given config
// TODO: Consider adding support PKCS12
func newTLSConfig(cfg *config.Config) (*tls.Config, error) {
	p := x509.NewCertPool()

	pemBlock, _ := pem.Decode(cfg.RootCA)
	if pemBlock == nil {
		return &tls.Config{}, errors.New("no pem data found in configured rootCA")
	}
	caCrt, err := x509.ParseCertificate(pemBlock.Bytes)
	if err != nil {
		return &tls.Config{}, err
	}
	p.AddCert(caCrt)
	crt, err := tls.X509KeyPair(cfg.ServerCrt, cfg.ServerKey)
	if err != nil {
		return &tls.Config{}, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		RootCAs:      p,
		ClientCAs:    p,
		Certificates: []tls.Certificate{crt},
	}, nil
}

// DownstreamListener binds to an address and listens for connections to forward
// Provides authn/authz to protect the forwarder from accepting connections
type DownstreamListener struct {
	// Upstream is the name that this listener will forward to.
	// Policy enforcement and forwarding will need this value
	Upstream string

	// The authz component. All requests will need to pass a query to this.
	policy *policyEnforcer
	// listener is an bound socket that is ready to accept connections
	listener net.Listener
	// fwdr allows l4 forwarding for open connections
	fwdr Forwarder

	logger *slog.Logger
}

// Server is a set of downstream listeners that are ready to forward connections using a LCU load balancer
type Server struct {
	Downstreams []*DownstreamListener
	Forwarder   Forwarder
}

// NewDownstreamListenersFromCfg is a helper function that initializes multiple listeners and returns them
// Use this in combination with `StartDownstreamListeners` to concurrently start all listeners
func NewDownstreamListeners(cfg *config.Config, fwdr Forwarder) ([]*DownstreamListener, error) {
	logger := slog.Default()
	d := []*DownstreamListener{}
	policy := newPolicyEnforcerFromConfig(cfg)
	tlsConf, err := newTLSConfig(cfg)
	if err != nil {
		return d, err
	}
	for _, v := range cfg.Listeners {
		l, err := tls.Listen("tcp", v.Addr, tlsConf)
		if err != nil {
			return d, err
		}
		d = append(d, &DownstreamListener{
			Upstream: v.Upstream,
			fwdr:     fwdr,
			policy:   policy,
			logger:   logger,
			listener: l,
		})
	}
	return d, nil
}

func NewServerFromCfg(cfg *config.Config) (*Server, error) {
	fwdr, err := forwarder.NewLeastConnectionsFromConfig(context.Background(), cfg)
	if err != nil {
		return &Server{}, err
	}
	d, err := NewDownstreamListeners(cfg, fwdr)
	if err != nil {
		return &Server{}, err
	}
	return &Server{
		Downstreams: d,
		Forwarder:   fwdr,
	}, nil
}

// verifyTLS forces the handshake to happen and verifies user authenticy and authorization.
// Returns a user that passes authn/authz or an error if the user certificate is not verified.
//
// The default implementation of TLS will only do the handshake whenever the conn is read/written to.
// That could be problematic for our forwarder since we will take a rate limiting token if we pass it a connection that hasn't been written/read to.
// This function will force the handshake to happen NOW and finish within 5 seconds.
func (d *DownstreamListener) verifyTLS(ctx context.Context, conn *tls.Conn) (string, error) {
	deadline, cancel := context.WithTimeout(ctx, 5.0*time.Second)
	defer cancel()
	if err := conn.HandshakeContext(deadline); err != nil {
		return "", err
	}

	user, ou, err := extractCertSubjFromConn(conn)
	if err != nil {
		return "", err
	}

	allow, err := d.policy.query(policyQuery{
		user:     user,
		ou:       ou,
		upstream: d.Upstream,
	})
	if err != nil {
		return "", err
	}
	if !allow {
		return "", errors.New("user is not authorized to access resource")
	}

	return user, nil
}

func extractCertSubjFromConn(conn *tls.Conn) (string, string, error) {
	cert := conn.ConnectionState().PeerCertificates[0]
	if len(cert.Subject.OrganizationalUnit) == 0 {
		return "", "", errors.New("user certificate has no OU set")
	}
	user := cert.Subject.CommonName
	ou := cert.Subject.OrganizationalUnit[0]
	return user, ou, nil
}

// handleConn performs authn/authz checks and forwards connections if they pass
func (d *DownstreamListener) handleConn(ctx context.Context, conn net.Conn) error {
	defer conn.Close()
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return errors.New("did not receive a TLS connection refusing to serve connection")
	}
	// verify authenticity and authorization for user
	user, err := d.verifyTLS(ctx, tlsConn)
	if err != nil {
		return err
	}

	// TODO: Could consider setting deadlines for read/write to conn
	// would be done with SetReadDeadline/SetWriteDeadline/SetDeadline method
	// Would need to also have a wrapper around conn Read/Write to reset the deadline
	// This would make it so potentially dead upstream servers don't hang the client side
	return d.fwdr.Forward(ctx, forwarder.FwdInfo{
		Upstream:       d.Upstream,
		Conn:           conn,
		RateLimiterKey: user,
	})
}

// serve will accept connections on a single downstream listener and will handle authn/authz.
// Errors returned from this are expected to be fatal to the functioning of the app
// e.g. accept from a listener returns an error.
//
// Errors received when handling connections are not returned and are logged as errors.
func (d *DownstreamListener) serve(ctx context.Context) error {
	defer d.listener.Close()
	connChan := make(chan net.Conn)
	ctx, cancel := context.WithCancelCause(ctx)
	fmt.Printf("%s <-> %s\n", d.listener.Addr().String(), d.Upstream)

	// Goroutine to accept connections and send them over a channel
	go func() {
		for {
			conn, err := d.listener.Accept()
			if err != nil {
				cancel(err)
				return
			}
			connChan <- conn
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case conn := <-connChan:
			// TODO: Consider adding some protection from a goroutine leak here? maybe we can trust the func or add a deadline
			go func() {
				err := d.handleConn(ctx, conn)
				if err != nil {
					d.logger.Error("handleConn.error", "upstream", d.Upstream, "error", err.Error())
				}
			}()
		}
	}
}

// ListenAndServe will start the server and forward connections that pass authn/authz
func (s *Server) ListenAndServe(ctx context.Context) error {
	e, ctx := errgroup.WithContext(ctx)

	for _, d := range s.Downstreams {
		d := d
		e.Go(func() error {
			return d.serve(ctx)
		})
	}

	fmt.Printf("Load balancer ready for connections...\nListening on:\n")
	return e.Wait()
}
