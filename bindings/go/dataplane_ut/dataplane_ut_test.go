package dataplaneut

import (
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
)

// TestHarnessLifecycle exercises construction, shared-memory access, and
// teardown of the Harness without running any packets.
// executionContextWorkers is the worker population every test below
// uses: two workers are the minimum that separates the worker that ran
// a round from a worker that never ran.
const executionContextWorkers = 2

// newExecutionContextHarness builds a two-worker harness with an
// otherwise empty topology. The generation-assignment protocol under
// test needs only generation switches, not packet-routing modules or
// device wiring.
func newExecutionContextHarness(t *testing.T) *Harness {
	t.Helper()

	harness, err := NewHarness(Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: executionContextWorkers,
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	return harness
}

// Test_WorkerExecutionContext_Bootstrap_FieldIsNullAndNothingAcked
// verifies that a fresh harness with no installs reports a zero
// published generation, no per-worker context in any worker's field,
// and no acknowledged generation on any worker.
func Test_WorkerExecutionContext_Bootstrap_FieldIsNullAndNothingAcked(t *testing.T) {
	harness := newExecutionContextHarness(t)

	require.Zero(t, harness.PublishedGeneration(), "no install ran, so no generation is published")
	for workerIdx := range executionContextWorkers {
		published, err := harness.PublishedExecutionContext(workerIdx)
		require.NoError(t, err)
		require.Zero(
			t,
			published,
			"worker %d must have no published context before any install",
			workerIdx,
		)
		assigned, err := harness.WorkerExecutionContext(workerIdx)
		require.NoError(t, err)
		require.Zero(
			t,
			assigned,
			"worker %d must hold no context before any install",
			workerIdx,
		)
		generation, err := harness.WorkerGeneration(workerIdx)
		require.NoError(t, err)
		require.Zero(
			t,
			generation,
			"worker %d must acknowledge nothing before any install",
			workerIdx,
		)
	}
}

// Test_WorkerExecutionContext_Install_AssignsPublishedContextToEveryWorker
// verifies that one empty-pipeline install assigns every worker,
// synchronously on return, the per-worker execution context of the
// newly published generation.
//
// Every acknowledgement must stay zero: an install switches contexts
// but only a round acknowledges, and no round has run.
func Test_WorkerExecutionContext_Install_AssignsPublishedContextToEveryWorker(t *testing.T) {
	harness := newExecutionContextHarness(t)

	require.NoError(t, harness.InstallEmptyPipeline("assigned"))

	require.NotZero(t, harness.PublishedGeneration(), "an install must publish a new generation")
	for workerIdx := range executionContextWorkers {
		published, err := harness.PublishedExecutionContext(workerIdx)
		require.NoError(t, err)
		require.NotZero(
			t,
			published,
			"worker %d must have a published context after an install",
			workerIdx,
		)
		assigned, err := harness.WorkerExecutionContext(workerIdx)
		require.NoError(t, err)
		require.Equal(
			t,
			published,
			assigned,
			"worker %d's field must already hold the published context on return from the install",
			workerIdx,
		)
		generation, err := harness.WorkerGeneration(workerIdx)
		require.NoError(t, err)
		require.Zero(
			t,
			generation,
			"worker %d must not acknowledge a generation without a round",
			workerIdx,
		)
	}
}

// Test_WorkerExecutionContext_Reinstall_ReassignsNewContextToEveryWorker
// verifies that a second install publishes a fresh per-worker context
// and every worker's field follows it, pinning re-assignment on every
// generation switch rather than a one-time bootstrap stamp.
func Test_WorkerExecutionContext_Reinstall_ReassignsNewContextToEveryWorker(t *testing.T) {
	harness := newExecutionContextHarness(t)

	require.NoError(t, harness.InstallEmptyPipeline("first"))
	firstGeneration := harness.PublishedGeneration()
	firstContexts := [executionContextWorkers]uintptr{}
	for workerIdx := range executionContextWorkers {
		published, err := harness.PublishedExecutionContext(workerIdx)
		require.NoError(t, err)
		firstContexts[workerIdx] = published
	}

	require.NoError(t, harness.InstallEmptyPipeline("second"))
	require.Greater(
		t,
		harness.PublishedGeneration(),
		firstGeneration,
		"a reinstall must advance the published generation",
	)

	for workerIdx := range executionContextWorkers {
		second, err := harness.PublishedExecutionContext(workerIdx)
		require.NoError(t, err)
		require.NotZero(
			t,
			second,
			"worker %d must have a published context after the second install",
			workerIdx,
		)
		require.NotEqual(
			t,
			firstContexts[workerIdx],
			second,
			"worker %d's second generation must publish a fresh context",
			workerIdx,
		)
		assigned, err := harness.WorkerExecutionContext(workerIdx)
		require.NoError(t, err)
		require.Equal(
			t,
			second,
			assigned,
			"worker %d's field must follow the second generation",
			workerIdx,
		)
	}
}

// Test_WorkerExecutionContext_Round_AcksAssignedGenerationOnThatWorkerOnly
// verifies that one round run on a single worker acknowledges exactly
// the published generation on that worker, while a worker that never
// ran keeps acknowledging zero.
func Test_WorkerExecutionContext_Round_AcksAssignedGenerationOnThatWorkerOnly(t *testing.T) {
	harness := newExecutionContextHarness(t)

	require.NoError(t, harness.InstallEmptyPipeline("round"))

	ethernet := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ipv4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("1.2.3.4"),
		DstIP:    net.ParseIP("10.0.0.5"),
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}
	packet := xpacket.LayersToPacket(t, &ethernet, &ipv4, &icmp)

	// The topology wires no device, so the round drops the packet; the
	// dropped packet proves the round processed input on worker 0.
	result, err := harness.HandlePacketsOnWorker(0, packet)
	require.NoError(t, err)
	require.Empty(t, result.Output)
	require.Len(t, result.Drop, 1)

	acknowledged, err := harness.WorkerGeneration(0)
	require.NoError(t, err)
	require.Equal(
		t,
		harness.PublishedGeneration(),
		acknowledged,
		"worker 0 must acknowledge the published generation after its round",
	)
	idleAcknowledged, err := harness.WorkerGeneration(1)
	require.NoError(t, err)
	require.Zero(
		t,
		idleAcknowledged,
		"worker 1 never ran a round and must keep acknowledging zero",
	)
}

// Test_WorkerExecutionContext_InstallAfterRound_CompletesDespiteStaleAcknowledgement
// verifies that an install issued after a completed round returns even
// though the round left its worker acknowledging the generation it
// processed.
//
// No later round exists to move that acknowledgement, so the switch
// must not wait for one. This is the regression shape of a harness
// hanging on its first update after driving packets (#1881).
func Test_WorkerExecutionContext_InstallAfterRound_CompletesDespiteStaleAcknowledgement(t *testing.T) {
	// Teardown is owned here rather than by the shared fixture.
	//
	// When the probe below times out, the install stays blocked
	// inside the harness, and freeing the arena under it would turn
	// the timeout into a use-after-free — the failing binary leaks it
	// instead.
	harness, err := NewHarness(Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: executionContextWorkers,
	})
	require.NoError(t, err)
	installBlocked := false
	t.Cleanup(func() {
		if !installBlocked {
			harness.Free()
		}
	})

	require.NoError(t, harness.InstallEmptyPipeline("first"))

	ethernet := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ipv4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("1.2.3.4"),
		DstIP:    net.ParseIP("10.0.0.5"),
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}
	packet := xpacket.LayersToPacket(t, &ethernet, &ipv4, &icmp)

	// The topology wires no device, so the round drops the packet and
	// leaves worker 0 acknowledging the generation it just processed.
	result, err := harness.HandlePacketsOnWorker(0, packet)
	require.NoError(t, err)
	require.Len(t, result.Drop, 1)
	roundGeneration, err := harness.WorkerGeneration(0)
	require.NoError(t, err)
	require.Equal(
		t,
		harness.PublishedGeneration(),
		roundGeneration,
		"worker 0 must acknowledge the generation its round processed",
	)

	// The install must complete on its own: nothing drives another
	// round on this harness, so no further acknowledgement can arrive.
	done := make(chan error, 1)
	go func() {
		done <- harness.InstallEmptyPipeline("second")
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		installBlocked = true
		t.Fatal("install after a completed round never returned")
	}

	require.Greater(
		t,
		harness.PublishedGeneration(),
		roundGeneration,
		"a generation newer than the round's must be published",
	)

	// Only a round moves an acknowledgement, and none has run since:
	// completing the switch must not forge one.
	acknowledged, err := harness.WorkerGeneration(0)
	require.NoError(t, err)
	require.Equal(
		t,
		roundGeneration,
		acknowledged,
		"worker 0's acknowledgement must stay at the generation its round processed",
	)
	idleAcknowledged, err := harness.WorkerGeneration(1)
	require.NoError(t, err)
	require.Zero(
		t,
		idleAcknowledged,
		"worker 1 never ran a round and must keep acknowledging zero",
	)

	for workerIdx := range executionContextWorkers {
		published, err := harness.PublishedExecutionContext(workerIdx)
		require.NoError(t, err)
		assigned, err := harness.WorkerExecutionContext(workerIdx)
		require.NoError(t, err)
		require.Equal(
			t,
			published,
			assigned,
			"worker %d's field must hold the context published after the round",
			workerIdx,
		)
	}
}

// Test_WorkerExecutionContext_ConcurrentPublish_WaitsForInFlightRound
// verifies that a publish running while a round holds the round lock
// blocks until the round releases it, so no round can still touch the
// retired context when the old generation is freed.
func Test_WorkerExecutionContext_ConcurrentPublish_WaitsForInFlightRound(t *testing.T) {
	harness := newExecutionContextHarness(t)

	require.NoError(t, harness.InstallEmptyPipeline("before"))

	// Hold the round lock across the publish, exactly as a round holds
	// it across every context dereference: the install may retire the
	// old contexts only after this release.
	harness.holdRoundLock()

	publishDone := make(chan error, 1)
	go func() {
		publishDone <- harness.InstallEmptyPipeline("during")
	}()

	select {
	case err := <-publishDone:
		t.Fatalf("install completed while the round lock is held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	harness.releaseRoundLock()
	require.NoError(t, <-publishDone)

	for workerIdx := range executionContextWorkers {
		published, err := harness.PublishedExecutionContext(workerIdx)
		require.NoError(t, err)
		assigned, err := harness.WorkerExecutionContext(workerIdx)
		require.NoError(t, err)
		require.Equal(
			t,
			published,
			assigned,
			"worker %d's field must hold the context published after the round lock was released",
			workerIdx,
		)
	}
}

func TestHarnessLifecycle(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: 1,
	}

	h, err := NewHarness(cfg)
	require.NoError(t, err)
	require.NotNil(t, h)
	defer h.Free()

	shm := h.SharedMemory()
	require.NotNil(t, shm)
}

// verifies that NewHarness rejects a packet recirculation limit outside
// the 4..256 range on the Go side instead of passing it to the C layer.
func Test_NewHarness_RejectsInvalidPacketRecircLimit(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		limit uint16
	}{
		{name: "below_minimum", limit: 3},
		{name: "above_maximum", limit: 257},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			harness, err := NewHarness(Config{
				CPMemory:          uint64(datasize.MB * 32),
				DPMemory:          uint64(datasize.MB * 4),
				WorkerCount:       1,
				PacketRecircLimit: testCase.limit,
			})
			require.Error(t, err)
			require.Nil(t, harness)
		})
	}
}

// TestTimeRoundTrip verifies that SetCurrentTime and CurrentTime agree and
// that AdvanceTime correctly accumulates the delta.
func TestTimeRoundTrip(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB),
		WorkerCount: 1,
	}

	h, err := NewHarness(cfg)
	require.NoError(t, err)
	defer h.Free()

	epoch := time.Unix(0, 1_000_000_000)
	h.SetCurrentTime(epoch)
	got := h.CurrentTime()
	assert.Equal(t, epoch.UnixNano(), got.UnixNano())

	advanced := h.AdvanceTime(500 * time.Millisecond)
	assert.Equal(t, epoch.Add(500*time.Millisecond).UnixNano(), advanced.UnixNano())
	assert.Equal(t, advanced.UnixNano(), h.CurrentTime().UnixNano())
}

// TestAgentAttach checks that a control-plane agent can be attached to the
// shared-memory arena exposed by the harness.
func TestAgentAttach(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB),
		WorkerCount: 1,
	}

	h, err := NewHarness(cfg)
	require.NoError(t, err)
	defer h.Free()

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("smoke-agent", 0, datasize.MB*2)
	require.NoError(t, err)
	require.NotNil(t, agent)
}

// TestNewHarness_WorkersLengthMismatch verifies that Config.Workers, when
// set, must have exactly WorkerCount entries — a mismatched length is
// rejected instead of silently misassigning or truncating workers.
func TestNewHarness_WorkersLengthMismatch(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: 2,
		Workers:     []WorkerSpec{{DeviceID: 0, QueueID: 0}},
	}

	h, err := NewHarness(cfg)
	require.Error(t, err)
	require.Nil(t, h)
}

// Verifies that NewHarness rejects a WorkerSpec.DeviceID at or beyond the
// configured device count instead of stamping dp_worker->device_id with a
// value that would later index the C-side per-device scheduling arrays out
// of bounds.
func TestNewHarness_WorkerDeviceIDOutOfRange(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: 2,
		Devices:     []string{"port0", "port1"},
		Workers: []WorkerSpec{
			{DeviceID: 0, QueueID: 0},
			{DeviceID: 5, QueueID: 0},
		},
	}

	h, err := NewHarness(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds topology device count")
	require.Nil(t, h)
}

// TestHandleSegmentedPacketsOnDevice_InvalidDeviceID verifies that
// HandleSegmentedPacketsOnDevice rejects an rxDeviceID at or beyond the
// harness's registered device count instead of stamping a packet that
// would index the C-side per-device scheduling arrays out of bounds.
func TestHandleSegmentedPacketsOnDevice_InvalidDeviceID(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: 1,
	}

	h, err := NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	result, err := h.HandleSegmentedPacketsOnDevice(0, 5, [][]byte{{0x00}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds topology device count")
	require.Nil(t, result)
}

// TestHandleSegmentedPacketsOnDevice_InvalidWorker verifies that
// HandleSegmentedPacketsOnDevice rejects a worker index at or beyond the
// harness's registered worker count instead of indexing the C-side worker
// array out of bounds.
func TestHandleSegmentedPacketsOnDevice_InvalidWorker(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: 1,
	}

	h, err := NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	result, err := h.HandleSegmentedPacketsOnDevice(3, 0, [][]byte{{0x00}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds topology worker count")
	require.Nil(t, result)
}

// TestHandlePacketsOnWorker_InvalidWorker verifies that HandlePacketsOnWorker
// rejects a worker index at or beyond the harness's registered worker count
// instead of indexing the C-side worker array out of bounds.
func TestHandlePacketsOnWorker_InvalidWorker(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: 1,
	}

	h, err := NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	result, err := h.HandlePacketsOnWorker(3)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds topology worker count")
	require.Nil(t, result)
}

// TestHandleSegmentedPacketsOnDevice_ForwardDeviceScopedRule verifies that
// Config.Workers assigns each worker's own device and that
// HandleSegmentedPacketsOnDevice stamps the injected packet's ingress
// device, together letting a forward rule scoped to a non-zero device
// ("port1") demux the packet to that device's egress.
//
// The assertions bracket both knobs: WorkerCounters reads dp_worker's
// device_id back to prove Config.Workers was honored, and a baseline call
// through the pre-existing HandleSegmentedPackets (which still pins every
// packet to device 0) proves the same rule cannot match without the new
// injection knob.
func TestHandleSegmentedPacketsOnDevice_ForwardDeviceScopedRule(t *testing.T) {
	cfg := Config{
		CPMemory:      uint64(datasize.MB * 64),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   2,
		Devices:       []string{"port0", "port1"},
		Modules:       []string{"forward"},
		DevicesToLoad: []string{"plain"},
		Workers: []WorkerSpec{
			{DeviceID: 0, QueueID: 0},
			{DeviceID: 1, QueueID: 0},
		},
	}
	h, err := NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	shm := h.SharedMemory()
	agent, err := shm.AgentAttach("device-inject-test", 0, datasize.MB*16)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	// Confirm the Workers config threaded through to the C-side dp_worker
	// fields: without it, both workers would read back device 0.
	workerCounters, err := shm.DPConfig(0).WorkerCounters()
	require.NoError(t, err)
	require.Len(t, workerCounters, 2)
	assert.Equal(t, uint32(0), workerCounters[0].DeviceID)
	assert.Equal(t, uint32(1), workerCounters[1].DeviceID)

	backend := forward.NewBackend(agent)
	rule := cforward.ForwardRule{
		Target:  "port1",
		Mode:    cforward.ModeOut,
		Counter: "port1_rule",
		Devices: filter.Devices{{Name: "port1"}},
	}
	moduleHandle, err := backend.UpdateModule("demux", []cforward.ForwardRule{rule})
	require.NoError(t, err)
	t.Cleanup(func() { _ = moduleHandle.Free() })

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "demux",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    "demux_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "forward", Name: "demux"}},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "demux",
		Functions: []string{"demux"},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "dummy_out"}))

	// Port 0 has no output pipeline: the rule never targets it, so an
	// unmatched packet has nowhere to go but drop.
	_, err = plain.UpdateDevices(agent, []ffi.DeviceConfig{
		{
			Name:  "port0",
			Input: []ffi.DevicePipelineConfig{{Name: "demux", Weight: 1}},
		},
		{
			Name:   "port1",
			Input:  []ffi.DevicePipelineConfig{{Name: "demux", Weight: 1}},
			Output: []ffi.DevicePipelineConfig{{Name: "dummy_out", Weight: 1}},
		},
	})
	require.NoError(t, err)

	eth := layers.Ethernet{
		SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
		DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ip4 := layers.IPv4{
		Version:  4,
		TTL:      64,
		Protocol: layers.IPProtocolICMPv4,
		SrcIP:    net.ParseIP("1.2.3.4"),
		DstIP:    net.ParseIP("10.0.0.5"),
	}
	icmp := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(layers.ICMPv4TypeEchoRequest, 0),
	}
	pkt := xpacket.LayersToPacket(t, &eth, &ip4, &icmp)
	payload := pkt.Data()

	// Baseline: the pre-existing entrypoint pins every packet to device 0,
	// so the port1-scoped rule cannot match. The packet passes through
	// unmatched and is dropped by the input entry point's no-transmit
	// policy.
	baseline, err := h.HandleSegmentedPackets([][]byte{payload})
	require.NoError(t, err)
	assert.Empty(t, baseline.Output, "a packet pinned to device 0 must not match the port1-scoped rule")
	require.Len(t, baseline.Drop, 1)

	// With the packet's ingress device set to port1 and the round run on
	// the worker that owns port1, the device-scoped rule matches and
	// redirects the packet out through port1.
	result, err := h.HandleSegmentedPacketsOnDevice(1, 1, [][]byte{payload})
	require.NoError(t, err)
	require.Len(t, result.Output, 1, "a packet injected on port1 must match the device-scoped rule and reach output")
	assert.Empty(t, result.Drop)

	// The round ran on worker 1, so the per-rule counter lands in worker
	// 1's slot rather than worker 0's — RequireModuleCounter always reads
	// worker 0, so the module counters are read directly here instead.
	counters := shm.DPConfig(0).ModuleCounters(
		"port1", "demux", "demux", "demux_chain", "forward", "demux",
		[]string{"port1_rule"},
	)
	require.Len(t, counters, 1)
	require.GreaterOrEqual(t, len(counters[0].Values), 2)
	assert.Equal(t, []uint64{0, 0}, counters[0].Values[0], "worker 0 never ran the round")
	assert.Equal(t, []uint64{1, uint64(len(payload))}, counters[0].Values[1], "worker 1 ran the matching round")
}

// arenaReaderRole names the environment variable that turns a re-executed
// test binary into the reader half of the file-backed arena test.
const arenaReaderRole = "DATAPLANE_UT_TEST_ROLE"

// arenaReaderBase is the address the reader maps the arena at, proving
// the mapping resolves wherever a reader puts it rather than only at the
// address the writer happened to get.
//
// The mapping is requested without replacement, so a clash with anything
// already mapped there fails the child instead of hiding.
const arenaReaderBase = uintptr(0x7a0000000000)

// TestMain hands a re-executed test binary to its reader role before the
// tests run, so the child never enters the test list.
func TestMain(m *testing.M) {
	if os.Getenv(arenaReaderRole) == "arena-reader" {
		os.Exit(runArenaReader(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// addressPointer turns a bare address into the pointer a mapping hint
// takes.
//
// A direct uintptr conversion is what vet's unsafeptr rule rejects, so the
// value is reinterpreted through its own storage.
func addressPointer(address uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&address))
}

// runArenaReader maps the arena file at arenaReaderBase in this fresh
// process and prints whether the dataplane inside it is ready.
//
// One report line and the exit code stand in for the testing package,
// which never runs in the reader.
func runArenaReader(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "arena-reader: expected the arena path")
		return 2
	}
	file, err := os.OpenFile(args[0], os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "arena-reader:", err)
		return 1
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		fmt.Fprintln(os.Stderr, "arena-reader:", err)
		return 1
	}
	size := uintptr(info.Size())
	mapping, err := unix.MmapPtr(
		int(file.Fd()),
		0,
		addressPointer(arenaReaderBase),
		size,
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_SHARED|unix.MAP_FIXED_NOREPLACE,
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "arena-reader: mmap:", err)
		return 1
	}
	defer unix.MunmapPtr(mapping, size)
	fmt.Printf(
		"arena-reader mapping=%#x ready=%t\n",
		uintptr(mapping),
		arenaHandle(mapping, size).DataplaneReady(0),
	)
	return 0
}

// harnessArenaConfig is the one-worker topology the file-backed arena
// tests build, with the arena file inside the test's temporary directory.
func harnessArenaConfig(t *testing.T) Config {
	t.Helper()

	return Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: 1,
		ArenaPath:   filepath.Join(t.TempDir(), "arena"),
	}
}

// Test_Harness_ArenaPath_ChildProcessMapsLiveArena verifies that a
// harness whose arena is a file leaves the live dataplane state readable
// from another process: a re-executed test binary maps the file at its
// own fixed address and finds the instance ready there.
func Test_Harness_ArenaPath_ChildProcessMapsLiveArena(t *testing.T) {
	cfg := harnessArenaConfig(t)
	harness, err := NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	reader := exec.Command(os.Args[0], cfg.ArenaPath)
	reader.Env = append(os.Environ(), arenaReaderRole+"=arena-reader")
	output, err := reader.CombinedOutput()
	require.NoError(t, err, string(output))
	require.Contains(
		t,
		string(output),
		fmt.Sprintf("arena-reader mapping=%#x ready=true", arenaReaderBase),
	)
}

// Test_Harness_Raw_ReturnsTheHandle verifies that the raw accessor hands
// out the C handle the harness owns, so C-level callers and the Go
// methods drive one instance, and that it is nil once the harness is
// freed.
func Test_Harness_Raw_ReturnsTheHandle(t *testing.T) {
	harness, err := NewHarness(Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: 1,
	})
	require.NoError(t, err)

	require.NotNil(t, harness.Raw())
	require.Equal(t, unsafe.Pointer(harness.ptr), harness.Raw())

	harness.Free()
	require.Nil(t, harness.Raw())
}

// Test_Harness_ArenaPath_RejectsAnExistingFile verifies that a harness
// refuses a backing file that already exists, so a leftover arena from an
// earlier run is never silently reused as live dataplane state.
func Test_Harness_ArenaPath_RejectsAnExistingFile(t *testing.T) {
	cfg := harnessArenaConfig(t)
	require.NoError(t, os.WriteFile(cfg.ArenaPath, []byte("stale"), 0o600))

	harness, err := NewHarness(cfg)
	require.Error(t, err)
	require.Nil(t, harness)
	require.FileExists(t, cfg.ArenaPath, "a file the harness did not create must survive")
}

// Test_Harness_Free_UnlinksArenaFile verifies that the arena file holds
// the whole arena while the harness lives and is removed by Free, so no
// mapping file outlives its test.
func Test_Harness_Free_UnlinksArenaFile(t *testing.T) {
	cfg := harnessArenaConfig(t)
	harness, err := NewHarness(cfg)
	require.NoError(t, err)

	info, err := os.Stat(cfg.ArenaPath)
	require.NoError(t, err)
	require.Equal(t, int64(cfg.CPMemory+cfg.DPMemory), info.Size())

	harness.Free()
	_, err = os.Stat(cfg.ArenaPath)
	require.ErrorIs(t, err, fs.ErrNotExist)
}
