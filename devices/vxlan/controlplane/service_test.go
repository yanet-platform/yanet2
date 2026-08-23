package vxlan_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	vxlan "github.com/yanet-platform/yanet2/devices/vxlan/controlplane"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

// newTunnelRequest builds a valid UpdateDevice request for the given name.
//
// The tests run without any registered pipelines, so the device carries no
// pipeline bindings.
func newTunnelRequest(name string) *vxlanpb.UpdateDeviceVxlanRequest {
	return &vxlanpb.UpdateDeviceVxlanRequest{
		Name:    name,
		Device:  &commonpb.Device{},
		Vni:     4242,
		DstPort: 4789,
		SrcMac:  "02:00:00:00:00:01",
		DstMac:  "02:00:00:00:00:02",
		SrcIp:   "10.0.0.1",
		DstIp:   "10.0.0.2",
	}
}

// attachVxlanAgent builds a harness-backed agent for service-level tests.
func attachVxlanAgent(t *testing.T) (*ffi.SharedMemory, *ffi.Agent) {
	t.Helper()

	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(datasize.MB * 32),
		DPMemory:      uint64(datasize.MB * 4),
		WorkerCount:   1,
		DevicesToLoad: []string{"plain", "vxlan"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)

	shm := harness.SharedMemory()
	agent, err := shm.AgentAttach("vxlan", 0, datasize.MB*2)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	return shm, agent
}

// TestUpdateDevice_DrainsSupersededDevices verifies that repeated
// UpdateDevice calls do not leak shared-memory arena space.
//
// Each update after the first retires the previous generation's device, and
// the service releases it through the reference tracker. A retired device
// whose generation reference is already dropped parks on the agent, and every
// later construction reclaims the previous parked entry before parking a new
// one, so free bytes must settle instead of decreasing indefinitely.
func TestUpdateDevice_DrainsSupersededDevices(t *testing.T) {
	shm, agent := attachVxlanAgent(t)

	service := vxlan.NewDeviceVxlanService(agent)
	request := newTunnelRequest("d0")

	const updateCount = 32

	var previousFreeBytes uint64
	for idx := range updateCount {
		_, err := service.UpdateDevice(t.Context(), request)
		require.NoError(t, err)

		freeBytes := freeBytesForAgent(t, shm, "vxlan")
		// The first supersede parks the old device without destroying
		// it, so its space stays held once; every later construction
		// reclaims the previous parked entry before parking a new one.
		if idx > 1 {
			require.Equalf(
				t,
				previousFreeBytes,
				freeBytes,
				"free bytes changed on update %d: %d -> %d",
				idx,
				previousFreeBytes,
				freeBytes,
			)
		}
		previousFreeBytes = freeBytes
	}
}

// TestUpdateDevice_InvalidArguments verifies that malformed tunnel fields are
// rejected before any shared-memory call.
//
// The service holds a nil agent, so a request that reached the FFI layer
// would panic instead of failing a validation assertion.
func TestUpdateDevice_InvalidArguments(t *testing.T) {
	service := vxlan.NewDeviceVxlanService(nil)

	cases := []struct {
		name   string
		modify func(*vxlanpb.UpdateDeviceVxlanRequest)
	}{
		{
			name:   "missing_name",
			modify: func(request *vxlanpb.UpdateDeviceVxlanRequest) { request.Name = "" },
		},
		{
			name: "name_beyond_device_name_limit",
			modify: func(request *vxlanpb.UpdateDeviceVxlanRequest) {
				request.Name = strings.Repeat("d", 80)
			},
		},
		{
			name: "name_with_embedded_nul",
			modify: func(request *vxlanpb.UpdateDeviceVxlanRequest) {
				request.Name = "tun0\x00suffix"
			},
		},
		{
			name:   "vni_over_24_bits",
			modify: func(request *vxlanpb.UpdateDeviceVxlanRequest) { request.Vni = 1 << 24 },
		},
		{
			name:   "zero_dst_port",
			modify: func(request *vxlanpb.UpdateDeviceVxlanRequest) { request.DstPort = 0 },
		},
		{
			name:   "dst_port_over_16_bits",
			modify: func(request *vxlanpb.UpdateDeviceVxlanRequest) { request.DstPort = 65536 },
		},
		{
			name:   "unparseable_src_mac",
			modify: func(request *vxlanpb.UpdateDeviceVxlanRequest) { request.SrcMac = "not-a-mac" },
		},
		{
			name: "eui64_src_mac",
			modify: func(request *vxlanpb.UpdateDeviceVxlanRequest) {
				request.SrcMac = "02:00:00:00:00:00:00:01"
			},
		},
		{
			name:   "unparseable_dst_mac",
			modify: func(request *vxlanpb.UpdateDeviceVxlanRequest) { request.DstMac = "02:00:00:zz:00:02" },
		},
		{
			name:   "ipv6_src_ip",
			modify: func(request *vxlanpb.UpdateDeviceVxlanRequest) { request.SrcIp = "2001:db8::1" },
		},
		{
			name:   "unparseable_src_ip",
			modify: func(request *vxlanpb.UpdateDeviceVxlanRequest) { request.SrcIp = "10.0.0" },
		},
		{
			name:   "ipv6_dst_ip",
			modify: func(request *vxlanpb.UpdateDeviceVxlanRequest) { request.DstIp = "::1" },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := newTunnelRequest("d0")
			tc.modify(request)

			response, err := service.UpdateDevice(t.Context(), request)
			require.Nil(t, response)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

// TestUpdateDevice_NameLengthBoundary verifies that the longest name the
// C device configuration can carry without truncation is accepted and
// served back, while one byte more is rejected before any shared-memory
// call.
func TestUpdateDevice_NameLengthBoundary(t *testing.T) {
	_, agent := attachVxlanAgent(t)

	service := vxlan.NewDeviceVxlanService(agent)

	longest := strings.Repeat("d", vxlan.DeviceNameMaxLength)
	request := newTunnelRequest(longest)
	_, err := service.UpdateDevice(t.Context(), request)
	require.NoError(t, err)

	response, err := service.GetDevice(
		t.Context(),
		&vxlanpb.GetDeviceVxlanRequest{Name: longest},
	)
	require.NoError(t, err)
	require.Equal(t, longest, response.GetName())

	overlong := strings.Repeat("d", vxlan.DeviceNameMaxLength+1)
	updateResponse, err := service.UpdateDevice(
		t.Context(),
		newTunnelRequest(overlong),
	)
	require.Nil(t, updateResponse)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestUpdateDevice_ConcurrentUpdates verifies that concurrent callers cannot
// publish interleaved generations or free a device still referenced by a
// live generation.
func TestUpdateDevice_ConcurrentUpdates(t *testing.T) {
	_, agent := attachVxlanAgent(t)

	service := vxlan.NewDeviceVxlanService(agent)

	const goroutineCount = 4
	const updateCount = 8

	var group errgroup.Group
	for idx := range goroutineCount {
		group.Go(func() error {
			name := "d1"
			if idx%2 == 0 {
				name = "d0"
			}
			for round := range updateCount {
				if _, err := service.UpdateDevice(t.Context(), newTunnelRequest(name)); err != nil {
					return fmt.Errorf("update %s round %d: %w", name, round, err)
				}
			}
			return nil
		})
	}
	require.NoError(t, group.Wait())
}

// freeBytesForAgent returns the free byte count reported for the named
// agent's first instance.
func freeBytesForAgent(t *testing.T, shm *ffi.SharedMemory, name string) uint64 {
	t.Helper()

	for _, agentInfo := range shm.DPConfig(0).Agents() {
		if agentInfo.Name == name {
			require.NotEmpty(t, agentInfo.Instances)
			return agentInfo.Instances[0].FreeBytes
		}
	}

	t.Fatalf("agent %q not found in dataplane config", name)
	return 0
}

// TestGetDevice_InvalidName verifies that a GetDevice without a usable name
// is rejected before the cache is consulted.
func TestGetDevice_InvalidName(t *testing.T) {
	service := vxlan.NewDeviceVxlanService(nil)

	cases := []struct {
		name       string
		deviceName string
		code       codes.Code
	}{
		{name: "empty_name", deviceName: "", code: codes.InvalidArgument},
		{name: "unknown_name", deviceName: "missing", code: codes.NotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response, err := service.GetDevice(
				t.Context(),
				&vxlanpb.GetDeviceVxlanRequest{Name: tc.deviceName},
			)
			require.Nil(t, response)
			require.Equal(t, tc.code, status.Code(err))
		})
	}
}

// TestGetDevice_RoundTrip verifies that GetDevice echoes exactly the
// settings of the accepted UpdateDevice call.
//
// The tests run without any registered pipelines, so a published device
// cannot carry pipeline bindings; the mutation step below still verifies the
// cached Device is a snapshot, not an alias of the request.
func TestGetDevice_RoundTrip(t *testing.T) {
	_, agent := attachVxlanAgent(t)

	service := vxlan.NewDeviceVxlanService(agent)

	request := newTunnelRequest("d0")
	_, err := service.UpdateDevice(t.Context(), request)
	require.NoError(t, err)

	// Mutating the request after the update must not leak into the cache.
	request.SrcMac = "02:00:00:00:00:03"
	request.Device = &commonpb.Device{
		Input: []*commonpb.DevicePipeline{{Name: "mutated", Weight: 1}},
	}

	response, err := service.GetDevice(t.Context(), &vxlanpb.GetDeviceVxlanRequest{Name: "d0"})
	require.NoError(t, err)
	require.Equal(t, "d0", response.GetName())
	require.Equal(t, uint32(4242), response.GetVni())
	require.Equal(t, uint32(4789), response.GetDstPort())
	require.Equal(t, "02:00:00:00:00:01", response.GetSrcMac())
	require.Equal(t, "02:00:00:00:00:02", response.GetDstMac())
	require.Equal(t, "10.0.0.1", response.GetSrcIp())
	require.Equal(t, "10.0.0.2", response.GetDstIp())
	require.Empty(t, response.GetDevice().GetInput())
	require.Empty(t, response.GetDevice().GetOutput())
}

// TestGetDevice_ReflectsLatestUpdate verifies that a second UpdateDevice
// fully replaces what GetDevice serves.
func TestGetDevice_ReflectsLatestUpdate(t *testing.T) {
	_, agent := attachVxlanAgent(t)

	service := vxlan.NewDeviceVxlanService(agent)

	first := newTunnelRequest("d0")
	first.Vni = 100
	first.DstPort = 1000
	first.SrcMac = "02:00:00:00:00:11"
	first.DstMac = "02:00:00:00:00:12"
	first.SrcIp = "10.0.1.1"
	first.DstIp = "10.0.1.2"

	second := newTunnelRequest("d0")
	second.Vni = 200
	second.DstPort = 2000
	second.SrcMac = "02:00:00:00:00:21"
	second.DstMac = "02:00:00:00:00:22"
	second.SrcIp = "10.0.2.1"
	second.DstIp = "10.0.2.2"

	_, err := service.UpdateDevice(t.Context(), first)
	require.NoError(t, err)

	response, err := service.GetDevice(t.Context(), &vxlanpb.GetDeviceVxlanRequest{Name: "d0"})
	require.NoError(t, err)
	require.Equal(t, uint32(100), response.GetVni())
	require.Equal(t, uint32(1000), response.GetDstPort())
	require.Equal(t, "02:00:00:00:00:11", response.GetSrcMac())
	require.Equal(t, "10.0.1.2", response.GetDstIp())

	_, err = service.UpdateDevice(t.Context(), second)
	require.NoError(t, err)

	response, err = service.GetDevice(t.Context(), &vxlanpb.GetDeviceVxlanRequest{Name: "d0"})
	require.NoError(t, err)
	require.Equal(t, uint32(200), response.GetVni())
	require.Equal(t, uint32(2000), response.GetDstPort())
	require.Equal(t, "02:00:00:00:00:21", response.GetSrcMac())
	require.Equal(t, "10.0.2.2", response.GetDstIp())
}

// TestGetDevice_ConcurrentWithUpdates verifies that a GetDevice racing an
// UpdateDevice on the same name always serves one published generation
// whole, never a torn mix of two.
func TestGetDevice_ConcurrentWithUpdates(t *testing.T) {
	_, agent := attachVxlanAgent(t)

	service := vxlan.NewDeviceVxlanService(agent)

	first := newTunnelRequest("d0")
	first.Vni = 100
	first.SrcMac = "02:00:00:00:00:11"
	first.DstMac = "02:00:00:00:00:12"

	second := newTunnelRequest("d0")
	second.Vni = 200
	second.SrcMac = "02:00:00:00:00:21"
	second.DstMac = "02:00:00:00:00:22"

	_, err := service.UpdateDevice(t.Context(), first)
	require.NoError(t, err)

	var group errgroup.Group
	group.Go(func() error {
		for round := range 16 {
			request := first
			if round%2 == 1 {
				request = second
			}
			if _, err := service.UpdateDevice(t.Context(), request); err != nil {
				return fmt.Errorf("update round %d: %w", round, err)
			}
		}
		return nil
	})
	group.Go(func() error {
		for range 16 {
			response, err := service.GetDevice(
				t.Context(),
				&vxlanpb.GetDeviceVxlanRequest{Name: "d0"},
			)
			if err != nil {
				return fmt.Errorf("get: %w", err)
			}
			switch response.GetVni() {
			case 100:
				if response.GetSrcMac() != first.SrcMac || response.GetDstMac() != first.DstMac {
					return fmt.Errorf("torn generation: vni 100 with macs %q/%q",
						response.GetSrcMac(), response.GetDstMac())
				}
			case 200:
				if response.GetSrcMac() != second.SrcMac || response.GetDstMac() != second.DstMac {
					return fmt.Errorf("torn generation: vni 200 with macs %q/%q",
						response.GetSrcMac(), response.GetDstMac())
				}
			default:
				return fmt.Errorf("unexpected vni %d", response.GetVni())
			}
		}
		return nil
	})
	require.NoError(t, group.Wait())
}
