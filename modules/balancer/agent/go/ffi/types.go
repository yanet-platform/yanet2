package ffi

// Go type definitions for balancer FFI operations, defining structures for virtual services,
// real servers, sessions, statistics, and configuration with support for IPv4/IPv6,
// TCP/UDP protocols, and various load balancing algorithms.

import (
	"net/netip"
	"time"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
)

// AddrRange represents a range of IP addresses
type AddrRange struct {
	From netip.Addr
	To   netip.Addr
}

// PortRange represents a range of port numbers
type PortRange struct {
	From uint16
	To   uint16
}

// AllowedSources represents an allowed source with network and optional port ranges
type AllowedSources struct {
	Nets       []xnetip.NetWithMask // Network with address and arbitrary mask
	PortRanges []PortRange          // Optional port ranges
	Tag        uint32               // Tag for identification/filtering
}

// VsScheduler represents the scheduling algorithm for a virtual service
type VsScheduler uint32

const (
	VsSchedulerSourceHash VsScheduler = 0 // source_hash
	VsSchedulerRoundRobin VsScheduler = 1 // round_robin
)

type VsTransportProto uint32

const (
	VsTransportProtoTCP VsTransportProto = 0 // IPPROTO_TCP
	VsTransportProtoUDP VsTransportProto = 1 // IPPROTO_UDP
)

// VsFlags represents flags for virtual service configuration
type VsFlags struct {
	PureL3 bool // VS_PURE_L3_FLAG - serve all ports
	FixMSS bool // VS_FIX_MSS_FLAG - fix MSS TCP option
	GRE    bool // VS_GRE_FLAG - use GRE tunneling
	OPS    bool // VS_OPS_FLAG - One Packet Scheduling (disable sessions)
}

// VsIdentifier uniquely identifies a virtual service
type VsIdentifier struct {
	Addr           netip.Addr       // Virtual service address
	Port           uint16           // Destination port (0 if PureL3)
	TransportProto VsTransportProto // TCP or UDP
}

// RelativeRealIdentifier identifies a real server relative to its VS
type RelativeRealIdentifier struct {
	Addr netip.Addr // Real endpoint address
	Port uint16     // Destination port on the real
}

// RealIdentifier uniquely identifies a real server within a virtual service
type RealIdentifier struct {
	VsIdentifier VsIdentifier
	Relative     RelativeRealIdentifier
}

// RealConfig contains static configuration for a real server
type RealConfig struct {
	Identifier RelativeRealIdentifier // Relative identifier (within VS context)
	Src        xnetip.NetWithMask     // Source network/addresses for this real (supports arbitrary masks)
	Weight     uint16                 // Scheduler weight [0..MAX_REAL_WEIGHT]
}

// RealUpdate represents a partial update for a real server
type RealUpdate struct {
	Identifier RealIdentifier
	Weight     uint16 // New weight (DONT_UPDATE_REAL_WEIGHT to skip)
	Enabled    uint8  // 0=disabled, non-zero=enabled (DONT_UPDATE_REAL_ENABLED to skip)
}

// RealStats contains statistics for a real server
type RealStats struct {
	PacketsRealDisabled uint64 // Packets while real was disabled
	OpsPackets          uint64 // One-Packet Scheduling packets
	ErrorIcmpPackets    uint64 // ICMP error packets
	CreatedSessions     uint64 // Sessions created with this real
	Packets             uint64 // Total packets sent to real
	Bytes               uint64 // Total bytes sent to real
}

// RealInfo contains runtime information about a real server
type RealInfo struct {
	Dst                 netip.Addr // Real destination address
	LastPacketTimestamp time.Time  // Last packet time observed
	ActiveSessions      uint64     // Active sessions to this real
}

// VsConfig contains static configuration for a virtual service
type VsConfig struct {
	Identifier     VsIdentifier
	Flags          VsFlags
	Scheduler      VsScheduler
	Reals          []RealConfig
	AllowedSources []AllowedSources // Client source allowlist with networks and optional port ranges
	PeersV4        []netip.Addr     // IPv4 peer balancers for ICMP
	PeersV6        []netip.Addr     // IPv6 peer balancers for ICMP
}

// VsStats contains per-virtual-service runtime counters
type VsStats struct {
	IncomingPackets        uint64 // Packets received for this VS
	IncomingBytes          uint64 // Bytes received for this VS
	PacketSrcNotAllowed    uint64 // Dropped due to disallowed source
	NoReals                uint64 // Failed real selection (all disabled)
	OpsPackets             uint64 // OPS packets sent without session
	SessionTableOverflow   uint64 // Failed to create session
	EchoIcmpPackets        uint64 // ICMP echo packets processed
	ErrorIcmpPackets       uint64 // ICMP error packets forwarded
	RealIsDisabled         uint64 // Session exists but real disabled
	RealIsRemoved          uint64 // Session exists but real removed
	NotRescheduledPackets  uint64 // No session and packet doesn't start one
	BroadcastedIcmpPackets uint64 // ICMP broadcasted to peers
	CreatedSessions        uint64 // Sessions created for this VS
	OutgoingPackets        uint64 // Packets sent to selected real
	OutgoingBytes          uint64 // Bytes sent to selected real
}

// VsInfo contains runtime information about a virtual service
type VsInfo struct {
	Identifier          VsIdentifier
	LastPacketTimestamp time.Time
	ActiveSessions      uint64
	Reals               []RealInfo
}

// NamedVsStats pairs a VS identifier with its statistics
type NamedVsStats struct {
	Identifier VsIdentifier
	Stats      VsStats
	Reals      []struct {
		Dst   netip.Addr
		Stats RealStats
	}
	AllowedSources []struct {
		Tag    uint32
		Passes uint64
	}
}

// SessionsTimeouts contains timeout configuration per transport/state
type SessionsTimeouts struct {
	TCPSynAck uint32 // Timeout for TCP SYN-ACK sessions (seconds)
	TCPSyn    uint32 // Timeout for TCP SYN sessions (seconds)
	TCPFin    uint32 // Timeout for TCP FIN sessions (seconds)
	TCP       uint32 // Default timeout for TCP packets (seconds)
	UDP       uint32 // Default timeout for UDP packets (seconds)
	Default   uint32 // Fallback timeout for other packets (seconds)
}

// SessionIdentifier uniquely identifies a session
type SessionIdentifier struct {
	ClientIP   netip.Addr     // Client source IP
	ClientPort uint16         // Client source port
	Real       RealIdentifier // Selected real endpoint
}

// SessionInfo contains runtime session metadata
type SessionInfo struct {
	CreateTimestamp     time.Time     // Session creation time
	LastPacketTimestamp time.Time     // Last packet time observed
	Timeout             time.Duration // Current timeout applied (seconds)
}

// Sessions contains a list of active sessions
type Sessions struct {
	Sessions []struct {
		Identifier SessionIdentifier
		Info       SessionInfo
	}
}

// PacketHandlerConfig defines runtime parameters for session handling
type PacketHandlerConfig struct {
	SessionsTimeouts SessionsTimeouts
	VirtualServices  []VsConfig
	SourceV4         netip.Addr   // IPv4 source for generated packets
	SourceV6         netip.Addr   // IPv6 source for generated packets
	DecapV4          []netip.Addr // IPv4 addresses to decapsulate
	DecapV6          []netip.Addr // IPv6 addresses to decapsulate
}

// PacketHandlerRef optionally narrows statistics to a specific handler
type PacketHandlerRef struct {
	Device   *string // Optional device name
	Pipeline *string // Optional pipeline name
	Function *string // Optional function name
	Chain    *string // Optional chain name
}

// StateConfig contains session table sizing configuration
type StateConfig struct {
	TableCapacity uint // Number of session table entries
}

// BalancerConfig combines packet handler and state configuration
type BalancerConfig struct {
	Handler PacketHandlerConfig
	State   StateConfig
}

// L4Stats contains module counters for L4 packets
type L4Stats struct {
	IncomingPackets  uint64 // L4 packets received
	SelectVsFailed   uint64 // Failed to select virtual service
	InvalidPackets   uint64 // Invalid or malformed packets
	SelectRealFailed uint64 // Failed to select a real
	OutgoingPackets  uint64 // Packets sent to selected real
}

// IcmpStats contains counters for ICMP packets
type IcmpStats struct {
	IncomingPackets           uint64 // ICMP packets received
	SrcNotAllowed             uint64 // Source not allowed by VS policy
	EchoResponses             uint64 // Echo replies generated
	PayloadTooShortIP         uint64 // Payload too short for IP header
	UnmatchingSrcFromOriginal uint64 // Original src doesn't match dst
	PayloadTooShortPort       uint64 // Payload too short for ports
	UnexpectedTransport       uint64 // Original transport not TCP/UDP
	UnrecognizedVs            uint64 // Destination not recognized as VS
	ForwardedPackets          uint64 // ICMP forwarded to real
	BroadcastedPackets        uint64 // ICMP broadcasts sent to peers
	PacketClonesSent          uint64 // Packet clones created/sent
	PacketClonesReceived      uint64 // Packet clones received
	PacketCloneFailures       uint64 // Failures creating packet clone
}

// CommonStats contains total incoming/outgoing packet counts
type CommonStats struct {
	IncomingPackets        uint64 // Total incoming packets
	IncomingBytes          uint64 // Total incoming bytes
	UnexpectedNetworkProto uint64 // Unsupported network protocol
	DecapSuccessful        uint64 // Packets successfully decapsulated
	DecapFailed            uint64 // Packets that failed decapsulation
	OutgoingPackets        uint64 // Total outgoing packets
	OutgoingBytes          uint64 // Total outgoing bytes
}

// BalancerStats contains aggregated statistics for the balancer
type BalancerStats struct {
	L4       L4Stats
	IcmpIpv4 IcmpStats
	IcmpIpv6 IcmpStats
	Common   CommonStats
	Vs       []NamedVsStats
}

// BalancerInfo contains aggregated information about a balancer instance
type BalancerInfo struct {
	ActiveSessions      uint64
	LastPacketTimestamp time.Time
	Vs                  []VsInfo
}

// GraphReal represents a real server in the graph topology
type GraphReal struct {
	Identifier RelativeRealIdentifier
	Weight     uint16
	Enabled    bool
}

// GraphVs represents a virtual service in the graph topology
type GraphVs struct {
	Identifier VsIdentifier
	Reals      []GraphReal
}

// BalancerGraph represents the topology of VS to Reals relationships
type BalancerGraph struct {
	VirtualServices []GraphVs
}

// BalancerManagerWlcConfig contains WLC algorithm configuration
type BalancerManagerWlcConfig struct {
	Power         uint     // Power factor for weight calculations
	MaxRealWeight uint     // Maximum weight value for any real
	Vs            []uint32 // Array of virtual service IDs
}

// BalancerManagerConfig contains complete manager configuration
type BalancerManagerConfig struct {
	Balancer      BalancerConfig
	Wlc           BalancerManagerWlcConfig
	RefreshPeriod time.Duration // Refresh interval
	MaxLoadFactor float32       // Maximum load factor (0.0 to 1.0)
}

// UpdateInfo contains metadata about what was reused during a balancer update
type UpdateInfo struct {
	VsIpv4MatcherReused bool           // Whether IPv4 VS matcher was reused
	VsIpv6MatcherReused bool           // Whether IPv6 VS matcher was reused
	ACLReusedVs         []VsIdentifier // VS identifiers for which ACL was reused
}

// RealsUsage contains memory usage for real servers within a VS
type RealsUsage struct {
	CountersUsage uint64
	DataUsage     uint64
	TotalUsage    uint64
}

// VsInspect contains memory usage for a single virtual service
type VsInspect struct {
	ACLUsage      uint64
	RingUsage     uint64
	CountersUsage uint64
	RealsUsage    RealsUsage
	OtherUsage    uint64
	TotalUsage    uint64
}

// NamedVsInspect pairs a VS identifier with its memory inspection
type NamedVsInspect struct {
	Identifier VsIdentifier
	Inspect    VsInspect
}

// PacketHandlerVsInspect contains memory usage for IPv4 or IPv6 packet handler VS section
type PacketHandlerVsInspect struct {
	MatcherUsage   uint64
	SummaryVsUsage uint64
	VsInspects     []NamedVsInspect
	AnnounceUsage  uint64
	IndexUsage     uint64
	TotalUsage     uint64
}

// PacketHandlerInspect contains complete packet handler memory usage
type PacketHandlerInspect struct {
	VsIpv4Inspect   PacketHandlerVsInspect
	VsIpv6Inspect   PacketHandlerVsInspect
	SummaryVsUsage  uint64
	VsIndexUsage    uint64
	RealsIndexUsage uint64
	CountersUsage   uint64
	DecapUsage      uint64
	TotalUsage      uint64
}

// StateInspect contains state memory usage
type StateInspect struct {
	VsRegistryUsage    uint64
	RealsRegistryUsage uint64
	SessionTableUsage  uint64
	TotalUsage         uint64
}

// BalancerInspect contains per-balancer memory inspection
type BalancerInspect struct {
	PacketHandler PacketHandlerInspect
	State         StateInspect
	OtherUsage    uint64
	TotalUsage    uint64
}

// NamedBalancerInspect pairs a balancer name with its memory inspection
type NamedBalancerInspect struct {
	Name    string
	Inspect BalancerInspect
}

// AgentInspect contains agent-level memory inspection
type AgentInspect struct {
	MemoryLimit uint64
	MemoryUsage uint64
	Balancers   []NamedBalancerInspect
}
