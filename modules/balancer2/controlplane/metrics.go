package balancer2

import (
	"net"
	"strconv"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

type commonMetricEntry struct {
	name   string
	getter func(common *balancerpb.CommonStats, l4 *balancerpb.L4Stats) uint64
}

type vsMetricEntry struct {
	name   string
	getter func(stats *balancerpb.VsStats) uint64
}

type realMetricEntry struct {
	name   string
	getter func(stats *balancerpb.RealStats) uint64
}

type realGaugeEntry struct {
	name   string
	getter func(real *balancerpb.RealState) float64
}

func baseLabelsFromRef(
	ref *balancerpb.PacketHandlerRef,
	configName string,
) []*commonpb.Label {
	return []*commonpb.Label{
		{Name: "device", Value: ref.GetDevice()},
		{Name: "pipeline", Value: ref.GetPipeline()},
		{Name: "function", Value: ref.GetFunction()},
		{Name: "chain", Value: ref.GetChain()},
		{Name: "config", Value: configName},
	}
}

func vsLabelsFromID(id *balancerpb.VsIdentifier) []*commonpb.Label {
	return []*commonpb.Label{
		{Name: "vip", Value: net.IP(id.GetAddr()).String()},
		{Name: "vs_port", Value: strconv.Itoa(int(id.GetPort()))},
		{Name: "proto", Value: id.GetProto().String()},
	}
}

func realLabelsFromID(id *balancerpb.RelativeRealIdentifier) []*commonpb.Label {
	return []*commonpb.Label{
		{Name: "real_ip", Value: net.IP(id.GetIp()).String()},
	}
}

func commonMetricsTable() []commonMetricEntry {
	return []commonMetricEntry{
		{
			name: "incoming_bits",
			getter: func(c *balancerpb.CommonStats, _ *balancerpb.L4Stats) uint64 {
				return c.GetIncomingBytes() * 8
			},
		},
		{
			name: "incoming_packets",
			getter: func(c *balancerpb.CommonStats, _ *balancerpb.L4Stats) uint64 {
				return c.GetIncomingPackets()
			},
		},
		{
			name: "outgoing_bits",
			getter: func(c *balancerpb.CommonStats, _ *balancerpb.L4Stats) uint64 {
				return c.GetOutgoingBytes() * 8
			},
		},
		{
			name: "outgoing_packets",
			getter: func(c *balancerpb.CommonStats, _ *balancerpb.L4Stats) uint64 {
				return c.GetOutgoingPackets()
			},
		},
		{
			name: "l4_incoming_packets",
			getter: func(_ *balancerpb.CommonStats, l *balancerpb.L4Stats) uint64 {
				return l.GetIncomingPackets()
			},
		},
		{
			name: "l4_outgoing_packets",
			getter: func(_ *balancerpb.CommonStats, l *balancerpb.L4Stats) uint64 {
				return l.GetOutgoingPackets()
			},
		},
		{
			name: "l4_select_vs_failed",
			getter: func(_ *balancerpb.CommonStats, l *balancerpb.L4Stats) uint64 {
				return l.GetSelectVsFailed()
			},
		},
	}
}

func vsMetricsTable() []vsMetricEntry {
	return []vsMetricEntry{
		{
			name:   "vs_incoming_bits",
			getter: func(s *balancerpb.VsStats) uint64 { return s.GetIncomingBytes() * 8 },
		},
		{
			name:   "vs_incoming_packets",
			getter: func(s *balancerpb.VsStats) uint64 { return s.GetIncomingPackets() },
		},
		{
			name:   "vs_outgoing_bits",
			getter: func(s *balancerpb.VsStats) uint64 { return s.GetOutgoingBytes() * 8 },
		},
		{
			name:   "vs_outgoing_packets",
			getter: func(s *balancerpb.VsStats) uint64 { return s.GetOutgoingPackets() },
		},
		{
			name:   "vs_created_sessions",
			getter: func(s *balancerpb.VsStats) uint64 { return s.GetCreatedSessions() },
		},
		{
			name:   "vs_packet_src_not_allowed",
			getter: func(s *balancerpb.VsStats) uint64 { return s.GetPacketSrcNotAllowed() },
		},
		{
			name:   "vs_no_reals",
			getter: func(s *balancerpb.VsStats) uint64 { return s.GetNoReals() },
		},
		{
			name:   "vs_session_table_overflow",
			getter: func(s *balancerpb.VsStats) uint64 { return s.GetSessionTableOverflow() },
		},
		{
			name:   "vs_real_is_disabled",
			getter: func(s *balancerpb.VsStats) uint64 { return s.GetRealIsDisabled() },
		},
		{
			name:   "vs_not_rescheduled_packets",
			getter: func(s *balancerpb.VsStats) uint64 { return s.GetNotRescheduledPackets() },
		},
	}
}

func realMetricsTable() []realMetricEntry {
	return []realMetricEntry{
		{
			name:   "real_incoming_bits",
			getter: func(s *balancerpb.RealStats) uint64 { return s.GetBytes() * 8 },
		},
		{
			name:   "real_incoming_packets",
			getter: func(s *balancerpb.RealStats) uint64 { return s.GetPackets() },
		},
		{
			name:   "real_created_sessions",
			getter: func(s *balancerpb.RealStats) uint64 { return s.GetCreatedSessions() },
		},
		{
			name:   "packets_real_disabled",
			getter: func(s *balancerpb.RealStats) uint64 { return s.GetPacketsRealDisabled() },
		},
	}
}

func realGaugeMetricsTable() []realGaugeEntry {
	return []realGaugeEntry{
		{
			name: "real_active_sessions",
			getter: func(r *balancerpb.RealState) float64 {
				return float64(r.GetActiveSessions())
			},
		},
	}
}

func collectStateMetrics(state *balancerpb.BalancerState) []*commonpb.Metric {
	if state == nil {
		return nil
	}

	base := baseLabelsFromRef(state.GetRef(), state.GetConfigName())

	var result []*commonpb.Metric
	result = appendCommonMetrics(result, state, base)
	result = appendVsAndRealMetrics(result, state, base)
	return result
}

func appendCommonMetrics(
	out []*commonpb.Metric,
	state *balancerpb.BalancerState,
	base []*commonpb.Label,
) []*commonpb.Metric {
	common := state.GetCommonStats()
	l4 := state.GetL4Stats()

	table := commonMetricsTable()
	for idx := range table {
		entry := table[idx]
		out = append(out, &commonpb.Metric{
			Name:   entry.name,
			Labels: base,
			Value: &commonpb.Metric_Counter{
				Counter: entry.getter(common, l4),
			},
		})
	}
	return out
}

func appendVsAndRealMetrics(
	out []*commonpb.Metric,
	state *balancerpb.BalancerState,
	base []*commonpb.Label,
) []*commonpb.Metric {
	vsTable := vsMetricsTable()
	realTable := realMetricsTable()
	gaugeTable := realGaugeMetricsTable()

	for _, vs := range state.GetVs() {
		vsID := vs.GetConfig().GetId()
		if vsID == nil {
			continue
		}
		vsLabels := vsLabelsFromID(vsID)

		out = appendVsCounters(out, vs, vsTable, vsLabels, base)
		out = appendACLCounters(out, vs, vsLabels, base)
		out = appendRealMetrics(out, vs, realTable, gaugeTable, vsLabels, base)
	}
	return out
}

func appendVsCounters(
	out []*commonpb.Metric,
	vs *balancerpb.VsState,
	table []vsMetricEntry,
	vsLabels []*commonpb.Label,
	base []*commonpb.Label,
) []*commonpb.Metric {
	labels := joinLabels(vsLabels, base)
	stats := vs.GetStats()
	for idx := range table {
		entry := table[idx]
		out = append(out, &commonpb.Metric{
			Name:   entry.name,
			Labels: labels,
			Value:  &commonpb.Metric_Counter{Counter: entry.getter(stats)},
		})
	}
	return out
}

func appendACLCounters(
	out []*commonpb.Metric,
	vs *balancerpb.VsState,
	vsLabels []*commonpb.Label,
	base []*commonpb.Label,
) []*commonpb.Metric {
	for _, acl := range vs.GetAllowedSourcesStats() {
		labels := joinLabels(vsLabels, base)
		labels = append(labels, &commonpb.Label{
			Name:  "acl_tag",
			Value: acl.GetTag(),
		})
		out = append(out, &commonpb.Metric{
			Name:   "acl_passes",
			Labels: labels,
			Value:  &commonpb.Metric_Counter{Counter: acl.GetPasses()},
		})
	}
	return out
}

func appendRealMetrics(
	out []*commonpb.Metric,
	vs *balancerpb.VsState,
	counters []realMetricEntry,
	gauges []realGaugeEntry,
	vsLabels []*commonpb.Label,
	base []*commonpb.Label,
) []*commonpb.Metric {
	for _, real := range vs.GetReals() {
		realID := real.GetConfig().GetId()
		if realID == nil {
			continue
		}
		realLabels := realLabelsFromID(realID)

		counterLabels := joinLabels(realLabels, base, vsLabels)
		stats := real.GetStats()
		for idx := range counters {
			entry := counters[idx]
			out = append(out, &commonpb.Metric{
				Name:   entry.name,
				Labels: counterLabels,
				Value: &commonpb.Metric_Counter{
					Counter: entry.getter(stats),
				},
			})
		}

		gaugeLabels := joinLabels(vsLabels, base, realLabels)
		for idx := range gauges {
			entry := gauges[idx]
			out = append(out, &commonpb.Metric{
				Name:   entry.name,
				Labels: gaugeLabels,
				Value: &commonpb.Metric_Gauge{
					Gauge: entry.getter(real),
				},
			})
		}
	}
	return out
}

// joinLabels returns a fresh label slice that concatenates the given groups
// in order. A fresh backing array is allocated so the caller can append
// additional labels (e.g. an acl_tag) without mutating any of the input
// groups' backing arrays.
func joinLabels(groups ...[]*commonpb.Label) []*commonpb.Label {
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	out := make([]*commonpb.Label, 0, total)
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}
