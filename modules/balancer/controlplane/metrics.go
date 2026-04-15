package balancer

import (
	"strings"
	"time"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/common/go/relptr"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
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

func collectCounterMetrics(
	services []VS,
	counters []yanet.CounterInfo,
	refLabels []*commonpb.Label,
) []*commonpb.Metric {
	var result []*commonpb.Metric

	var (
		cmn *CommonStats
		l4s *L4Stats
		iv4 *IcmpStats
		iv6 *IcmpStats
	)

	for _, counter := range counters {
		name := counter.Name
		switch {
		case name == "cmn":
			cmn = commonStats(counter.Values)
		case name == "l4":
			l4s = l4Stats(counter.Values)
		case name == "iv4":
			iv4 = icmpStats(counter.Values)
		case name == "iv6":
			iv6 = icmpStats(counter.Values)
		case strings.HasPrefix(name, "vs_"):
			result = append(result, collectVSMetrics(services, counter, refLabels)...)
		case strings.HasPrefix(name, "rl_"):
			result = append(result, collectRealMetrics(services, counter, refLabels)...)
		case strings.HasPrefix(name, "acl_"):
			result = append(result, collectACLMetrics(services, counter, refLabels)...)
		}
	}

	for _, c := range commonCounters {
		result = append(result, &commonpb.Metric{
			Name:   c.name,
			Labels: refLabels,
			Value:  &commonpb.Metric_Counter{Counter: c.getter(cmn, l4s, iv4, iv6)},
		})
	}

	return result
}

func collectVSMetrics(
	services []VS,
	counter yanet.CounterInfo,
	refLabels []*commonpb.Label,
) []*commonpb.Metric {
	vsStableIndex, ok := vsIndexFromCounterName(counter.Name)
	if !ok {
		return nil
	}
	vs, ok := resolveVS(services, vsStableIndex)
	if !ok {
		return nil
	}
	vsLabels := append(vs.labels(), refLabels...)
	stats := vsStats(counter.Values)
	result := make([]*commonpb.Metric, 0, len(vsCounters))
	for _, c := range vsCounters {
		result = append(result, &commonpb.Metric{
			Name:   c.name,
			Labels: vsLabels,
			Value:  &commonpb.Metric_Counter{Counter: c.getter(stats)},
		})
	}
	return result
}

func collectRealMetrics(
	services []VS,
	counter yanet.CounterInfo,
	refLabels []*commonpb.Label,
) []*commonpb.Metric {
	vsStableIndex, realStableIndex, ok := realIndexFromCounterName(counter.Name)
	if !ok {
		return nil
	}
	vs, ok := resolveVS(services, vsStableIndex)
	if !ok {
		return nil
	}
	r, ok := resolveReal(vs, realStableIndex)
	if !ok {
		return nil
	}
	realLabels := append(r.labels(), refLabels...)
	realLabels = append(realLabels, vs.labels()...)
	stats := realStats(counter.Values)
	result := make([]*commonpb.Metric, 0, len(realCounters))
	for _, c := range realCounters {
		result = append(result, &commonpb.Metric{
			Name:   c.name,
			Labels: realLabels,
			Value:  &commonpb.Metric_Counter{Counter: c.getter(stats)},
		})
	}
	return result
}

func collectACLMetrics(
	services []VS,
	counter yanet.CounterInfo,
	refLabels []*commonpb.Label,
) []*commonpb.Metric {
	vsStableIndex, tag, ok := aclTagFromCounterName(counter.Name)
	if !ok {
		return nil
	}
	vs, ok := resolveVS(services, vsStableIndex)
	if !ok {
		return nil
	}
	aclLabels := append(vs.labels(), refLabels...)
	aclLabels = append(aclLabels, &commonpb.Label{Name: "acl_tag", Value: tag})
	return []*commonpb.Metric{{
		Name:   "acl_passes",
		Labels: aclLabels,
		Value:  &commonpb.Metric_Counter{Counter: aggregateACLPasses(counter.Values)},
	}}
}

func collectSessionMetrics(
	services []VS,
	workers uint32,
	now time.Time,
	refLabels []*commonpb.Label,
) []*commonpb.Metric {
	var result []*commonpb.Metric

	for vsIdx := range services {
		vs := &services[vsIdx]
		if vs.isRemoved() {
			continue
		}
		reals := relptr.Slice(&vs.Reals, vs.Reals_count)
		for realIdx := range reals {
			r := &reals[realIdx]
			if r.isRemoved() {
				continue
			}
			active, _ := r.sessions(workers, now)
			realLabels := append(vs.labels(), refLabels...)
			realLabels = append(realLabels, r.labels()...)
			result = append(result, &commonpb.Metric{
				Name:   "real_active_sessions",
				Labels: realLabels,
				Value:  &commonpb.Metric_Gauge{Gauge: float64(active)},
			})
		}
	}

	return result
}
