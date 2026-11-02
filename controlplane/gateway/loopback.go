package gateway

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// errBrowserConnectionClosed fails a call that arrives after its browser
// connection has closed.
var errBrowserConnectionClosed = errors.New("browser connection closed")

// loopbackCredentials are the transport credentials of the loopback server.
//
// An in-memory connection opened for a browser TLS connection reports that
// connection's TLS state as its peer, so the client certificate the browser
// presented authenticates the calls made over it. Any other in-memory
// connection runs no transport security.
type loopbackCredentials struct {
	credentials.TransportCredentials
}

// newLoopbackCredentials returns credentials that add no transport security
// of their own.
func newLoopbackCredentials() loopbackCredentials {
	return loopbackCredentials{TransportCredentials: insecure.NewCredentials()}
}

// ServerHandshake reports the TLS state of the browser connection an
// in-memory connection was opened for.
func (m loopbackCredentials) ServerHandshake(rawConn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, ok := rawConn.(*browserPeerConn)
	if !ok {
		return m.TransportCredentials.ServerHandshake(rawConn)
	}

	info := credentials.TLSInfo{
		State:         *conn.State,
		SecurityLevel: credentials.PrivacyAndIntegrity,
	}

	return rawConn, info, nil
}

// Clone returns a copy that still reports the TLS state of browser
// connections.
func (m loopbackCredentials) Clone() credentials.TransportCredentials {
	return loopbackCredentials{TransportCredentials: m.TransportCredentials.Clone()}
}

// browserPeerListener is an in-memory listener whose every connection
// carries the TLS state of one browser connection.
type browserPeerListener struct {
	*bufconn.Listener
	state *tls.ConnectionState
}

// Accept returns the next in-memory connection, tagged with the TLS state
// of the browser connection.
func (m *browserPeerListener) Accept() (net.Conn, error) {
	conn, err := m.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return &browserPeerConn{Conn: conn, State: m.state}, nil
}

// browserPeerConn is an in-memory connection opened for a browser TLS
// connection.
type browserPeerConn struct {
	net.Conn
	// State is the TLS state of the browser connection.
	State *tls.ConnectionState
}

// browserConnection is the way into the loopback server for the calls of
// one browser TLS connection, opened by its first call.
type browserConnection struct {
	tlsConn  *tls.Conn
	server   *grpc.Server
	endpoint string
	mu       sync.Mutex
	backend  *backend
	listener *bufconn.Listener
	closed   bool
}

// Backend returns the connection into the loopback server that carries the
// TLS state of the browser connection, opening it on first use.
func (m *browserConnection) Backend() (*backend, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, errBrowserConnectionClosed
	}
	if m.backend != nil {
		return m.backend, nil
	}

	// A browser connection serves calls only once its handshake is done, so
	// the state is final here.
	state := m.tlsConn.ConnectionState()
	listener := newMemoryListener()
	go func() {
		_ = serve(m.server, &browserPeerListener{Listener: listener, state: &state})
	}()

	peer, err := dialMemoryBackend(m.endpoint, listener, insecure.NewCredentials())
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	m.backend, m.listener = peer, listener

	return peer, nil
}

// Close closes the way into the loopback server, and every call made after
// it fails.
func (m *browserConnection) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	if m.backend == nil {
		return
	}

	_ = m.backend.Close()
	_ = m.listener.Close()
}

// browserConnectionKey keys the browser connection a call arrived on in the
// call's context.
type browserConnectionKey struct{}

// browserConnectionFromContext returns the browser connection a call
// arrived on, if it arrived on a browser TLS connection.
func browserConnectionFromContext(ctx context.Context) (*browserConnection, bool) {
	browser, ok := ctx.Value(browserConnectionKey{}).(*browserConnection)
	return browser, ok
}

// browserConnections tracks the way into the loopback server of every open
// browser TLS connection of the HTTP server.
type browserConnections struct {
	server      *grpc.Server
	endpoint    string
	mu          sync.Mutex
	connections map[net.Conn]*browserConnection
}

// newBrowserConnections returns a tracker opening its ways into the loopback
// server, each labeled with the address the gateway is reachable at.
func newBrowserConnections(server *grpc.Server, endpoint string) *browserConnections {
	return &browserConnections{
		server:      server,
		endpoint:    endpoint,
		connections: map[net.Conn]*browserConnection{},
	}
}

// Track starts tracking a newly accepted connection and returns the context
// its calls are served under.
//
// A plaintext connection carries no TLS state and is not tracked.
func (m *browserConnections) Track(ctx context.Context, conn net.Conn) context.Context {
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return ctx
	}

	browser := &browserConnection{tlsConn: tlsConn, server: m.server, endpoint: m.endpoint}
	m.add(conn, browser)

	return context.WithValue(ctx, browserConnectionKey{}, browser)
}

// Forget closes the way into the loopback server of a connection that
// closed or left the HTTP server.
func (m *browserConnections) Forget(conn net.Conn, state http.ConnState) {
	if state != http.StateClosed && state != http.StateHijacked {
		return
	}

	if browser, ok := m.remove(conn); ok {
		browser.Close()
	}
}

func (m *browserConnections) add(conn net.Conn, browser *browserConnection) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.connections[conn] = browser
}

func (m *browserConnections) remove(conn net.Conn) (*browserConnection, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	browser, ok := m.connections[conn]
	delete(m.connections, conn)

	return browser, ok
}

// loopbackBackend proxies into the loopback server, over the way of the
// browser TLS connection a call arrived on when there is one.
type loopbackBackend struct {
	*backend
}

// GetConnection returns the connection into the loopback server for a call,
// forwarding its metadata the way every backend does.
func (m *loopbackBackend) GetConnection(ctx context.Context, fullMethodName string) (context.Context, *grpc.ClientConn, error) {
	browser, ok := browserConnectionFromContext(ctx)
	if !ok {
		return m.backend.GetConnection(ctx, fullMethodName)
	}

	peer, err := browser.Backend()
	if err != nil {
		return ctx, nil, err
	}

	return peer.GetConnection(ctx, fullMethodName)
}
