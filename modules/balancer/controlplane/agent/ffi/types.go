package ffi

import (
	"net/netip"
	"time"
)

// VsProto indicates transport protocol for a virtual service (TCP or UDP).
type VsProto int

const (
	ProtoUdp VsProto = iota
	ProtoTcp
)

func (p VsProto) String() string {
	switch p {
	case ProtoUdp:
		return "UDP"
	case ProtoTcp:
		return "TCP"
	default:
		return "unknown"
	}
}

// VsFlags represents virtual-service flags.
type VsFlags struct {
	// Use GRE for encapsulation
	GRE bool
	// One-Packet Scheduling (disable sessions)
	OPS bool
	// Pure L3 (serve all ports)
	PureL3 bool
	// Fix TCP MSS option
	FixMSS bool
}

// VsScheduler of the virtual service.
type VsScheduler int

const (
	// Select reals based on stable hash of source.
	VsSchedulerSourceHash VsScheduler = iota
	// Rotate reals (round-robin).
	VsSchedulerRoundRobin
)

// VsIdentifier uniquely identifies a virtual service by IP, port and transport.
type VsIdentifier struct {
	Ip    netip.Addr
	Port  uint16
	Proto VsProto
}

// RealIdentifier uniquely identifies a real endpoint within a VS.
type RealIdentifier struct {
	Vs VsIdentifier
	Ip netip.Addr
}

// Real describes a real backend for a virtual service.
// Not all fields are guaranteed to be populated for read-only queries.
type Real struct {
	Identifier RealIdentifier
	Weight     uint16
	SrcAddr    netip.Addr
	SrcMask    netip.Addr
	Enabled    bool
}

// L4Stats mirrors L4 module counters.
type L4Stats struct {
	IncomingPackets  uint64
	SelectVSFailed   uint64
	InvalidPackets   uint64
	SelectRealFailed uint64
	OutgoingPackets  uint64
}

// ICMPStats mirrors ICMP counters for an IP version.
type ICMPStats struct {
	IncomingPackets           uint64
	SrcNotAllowed             uint64
	EchoResponses             uint64
	PayloadTooShortIP         uint64
	UnmatchingSrcFromOriginal uint64
	PayloadTooShortPort       uint64
	UnexpectedTransport       uint64
	UnrecognizedVS            uint64
	ForwardedPackets          uint64
	BroadcastedPackets        uint64
	PacketClonesSent          uint64
	PacketClonesReceived      uint64
	PacketCloneFailures       uint64
}

// CommonStats mirrors aggregate pipeline counters.
type CommonStats struct {
	IncomingPackets        uint64
	IncomingBytes          uint64
	UnexpectedNetworkProto uint64
	DecapSuccessful        uint64
	DecapFailed            uint64
	OutgoingPackets        uint64
	OutgoingBytes          uint64
}

// VsStats mirrors per-virtual-service counters.
type VsStats struct {
	IncomingPackets        uint64
	IncomingBytes          uint64
	PacketSrcNotAllowed    uint64
	NoReals                uint64
	OpsPackets             uint64
	SessionTableOverflow   uint64
	EchoIcmpPackets        uint64
	ErrorIcmpPackets       uint64
	RealIsDisabled         uint64
	RealIsRemoved          uint64
	NotRescheduledPackets  uint64
	BroadcastedIcmpPackets uint64
	CreatedSessions        uint64
	OutgoingPackets        uint64
	OutgoingBytes          uint64
}

// RealStats mirrors per-real counters.
type RealStats struct {
	PacketsRealDisabled   uint64
	PacketsRealNotPresent uint64
	OpsPackets            uint64
	ErrorIcmpPackets      uint64
	CreatedSessions       uint64
	Packets               uint64
	Bytes                 uint64
}

// ModuleStats aggregates module-level stats.
type ModuleStats struct {
	L4     L4Stats
	ICMPv4 ICMPStats
	ICMPv6 ICMPStats
	Common CommonStats
}

// AsyncInfo represents asynchronously updated numeric value.
type AsyncInfo struct {
	Value     uint
	UpdatedAt time.Time
}

// VsInfo represents runtime info for a virtual service (no registry indices).
type VsInfo struct {
	VsIdentifier        VsIdentifier
	ActiveSessions      AsyncInfo
	LastPacketTimestamp time.Time
	Stats               VsStats
}

// RealInfo represents runtime info for a real (no registry indices).
type RealInfo struct {
	RealIdentifier      RealIdentifier
	ActiveSessions      AsyncInfo
	LastPacketTimestamp time.Time
	Stats               RealStats
	Enabled             bool
}

// SessionInfo represents a single tracked session.
type SessionInfo struct {
	ClientAddr          netip.Addr
	ClientPort          uint16
	Real                RealIdentifier
	CreateTimestamp     time.Time
	LastPacketTimestamp time.Time
	Timeout             time.Duration
}

// SessionsInfo is a container for sessions enumeration results.
type SessionsInfo struct {
	SessionsCount uint
	Sessions      []SessionInfo
}

// SessionsTimeouts configures timeouts per TCP/UDP state.
type SessionsTimeouts struct {
	TcpSynAck uint32
	TcpSyn    uint32
	TcpFin    uint32
	Tcp       uint32
	Udp       uint32
	Default   uint32
}

// BalancerAddresses contains module source and decapsulation endpoints.
type BalancerAddresses struct {
	SourceIpV4     [4]byte
	SourceIpV6     [16]byte
	DecapAddresses []netip.Addr
}

// BalancerInfo is aggregated module info (no registry indices).
type BalancerInfo struct {
	ActiveSessions AsyncInfo
	Module         ModuleStats
	VsInfo         []VsInfo
	RealInfo       []RealInfo
}

// Configuration types for creating and updating balancers

// RealConfig describes configuration for a real backend.
type RealConfig struct {
	Identifier RealIdentifier
	Weight     uint16
	SrcAddr    netip.Addr
	SrcMask    netip.Addr
}

// VsConfig describes configuration for a virtual service.
type VsConfig struct {
	Identifier VsIdentifier
	Flags      VsFlags
	Scheduler  VsScheduler
	Reals      []RealConfig
	// AllowedSrc defines client source networks allowed to use this VS.
	// Each entry is a CIDR prefix (IPv4 or IPv6).
	AllowedSrc []netip.Prefix
	// PeersV4 and PeersV6 are peer balancer addresses for ICMP broadcasts/responses.
	PeersV4 []netip.Addr
	PeersV6 []netip.Addr
}

// PacketHandlerConfig configures packet handling and virtual services.
type PacketHandlerConfig struct {
	SessionsTimeouts SessionsTimeouts
	VirtualServices  []VsConfig
	SourceIPv4       netip.Addr
	SourceIPv6       netip.Addr
	DecapAddresses   []netip.Addr
}

// BalancerConfig is the complete configuration for creating a balancer.
type BalancerConfig struct {
	TableSize uint // Session table size
	Handler   PacketHandlerConfig
}

// RealUpdate describes a selective update to a real's weight and/or enabled state.
type RealUpdate struct {
	Identifier RealIdentifier
	Weight     *uint16 // nil means don't update
	Enabled    *bool   // nil means don't update
}
