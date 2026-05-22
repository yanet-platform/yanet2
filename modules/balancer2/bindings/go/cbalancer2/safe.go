// Package cbalancer2 wraps the balancer2 controlplane C API in idiomatic Go.
package cbalancer2

import (
	"fmt"
	"math"
	"net/netip"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
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
// the encapsulation source (for the IPIP/GRE tunnel); its mask may be any
// (possibly non-contiguous) bitmask, and its address family must match Dst.
type RealConfig struct {
	Dst         netip.Addr
	Src         xnetip.NetWithMask
	CounterName string
}

// AllowedSources describes one entry in a virtual service's source allow
// list. A packet is admitted only if its source address matches one of the
// listed networks AND its source port matches one of the listed ranges. An
// empty set of networks disallows all networks; an empty set of ports allows
// all ports.
type AllowedSources struct {
	Net4s       filter.IPNets
	Net6s       filter.IPNets
	PortRanges  filter.PortRanges
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

// ParseCommonCounter decodes a raw counter row into a CommonCounter. Returns
// nil if the row's length does not match the dataplane counter layout.
func ParseCommonCounter(counter []uint64) *CommonCounter {
	if len(counter) != 8 {
		return nil
	}
	return &CommonCounter{
		IncomingPackets:          counter[0],
		IncomingBytes:            counter[1],
		UnexpectedNetworkProto:   counter[2],
		UnexpectedTransportProto: counter[3],
		DecapSuccessful:          counter[4],
		DecapFailed:              counter[5],
		OutgoingPackets:          counter[6],
		OutgoingBytes:            counter[7],
	}
}

// ParseL4Counter decodes a raw counter row into an L4Counter. Returns nil if
// the row's length does not match the dataplane counter layout.
func ParseL4Counter(counter []uint64) *L4Counter {
	if len(counter) != 5 {
		return nil
	}
	return &L4Counter{
		IncomingPackets:  counter[0],
		SelectVsFailed:   counter[1],
		TunnelFailed:     counter[2],
		SelectRealFailed: counter[3],
		OutgoingPackets:  counter[4],
	}
}

// ParseVsCounter decodes a raw counter row into a VsCounter. Returns nil if
// the row's length does not match the dataplane counter layout.
func ParseVsCounter(counter []uint64) *VsCounter {
	if len(counter) != 16 {
		return nil
	}
	return &VsCounter{
		IncomingPackets:        counter[0],
		IncomingBytes:          counter[1],
		PacketSrcNotAllowed:    counter[2],
		NoReals:                counter[3],
		SessionTableOverflow:   counter[4],
		EchoIcmpPackets:        counter[5],
		ErrorIcmpPackets:       counter[6],
		RealIsDisabled:         counter[7],
		RealIsRemoved:          counter[8],
		NotRescheduledPackets:  counter[9],
		BroadcastedIcmpPackets: counter[10],
		CreatedSessions:        counter[11],
		OutgoingPackets:        counter[12],
		MssMalformedPacket:     counter[13],
		MssNoHeadroom:          counter[14],
		OutgoingBytes:          counter[15],
	}
}

// ParseRealCounter decodes a raw counter row into a RealCounter. Returns nil
// if the row's length does not match the dataplane counter layout.
func ParseRealCounter(counter []uint64) *RealCounter {
	if len(counter) != 5 {
		return nil
	}
	return &RealCounter{
		PacketsRealDisabled: counter[0],
		ErrorIcmpPackets:    counter[1],
		CreatedSessions:     counter[2],
		Packets:             counter[3],
		Bytes:               counter[4],
	}
}
