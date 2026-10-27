package l3b_test

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/l3b/bindings/go/cl3b"
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
			require.NoError(t, backend.UpdateRealServerState("svc", 0, true))
			require.NoError(t, backend.UpdateRealServerState("svc", 1, true))
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
			require.Zero(t, view.RealServers[1].Weight)
			require.Equal(t, "172.16.0.10", outerDestination(flow))
			require.Equal(t, "172.16.0.10", outerDestination(packetOf(54321)))
			sessions, next, _, err := backend.ListSessions("svc", 0, 100)
			require.NoError(t, err)
			require.Zero(t, next)
			pins := map[uint16]string{}
			for _, session := range sessions {
				pins[session.SourcePort] = session.RealAddress.String()
			}
			require.Equal(t, "172.16.0.10", pins[12345])
			require.Equal(t, "172.16.0.10", pins[54321])
		})
	}
}

// Test_L3bModuleConfig_UpdateProtectsLinkedServiceLookup verifies that each rule
// reads its service and counter from the same protected published generation.
func Test_L3bModuleConfig_UpdateProtectsLinkedServiceLookup(t *testing.T) {
	for _, tc := range []struct {
		name     string
		services []string
		counters uint64
	}{
		{name: "present service", services: []string{"svc"}, counters: 1},
		{name: "multiple duplicate and missing services", services: []string{"svc", "other", "svc", "missing"}, counters: 3},
		{name: "missing service", services: []string{"missing"}},
		{name: "empty rules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, agent := setupL3bHarness(t, "port0", "test")
			for _, name := range []string{"svc", "other"} {
				service, _ := newVirtualService(t, agent, name, cl3bobject.VirtualServiceConfig{})
				require.NoError(t, service.Publish(agent))
			}
			module, err := cl3b.NewModuleConfig(agent, "lookup")
			require.NoError(t, err)
			t.Cleanup(func() { _ = module.Free() })
			rules := make([]cl3b.DestinationFilterRule, len(tc.services))
			for idx, name := range tc.services {
				rules[idx].VirtualService = name
			}
			witness, err := testutils.WithLinkedServiceLookup(agent, func() error {
				return module.Update(rules)
			})
			require.NoError(t, err)
			t.Logf("objects=%d counters=%d unprotected=%d/%d provenance=%d",
				len(witness.Lookups), witness.CounterLookups,
				witness.UnprotectedObjects, witness.UnprotectedCounters,
				witness.ProvenanceErrors,
			)
			require.Len(t, witness.Lookups, len(rules))
			require.Equal(t, tc.counters, witness.CounterLookups)
			require.Equal(t, [2]uint64{}, [2]uint64{witness.UnprotectedObjects, witness.UnprotectedCounters})
			require.Zero(t, witness.ProvenanceErrors)
			ids, err := testutils.ReadModuleCounterIDs(module.AsFFIModule(), len(rules))
			require.NoError(t, err)
			for idx, observed := range witness.Lookups {
				require.NotZero(t, observed.Generation)
				require.Positive(t, observed.GenerationNumber)
				require.Equal(t, observed.CounterID, ids[idx])
				if tc.services[idx] == "missing" {
					require.Zero(t, observed.Object)
					require.Zero(t, observed.CounterRegistry)
					require.Equal(t, ^uint64(0), ids[idx])
				} else {
					require.NotZero(t, observed.Object)
					require.NotZero(t, observed.CounterRegistry)
					require.NotEqual(t, ^uint64(0), ids[idx])
				}
			}
			if len(rules) == 4 {
				require.Equal(t, witness.Lookups[0].Object, witness.Lookups[2].Object)
				require.NotEqual(t, witness.Lookups[0].Object, witness.Lookups[1].Object)
			}
			require.NoError(t, module.Free())
			require.NoError(t, agent.DeleteObject("l3b_virtual_service", "other"))
		})
	}
}

// Test_L3bModuleConfig_UpdateDuringOtherAgentPublication verifies that module
// builds retain lookup provenance while another agent replaces and frees services.
func Test_L3bModuleConfig_UpdateDuringOtherAgentPublication(t *testing.T) {
	harness, updatingAgent := setupL3bHarness(t, "port0", "test")
	// Leave control-plane capacity for sanitizer red zones and metadata.
	publishingAgent, err := harness.SharedMemory().AgentAttach("l3b-publisher", 0, l3bMemSize/2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = publishingAgent.CleanUp() })
	current, table := newVirtualService(t, publishingAgent, "svc", cl3bobject.VirtualServiceConfig{})
	require.NoError(t, current.Publish(publishingAgent))
	t.Cleanup(func() { _ = current.Free() })

	const rounds = 128
	ready := make(chan struct{})
	published := make(chan struct{})
	group, ctx := errgroup.WithContext(t.Context())
	updates, replacements, retiredFrees := 0, 0, 0
	generations := map[uint64]struct{}{}
	group.Go(func() error {
		for range rounds {
			err := func() (err error) {
				module, err := cl3b.NewModuleConfig(updatingAgent, "lookup")
				if err != nil {
					return err
				}
				defer func() { err = errors.Join(err, module.Free()) }()
				select {
				case ready <- struct{}{}:
				case <-ctx.Done():
					return ctx.Err()
				}
				witness, err := testutils.WithLinkedServiceLookup(updatingAgent, func() error {
					return module.Update([]cl3b.DestinationFilterRule{
						{VirtualService: "svc"}, {VirtualService: "svc"},
					})
				})
				if err != nil {
					return err
				}
				if len(witness.Lookups) != 2 || witness.CounterLookups != 2 ||
					witness.UnprotectedObjects != 0 || witness.UnprotectedCounters != 0 ||
					witness.ProvenanceErrors != 0 {
					return fmt.Errorf("unprotected or unobserved module lookups: %+v", witness)
				}
				ids, err := testutils.ReadModuleCounterIDs(module.AsFFIModule(), 2)
				if err != nil {
					return err
				}
				for idx, observed := range witness.Lookups {
					// A lookup diverted by a concurrent
					// publication keeps its pin but records no
					// provenance; its counter ID must still
					// resolve through the pinned object.
					if observed.Racing {
						if ids[idx] == ^uint64(0) || ids[idx] != observed.CounterID {
							return fmt.Errorf("racing rule %d lost its counter: %+v", idx, observed)
						}
						continue
					}
					if observed.Generation == 0 || observed.GenerationNumber == 0 ||
						observed.Object == 0 || observed.CounterRegistry == 0 ||
						observed.CounterID == ^uint64(0) || ids[idx] != observed.CounterID {
						return fmt.Errorf("rule %d lost its protected counter provenance: %+v", idx, observed)
					}
					generations[observed.GenerationNumber] = struct{}{}
				}
				return nil
			}()
			if err != nil {
				return err
			}
			updates++
			// Synchronize rounds only after the observed update has returned.
			select {
			case <-published:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	group.Go(func() error {
		for range rounds {
			replacement, err := cl3bobject.CreateVirtualService(
				publishingAgent, "svc", cl3bobject.VirtualServiceConfig{}, table,
			)
			if err != nil {
				return err
			}
			select {
			case <-ready:
			case <-ctx.Done():
				return errors.Join(ctx.Err(), replacement.Free())
			}
			if err := replacement.Publish(publishingAgent); err != nil {
				return errors.Join(err, replacement.Free())
			}
			previous := current
			current = replacement
			replacements++
			// The publish may have retired a generation the other
			// goroutine's linked-service read still pins, so the
			// retiring free can be answered with still-referenced
			// until that read drains; keep trying, but bound the
			// wait so a leaked pin reports instead of hanging the
			// suite.
			freeDeadline := time.Now().Add(30 * time.Second)
			for {
				err := previous.Free()
				if !errors.Is(err, ffi.ErrStillReferenced) {
					if err != nil {
						return err
					}
					break
				}
				if time.Now().After(freeDeadline) {
					return fmt.Errorf(
						"retired service stayed referenced for %v; a generation pin leaked",
						30*time.Second,
					)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Millisecond):
				}
			}
			retiredFrees++
			select {
			case published <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	require.NoError(t, group.Wait())
	require.Equal(t, rounds, updates)
	require.Equal(t, rounds, replacements)
	require.Equal(t, rounds, retiredFrees)
	require.Greater(t, len(generations), 1)
	t.Logf("updates=%d replacements=%d retired-frees=%d observed-generations=%d",
		updates, replacements, retiredFrees, len(generations),
	)
	require.NoError(t, publishingAgent.DeleteObject("l3b_virtual_service", "svc"))
	require.NoError(t, current.Free())
	require.NoError(t, publishingAgent.DeleteObject("l3b_session_table", "svc"))
	require.NoError(t, table.Free())
}
