package mbalancer

import "github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"

type RealStats struct {
	RealDisabledPackets uint64
	OpsPackets          uint64
	CreatedSessions     uint64
	SendPackets         uint64
	SendBytes           uint64
}

func (stats *RealStats) IntoProto() balancerpb.RealStats {
	return balancerpb.RealStats{
		RealDisabledPackets: stats.RealDisabledPackets,
		OpsPackets:          stats.OpsPackets,
		CreatedSessions:     stats.CreatedSessions,
		SendPackets:         stats.SendPackets,
		SendBytes:           stats.SendBytes,
	}
}

type VsStats struct {
	IncomingPackets uint64
	IncomingBytes   uint64

	// packet drop reasons

	PacketSrcNotAllowed uint64
	NoReals             uint64

	// one packet scheduler packets

	OpsPackets uint64

	// packet drop reasons

	SessionTableOverflow uint64
	RealIsDisabled       uint64
	PacketNotRescheduled uint64

	CreatedSessions uint64
	OutgoingPackets uint64
	OutgoingBytes   uint64
}

func (stats *VsStats) IntoProto() balancerpb.VsStats {
	return balancerpb.VsStats{
		IncomingPackets:      stats.IncomingBytes,
		IncomingBytes:        stats.IncomingBytes,
		PacketSrcNotAllowed:  stats.PacketSrcNotAllowed,
		NoReals:              stats.NoReals,
		OpsPackets:           stats.OpsPackets,
		SessionTableOverflow: stats.SessionTableOverflow,
		RealIsDisabled:       stats.RealIsDisabled,
		PacketNotRescheduled: stats.PacketNotRescheduled,
		CreatedSessions:      stats.CreatedSessions,
		OutgoingPackets:      stats.OutgoingPackets,
		OutgoingBytes:        stats.OutgoingBytes,
	}
}
