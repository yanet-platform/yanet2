package balancer

import (
	"time"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/metrics"
)

// commonCounters maps per-position metric names to getters over the four
// "global" counter groups (cmn, l4, iv4, iv6).
var commonCounters = []struct {
	name   string
	getter func(*CommonStats, *L4Stats, *IcmpStats, *IcmpStats) uint64
}{
	{
		name: "incoming_bits",
		getter: func(c *CommonStats, _ *L4Stats, _ *IcmpStats, _ *IcmpStats) uint64 {
			return c.Incoming_bytes * 8
		},
	},
	{
		name: "incoming_packets",
		getter: func(c *CommonStats, _ *L4Stats, _ *IcmpStats, _ *IcmpStats) uint64 {
			return c.Incoming_packets
		},
	},
	{
		name: "outgoing_bits",
		getter: func(c *CommonStats, _ *L4Stats, _ *IcmpStats, _ *IcmpStats) uint64 {
			return c.Outgoing_bytes * 8
		},
	},
	{
		name: "outgoing_packets",
		getter: func(c *CommonStats, _ *L4Stats, _ *IcmpStats, _ *IcmpStats) uint64 {
			return c.Outgoing_packets
		},
	},
	{
		name: "l4_incoming_packets",
		getter: func(_ *CommonStats, l *L4Stats, _ *IcmpStats, _ *IcmpStats) uint64 {
			return l.Incoming_packets
		},
	},
	{
		name: "l4_outgoing_packets",
		getter: func(_ *CommonStats, l *L4Stats, _ *IcmpStats, _ *IcmpStats) uint64 {
			return l.Outgoing_packets
		},
	},
	{
		name: "l4_select_vs_failed",
		getter: func(_ *CommonStats, l *L4Stats, _ *IcmpStats, _ *IcmpStats) uint64 {
			return l.Select_vs_failed
		},
	},
	{
		name: "icmp_ipv4_incoming_packets",
		getter: func(_ *CommonStats, _ *L4Stats, i4 *IcmpStats, _ *IcmpStats) uint64 {
			return i4.Incoming_packets
		},
	},
	{
		name: "icmp_ipv4_forwarded_packets",
		getter: func(_ *CommonStats, _ *L4Stats, i4 *IcmpStats, _ *IcmpStats) uint64 {
			return i4.Forwarded_packets
		},
	},
	{
		name: "icmp_ipv4_packet_clones_sent",
		getter: func(_ *CommonStats, _ *L4Stats, i4 *IcmpStats, _ *IcmpStats) uint64 {
			return i4.Packet_clones_sent
		},
	},
	{
		name: "icmp_ipv4_packet_clones_received",
		getter: func(_ *CommonStats, _ *L4Stats, i4 *IcmpStats, _ *IcmpStats) uint64 {
			return i4.Packet_clones_received
		},
	},
	{
		name: "icmp_ipv4_packet_clone_failures",
		getter: func(_ *CommonStats, _ *L4Stats, i4 *IcmpStats, _ *IcmpStats) uint64 {
			return i4.Packet_clone_failures
		},
	},
	{
		name: "icmp_ipv6_incoming_packets",
		getter: func(_ *CommonStats, _ *L4Stats, _ *IcmpStats, i6 *IcmpStats) uint64 {
			return i6.Incoming_packets
		},
	},
	{
		name: "icmp_ipv6_forwarded_packets",
		getter: func(_ *CommonStats, _ *L4Stats, _ *IcmpStats, i6 *IcmpStats) uint64 {
			return i6.Forwarded_packets
		},
	},
	{
		name: "icmp_ipv6_packet_clones_sent",
		getter: func(_ *CommonStats, _ *L4Stats, _ *IcmpStats, i6 *IcmpStats) uint64 {
			return i6.Packet_clones_sent
		},
	},
	{
		name: "icmp_ipv6_packet_clones_received",
		getter: func(_ *CommonStats, _ *L4Stats, _ *IcmpStats, i6 *IcmpStats) uint64 {
			return i6.Packet_clones_received
		},
	},
	{
		name: "icmp_ipv6_packet_clone_failures",
		getter: func(_ *CommonStats, _ *L4Stats, _ *IcmpStats, i6 *IcmpStats) uint64 {
			return i6.Packet_clone_failures
		},
	},
}

// vsCounters maps per-VS metric names to getters over VsStats.
var vsCounters = []struct {
	name   string
	getter func(*VsStats) uint64
}{
	{"vs_incoming_bits", func(s *VsStats) uint64 { return s.Incoming_bytes * 8 }},
	{"vs_incoming_packets", func(s *VsStats) uint64 { return s.Incoming_packets }},
	{"vs_outgoing_bits", func(s *VsStats) uint64 { return s.Outgoing_bytes * 8 }},
	{"vs_outgoing_packets", func(s *VsStats) uint64 { return s.Outgoing_packets }},
	{"vs_created_sessions", func(s *VsStats) uint64 { return s.Created_sessions }},
	{"vs_packet_src_not_allowed", func(s *VsStats) uint64 { return s.Packet_src_not_allowed }},
	{"vs_no_reals", func(s *VsStats) uint64 { return s.No_reals }},
	{"vs_session_table_overflow", func(s *VsStats) uint64 { return s.Session_table_overflow }},
	{"vs_echo_icmp_packets", func(s *VsStats) uint64 { return s.Echo_icmp_packets }},
	{"vs_error_icmp_packets", func(s *VsStats) uint64 { return s.Error_icmp_packets }},
	{"vs_real_is_disabled", func(s *VsStats) uint64 { return s.Real_is_disabled }},
	{"vs_real_is_removed", func(s *VsStats) uint64 { return s.Real_is_removed }},
	{"vs_not_rescheduled_packets", func(s *VsStats) uint64 { return s.Not_rescheduled_packets }},
	{"vs_broadcasted_icmp_packets", func(s *VsStats) uint64 { return s.Broadcasted_icmp_packets }},
}

// realCounters maps per-real metric names to getters over RealStats.
var realCounters = []struct {
	name   string
	getter func(*RealStats) uint64
}{
	{"real_incoming_bits", func(s *RealStats) uint64 { return s.Bytes * 8 }},
	{"real_incoming_packets", func(s *RealStats) uint64 { return s.Packets }},
	{"real_created_sessions", func(s *RealStats) uint64 { return s.Created_sessions }},
	{"real_icmp_error_packets", func(s *RealStats) uint64 { return s.Error_icmp_packets }},
	{"packets_real_disabled", func(s *RealStats) uint64 { return s.Packets_real_disabled }},
}

////////////////////////////////////////////////////////////////////////////////

type methodMetrics struct {
	latencies *metrics.MetricMap[*metrics.Histogram]
}

func newMethodMetrics() methodMetrics {
	return methodMetrics{
		latencies: metrics.NewMetricMap[*metrics.Histogram](),
	}
}

func (m *methodMetrics) collect() []*commonpb.Metric {
	return commonpb.MetricRefsToProto(m.latencies.Metrics())
}

var defaultLatencyBoundsMS = []float64{
	1,
	2,
	5,
	10,
	25,
	50,
	75,
	100,
	150,
	200,
	300,
	400,
	500,
	600,
	700,
	800,
	900,
	1000,
	1500,
	2000,
	3000,
	4000,
	5000,
}

type methodMetricsTracker struct {
	metricID  metrics.MetricID
	startTime time.Time
	metrics   methodMetrics
	latencies []float64
}

func newMetricsTracker(
	handlerName string,
	methodMetrics methodMetrics,
	latencies []float64,
	labels metrics.Labels,
) *methodMetricsTracker {
	id := metrics.MetricID{
		Name:   handlerName,
		Labels: labels,
	}
	return &methodMetricsTracker{
		metricID:  id,
		startTime: time.Now(),
		metrics:   methodMetrics,
		latencies: latencies,
	}
}

func (m *methodMetricsTracker) Fix() {
	duration := time.Since(m.startTime)
	m.metrics.latencies.GetOrCreate(m.metricID, func() *metrics.Histogram {
		return metrics.NewHistogram(m.latencies)
	}).Observe(float64(duration.Milliseconds()))
}
