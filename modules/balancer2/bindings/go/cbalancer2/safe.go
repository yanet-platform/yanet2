// Package cbalancer2 wraps the balancer2 controlplane C API in idiomatic Go.
package cbalancer2

import (
	"fmt"
	"math"
	"net/netip"

	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// SessionTimeouts holds per-state session expiry timeouts in seconds.
type SessionTimeouts struct {
	TCPSynAck uint32
	TCPSyn    uint32
	TCPFin    uint32
	TCP       uint32
	UDP       uint32
}

// RealConfig describes a single backend (real) the balancer can forward
// traffic to.
//
// Dst is the real's destination address. Src is the source network used as
// the encapsulation source (for the IPIP/GRE tunnel). Its mask may be any
// (possibly non-contiguous) bitmask, and its address family must match Dst.
type RealConfig struct {
	// Dst is the backend destination address.
	Dst netip.Addr
	// Src is required and must match the destination address family.
	Src *xnetip.Network
	// CounterName names the optional per-backend counter.
	CounterName string
}

// AllowedSources describes one entry in a virtual service's source allow
// list. A packet is admitted only if its source address matches one of the
// listed networks AND its source port matches one of the listed ranges. An
// empty set of networks disallows all networks. An empty set of ports allows
// all ports.
type AllowedSources struct {
	// Net4s is the contiguous IPv4 allowed-source set.
	Net4s []xnetip.Contiguous[xnetip.Network4]
	// Net6s is the bi-contiguous IPv6 allowed-source set.
	Net6s []xnetip.BiContiguous
	// PortRanges is the allowed source port range set.
	PortRanges filter.PortRanges
	// CounterName is the counter name for traffic accounting.
	CounterName string
}

// VSConfig describes a single virtual service.
//
// A VS is identified by the tuple (Dst, address family, Port, Transport).
// If Port is 0 the VS is L3-only and matches every destination port of the
// given transport.
type VSConfig struct {
	Dst            netip.Addr
	Port           uint16
	Transport      TransportProto
	AllowedSources []AllowedSources
	Scheduler      VSScheduler
	Tunnel         TunnelKind
	Reals          []RealConfig
	FixMSS         bool
	CounterName    string
}

// NewBalancer builds a balancer handle from its full configuration.
//
// The session table chain is referenced, not owned, and must outlive the
// returned handle. The caller must Free the returned balancer when done.
func NewBalancer(
	agent *ffi.Agent,
	name string,
	chain *SessionTableChain,
	timeouts SessionTimeouts,
	vs []VSConfig,
	commonCounterName string,
	l4CounterName string,
) (*Balancer, error) {
	if uint64(len(vs)) > math.MaxUint32 {
		return nil, fmt.Errorf("too many virtual services: %d", len(vs))
	}
	return createBalancer(agent, name, chain, timeouts, vs, commonCounterName, l4CounterName)
}

// NewSessionTable creates a session table with the given capacity (number of
// session entries it can hold).
func NewSessionTable(agent *ffi.Agent, capacity uint64) (*SessionTable, error) {
	return createSessionTable(agent, capacity)
}

// NewSessionTableChain creates a session table chain seeded with the given
// front table. The table is not owned by the chain and must outlive it.
func NewSessionTableChain(agent *ffi.Agent, front *SessionTable) (*SessionTableChain, error) {
	return createSessionTableChain(agent, front)
}
