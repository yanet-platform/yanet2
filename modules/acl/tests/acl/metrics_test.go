package test

import (
	"context"
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
)

// helpers
func sumCounter(counters []ffi.CounterInfo, name string) (pkts, bytes uint64) {
	for _, c := range counters {
		if c.Name != name {
			continue
		}
		for _, w := range c.Values {
			if len(w) > 0 {
				pkts += w[0]
			}
			if len(w) > 1 {
				bytes += w[1]
			}
		}
	}
	return
}

func dpCounters(t *testing.T, agent *ffi.Agent) []ffi.CounterInfo {
	t.Helper()
	return agent.DPConfig().ModuleCounters(
		defaultDeviceName, defaultPipelineName, defaultFunctionName,
		defaultChainName, "acl", defaultConfigName, nil,
	)
}

func findMetric(ms []*commonpb.Metric, name string, labels map[string]string) *commonpb.Metric {
	for _, m := range ms {
		if m.Name != name {
			continue
		}
		match := true
		for k, v := range labels {
			found := false
			for _, l := range m.Labels {
				if l.Name == k && l.Value == v {
					found = true
					break
				}
			}
			if !found {
				match = false
				break
			}
		}
		if match {
			return m
		}
	}
	return nil
}

func counterVal(m *commonpb.Metric) uint64 {
	if c, ok := m.Value.(*commonpb.Metric_Counter); ok {
		return c.Counter
	}
	return 0
}

func gaugeVal(m *commonpb.Metric) float64 {
	if g, ok := m.Value.(*commonpb.Metric_Gauge); ok {
		return g.Gauge
	}
	return 0
}

// passRule builds a simple IPv4 UDP PASS rule for the default device
func passRule(src, dst, counter string) acl.AclRule {
	return acl.AclRule{
		Action:        0,
		Counter:       counter,
		Devices:       []filter.Device{{Name: defaultDeviceName}},
		Src4s:         []filter.IPNet{{Addr: netip.MustParseAddr(src), Mask: netip.MustParseAddr("255.255.255.255")}},
		Dst4s:         []filter.IPNet{{Addr: netip.MustParseAddr(dst), Mask: netip.MustParseAddr("255.255.255.255")}},
		Src6s:         []filter.IPNet{},
		Dst6s:         []filter.IPNet{},
		SrcPortRanges: []filter.PortRange{{From: 0, To: 65535}},
		DstPortRanges: []filter.PortRange{{From: 0, To: 65535}},
		ProtoRanges:   []filter.ProtoRange{{From: 4352, To: 4607}},
	}
}

func denyRule(src, dst string) acl.AclRule {
	r := passRule(src, dst, "")
	r.Action = 1
	return r
}

func udpPacket(t *testing.T, src, dst string) gopacket.Packet {
	t.Helper()
	return xpacket.LayersToPacket(t, NewPacketGenerator().MakeUDPPacket(src, dst, 12345, 80, nil)...)
}

func newService(agent *ffi.Agent) *acl.ACLService {
	return acl.NewACLService(agent, 64*1024*1024, zap.NewNop().Sugar())
}

// 1. Compilation info (ffi.GetInfo)
func TestMetrics_CompilationInfo(t *testing.T) {
	setup, err := SetupTest(&TestConfig{rules: createBasicRules()})
	require.NoError(t, err)
	defer setup.Free()

	info := setup.module.GetInfo()
	// createBasicRules has port ranges → rules go into ip4_port filter, not pure ip4
	assert.Greater(t, info.CompilationTimeNs, uint64(0), "compilation_time_ns must be non-zero")
	assert.Greater(t, info.FilterRuleCountIp4Port, uint64(0), "filter_rule_count_ip4_port must be > 0")
}

func TestMetrics_CompilationInfo_IPv6(t *testing.T) {
	setup, err := SetupTest(&TestConfig{rules: createIPv6Rules()})
	require.NoError(t, err)
	defer setup.Free()

	assert.Greater(t, setup.module.GetInfo().FilterRuleCountIp6, uint64(0))
}

// 2. Action counters
func TestMetrics_ActionCounters(t *testing.T) {
	tests := []struct {
		name        string
		rules       []acl.AclRule
		src         string
		counterName string
	}{
		{"allow", []acl.AclRule{passRule("10.0.0.1", "10.0.0.2", "")}, "10.0.0.1", "acl_action_allow"},
		{"deny", []acl.AclRule{denyRule("10.0.0.1", "10.0.0.2")}, "10.0.0.1", "acl_action_deny"},
		// 172.16.0.1 doesn't match the pass rule -> no_match
		{"no_match", []acl.AclRule{passRule("10.0.0.1", "10.0.0.2", "")}, "172.16.0.1", "acl_no_match"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setup, err := SetupTest(&TestConfig{rules: tc.rules})
			require.NoError(t, err)
			defer setup.Free()

			_, err = setup.mock.HandlePackets(udpPacket(t, tc.src, "10.0.0.2"))
			require.NoError(t, err)

			pkts, bytes := sumCounter(dpCounters(t, setup.agent), tc.counterName)
			assert.Equal(t, uint64(1), pkts)
			assert.Greater(t, bytes, uint64(0))
		})
	}
}

// 3. Per-rule counter
func TestMetrics_PerRuleCounter(t *testing.T) {
	const name = "acl_http"
	setup, err := SetupTest(&TestConfig{rules: []acl.AclRule{passRule("10.0.0.1", "10.0.0.2", name)}})
	require.NoError(t, err)
	defer setup.Free()

	_, err = setup.mock.HandlePackets(udpPacket(t, "10.0.0.1", "10.0.0.2"))
	require.NoError(t, err)

	pkts, bytes := sumCounter(dpCounters(t, setup.agent), name)
	assert.Equal(t, uint64(1), pkts)
	assert.Greater(t, bytes, uint64(0))
}

// 4. ACLService.Metrics()
func TestMetrics_ServiceCounters(t *testing.T) {
	setup, err := SetupTest(&TestConfig{rules: []acl.AclRule{passRule("10.0.0.1", "10.0.0.2", "acl_web")}})
	require.NoError(t, err)
	defer setup.Free()

	_, err = setup.mock.HandlePackets(udpPacket(t, "10.0.0.1", "10.0.0.2"))
	require.NoError(t, err)

	svc := newService(setup.agent)
	ms, err := svc.Metrics()
	require.NoError(t, err)

	// action counter with expected labels
	m := findMetric(ms, "acl_action_allow_packets", map[string]string{
		"config": defaultConfigName, "device": defaultDeviceName,
	})
	require.NotNil(t, m)
	assert.Equal(t, uint64(1), counterVal(m))

	// per-rule counter
	mr := findMetric(ms, "acl_rule_packets", map[string]string{"counter": "acl_web"})
	require.NotNil(t, mr)
	assert.Equal(t, uint64(1), counterVal(mr))

	assert.Nil(t, findMetric(ms, "acl_action_deny_packets", nil))
}

// 5. Gauge metrics
func ip4b(addr string) []byte { return net.ParseIP(addr).To4() }

func makeProtoRule(src, dst string, kind aclpb.ActionKind) *aclpb.Rule {
	mask := ip4b("255.255.255.255")
	return &aclpb.Rule{
		Action:        &aclpb.Action{Kind: kind},
		Devices:       []*filterpb.Device{{Name: defaultDeviceName}},
		Srcs:          []*filterpb.IPNet{{Addr: ip4b(src), Mask: mask}},
		Dsts:          []*filterpb.IPNet{{Addr: ip4b(dst), Mask: mask}},
		SrcPortRanges: []*filterpb.PortRange{{From: 0, To: 65535}},
		DstPortRanges: []*filterpb.PortRange{{From: 0, To: 65535}},
		ProtoRanges:   []*filterpb.ProtoRange{{From: 4352, To: 4607}},
	}
}

func TestMetrics_ServiceGauges(t *testing.T) {
	const memBytes = 64 * 1024 * 1024
	setup, err := SetupTest(&TestConfig{rules: createBasicRules()})
	require.NoError(t, err)
	defer setup.Free()

	svc := acl.NewACLService(setup.agent, memBytes, zap.NewNop().Sugar())
	_, err = svc.UpdateConfig(context.Background(), &aclpb.UpdateConfigRequest{
		Name:  defaultConfigName,
		Rules: []*aclpb.Rule{makeProtoRule("10.0.0.1", "10.0.0.2", aclpb.ActionKind_ACTION_KIND_PASS)},
	})
	require.NoError(t, err)

	ms, err := svc.Metrics()
	require.NoError(t, err)

	cfgLabel := map[string]string{"config": defaultConfigName}

	m := findMetric(ms, "acl_compilation_time_ns", cfgLabel)
	require.NotNil(t, m)
	assert.Greater(t, gaugeVal(m), float64(0))

	ip4 := findMetric(ms, "acl_filter_rule_count_ip4", cfgLabel)
	require.NotNil(t, ip4)
	assert.Greater(t, gaugeVal(ip4), float64(0))

	mem := findMetric(ms, "acl_memory_bytes", cfgLabel)
	require.NotNil(t, mem)
	assert.Equal(t, float64(memBytes), gaugeVal(mem))
}

// 6. Handler latency histograms
func TestMetrics_HandlerLatency(t *testing.T) {
	setup, err := SetupTest(&TestConfig{rules: createBasicRules()})
	require.NoError(t, err)
	defer setup.Free()

	svc := newService(setup.agent)
	updateReq := &aclpb.UpdateConfigRequest{
		Name:  defaultConfigName,
		Rules: []*aclpb.Rule{makeProtoRule("10.0.0.1", "10.0.0.2", aclpb.ActionKind_ACTION_KIND_PASS)},
	}

	_, _ = svc.UpdateConfig(context.Background(), updateReq)
	_, _ = svc.ShowConfig(context.Background(), &aclpb.ShowConfigRequest{Name: defaultConfigName})
	_, _ = svc.ListConfigs(context.Background(), &aclpb.ListConfigsRequest{})
	_, _ = svc.DeleteConfig(context.Background(), &aclpb.DeleteConfigRequest{Name: defaultConfigName})
	_, _ = svc.GetMetrics(context.Background(), &aclpb.GetMetricsRequest{})

	ms, err := svc.Metrics()
	require.NoError(t, err)

	for _, handler := range []string{"UpdateConfig", "ShowConfig", "ListConfigs", "DeleteConfig", "GetMetrics"} {
		m := findMetric(ms, "acl_handler_call_latency_ms", map[string]string{"handler": handler})
		require.NotNil(t, m, "latency histogram missing for handler %q", handler)
		_, ok := m.Value.(*commonpb.Metric_Histogram)
		assert.True(t, ok, "metric for %q must be a histogram", handler)
	}
}
