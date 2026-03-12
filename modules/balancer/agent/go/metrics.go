package balancer

import (
	"time"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
)

var commonCounters = []struct {
	name   string
	getter func(*ffi.BalancerStats) uint64
}{
	{
		name: "incoming_bits",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.Common.IncomingBytes * 8
		},
	},
	{
		name: "incoming_packets",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.Common.IncomingPackets
		},
	},
	{
		name: "outgoing_bits",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.Common.OutgoingBytes * 8
		},
	},
	{
		name: "outgoing_packets",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.Common.OutgoingPackets
		},
	},
	{
		name: "l4_incoming_packets",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.L4.IncomingPackets
		},
	},
	{
		name: "l4_outgoing_packets",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.L4.OutgoingPackets
		},
	},
	{
		name: "l4_select_vs_failed",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.L4.SelectVsFailed
		},
	},
	{
		name: "icmp_ipv4_incoming_packets",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.IcmpIpv4.IncomingPackets
		},
	},
	{
		name: "icmp_ipv6_incoming_packets",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.IcmpIpv6.IncomingPackets
		},
	},
	{
		name: "icmp_ipv4_forwarded_packets",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.IcmpIpv4.ForwardedPackets
		},
	},
	{
		name: "icmp_ipv4_packet_clones_sent",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.IcmpIpv4.PacketClonesSent
		},
	},
	{
		name: "icmp_ipv4_packet_clones_received",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.IcmpIpv4.PacketClonesReceived
		},
	},
	{
		name: "icmp_ipv4_packet_clone_failures",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.IcmpIpv4.PacketCloneFailures
		},
	},
	{
		name: "icmp_ipv6_forwarded_packets",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.IcmpIpv6.ForwardedPackets
		},
	},
	{
		name: "icmp_ipv6_packet_clones_sent",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.IcmpIpv6.PacketClonesSent
		},
	},
	{
		name: "icmp_ipv6_packet_clones_received",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.IcmpIpv6.PacketClonesReceived
		},
	},
	{
		name: "icmp_ipv6_packet_clone_failures",
		getter: func(s *ffi.BalancerStats) uint64 {
			return s.IcmpIpv6.PacketCloneFailures
		},
	},
}

var vsCounters = []struct {
	name   string
	getter func(*ffi.VsStats) uint64
}{
	{
		name: "vs_incoming_bits",
		getter: func(s *ffi.VsStats) uint64 {
			return s.IncomingBytes * 8
		},
	},
	{
		name: "vs_incoming_packets",
		getter: func(s *ffi.VsStats) uint64 {
			return s.IncomingPackets
		},
	},
	{
		name: "vs_outgoing_bits",
		getter: func(s *ffi.VsStats) uint64 {
			return s.OutgoingBytes * 8
		},
	},
	{
		name: "vs_outgoing_packets",
		getter: func(s *ffi.VsStats) uint64 {
			return s.OutgoingPackets
		},
	},
	{
		name: "vs_created_sessions",
		getter: func(s *ffi.VsStats) uint64 {
			return s.CreatedSessions
		},
	},
	{
		name: "vs_packet_src_not_allowed",
		getter: func(s *ffi.VsStats) uint64 {
			return s.PacketSrcNotAllowed
		},
	},
	{
		name: "vs_no_reals",
		getter: func(s *ffi.VsStats) uint64 {
			return s.NoReals
		},
	},
	{
		name: "vs_ops_packets",
		getter: func(s *ffi.VsStats) uint64 {
			return s.OpsPackets
		},
	},
	{
		name: "vs_session_table_overflow",
		getter: func(s *ffi.VsStats) uint64 {
			return s.SessionTableOverflow
		},
	},
	{
		name: "vs_echo_icmp_packets",
		getter: func(s *ffi.VsStats) uint64 {
			return s.EchoIcmpPackets
		},
	},
	{
		name: "vs_error_icmp_packets",
		getter: func(s *ffi.VsStats) uint64 {
			return s.ErrorIcmpPackets
		},
	},
	{
		name: "vs_real_is_disabled",
		getter: func(s *ffi.VsStats) uint64 {
			return s.RealIsDisabled
		},
	},
	{
		name: "vs_real_is_removed",
		getter: func(s *ffi.VsStats) uint64 {
			return s.RealIsRemoved
		},
	},
	{
		name: "vs_not_rescheduled_packets",
		getter: func(s *ffi.VsStats) uint64 {
			return s.NotRescheduledPackets
		},
	},
	{
		name: "vs_broadcasted_icmp_packets",
		getter: func(s *ffi.VsStats) uint64 {
			return s.BroadcastedIcmpPackets
		},
	},
}

var realCounters = []struct {
	name   string
	getter func(*ffi.RealStats) uint64
}{
	{
		name: "real_incoming_bits",
		getter: func(s *ffi.RealStats) uint64 {
			return s.Bytes * 8
		},
	},
	{
		name: "real_incoming_packets",
		getter: func(s *ffi.RealStats) uint64 {
			return s.Packets
		},
	},
	{
		name: "real_created_sessions",
		getter: func(s *ffi.RealStats) uint64 {
			return s.CreatedSessions
		},
	},
	{
		name: "real_icmp_error_packets",
		getter: func(s *ffi.RealStats) uint64 {
			return s.ErrorIcmpPackets
		},
	},
	{
		name: "real_ops_packets",
		getter: func(s *ffi.RealStats) uint64 {
			return s.OpsPackets
		},
	},
	{
		name: "packets_real_disabled",
		getter: func(s *ffi.RealStats) uint64 {
			return s.PacketsRealDisabled
		},
	},
}

////////////////////////////////////////////////////////////////////////////////

type handlersMetrics struct {
	callLatencies *metrics.MetricMap[*metrics.Histogram]
}

func newHandlersMetrics() handlersMetrics {
	return handlersMetrics{
		callLatencies: metrics.NewMetricMap[*metrics.Histogram](),
	}
}

func (m *handlersMetrics) collect() []*commonpb.Metric {
	return commonpb.MetricRefsToProto(m.callLatencies.Metrics())
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

type handlerMetricTracker struct {
	metricID  metrics.MetricID
	startTime time.Time
	metrics   *handlersMetrics
	latencies []float64
}

func newHandlerMetricTracker(
	handlerName string,
	handlerMetrics *handlersMetrics,
	latencies []float64,
	labels metrics.Labels,
) *handlerMetricTracker {
	if handlerMetrics == nil || latencies == nil {
		return nil
	}
	id := metrics.MetricID{
		Name:   handlerName,
		Labels: labels,
	}
	return &handlerMetricTracker{
		metricID:  id,
		startTime: time.Now(),
		metrics:   handlerMetrics,
		latencies: latencies,
	}
}

func (m *handlerMetricTracker) Fix() {
	duration := time.Since(m.startTime)

	// update latencies
	m.metrics.callLatencies.GetOrCreate(m.metricID, func() *metrics.Histogram {
		return metrics.NewHistogram(m.latencies)
	}).Observe(float64(duration.Milliseconds()))
}
