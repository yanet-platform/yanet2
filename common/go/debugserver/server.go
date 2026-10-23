package debugserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"net/netip"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
)

// readHeaderTimeout bounds a stalled request header.
//
// No write timeout is set, since a CPU profile or trace streams for as long
// as the request asks.
const readHeaderTimeout = 10 * time.Second

type options struct {
	Log *zap.Logger
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop(),
	}
}

// Option configures a Server.
type Option func(*options)

// WithLog sets the logger.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

// Server serves the runtime profiles under /debug/pprof/.
type Server struct {
	endpoint string
	log      *zap.Logger
}

// NewServer constructs a debug server for cfg.
func NewServer(cfg *Config, options ...Option) *Server {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	return &Server{
		endpoint: cfg.Endpoint,
		log:      opts.Log,
	}
}

// Run serves until ctx is done.
//
// A server without an endpoint returns at once. Requests still in flight
// when ctx is done are cut off rather than awaited, so a long profile does
// not hold up shutdown.
func (m *Server) Run(ctx context.Context) error {
	if m.endpoint == "" {
		return nil
	}

	listener, err := m.listen()
	if err != nil {
		return fmt.Errorf("failed to listen on debug endpoint %q: %w", m.endpoint, err)
	}

	server := &http.Server{
		Handler:           newMux(),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	stop := context.AfterFunc(ctx, func() {
		_ = server.Close()
	})
	defer stop()

	m.log.Info("debug server listening", zap.String("endpoint", m.endpoint))
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return fmt.Errorf("debug server failed: %w", err)
}

func (m *Server) listen() (net.Listener, error) {
	if strings.HasPrefix(m.endpoint, "/") {
		return listenUnix(m.endpoint)
	}

	if !isLoopback(m.endpoint) {
		m.log.Warn("debug endpoint is reachable beyond loopback",
			zap.String("endpoint", m.endpoint),
		)
	}

	return net.Listen("tcp", m.endpoint)
}

// isLoopback reports whether a TCP endpoint binds loopback only.
//
// A wildcard or empty host binds every interface, and a host name other
// than localhost is not resolved here, so both count as not loopback.
func isLoopback(endpoint string) bool {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}

	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}

// listenUnix listens on a socket at path that only its owner can connect
// to.
//
// A stale socket at path is replaced; a socket still served or any other
// kind of file is an error. Connecting needs write permission, which the
// default umask already withholds from other users before the socket is
// restricted to its owner.
func listenUnix(path string) (net.Listener, error) {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	case info.Mode().Type() != os.ModeSocket:
		return nil, fmt.Errorf("%s exists and is not a socket", path)
	default:
		if conn, err := net.Dial("unix", path); err == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("%s is already being served", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}

	return listener, nil
}

// newMux routes the runtime profiles on a mux of its own.
//
// The pprof package also registers these routes on the process-wide
// default mux, which no server in this codebase serves.
func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)

	return mux
}
