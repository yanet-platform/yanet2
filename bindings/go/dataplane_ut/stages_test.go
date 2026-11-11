package dataplaneut_test

import (
	"net"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/devices/plain/bindings/go/cplain"
	"github.com/yanet-platform/yanet2/devices/vlan/bindings/go/cvlan"
)

// newStageHarness publishes a wire output entry on two real workers.
func newStageHarness(t *testing.T) (*dataplaneut.Harness, *ffi.Agent) {
	t.Helper()
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory: uint64(64 * datasize.MB), DPMemory: uint64(4 * datasize.MB), WorkerCount: 2,
		Devices: []string{"port0"}, DevicesToLoad: []string{"plain", "vlan"},
		Modules: []string{"forward"},
	},
	)
	require.NoError(t, err)
	t.Cleanup(harness.Free)
	agent, err := harness.SharedMemory().AgentAttach("stages", 0, 16*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, agent.CleanUp()) })
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "wire"}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "wire-out"}))
	_, err = cplain.UpdateDevices(agent, []ffi.DeviceConfig{{Name: "port0", Input: []ffi.DevicePipelineConfig{{Name: "wire", Weight: 1}}, Output: []ffi.DevicePipelineConfig{{Name: "wire-out", Weight: 1}}}})
	require.NoError(t, err)
	return harness, agent
}

// Test_HarnessStages_RoundAndGenerationValidation verifies that only the opened
// worker and current generation can execute or resume a stage.
func Test_HarnessStages_RoundAndGenerationValidation(t *testing.T) {
	harness, _ := newStageHarness(t)
	packet := stagePacket(t)
	stage := dataplaneut.Stage{Output: true}
	_, err := harness.RunStage(stage, packet.Data())
	require.Error(t, err)
	for _, worker := range []int{-1, 2} {
		require.Error(t, harness.BeginRound(worker))
		_, err = harness.RunStage(dataplaneut.Stage{Worker: worker}, packet.Data())
		require.Error(t, err)
	}
	require.NoError(t, harness.BeginRound(0))
	_, err = harness.RunStage(dataplaneut.Stage{Worker: 1, Output: true}, packet.Data())
	require.Error(t, err)
	_, err = harness.RunStage(dataplaneut.Stage{DeviceID: 1}, packet.Data())
	require.Error(t, err)
	for range 2 {
		result, err := harness.RunStage(stage, packet.Data())
		require.NoError(t, err)
		require.Equal(t, [][]byte{packet.Data()}, result.Output)
		require.Equal(t, []uint16{0}, result.OutputFlags)
		require.Empty(t, result.Drop)
	}
	_, err = harness.HandlePackets(packet)
	require.NoError(t, err)
	_, err = harness.RunStage(stage, packet.Data())
	require.Error(t, err)
	require.NoError(t, harness.BeginRound(0))
	require.NoError(t, harness.InstallEmptyPipeline("next"))
	_, err = harness.RunStage(stage, packet.Data())
	require.Error(t, err)
	require.NoError(t, harness.BeginRound(0))
	_, err = harness.RunStage(stage, packet.Data())
	require.NoError(t, err)
	require.Zero(t, harness.OutstandingMbufs())
}

// Test_HarnessStages_AllocationBudgetBounds verifies that pool limits reject
// values outside their native capacity and can be restored after exhaustion.
func Test_HarnessStages_AllocationBudgetBounds(t *testing.T) {
	harness, _ := newStageHarness(t)
	for _, budget := range []int{-2, 1025, int(^uint(0) >> 1)} {
		require.Error(t, harness.SetAllocationBudget(budget))
	}
	require.NoError(t, harness.SetAllocationBudget(0))
	require.NoError(t, harness.SetAllocationBudget(-1))
}

// stagePacket returns one valid IPv4 frame suitable for either entry direction.
func stagePacket(t *testing.T) gopacket.Packet {
	t.Helper()
	return xpacket.LayersToPacket(t, &layers.Ethernet{SrcMAC: net.HardwareAddr{0, 1, 2, 3, 4, 5}, DstMAC: net.HardwareAddr{0, 1, 2, 3, 4, 6}, EthernetType: layers.EthernetTypeIPv4}, &layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolICMPv4, SrcIP: net.IPv4(10, 0, 0, 1), DstIP: net.IPv4(10, 0, 0, 2)}, &layers.ICMPv4{TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0)})
}

// Test_HarnessStages_InvalidPacketsLeaveRoundUsable verifies that failed raw
// construction frees all preceding inputs without consuming the opened round.
func Test_HarnessStages_InvalidPacketsLeaveRoundUsable(t *testing.T) {
	harness, _ := newStageHarness(t)
	require.NoError(t, harness.BeginRound(0))
	packet := stagePacket(t)
	for _, tc := range []struct {
		name    string
		payload []byte
	}{
		{name: "empty raw payload"},
		{name: "truncated ethernet after valid input", payload: []byte{1}},
		{name: "oversized raw payload", payload: make([]byte, 65536)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := harness.RunStage(dataplaneut.Stage{Output: true}, packet.Data(), tc.payload)
			require.Error(t, err)
			require.Nil(t, result)
			require.Zero(t, harness.OutstandingMbufs())
			result, err = harness.RunStage(dataplaneut.Stage{Output: true}, packet.Data())
			require.NoError(t, err)
			require.Equal(t, [][]byte{packet.Data()}, result.Output)
		},
		)
	}
}

// Test_HarnessStages_RedirectMatchesOrdinaryRound verifies that a staged input
// runs its scheduled output through the same production dispatch and counters.
func Test_HarnessStages_RedirectMatchesOrdinaryRound(t *testing.T) {
	harness, agent := newStageHarness(t)
	_, err := forward.NewBackend(agent).Update("redirect", forward.RuleSet{Compiled: []cforward.ForwardRule{{Target: "port0", Mode: cforward.ModeOut, Counter: "forwarded"}}})
	require.NoError(t, err)
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{Name: "redirect", Chains: []ffi.FunctionChainConfig{{Weight: 1, Chain: ffi.ChainConfig{Name: "redirect", Modules: []ffi.ChainModuleConfig{{Type: "forward", Name: "redirect"}}}}}}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "redirect", Functions: []string{"redirect"}}))
	_, err = cplain.UpdateDevices(agent, []ffi.DeviceConfig{{Name: "port0", Input: []ffi.DevicePipelineConfig{{Name: "redirect", Weight: 1}}, Output: []ffi.DevicePipelineConfig{{Name: "wire-out", Weight: 1}}}})
	require.NoError(t, err)
	packet := stagePacket(t)
	ordinary, err := harness.HandlePackets(packet)
	require.NoError(t, err)
	require.Len(t, ordinary.Output, 1)
	require.Empty(t, ordinary.Drop)
	require.NoError(t, harness.BeginRound(0))
	staged, err := harness.RunStage(dataplaneut.Stage{}, packet.Data())
	require.NoError(t, err)
	require.Equal(t, [][]byte{ordinary.Output[0].RawData}, staged.Output)
	require.Empty(t, staged.Drop)
	_, err = harness.RunStage(dataplaneut.Stage{})
	require.NoError(t, err)
	require.Zero(t, harness.OutstandingMbufs())
	dataplaneut.RequireModuleCounter(t, harness, dataplaneut.CounterPath{Device: "port0", Pipeline: "redirect", Function: "redirect", Chain: "redirect", ModuleType: "forward", ModuleName: "redirect"}, "rx", 2, uint64(2*len(packet.Data())))
}

// Test_HarnessStages_LogicalDeviceBeyondPhysicalTopology verifies that a
// published logical entry can run even when its index exceeds the wire ports.
func Test_HarnessStages_LogicalDeviceBeyondPhysicalTopology(t *testing.T) {
	harness, agent := newStageHarness(t)
	_, err := forward.NewBackend(agent).Update("logical-route", forward.RuleSet{Compiled: []cforward.ForwardRule{{Target: "port0", Mode: cforward.ModeOut, Counter: "forwarded"}}})
	require.NoError(t, err)
	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{Name: "logical-route", Chains: []ffi.FunctionChainConfig{{Weight: 1, Chain: ffi.ChainConfig{Name: "logical-route", Modules: []ffi.ChainModuleConfig{{Type: "forward", Name: "logical-route"}}}}}}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "logical-route", Functions: []string{"logical-route"}}))
	logical, err := cvlan.NewDeviceConfig(agent, "logical", []cvlan.Pipeline{{Name: "logical-route", Weight: 1}}, []cvlan.Pipeline{{Name: "wire-out", Weight: 1}}, 42)
	require.NoError(t, err)
	require.NoError(t, agent.UpdateDevices([]ffi.ShmDeviceConfig{logical.AsFFIDevice()}))
	device, found := harness.SharedMemory().DPConfig(0).Device("vlan", "logical")
	require.True(t, found)
	require.GreaterOrEqual(t, device.Index, uint32(1))
	require.NoError(t, harness.BeginRound(0))
	packet := stagePacket(t)
	result, err := harness.RunStage(dataplaneut.Stage{DeviceID: uint16(device.Index)}, packet.Data())
	require.NoError(t, err)
	require.Equal(t, [][]byte{packet.Data()}, result.Output)
	require.Empty(t, result.Drop)
	require.Zero(t, harness.OutstandingMbufs())
}
