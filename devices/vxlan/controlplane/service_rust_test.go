//go:build yanet_dataplane_rust

package vxlan_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	"github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
	"github.com/yanet-platform/yanet2/devices/vxlan/controlplane/vxlanpb/v1"
)

// Test_DeviceVxlanService_UpdateDevice_DrainsUnusedDevices verifies that
// repeated UpdateDevice calls do not leak shared-memory arena space.
//
// Each update after the first supersedes the previous generation's device;
// freeing that handle destroys it once it is dangling, so the arena must
// settle after the first supersede instead of decreasing indefinitely.
func Test_DeviceVxlanService_UpdateDevice_DrainsUnusedDevices(t *testing.T) {
	service, _, shm := newVxlanService(t, "vxlan")

	request := &vxlanpb.UpdateDeviceVxlanRequest{
		Name:   "d0",
		Device: &commonpb.Device{},
		Tunnel: validTunnel(),
	}

	const updateCount = 32

	var previousFreeBytes uint64
	for idx := range updateCount {
		_, err := service.UpdateDevice(t.Context(), request)
		require.NoError(t, err)

		freeBytes := freeBytesForAgent(t, shm, "vxlan")
		// From the third update on, every update destroys its superseded
		// predecessor, so free bytes must hold steady.
		//
		// The second update is only the baseline: it supersedes the first
		// before any free ran against it.
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

// Test_DeviceVxlanService_UpdateDevice_UnknownPipelineKeepsArena verifies
// that an update naming a pipeline the configuration lacks is refused with
// FailedPrecondition and frees the device it built.
func Test_DeviceVxlanService_UpdateDevice_UnknownPipelineKeepsArena(t *testing.T) {
	service, _, shm := newVxlanService(t, "vxlan")
	freeBefore := freeBytesForAgent(t, shm, "vxlan")

	resp, err := service.UpdateDevice(t.Context(), &vxlanpb.UpdateDeviceVxlanRequest{
		Name: "d0",
		Device: &commonpb.Device{
			Input: []*commonpb.DevicePipeline{{Name: "missing", Weight: 1}},
		},
		Tunnel: validTunnel(),
	})

	require.Nil(t, resp)
	require.Equal(t, codes.FailedPrecondition, status.Code(err), "error: %v", err)
	require.Equal(t, freeBefore, freeBytesForAgent(t, shm, "vxlan"))

	_, err = service.ShowDevice(t.Context(), &vxlanpb.ShowDeviceVxlanRequest{Name: "d0"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_DeviceVxlanService_ShowDevice_ReportsTunnelAndBindings verifies that
// ShowDevice follows the latest published generation.
//
// The device is republished with a different tunnel and binding set between
// two reads, so the second read must report the replacement.
func Test_DeviceVxlanService_ShowDevice_ReportsTunnelAndBindings(t *testing.T) {
	service, agent, _ := newVxlanService(t, "vxlan")

	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "p-in"}))
	require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: "p-out"}))

	first := validTunnel()
	_, err := service.UpdateDevice(t.Context(), &vxlanpb.UpdateDeviceVxlanRequest{
		Name: "d0",
		Device: &commonpb.Device{
			Input:  []*commonpb.DevicePipeline{{Name: "p-in", Weight: 3}},
			Output: []*commonpb.DevicePipeline{{Name: "p-out", Weight: 1}},
		},
		Tunnel: first,
	})
	require.NoError(t, err)

	resp, err := service.ShowDevice(t.Context(), &vxlanpb.ShowDeviceVxlanRequest{Name: "d0"})
	require.NoError(t, err)
	require.True(t, proto.Equal(first, resp.GetTunnel()), "tunnel: %v", resp.GetTunnel())
	require.True(t, proto.Equal(&commonpb.Device{
		Input:  []*commonpb.DevicePipeline{{Name: "p-in", Weight: 3}},
		Output: []*commonpb.DevicePipeline{{Name: "p-out", Weight: 1}},
	}, resp.GetDevice()))

	second := &vxlanpb.VxlanTunnel{
		LocalIp:   commonpb.NewIPv4Address([4]byte{203, 0, 113, 9}),
		RemoteIp:  commonpb.NewIPv4Address([4]byte{10, 1, 2, 3}),
		LocalMac:  commonpb.NewMACAddressEUI48([6]byte{0x3a, 0xac, 0x26, 0x9b, 0x5b, 0xf9}),
		RemoteMac: commonpb.NewMACAddressEUI48([6]byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}),
		Vni:       16777215,
	}
	_, err = service.UpdateDevice(t.Context(), &vxlanpb.UpdateDeviceVxlanRequest{
		Name: "d0",
		Device: &commonpb.Device{
			Output: []*commonpb.DevicePipeline{{Name: "p-out", Weight: 5}},
		},
		Tunnel: second,
	})
	require.NoError(t, err)

	resp, err = service.ShowDevice(t.Context(), &vxlanpb.ShowDeviceVxlanRequest{Name: "d0"})
	require.NoError(t, err)
	require.True(t, proto.Equal(second, resp.GetTunnel()), "tunnel: %v", resp.GetTunnel())
	require.True(t, proto.Equal(&commonpb.Device{
		Output: []*commonpb.DevicePipeline{{Name: "p-out", Weight: 5}},
	}, resp.GetDevice()))
}

// Test_DeviceVxlanService_ShowDevice_IgnoresOtherDeviceTypes verifies that
// ShowDevice reports NotFound for a name registered by a device of a
// different type.
func Test_DeviceVxlanService_ShowDevice_IgnoresOtherDeviceTypes(t *testing.T) {
	service, agent, _ := newVxlanService(t, "plain", "vxlan")

	_, err := plain.NewDevicePlainService(agent).UpdateDevice(t.Context(), &plainpb.UpdateDevicePlainRequest{
		Name:   "p0",
		Device: &commonpb.Device{},
	})
	require.NoError(t, err)

	resp, err := service.ShowDevice(t.Context(), &vxlanpb.ShowDeviceVxlanRequest{Name: "p0"})

	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_DeviceVxlanService_Concurrent_UpdateAndShow verifies that updates of
// distinct devices and reads of their tunnels may overlap, and that every
// device ends with the last tunnel its writer published.
func Test_DeviceVxlanService_Concurrent_UpdateAndShow(t *testing.T) {
	service, _, _ := newVxlanService(t, "vxlan")

	const (
		writerCount = 4
		updateCount = 16
	)

	var group errgroup.Group
	for writer := range writerCount {
		group.Go(func() error {
			for update := range updateCount {
				tunnel := validTunnel()
				tunnel.Vni = uint32(writer*updateCount + update)
				_, err := service.UpdateDevice(t.Context(), &vxlanpb.UpdateDeviceVxlanRequest{
					Name:   fmt.Sprintf("d%d", writer),
					Device: &commonpb.Device{},
					Tunnel: tunnel,
				})
				if err != nil {
					return err
				}
			}
			return nil
		})
	}
	group.Go(func() error {
		for range writerCount * updateCount {
			for writer := range writerCount {
				_, err := service.ShowDevice(t.Context(), &vxlanpb.ShowDeviceVxlanRequest{
					Name: fmt.Sprintf("d%d", writer),
				})
				if err != nil && status.Code(err) != codes.NotFound {
					return err
				}
			}
		}
		return nil
	})
	require.NoError(t, group.Wait())

	for writer := range writerCount {
		resp, err := service.ShowDevice(t.Context(), &vxlanpb.ShowDeviceVxlanRequest{
			Name: fmt.Sprintf("d%d", writer),
		})
		require.NoError(t, err)
		require.Equal(t, uint32(writer*updateCount+updateCount-1), resp.GetTunnel().GetVni())
	}
}
