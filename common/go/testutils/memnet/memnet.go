// Package memnet connects tests to in-memory listeners by address, so gRPC
// servers and socket peers run inside a synctest bubble.
//
// Its connections block on condition variables: a goroutine waiting on one
// counts as durably blocked inside a bubble, which a goroutine waiting on a
// socket never does.
package memnet

import (
	"context"
	"net"
	"sync"
	"syscall"

	"google.golang.org/grpc/test/bufconn"
)

// BufferSize is how many bytes a connection holds in each direction before
// a write blocks until the peer reads.
const BufferSize = 64 * 1024

// Network binds in-memory listeners to addresses and dials them.
type Network struct {
	mu        sync.Mutex
	listeners map[string]*bufconn.Listener
}

// NewNetwork returns a network without listeners.
func NewNetwork() *Network {
	return &Network{listeners: map[string]*bufconn.Listener{}}
}

// Listen binds a new listener to address in place of the previous one.
func (m *Network) Listen(address string) *bufconn.Listener {
	m.mu.Lock()
	defer m.mu.Unlock()

	listener := bufconn.Listen(BufferSize)
	m.listeners[address] = listener
	return listener
}

// Dial connects to the listener bound to address, and fails as a refused
// connection does when none is.
//
// It waits for the listener to accept the connection, and fails once that
// listener is closed.
func (m *Network) Dial(ctx context.Context, address string) (net.Conn, error) {
	m.mu.Lock()
	listener := m.listeners[address]
	m.mu.Unlock()

	if listener == nil {
		return nil, &net.OpError{Op: "dial", Net: "memory", Err: syscall.ECONNREFUSED}
	}
	return listener.DialContext(ctx)
}
