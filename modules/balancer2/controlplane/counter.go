package balancer2

import (
	"strings"

	"github.com/yanet-platform/yanet2/modules/balancer2/bindings/go/cbalancer2"
	balancerpb "github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb/v1"
)

// aggregateCounterValues sums worker-local counter rows into a single row.
//
// Returns nil if there are no rows or if rows have inconsistent widths.
func aggregateCounterValues(values [][]uint64) []uint64 {
	if len(values) == 0 {
		return nil
	}
	width := len(values[0])
	out := make([]uint64, width)
	for _, row := range values {
		if len(row) != width {
			return nil
		}
		for idx, v := range row {
			out[idx] += v
		}
	}
	return out
}

func splitACLCounterName(name string) (string, string, bool) {
	rest := strings.TrimPrefix(name, aclCounterPrefix+"_")
	vsKey, tag, ok := strings.Cut(rest, "_")
	if !ok || vsKey == "" || tag == "" {
		return "", "", false
	}
	return vsKey, tag, true
}

func splitRealCounterName(name string) (string, string, bool) {
	rest := strings.TrimPrefix(name, realCounterPrefix+"_")
	idx := strings.LastIndex(rest, "_")
	if idx <= 0 || idx == len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

func commonCounterToProto(c *cbalancer2.CommonCounter) *balancerpb.CommonStats {
	return &balancerpb.CommonStats{
		IncomingPackets:          c.IncomingPackets,
		IncomingBytes:            c.IncomingBytes,
		UnexpectedNetworkProto:   c.UnexpectedNetworkProto,
		UnexpectedTransportProto: c.UnexpectedTransportProto,
		DecapSuccessful:          c.DecapSuccessful,
		DecapFailed:              c.DecapFailed,
		OutgoingPackets:          c.OutgoingPackets,
		OutgoingBytes:            c.OutgoingBytes,
	}
}

func l4CounterToProto(c *cbalancer2.L4Counter) *balancerpb.L4Stats {
	return &balancerpb.L4Stats{
		IncomingPackets:  c.IncomingPackets,
		SelectVsFailed:   c.SelectVsFailed,
		InvalidPackets:   c.TunnelFailed,
		SelectRealFailed: c.SelectRealFailed,
		OutgoingPackets:  c.OutgoingPackets,
	}
}

func vsCounterToProto(c *cbalancer2.VsCounter) *balancerpb.VsStats {
	return &balancerpb.VsStats{
		IncomingPackets:        c.IncomingPackets,
		IncomingBytes:          c.IncomingBytes,
		PacketSrcNotAllowed:    c.PacketSrcNotAllowed,
		NoReals:                c.NoReals,
		SessionTableOverflow:   c.SessionTableOverflow,
		EchoIcmpPackets:        c.EchoIcmpPackets,
		ErrorIcmpPackets:       c.ErrorIcmpPackets,
		RealIsDisabled:         c.RealIsDisabled,
		NotRescheduledPackets:  c.NotRescheduledPackets,
		BroadcastedIcmpPackets: c.BroadcastedIcmpPackets,
		CreatedSessions:        c.CreatedSessions,
		OutgoingPackets:        c.OutgoingPackets,
		OutgoingBytes:          c.OutgoingBytes,
		FixMssMalformed:        c.MssMalformedPacket,
	}
}

func realCounterToProto(c *cbalancer2.RealCounter) *balancerpb.RealStats {
	return &balancerpb.RealStats{
		PacketsRealDisabled: c.PacketsRealDisabled,
		ErrorIcmpPackets:    c.ErrorIcmpPackets,
		CreatedSessions:     c.CreatedSessions,
		Packets:             c.Packets,
		Bytes:               c.Bytes,
	}
}
