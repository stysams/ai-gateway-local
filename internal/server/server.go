// Package server hosts the headless gateway's HTTP surface: health checks,
// the loopback-only management API prefix and the /v1 data plane. It supports
// loopback and an explicit all-local-interfaces bind without exposing the
// management plane to non-loopback clients or silently changing ports.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"ai-gateway/internal/autostart"
	"ai-gateway/internal/config"
	"ai-gateway/internal/logstore"
	"ai-gateway/internal/outbound/anthropic"
	"ai-gateway/internal/outbound/openaichat"
	"ai-gateway/internal/outbound/openairesponses"
	"ai-gateway/internal/point"
	"ai-gateway/internal/secret"
)

// ShutdownGrace is the maximum time in-flight requests may take to finish
// during graceful shutdown (docs/v1-scheme.md §11.1).
const ShutdownGrace = 30 * time.Second

// Server assembles the HTTP server around the config manager, the system
// key store and the upstream connection pool.
type Server struct {
	cfg     *config.Manager
	secrets secret.Store
	version string
	pid     int
	httpSrv *http.Server
	ln      net.Listener

	// upstreams owns the three adapter pools over shared, safely configured
	// transports; all providers of one protocol reuse their pool's
	// connection pool (task packages C and D).
	upstreamsChat      *openaichat.Pool
	upstreamsResponses *openairesponses.Pool
	upstreamsAnthropic *anthropic.Pool
	warnings           *logstore.Writer
	points             *point.Manager
	autostart          autostart.Registrar
	modelMu            sync.RWMutex
	modelCache         map[string][]ProviderModel
	limiter            *requestLimiter
	metrics            *gatewayMetrics
	circuits           *circuitBreaker

	// txMu serializes provider write transactions (create/update/delete):
	// each transaction reads a config snapshot, mutates it and atomically
	// writes it back together with the key store steps. Without the lock,
	// concurrent writers could both build on the same stale snapshot and
	// silently drop each other's update (docs/v1-scheme.md §6.3). Read-only
	// endpoints do not take it.
	txMu         sync.Mutex
	shutdownCh   chan struct{}
	shutdownOnce sync.Once
}

// New creates a Server. version and pid are reported by /api/v1/status;
// secrets is the system key store consulted by readyz, doctor and the
// provider CRUD transactions (docs/v1-scheme.md §6, §9.2).
func New(cfg *config.Manager, secrets secret.Store, version string, pid int) *Server {
	s := &Server{
		cfg:     cfg,
		secrets: secrets,
		version: version,
		pid:     pid,
	}
	s.shutdownCh = make(chan struct{})
	s.upstreamsChat = openaichat.NewPool(secrets)
	s.upstreamsResponses = openairesponses.NewPool(secrets)
	s.upstreamsAnthropic = anthropic.NewPool(secrets)
	s.warnings = logstore.New(filepath.Dir(cfg.Path()))
	s.points = point.New(filepath.Dir(cfg.Path()))
	s.autostart = autostart.NewCurrentExecutable()
	s.modelCache = make(map[string][]ProviderModel)
	s.limiter = newRequestLimiter()
	s.metrics = newGatewayMetrics()
	s.circuits = newCircuitBreaker()
	s.httpSrv = &http.Server{
		Handler:           desktopCORS(s.routes()),
		ReadHeaderTimeout: 10 * time.Second,
		// No overall WriteTimeout: streaming responses (task packages C+)
		// must not be cut off mid-reasoning (docs/v1-scheme.md §9.4).
	}
	return s
}

// Supported listener hosts. 0.0.0.0 lets other local clients reach the
// gateway; callers must opt into it through config.
const loopbackHost = "127.0.0.1"
const allInterfacesHost = "0.0.0.0"

// Listen binds 127.0.0.1:port without serving yet. The host part of addr is
// enforced to be exactly 127.0.0.1 (docs/v1-scheme.md §1.2): the gateway
// never listens on a wildcard, an interface address or a resolved hostname,
// no matter who calls Listen. A bind failure (e.g. the port is already in
// use) is returned as an error; the gateway never silently switches ports.
func (s *Server) Listen(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	allowAllInterfaces := false
	if cfg := s.cfg.View(); cfg != nil {
		allowAllInterfaces = cfg.Listen.HostValue() == allInterfacesHost
		if _, err := s.warnings.Retain(cfg.Logging.Dir, cfg.Logging.RetentionDays, int64(cfg.Logging.QuotaBytes), time.Now()); err != nil {
			return fmt.Errorf("retain logs: %w", err)
		}
	}
	if host != loopbackHost && !(host == allInterfacesHost && allowAllInterfaces) {
		return fmt.Errorf("listen on %s: refused: supported hosts are %s and %s", addr, loopbackHost, allInterfacesHost)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	s.ln = ln
	return nil
}

// Serve blocks until Shutdown is called or the listener fails. It returns
// nil after a graceful shutdown.
func (s *Server) Serve() error {
	if s.ln == nil {
		return fmt.Errorf("Serve called before Listen")
	}
	err := s.httpSrv.Serve(s.ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// ListenAndServe binds addr and serves until Shutdown is called.
func (s *Server) ListenAndServe(addr string) error {
	if err := s.Listen(addr); err != nil {
		return err
	}
	return s.Serve()
}

// Addr returns the bound listener address, or "" before ListenAndServe.
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Shutdown gracefully stops accepting new requests and waits up to the
// context deadline for in-flight requests to finish.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.httpSrv.Shutdown(ctx)
	if logErr := s.warnings.Close(); err == nil {
		err = logErr
	}
	return err
}

// RequestShutdown asks the server to begin graceful shutdown. It is idempotent.
func (s *Server) RequestShutdown() {
	s.shutdownOnce.Do(func() { close(s.shutdownCh) })
}

// ShutdownRequested is closed when the management API asks for shutdown.
func (s *Server) ShutdownRequested() <-chan struct{} { return s.shutdownCh }
