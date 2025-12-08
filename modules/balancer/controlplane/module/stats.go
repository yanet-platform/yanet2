package module

import (
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

type L4Stats struct {
	IncomingPackets  uint64
	SelectVSFailed   uint64
	InvalidPackets   uint64
	SelectRealFailed uint64
	OutgoingPackets  uint64
}

// IntoProto converts L4Stats to protobuf message.
func (s L4Stats) IntoProto() *balancerpb.L4Stats {
	return &balancerpb.L4Stats{
		IncomingPackets:  s.IncomingPackets,
		SelectVsFailed:   s.SelectVSFailed,
		InvalidPackets:   s.InvalidPackets,
		SelectRealFailed: s.SelectRealFailed,
		OutgoingPackets:  s.OutgoingPackets,
	}
}

type ICMPStats struct {
	IncomingPackets           uint64
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

// IntoProto converts ICMPStats to protobuf message.
func (s ICMPStats) IntoProto() *balancerpb.IcmpStats {
	return &balancerpb.IcmpStats{
		IncomingPackets:           s.IncomingPackets,
		EchoResponses:             s.EchoResponses,
		PayloadTooShortIp:         s.PayloadTooShortIP,
		UnmatchingSrcFromOriginal: s.UnmatchingSrcFromOriginal,
		PayloadTooShortPort:       s.PayloadTooShortPort,
		UnexpectedTransport:       s.UnexpectedTransport,
		UnrecognizedVs:            s.UnrecognizedVS,
		ForwardedPackets:          s.ForwardedPackets,
		BroadcastedPackets:        s.BroadcastedPackets,
		PacketClonesSent:          s.PacketClonesSent,
		PacketClonesReceived:      s.PacketClonesReceived,
		PacketCloneFailures:       s.PacketCloneFailures,
	}
}

type CommonStats struct {
	IncomingPackets        uint64
	IncomingBytes          uint64
	UnexpectedNetworkProto uint64
	DecapSuccessful        uint64
	DecapFailed            uint64
	OutgoingPackets        uint64
	OutgoingBytes          uint64
}

// IntoProto converts CommonStats to protobuf message.
func (s CommonStats) IntoProto() *balancerpb.CommonStats {
	return &balancerpb.CommonStats{
		IncomingPackets:        s.IncomingPackets,
		IncomingBytes:          s.IncomingBytes,
		UnexpectedNetworkProto: s.UnexpectedNetworkProto,
		DecapSuccessful:        s.DecapSuccessful,
		DecapFailed:            s.DecapFailed,
		OutgoingPackets:        s.OutgoingPackets,
		OutgoingBytes:          s.OutgoingBytes,
	}
}

// VsStats mirrors struct balancer_vs_stats (plus registry index and identifier)
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

// IntoProto converts VsStats to protobuf message.
func (s VsStats) IntoProto() *balancerpb.VsStats {
	return &balancerpb.VsStats{
		IncomingPackets:        s.IncomingPackets,
		IncomingBytes:          s.IncomingBytes,
		PacketSrcNotAllowed:    s.PacketSrcNotAllowed,
		NoReals:                s.NoReals,
		OpsPackets:             s.OpsPackets,
		SessionTableOverflow:   s.SessionTableOverflow,
		EchoIcmpPackets:        s.EchoIcmpPackets,
		ErrorIcmpPackets:       s.ErrorIcmpPackets,
		RealIsDisabled:         s.RealIsDisabled,
		RealIsRemoved:          s.RealIsRemoved,
		NotRescheduledPackets:  s.NotRescheduledPackets,
		BroadcastedIcmpPackets: s.BroadcastedIcmpPackets,
		CreatedSessions:        s.CreatedSessions,
		OutgoingPackets:        s.OutgoingPackets,
		OutgoingBytes:          s.OutgoingBytes,
	}
}

type VsStatsInfo struct {
	// VsRegistryIdx is the index of the virtual service in the registry.
	VsRegistryIdx uint

	// Identifier of the virtual service.
	VsIdentifier VsIdentifier

	// Statistics of the virtual service.
	Stats VsStats
}

// IntoProto converts VsStatsInfo to protobuf message.
func (s VsStatsInfo) IntoProto() *balancerpb.VsStatsInfo {
	return &balancerpb.VsStatsInfo{
		VsRegistryIdx: uint32(s.VsRegistryIdx),
		Ip:            s.VsIdentifier.Ip.AsSlice(),
		Port:          uint32(s.VsIdentifier.Port),
		Proto:         s.VsIdentifier.Proto.IntoProto(),
		Stats:         s.Stats.IntoProto(),
	}
}

// RealStats mirrors struct balancer_real_stats (plus registry index and identifier)
type RealStats struct {
	PacketsRealDisabled   uint64
	PacketsRealNotPresent uint64
	OpsPackets            uint64
	ErrorIcmpPackets      uint64
	CreatedSessions       uint64
	Packets               uint64
	Bytes                 uint64
}

// IntoProto converts RealStats to protobuf message.
func (s RealStats) IntoProto() *balancerpb.RealStats {
	return &balancerpb.RealStats{
		PacketsRealDisabled:   s.PacketsRealDisabled,
		PacketsRealNotPresent: s.PacketsRealNotPresent,
		OpsPackets:            s.OpsPackets,
		ErrorIcmpPackets:      s.ErrorIcmpPackets,
		CreatedSessions:       s.CreatedSessions,
		Packets:               s.Packets,
		Bytes:                 s.Bytes,
	}
}

type RealStatsInfo struct {
	// RealRegistryIdx is the index of the real in the registry.
	RealRegistryIdx uint

	// Identifier of the real.
	RealIdentifier RealIdentifier

	// Statistics of the real.
	Stats RealStats
}

// IntoProto converts RealStatsInfo to protobuf message.
func (s RealStatsInfo) IntoProto() *balancerpb.RealStatsInfo {
	return &balancerpb.RealStatsInfo{
		RealRegistryIdx: uint32(s.RealRegistryIdx),
		VsIp:            s.RealIdentifier.Vs.Ip.AsSlice(),
		Port:            uint32(s.RealIdentifier.Vs.Port),
		Proto:           s.RealIdentifier.Vs.Proto.IntoProto(),
		RealIp:          s.RealIdentifier.Ip.AsSlice(),
		Stats:           s.Stats.IntoProto(),
	}
}

// Stats of the balancer module
type ModuleStats struct {
	L4     L4Stats
	ICMPv4 ICMPStats
	ICMPv6 ICMPStats
	Common CommonStats
}

// IntoProto converts ModuleStats to protobuf message.
func (s ModuleStats) IntoProto() *balancerpb.ModuleStats {
	return &balancerpb.ModuleStats{
		L4:     s.L4.IntoProto(),
		Icmpv4: s.ICMPv4.IntoProto(),
		Icmpv6: s.ICMPv6.IntoProto(),
		Common: s.Common.IntoProto(),
	}
}

// BalancerStats is the stats of the balancer,
// which includes module stats and stats about virtual services
// and reals.
type BalancerStats struct {
	Module ModuleStats
	Vs     []VsStatsInfo
	Reals  []RealStatsInfo
}

// IntoProto converts BalancerStats to protobuf message.
func (s BalancerStats) IntoProto() *balancerpb.BalancerStats {
	vsStats := make([]*balancerpb.VsStatsInfo, 0, len(s.Vs))
	for i := range s.Vs {
		vsStats = append(vsStats, s.Vs[i].IntoProto())
	}

	realStats := make([]*balancerpb.RealStatsInfo, 0, len(s.Reals))
	for i := range s.Reals {
		realStats = append(realStats, s.Reals[i].IntoProto())
	}

	return &balancerpb.BalancerStats{
		Module: s.Module.IntoProto(),
		Vs:     vsStats,
		Reals:  realStats,
	}
}
