package dataplaneut

import (
	"net"
	"testing"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestHandlePacketsOnDevice_InvalidDeviceID verifies that
// HandlePacketsOnDevice rejects an rxDeviceID at or beyond the harness's
// registered device count instead of stamping a packet that would index the
// C-side per-device scheduling arrays out of bounds.
func TestHandlePacketsOnDevice_InvalidDeviceID(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: 1,
	}

	h, err := NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	result, err := h.HandlePacketsOnDevice(0, 5)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds topology device count")
	require.Nil(t, result)
}

// TestHandlePacketsOnDevice_InvalidWorker verifies that HandlePacketsOnDevice
// rejects a worker index at or beyond the harness's registered worker count
// instead of indexing the C-side worker array out of bounds.
func TestHandlePacketsOnDevice_InvalidWorker(t *testing.T) {
	cfg := Config{
		CPMemory:    uint64(datasize.MB * 32),
		DPMemory:    uint64(datasize.MB * 4),
		WorkerCount: 1,
	}

	h, err := NewHarness(cfg)
	require.NoError(t, err)
	t.Cleanup(h.Free)

	result, err := h.HandlePacketsOnDevice(3, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds topology worker count")
	require.Nil(t, result)
}

// TestHandlePacketsOnDevice_EgressDeviceID verifies that OutputPacket.DeviceID
// carries the egress device the round resolved rather than the ingress one,
// and that HandlePacketsOnDevice actually chooses that ingress device.
//
// Two crossed forward rules — port0 ingress redirected out through port1 and
// port1 ingress out through port0 — let the same packet bytes leave through
// opposite devices depending only on the injected ingress device. A harness
// that discarded tx_device_id, or an entrypoint that still pinned every packet
// to device 0, could not produce both answers.
func TestHandlePacketsOnDevice_EgressDeviceID(t *testing.T) {
	const (
		port0 uint16 = 0
		port1 uint16 = 1
	)

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
	agent, err := shm.AgentAttach("egress-device-test", 0, datasize.MB*16)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	backend := forward.NewBackend(agent)
	moduleHandle, err := backend.UpdateModule("cross", []cforward.ForwardRule{
		{
			Target:  "port1",
			Mode:    cforward.ModeOut,
			Counter: "port0_to_port1",
			Devices: filter.Devices{{Name: "port0"}},
		},
		{
			Target:  "port0",
			Mode:    cforward.ModeOut,
			Counter: "port1_to_port0",
			Devices: filter.Devices{{Name: "port1"}},
		},
	})
	require.NoError(t, err)
	t.Cleanup(moduleHandle.Free)

	require.NoError(t, agent.UpdateFunction(ffi.FunctionConfig{
		Name: "cross",
		Chains: []ffi.FunctionChainConfig{{
			Weight: 1,
			Chain: ffi.ChainConfig{
				Name:    "cross_chain",
				Modules: []ffi.ChainModuleConfig{{Type: "forward", Name: "cross"}},
			},
		}},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{
		Name:      "cross",
		Functions: []string{"cross"},
	}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "sink"}))

	// Both devices carry the same input pipeline, so which rule matches is
	// decided purely by the injected ingress device, and both carry an
	// output pipeline so either egress can complete.
	require.NoError(t, agent.UpdatePlainDevices([]ffi.DeviceConfig{
		{
			Name:   "port0",
			Input:  []ffi.DevicePipelineConfig{{Name: "cross", Weight: 1}},
			Output: []ffi.DevicePipelineConfig{{Name: "sink", Weight: 1}},
		},
		{
			Name:   "port1",
			Input:  []ffi.DevicePipelineConfig{{Name: "cross", Weight: 1}},
			Output: []ffi.DevicePipelineConfig{{Name: "sink", Weight: 1}},
		},
	}))

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

	fromPort0, err := h.HandlePacketsOnDevice(0, port0, pkt)
	require.NoError(t, err)
	require.Len(t, fromPort0.Output, 1)
	assert.Empty(t, fromPort0.Drop)
	assert.Equal(t, port1, fromPort0.Output[0].DeviceID, "ingress port0 must egress on port1")
	assert.Len(t, fromPort0.OutputOn(port1), 1)
	assert.Empty(t, fromPort0.OutputOn(port0))

	fromPort1, err := h.HandlePacketsOnDevice(1, port1, pkt)
	require.NoError(t, err)
	require.Len(t, fromPort1.Output, 1)
	assert.Empty(t, fromPort1.Drop)
	assert.Equal(t, port0, fromPort1.Output[0].DeviceID, "ingress port1 must egress on port0")
	assert.Len(t, fromPort1.OutputOn(port0), 1)
	assert.Empty(t, fromPort1.OutputOn(port1))

	// HandlePackets pins the ingress to device 0, so it must agree with the
	// explicit port0 injection above.
	pinned, err := h.HandlePackets(pkt)
	require.NoError(t, err)
	require.Len(t, pinned.Output, 1)
	assert.Equal(t, port1, pinned.Output[0].DeviceID)
}
