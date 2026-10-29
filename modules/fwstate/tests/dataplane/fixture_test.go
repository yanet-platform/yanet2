package fwstate_test

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	modulefwstate "github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	objectfwstate "github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
)

const (
	frameSize        = objectfwstate.SyncFrameSize
	syncRecordSize   = objectfwstate.StashRecordSize
	stashDefaultSize = objectfwstate.DefaultStashSize
	recordApplied    = objectfwstate.StashRecordApplied
	recordSuppressed = objectfwstate.StashRecordSuppressed
)

type fwstateFixture struct {
	Testing   *testing.T
	Harness   *dataplaneut.Harness
	Agent     *ffi.Agent
	Maps      [2]*objectfwstate.MapObjectConfig
	Configs   []*testConfig
	Published bool
}

type testConfig struct {
	Fixture *fwstateFixture
	Name    string
	Sync    modulefwstate.SyncConfig
	Devices []uint16
}

type workerRef struct {
	Fixture *fwstateFixture
	Index   int
}
type localEvent struct{ frame []byte }

// newHarnessAgent returns a real two-worker harness and its attached agent.
func newHarnessAgent(t *testing.T) (*dataplaneut.Harness, *ffi.Agent) {
	t.Helper()
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(128 * datasize.MB),
		DPMemory:      uint64(4 * datasize.MB),
		WorkerCount:   2,
		Devices:       []string{"port0", "port1", "port2"},
		Modules:       []string{"fwstate"},
		DevicesToLoad: []string{"plain"},
		ObjectsToLoad: []string{"fwstate_map_v4", "fwstate_map_v6"},
	},
	)
	require.NoError(t, err)
	t.Cleanup(harness.Free)
	agent, err := harness.SharedMemory().AgentAttach("fwstate-test", 0, 64*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, agent.CleanUp()) })
	t.Cleanup(func() { require.Zero(t, harness.OutstandingMbufs()) })
	harness.SetCurrentTime(time.Unix(1, 0))
	return harness, agent
}

// newFWStateFixture owns published family maps and lazily installs final
// configs.
func newFWStateFixture(t *testing.T, stashSize uint64) *testConfig {
	t.Helper()
	harness, agent := newHarnessAgent(t)
	fixture := &fwstateFixture{Testing: t, Harness: harness, Agent: agent}
	for idx, kind := range []objectfwstate.Kind{objectfwstate.KindV4, objectfwstate.KindV6} {
		object, err := objectfwstate.NewMapObjectConfig(agent, "fw-"+kind.String(), kind)
		require.NoError(t, err)
		require.NoError(t, object.CreateMap(objectfwstate.MapConfig{
			IndexSize: 1024, ExtraBucketCount: 64,
			WorkerCount: 2, StashSize: stashSize,
		},
		),
		)
		require.NoError(t, object.Publish(agent))
		fixture.Maps[idx] = object
	}
	config := &testConfig{Fixture: fixture, Name: "test", Devices: []uint16{0, 1}, Sync: modulefwstate.SyncConfig{
		Endpoints: []modulefwstate.SyncEndpoint{{
			SrcAddr: netip.MustParseAddr("fd00::1").As16(),
			DstAddr: netip.MustParseAddr("ff02::1").As16(),
			Port:    9999,
		}},
		TcpSynAck: 120e9, TcpSyn: 120e9, TcpFin: 120e9, Tcp: 120e9, Udp: 30e9, Default: 16e9,
	}}
	fixture.Configs = append(fixture.Configs, config)
	return config
}

// Publish installs each placement as an output entry using production builders.
func (m *fwstateFixture) Publish() {
	if m.Published {
		return
	}
	m.Published = true
	require.NoError(m.Testing, m.Agent.UpdatePipeline(ffi.PipelineConfig{Name: "empty"}))
	require.NoError(m.Testing, m.Agent.UpdatePipeline(ffi.PipelineConfig{Name: "empty-out"}))
	devices := make([]ffi.DeviceConfig, 3)
	for idx := range devices {
		devices[idx] = ffi.DeviceConfig{Name: fmt.Sprintf("port%d", idx), Input: []ffi.DevicePipelineConfig{{Name: "empty", Weight: 1}}}
	}
	for _, config := range m.Configs {
		module, err := modulefwstate.NewModuleConfig(m.Agent, config.Name, &config.Sync, m.Maps[0].Name(), m.Maps[1].Name())
		require.NoError(m.Testing, err)
		require.NoError(m.Testing, m.Agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}))
		require.NoError(m.Testing, m.Agent.UpdateFunction(ffi.FunctionConfig{
			Name: config.Name,
			Chains: []ffi.FunctionChainConfig{{
				Weight: 1,
				Chain: ffi.ChainConfig{
					Name:    config.Name,
					Modules: []ffi.ChainModuleConfig{{Type: "fwstate", Name: config.Name}},
				},
			}},
		},
		),
		)
		require.NoError(m.Testing, m.Agent.UpdatePipeline(ffi.PipelineConfig{Name: config.Name, Functions: []string{config.Name}}))
		for _, device := range config.Devices {
			devices[device].Output = []ffi.DevicePipelineConfig{{Name: config.Name, Weight: 1}}
		}
	}
	for idx := range devices {
		if len(devices[idx].Output) == 0 {
			devices[idx].Output = []ffi.DevicePipelineConfig{{Name: "empty-out", Weight: 1}}
		}
	}
	_, err := cplain.UpdateDevices(m.Agent, devices)
	require.NoError(m.Testing, err)
}

// Worker identifies an actual worker owned by this fixture.
func (m *testConfig) Worker(index int) workerRef { return workerRef{m.Fixture, index} }

// nextRound advances deterministic time and opens a real worker iteration.
func nextRound(worker workerRef) {
	worker.Fixture.Publish()
	worker.Fixture.Harness.AdvanceTime(time.Nanosecond)
	require.NoError(worker.Fixture.Testing, worker.Fixture.Harness.BeginRound(worker.Index))
}

// stashEvents appends local records to the real published family maps.
func stashEvents(config *testConfig, worker workerRef, events ...localEvent) int {
	workers, err := config.Fixture.Harness.SharedMemory().DPConfig(0).WorkerCounters()
	require.NoError(config.Fixture.Testing, err)
	iteration := workers[worker.Index].Iterations
	for _, event := range events {
		idx := 0
		if event.frame[15] == 6 {
			idx = 1
		}
		require.NoError(config.Fixture.Testing, config.Fixture.Maps[idx].AppendLocalEvents(uint16(worker.Index), iteration, event.frame))
	}
	return len(events)
}

// runConfig executes the config's first published output placement.
func runConfig(config *testConfig, worker workerRef, flagged bool, packets ...gopacket.Packet) *dataplaneut.StageResult {
	return runConfigPlacement(config, worker, 0, flagged, packets...)
}

// runConfigPlacement chooses a real device placement within the current round.
func runConfigPlacement(config *testConfig, worker workerRef, context uint16, flagged bool, packets ...gopacket.Packet) *dataplaneut.StageResult {
	data := make([][]byte, len(packets))
	for idx := range packets {
		data[idx] = packets[idx].Data()
	}
	flags := uint16(0)
	if flagged {
		flags = modulefwstate.SyncInternalPacketFlag
	}
	result, err := config.Fixture.Harness.RunStage(dataplaneut.Stage{
		Worker: worker.Index, DeviceID: config.Devices[context],
		Output: true, PacketFlags: flags,
	}, data...,
	)
	require.NoError(config.Fixture.Testing, err)
	require.Zero(config.Fixture.Testing, config.Fixture.Harness.OutstandingMbufs())
	return result
}

// fwstateHandleLocalEvents applies local records in a fresh worker round.
func fwstateHandleLocalEvents(config *testConfig, packets ...gopacket.Packet) (*dataplaneut.StageResult, error) {
	worker := config.Worker(0)
	nextRound(worker)
	for _, packet := range packets {
		stashEvents(config, worker, localEvent{syncFrameOf(packet)})
	}
	return runConfig(config, worker, false), nil
}

// fwstateHandleWirePackets delivers ordinary packets in a fresh round.
func fwstateHandleWirePackets(config *testConfig, packets ...gopacket.Packet) (*dataplaneut.StageResult, error) {
	worker := config.Worker(0)
	nextRound(worker)
	return runConfig(config, worker, false, packets...), nil
}

// fwstateHandleFlaggedPackets delivers emissions already marked as local.
func fwstateHandleFlaggedPackets(config *testConfig, packets ...gopacket.Packet) (*dataplaneut.StageResult, error) {
	worker := config.Worker(0)
	nextRound(worker)
	return runConfig(config, worker, true, packets...), nil
}

// SetSyncSuppressTimeout selects the suppression window before publication.
func SetSyncSuppressTimeout(config *testConfig, timeout uint64) {
	config.Sync.SyncSuppressTimeout = timeout
}

// ClearSyncDestination disables every endpoint before publication.
func ClearSyncDestination(config *testConfig) {
	config.Sync.Endpoints = nil
}

// SetSyncUnicastDestination enables the unicast endpoint in addition to the
// configured ones before publication.
func SetSyncUnicastDestination(config *testConfig) {
	config.Sync.Endpoints = append(config.Sync.Endpoints, modulefwstate.SyncEndpoint{
		SrcAddr: netip.MustParseAddr("fd00::1").As16(),
		DstAddr: netip.MustParseAddr("2001:db8::3").As16(),
		Port:    10000,
	})
}

// SetSyncTCPTimeouts makes established and closing deadlines distinguishable.
func SetSyncTCPTimeouts(config *testConfig, established, closing uint64) {
	config.Sync.Tcp = established
	config.Sync.TcpFin = closing
}

// SetSyncMTU selects the packet batching limit before publication.
func SetSyncMTU(config *testConfig, mtu uint16) { config.Sync.SyncMTU = mtu }

// fwstateSiblingModuleConfig adds an independently configured real placement.
func fwstateSiblingModuleConfig(first *testConfig, name string) *testConfig {
	second := &testConfig{Fixture: first.Fixture, Name: name, Sync: first.Sync, Devices: []uint16{2}}
	ClearSyncDestination(second)
	SetSyncUnicastDestination(second)
	first.Fixture.Configs = append(first.Fixture.Configs, second)
	return second
}

// moduleCounter sums the config's real placement and worker counter storages.
func moduleCounter(config *testConfig, name string) uint64 {
	var total uint64
	for _, device := range config.Devices {
		counters := config.Fixture.Harness.SharedMemory().DPConfig(0).ModuleCounters(fmt.Sprintf("port%d", device), config.Name, config.Name, config.Name, "fwstate", config.Name, []string{name})
		require.Len(config.Fixture.Testing, counters, 1)
		for _, values := range counters[0].Values {
			total += values[0]
		}
	}
	return total
}

// familyMap selects the actual object owning the requested address family.
func familyMap(config *testConfig, isIPv6 bool) *objectfwstate.MapObjectConfig {
	if isIPv6 {
		return config.Fixture.Maps[1]
	}
	return config.Fixture.Maps[0]
}

// GetStateValue reads the current entry and actual deadline from its owner.
func GetStateValue(config *testConfig, protocol layers.IPProtocol, sourcePort, destinationPort uint16, source, destination string) objectfwstate.StateLookup {
	sourceAddress := netip.MustParseAddr(source)
	key := objectfwstate.StateKey{
		Proto: uint32(protocol), SrcPort: uint32(sourcePort), DstPort: uint32(destinationPort),
		SrcAddr: sourceAddress.AsSlice(), DstAddr: netip.MustParseAddr(destination).AsSlice(),
	}
	return xerror.Unwrap(familyMap(config, sourceAddress.Is6()).LookupState(key))
}

// CheckStateExists keeps expired entries visible until their layer is
// reclaimed.
func CheckStateExists(config *testConfig, protocol layers.IPProtocol, sourcePort, destinationPort uint16, source, destination string) bool {
	return GetStateValue(config, protocol, sourcePort, destinationPort, source, destination).Found
}

// GetStateDeadline reads the actual table deadline across all current layers.
func GetStateDeadline(config *testConfig, protocol layers.IPProtocol, sourcePort, destinationPort uint16, source, destination string) uint64 {
	return GetStateValue(config, protocol, sourcePort, destinationPort, source, destination).Deadline
}

// InsertNewLayer rotates both real maps without replacing their stashes.
func InsertNewLayer(config *testConfig) {
	for _, object := range config.Fixture.Maps {
		require.NoError(config.Fixture.Testing, object.InsertLayer(1024, 64, 2))
	}
}

// GetLayerCount counts current layers on both real maps.
func GetLayerCount(config *testConfig) (uint32, uint32) {
	return config.Fixture.Maps[0].GetStats().LayerCount, config.Fixture.Maps[1].GetStats().LayerCount
}

// TrimStaleLayers retires expired tails while the fixture has no active
// readers.
func TrimStaleLayers(config *testConfig, now uint64) error {
	for _, object := range config.Fixture.Maps {
		if err := object.UnlinkStaleLayers(now); err != nil {
			return err
		}
		if err := object.FreeStaleLayers(); err != nil {
			return err
		}
	}
	return nil
}

// stashSlotCount reads the real worker slot's current record count.
func stashSlotCount(config *testConfig, isIPv6 bool, worker uint16) int {
	return len(xerror.Unwrap(familyMap(config, isIPv6).StashRecords(worker)))
}

// stashRecordStatus reads a copied record through the object's binding.
func stashRecordStatus(config *testConfig, isIPv6 bool, worker uint16, index int) uint8 {
	return xerror.Unwrap(familyMap(config, isIPv6).StashRecords(worker))[index].Status
}

// stashSlotAddr observes allocation alignment without permitting mutation.
func stashSlotAddr(config *testConfig, isIPv6 bool, worker uint16) uintptr {
	return xerror.Unwrap(familyMap(config, isIPv6).StashLayout(worker)).SlotAddress
}

// stashRecordsAddr observes the actual independent worker record allocation.
func stashRecordsAddr(config *testConfig, isIPv6 bool, worker uint16) uintptr {
	return xerror.Unwrap(familyMap(config, isIPv6).StashLayout(worker)).RecordsAddress
}

// poolOutstanding reports real allocations still owned outside the pool.
func poolOutstanding(worker workerRef) uint64 { return worker.Fixture.Harness.OutstandingMbufs() }

// limitPool bounds the number of simultaneous allocations from the pool.
func limitPool(worker workerRef, extra uint32) {
	require.NoError(worker.Fixture.Testing, worker.Fixture.Harness.SetAllocationBudget(int(extra)))
}

// unlimitPool restores ordinary pool capacity.
func unlimitPool(worker workerRef) {
	require.NoError(worker.Fixture.Testing, worker.Fixture.Harness.SetAllocationBudget(-1))
}

type SyncFrameOption func([]byte)

// WithFrameFlags specifies both direction nibbles in their wire byte.
func WithFrameFlags(flags uint8) SyncFrameOption { return func(frame []byte) { frame[14] = flags } }

// WithFrameFib selects the state event's packet counter direction.
func WithFrameFib(direction uint8) SyncFrameOption {
	return func(frame []byte) { frame[12] = direction }
}

// createSyncFrame returns the fixed wire layout consumed by local stash
// records.
func createSyncFrame(protocol layers.IPProtocol, family uint8, sourcePort, destinationPort uint16, destination, source []byte, options ...SyncFrameOption) []byte {
	frame := make([]byte, frameSize)
	binary.NativeEndian.PutUint16(frame[8:10], destinationPort)
	binary.NativeEndian.PutUint16(frame[10:12], sourcePort)
	frame[13] = uint8(protocol)
	frame[15] = family
	copy(frame[16:32], destination)
	copy(frame[32:48], source)
	for _, option := range options {
		option(frame)
	}
	return frame
}

// syncFrameOf copies the local event carried by a serialized packet fixture.
func syncFrameOf(packet gopacket.Packet) []byte {
	return append([]byte(nil), packet.Layer(layers.LayerTypeUDP).(*layers.UDP).Payload[:frameSize]...)
}

// v6Event distinguishes IPv6 TCP state records by their source port.
func v6Event(sourcePort uint16) localEvent {
	return localEvent{createSyncFrame(layers.IPProtocolTCP, 6, sourcePort, 80, net.ParseIP("2001:db8::2"), net.ParseIP("2001:db8::1"))}
}

// v4Event distinguishes IPv4 UDP state records by their source port.
func v4Event(sourcePort uint16) localEvent {
	return localEvent{createSyncFrame(layers.IPProtocolUDP, 4, sourcePort, 53, nil, nil)}
}

// syncPayload copies endpoint and frame bytes from an emitted packet.
func syncPayload(raw []byte) (net.IP, uint16, []byte) {
	packet := gopacket.NewPacket(raw, layers.LayerTypeEthernet, gopacket.Default)
	network := packet.Layer(layers.LayerTypeIPv6).(*layers.IPv6)
	transport := packet.Layer(layers.LayerTypeUDP).(*layers.UDP)
	return network.DstIP, uint16(transport.DstPort), append([]byte(nil), transport.Payload...)
}
