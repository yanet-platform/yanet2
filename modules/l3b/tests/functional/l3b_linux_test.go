package l3b_test

import (
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"
	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	l3b "github.com/yanet-platform/yanet2/modules/l3b/controlplane"
	l3bpb "github.com/yanet-platform/yanet2/modules/l3b/controlplane/l3bpb/v1"
	"github.com/yanet-platform/yanet2/modules/l3b/internal/testutils"
	"github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// counterServiceConfig returns two reals and an accepting source filter.
func counterServiceConfig() cl3bobject.VirtualServiceConfig {
	return cl3bobject.VirtualServiceConfig{
		SourceFilterRules: []cl3bobject.SourceFilterRule{{
			Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			PortRanges: filter.PortRanges{{From: 1, To: 65535}},
		}},
		RealServers: []cl3bobject.RealServer{
			{Type: cl3bobject.IPv4, DestinationAddress: netip.MustParseAddr("172.16.0.10"), SourceNet: xnetip.MustParseNetwork("192.0.2.0/24")},
			{Type: cl3bobject.IPv4, DestinationAddress: netip.MustParseAddr("172.16.0.11"), SourceNet: xnetip.MustParseNetwork("192.0.2.0/24")},
		},
		RingCapacity: 200,
	}
}

// Test_L3bVirtualService_RequiredCounterFailureReclaimsCandidate verifies that
// every refusal rejects the real constructor without consuming borrowed state.
func Test_L3bVirtualService_RequiredCounterFailureReclaimsCandidate(t *testing.T) {
	for _, tc := range testutils.RequiredCounterFailures() {
		t.Run(tc.Name, func(t *testing.T) {
			_, agent := setupL3bHarness(t, "port0", "test")
			initialCapacity := agent.BlockAllocatorFreeSize()
			table, err := cl3bobject.CreateSessionTable(agent, "svc", 1, 64)
			require.NoError(t, err)
			t.Cleanup(func() { _ = table.Free() })
			capacity := agent.BlockAllocatorFreeSize()
			for range 3 {
				var candidate *cl3bobject.VirtualServiceObject
				witness, err := testutils.WithCounterFailure("svc", tc, func() error {
					var err error
					candidate, err = cl3bobject.CreateVirtualService(agent, "svc", counterServiceConfig(), table)
					return err
				})
				if candidate != nil {
					t.Cleanup(func() { _ = candidate.Free() })
				}
				require.Error(t, err)
				require.Nil(t, candidate)
				require.Equal(t, testutils.CounterWitness{Matches: 1, Failures: 1, RegisteredBefore: tc.RegisteredBefore}, witness)
				require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
				retry, err := cl3bobject.CreateVirtualService(agent, "svc", counterServiceConfig(), table)
				require.NoError(t, err)
				require.NoError(t, retry.Free())
				require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
			}
			require.NoError(t, table.Free())
			require.Equal(t, initialCapacity, agent.BlockAllocatorFreeSize())
		})
	}
}

// Test_L3bVirtualService_RealServerAllocationFailureReclaimsCandidate verifies that
// an allocation refusal after all registrations preserves borrowed state and retry.
func Test_L3bVirtualService_RealServerAllocationFailureReclaimsCandidate(t *testing.T) {
	_, agent := setupL3bHarness(t, "port0", "test")
	initialCapacity := agent.BlockAllocatorFreeSize()
	table, err := cl3bobject.CreateSessionTable(agent, "svc", 1, 64)
	require.NoError(t, err)
	t.Cleanup(func() { _ = table.Free() })
	capacity := agent.BlockAllocatorFreeSize()
	for range 3 {
		var candidate *cl3bobject.VirtualServiceObject
		witness, err := testutils.WithRealServerAllocationFailure("svc", func() error {
			var err error
			candidate, err = cl3bobject.CreateVirtualService(agent, "svc", counterServiceConfig(), table)
			return err
		})
		if candidate != nil {
			t.Cleanup(func() { _ = candidate.Free() })
		}
		require.EqualValues(t, 1, witness.Matches)
		require.EqualValues(t, 7, witness.RegisteredCounters)
		require.Positive(t, witness.ReservedBlocks)
		require.Positive(t, witness.ReservedBytes)
		require.True(t, witness.Exhausted)
		require.Error(t, err)
		require.Nil(t, candidate)
		require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
		retry, err := cl3bobject.CreateVirtualService(agent, "svc", counterServiceConfig(), table)
		require.NoError(t, err)
		require.NoError(t, retry.Free())
		require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
	}
	require.NoError(t, table.Free())
	require.Equal(t, initialCapacity, agent.BlockAllocatorFreeSize())
}

// Test_L3bBackend_RequiredCounterFailurePreservesActiveState verifies that a
// real rejected update keeps accepted state, accounting and pins through retry.
func Test_L3bBackend_RequiredCounterFailurePreservesActiveState(t *testing.T) {
	for _, tc := range testutils.RequiredCounterFailures() {
		t.Run(tc.Name, func(t *testing.T) {
			harness, agent := setupL3bHarness(t, "port0", "test")
			wirePipeline(t, agent, "port0", "test")
			backend := l3b.NewBackend(agent)
			request := &l3bpb.VirtualService{
				Name:             "svc",
				SessionIndexSize: 64,
				SourceFilterRules: []*l3bpb.SourceFilterRule{{
					Net4S:      []*commonpb.IPv4Network{commonpb.NewIPv4NetworkFrom4(xnetip.MustParseNetwork4("0.0.0.0/0"))},
					PortRanges: []*filterpb.PortRange{{From: 1, To: 65535}},
				}},
				RealServers: []*l3bpb.RealServer{
					{DestinationAddress: []byte{172, 16, 0, 10}, SourceNetwork: commonpb.NewIPNetworkFrom(xnetip.MustParseNetwork("192.0.2.0/24"))},
					{DestinationAddress: []byte{172, 16, 0, 11}, SourceNetwork: commonpb.NewIPNetworkFrom(xnetip.MustParseNetwork("192.0.2.0/24"))},
				},
			}
			require.NoError(t, backend.CreateService(request))
			require.NoError(t, backend.UpdateRealServerWeight("svc", 1, 0))
			module := publishModuleConfig(t, agent, "test", "svc")
			t.Cleanup(func() { _ = module.Free() })
			link := testutils.CounterLink{Device: "port0", Pipeline: "test", Function: "test", Chain: "test_chain", ModuleType: "l3b", Module: "test"}
			readState := func() (uintptr, testutils.PublishedCounterState) {
				executionContext, err := harness.PublishedExecutionContext(0)
				require.NoError(t, err)
				state, err := testutils.ReadCounterState(executionContext, "svc", link)
				require.NoError(t, err)
				return executionContext, state
			}
			packetOf := func(sourcePort uint16) gopacket.Packet {
				ethernet := layers.Ethernet{SrcMAC: net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}, DstMAC: net.HardwareAddr{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}, EthernetType: layers.EthernetTypeIPv4}
				ipv4 := layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolTCP, SrcIP: net.ParseIP("10.0.0.1"), DstIP: net.ParseIP("192.168.1.1")}
				tcp := layers.TCP{SrcPort: layers.TCPPort(sourcePort), DstPort: 80, Seq: 1, Window: 1024}
				require.NoError(t, tcp.SetNetworkLayerForChecksum(&ipv4))
				return xpacket.LayersToPacket(t, &ethernet, &ipv4, &tcp)
			}
			outerDestination := func(packet gopacket.Packet) string {
				result, err := harness.HandlePackets(packet)
				require.NoError(t, err)
				require.Empty(t, result.Drop)
				require.Len(t, result.Output, 1)
				parsed, err := framework.NewPacketParser().ParsePacket(result.Output[0].RawData)
				require.NoError(t, err)
				require.True(t, parsed.IsTunneled)
				return parsed.DstIP.String()
			}
			flow := packetOf(12345)
			require.Equal(t, "172.16.0.10", outerDestination(flow))
			_, initial := readState()
			require.Len(t, initial.Counters, 8)
			require.NotZero(t, initial.Table)
			require.Positive(t, initial.TableReferences)
			for _, required := range testutils.RequiredCounterFailures() {
				counter, ok := initial.Counters[testutils.CounterKey{Name: required.Name, Link: required.Size == 1}]
				require.True(t, ok)
				require.Equal(t, required.Size, counter.Size)
			}
			require.Equal(t, [2]uint64{1, 0}, initial.Counters[testutils.CounterKey{Name: "packets", Link: true}].Values)
			require.Equal(t, [2]uint64{1, uint64(len(flow.Data()))}, initial.Counters[testutils.CounterKey{Name: "incoming"}].Values)
			require.Equal(t, initial.Counters[testutils.CounterKey{Name: "incoming"}].Values, initial.Counters[testutils.CounterKey{Name: "real/0"}].Values)
			replacement := proto.Clone(request).(*l3bpb.VirtualService)
			replacement.IndexMask = 1
			replacement.SchedulerFlags = cl3bobject.SchedulerCounter
			for idx := range 3 {
				accepted, err := backend.GetService("svc")
				require.NoError(t, err)
				sessions, next, _, err := backend.ListSessions("svc", 0, 100)
				require.NoError(t, err)
				require.Zero(t, next)
				generation, before := readState()
				capacity := agent.BlockAllocatorFreeSize()
				witness, err := testutils.WithCounterFailure("svc", tc, func() error {
					return backend.UpdateService(replacement)
				})
				require.Error(t, err)
				require.Equal(t, testutils.CounterWitness{Matches: 1, Failures: 1, RegisteredBefore: tc.RegisteredBefore}, witness)
				require.Equal(t, capacity, agent.BlockAllocatorFreeSize())
				current, after := readState()
				require.Equal(t, generation, current)
				require.Equal(t, before, after)
				view, err := backend.GetService("svc")
				require.NoError(t, err)
				require.True(t, proto.Equal(accepted, view))
				require.Equal(t, []string{"svc"}, backend.ListServices())
				preserved, next, _, err := backend.ListSessions("svc", 0, 100)
				require.NoError(t, err)
				require.Zero(t, next)
				require.Equal(t, sessions, preserved)
				require.Equal(t, "172.16.0.10", outerDestination(flow))
				require.Equal(t, "172.16.0.10", outerDestination(packetOf(uint16(20000+idx))))
			}
			_, beforeRetry := readState()
			require.NoError(t, backend.UpdateService(replacement))
			_, retried := readState()
			require.NotEqual(t, beforeRetry.Service, retried.Service)
			require.Equal(t, beforeRetry.Table, retried.Table)
			require.Positive(t, retried.TableReferences)
			require.Equal(t, beforeRetry.Counters, retried.Counters)
			view, err := backend.GetService("svc")
			require.NoError(t, err)
			require.EqualValues(t, 1, view.GetIndexMask())
			require.Equal(t, "172.16.0.10", outerDestination(flow))
			require.Equal(t, "172.16.0.11", outerDestination(packetOf(54321)))
			sessions, next, _, err := backend.ListSessions("svc", 0, 100)
			require.NoError(t, err)
			require.Zero(t, next)
			pins := map[uint16]string{}
			for _, session := range sessions {
				pins[session.SourcePort] = session.RealAddress.String()
			}
			require.Equal(t, "172.16.0.10", pins[12345])
			require.Equal(t, "172.16.0.11", pins[54321])
		})
	}
}
