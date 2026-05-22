package cbalancer2

// Counter structs mirror the dataplane layouts defined in
// modules/balancer2/dataplane/types/stats.h. Field order and count must stay
// in sync with that header; the counter parsers rely on it.

// CommonCounter mirrors struct balancer_common_stats.
type CommonCounter struct {
	IncomingPackets          uint64
	IncomingBytes            uint64
	UnexpectedNetworkProto   uint64
	UnexpectedTransportProto uint64
	DecapSuccessful          uint64
	DecapFailed              uint64
	OutgoingPackets          uint64
	OutgoingBytes            uint64
}

// L4Counter mirrors struct balancer_l4_stats.
type L4Counter struct {
	IncomingPackets  uint64
	SelectVsFailed   uint64
	TunnelFailed     uint64
	SelectRealFailed uint64
	OutgoingPackets  uint64
}

// VsCounter mirrors struct balancer_vs_stats.
type VsCounter struct {
	IncomingPackets        uint64
	IncomingBytes          uint64
	PacketSrcNotAllowed    uint64
	NoReals                uint64
	SessionTableOverflow   uint64
	EchoIcmpPackets        uint64
	ErrorIcmpPackets       uint64
	RealIsDisabled         uint64
	RealIsRemoved          uint64
	NotRescheduledPackets  uint64
	BroadcastedIcmpPackets uint64
	CreatedSessions        uint64
	OutgoingPackets        uint64
	MssMalformedPacket     uint64
	MssNoHeadroom          uint64
	OutgoingBytes          uint64
}

// RealCounter mirrors struct balancer_real_stats.
type RealCounter struct {
	PacketsRealDisabled uint64
	ErrorIcmpPackets    uint64
	CreatedSessions     uint64
	Packets             uint64
	Bytes               uint64
}
