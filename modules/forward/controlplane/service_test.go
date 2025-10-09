package forward

import (
	"context"
	"net/netip"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	commonpb "github.com/yanet-platform/yanet2/common/proto"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

// Override ForwardService updater to avoid FFI calls in tests
func testUpdateModuleConfig(m *ForwardService, name string, instance uint32) error {
	m.log.Debugw("Skip FFI calls in tests", zap.String("module", name), zap.Uint32("instance", instance))
	return nil
}

func newTestService(agents []*ffi.Agent, deviceCount uint16) *ForwardService {
	logger, _ := zap.NewDevelopment()
	svc := NewForwardService(agents, logger.Sugar(), deviceCount)
	svc.updater = testUpdateModuleConfig
	return svc
}

func TestEnableL2Forward(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 1), 8)

	var srcId string = "3"
	var dstId string = "4"
	// Enable L2 forward from device 3 to 4
	req := &forwardpb.L2ForwardEnableRequest{
		Target:   &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 0},
		SrcDevId: string(srcId),
		DstDevId: string(dstId),
	}

	_, err := svc.EnableL2Forward(context.Background(), req)
	require.NoError(t, err)

	// Check that dstDevId is set
	key := instanceKey{name: "test-module", dataplaneInstance: 0}
	config := svc.configs[key]
	require.NotNil(t, config)
	require.Equal(t, dstId, string(config[DeviceID(srcId)].DstDevId))

	// Repeated assignment should just overwrite
	_, err = svc.EnableL2Forward(context.Background(), req)

	require.NoError(t, err)

	// All other devices should forward to itself.
	for idx, dev := range config {
		if DeviceID(idx) == DeviceID(srcId) {
			continue
		}
		require.Equal(t, dev.DstDevId, DeviceID(idx))
	}
}

func TestAddL3Forward(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 1), 8)

	// Add L3 forward
	addForwardReq := &forwardpb.AddL3ForwardRequest{
		Target:   &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 0},
		SrcDevId: "1",
		Forward: &forwardpb.L3ForwardEntry{
			Network:  "192.168.1.0/24",
			DstDevId: "2",
		},
	}
	_, err := svc.AddL3Forward(context.Background(), addForwardReq)
	require.NoError(t, err)

	// Check that the rule was added
	key := instanceKey{name: "test-module", dataplaneInstance: 0}
	config := svc.configs[key]
	device := config[DeviceID("1")]
	prefix := netip.MustParsePrefix("192.168.1.0/24")
	targetID, exists := device.Forwards[prefix]
	require.True(t, exists, "Forward rule should exist")
	require.Equal(t, DeviceID("2"), targetID)

	// Error: invalid network
	invalidReq := &forwardpb.AddL3ForwardRequest{
		Target:   &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 0},
		SrcDevId: "1",
		Forward: &forwardpb.L3ForwardEntry{
			Network:  "invalid",
			DstDevId: "2",
		},
	}
	_, err = svc.AddL3Forward(context.Background(), invalidReq)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse network")
}

func TestRemoveL3Forward(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 1), 8)

	// Add L3 forward
	_, err := svc.AddL3Forward(context.Background(), &forwardpb.AddL3ForwardRequest{
		Target:   &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 0},
		SrcDevId: "1",
		Forward: &forwardpb.L3ForwardEntry{
			Network:  "192.168.1.0/24",
			DstDevId: "2",
		},
	})
	require.NoError(t, err)

	// Remove L3 forward
	removeReq := &forwardpb.RemoveL3ForwardRequest{
		Target:   &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 0},
		SrcDevId: "1",
		Network:  "192.168.1.0/24",
	}
	_, err = svc.RemoveL3Forward(context.Background(), removeReq)
	require.NoError(t, err)

	// Check that the rule was removed
	key := instanceKey{name: "test-module", dataplaneInstance: 0}
	config := svc.configs[key]
	device := config["1"]
	prefix := netip.MustParsePrefix("192.168.1.0/24")
	_, exists := device.Forwards[prefix]
	require.False(t, exists, "Forward rule should be removed")
}

// getDevice is a helper function that finds a device configuration by source device ID
func getDevice(t *testing.T, SrcDevId string, cfg *forwardpb.Config) *forwardpb.ForwardDeviceConfig {
	devIdx := slices.IndexFunc(cfg.Devices, func(d *forwardpb.ForwardDeviceConfig) bool {
		return d.SrcDevId == SrcDevId
	})
	require.NotEqual(t, -1, devIdx)
	return cfg.Devices[devIdx]

}

func TestShowConfig(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 2), 8) // Create service with 2 agents for testing multiple dataplane instances

	// Setup: Add devices and forward rules
	// Set up default config for instance 0
	_, err := svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
		Target:   &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 0},
		SrcDevId: "1",
		DstDevId: "7",
	})
	require.NoError(t, err)

	// Set up minimal config for instance 1 to ensure it's returned by ShowConfig
	_, err = svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
		Target:   &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 1},
		SrcDevId: "1",
		DstDevId: "1",
	})
	require.NoError(t, err)

	key := instanceKey{name: "test-module", dataplaneInstance: 0}

	// Add forward rules directly to the config
	forward1 := netip.MustParsePrefix("192.168.1.0/24")
	forward2 := netip.MustParsePrefix("10.0.0.0/8")
	forward3 := netip.MustParsePrefix("172.16.0.0/12")
	forward4 := netip.MustParsePrefix("2001:db8::/32")
	svc.configs[key]["1"] = ForwardDeviceConfig{
		Forwards: map[netip.Prefix]DeviceID{
			forward1: "2",
			forward2: "2",
		},
		DstDevId: "7",
	}

	svc.configs[key]["3"] = ForwardDeviceConfig{
		Forwards: map[netip.Prefix]DeviceID{
			forward3: "4",
			forward4: "5",
		},
		DstDevId: "3",
	}

	// Set up configs for non-existent module test
	_, err = svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
		Target:   &commonpb.TargetModule{ConfigName: "non-existent-module", DataplaneInstance: 0},
		SrcDevId: "1",
		DstDevId: "1",
	})
	require.NoError(t, err)
	_, err = svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
		Target:   &commonpb.TargetModule{ConfigName: "non-existent-module", DataplaneInstance: 1},
		SrcDevId: "1",
		DstDevId: "1",
	})
	require.NoError(t, err)

	// Test scenario 1: Show configuration for instance 0
	t.Run("ShowInstance0", func(t *testing.T) {
		resp, err := svc.ShowConfig(context.Background(), &forwardpb.ShowConfigRequest{
			Target: &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 0},
		})

		require.NoError(t, err, "ShowConfig should not return an error")
		require.NotNil(t, resp, "Response should not be nil")
		require.Equal(t, uint32(0), resp.Instance, "Should return config for instance 0")
		require.NotNil(t, resp.Config, "Config should not be nil")

		// Check device 1 has 2 forward rules
		var srcDevId string = "1"
		dev1 := getDevice(t, srcDevId, resp.Config)
		require.Equal(t, "7", dev1.DstDevId, "Device 1 should forward to device 7")
		require.Len(t, dev1.Forwards, 2, "Device 1 should have 2 forwards")

		// Check device 3 has 2 forward rules
		srcDevId = "3"
		dev3 := getDevice(t, srcDevId, resp.Config)
		require.Equal(t, srcDevId, dev3.DstDevId, "Device 3 should forward to itself")
		require.Len(t, dev3.Forwards, 2, "Device 3 should have two forwards")

		networksFound := 0
		for _, fwd := range dev3.Forwards {
			if fwd.Network == forward3.String() {
				networksFound++
				require.Equal(t, "4", fwd.DstDevId, "Target device should match")
			} else if fwd.Network == forward4.String() {
				networksFound++
				require.Equal(t, "5", fwd.DstDevId, "Target device should match")
			}
		}
		require.Equal(t, 2, networksFound, "Device 3 should have networks %s and %s", forward3, forward4)
	})

	// Test scenario 2: Show configuration for specific instance
	t.Run("ShowSpecificInstance", func(t *testing.T) {
		resp, err := svc.ShowConfig(context.Background(), &forwardpb.ShowConfigRequest{
			Target: &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 1},
		})

		require.NoError(t, err, "ShowConfig should not return an error")
		require.NotNil(t, resp, "Response should not be nil")
		require.Equal(t, uint32(1), resp.Instance, "Response should be for instance 1")
	})

	// Test scenario 3: Show for non-existent config
	t.Run("ShowNonExistentConfig", func(t *testing.T) {
		resp, err := svc.ShowConfig(context.Background(), &forwardpb.ShowConfigRequest{
			Target: &commonpb.TargetModule{ConfigName: "non-existent-module", DataplaneInstance: 1},
		})

		require.NoError(t, err, "ShowConfig should not return an error for non-existent module")
		require.NotNil(t, resp, "Response should not be nil")
		require.Equal(t, uint32(1), resp.Instance, "Response should be for instance 1")
		// Config may be nil for non-existent module
	})
}

func TestInputValidation(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 1), 8)

	// Test AddForward validation
	t.Run("AddL3Forward", func(t *testing.T) {
		// Nil target
		_, err := svc.AddL3Forward(context.Background(), &forwardpb.AddL3ForwardRequest{
			Target: nil,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target module cannot be nil")

		// Test missing target in RemoveL3Forward
		_, err = svc.RemoveL3Forward(context.Background(), &forwardpb.RemoveL3ForwardRequest{
			SrcDevId: "1",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target module cannot be nil")

		// Test missing target in AddForward
		_, err = svc.AddL3Forward(context.Background(), &forwardpb.AddL3ForwardRequest{
			SrcDevId: "1",
			Forward: &forwardpb.L3ForwardEntry{
				Network:  "192.168.1.0/24",
				DstDevId: "2",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target module cannot be nil")

		// Test missing target in RemoveForward
		_, err = svc.RemoveL3Forward(context.Background(), &forwardpb.RemoveL3ForwardRequest{
			SrcDevId: "1",
			Network:  "192.168.1.0/24",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target module cannot be nil")

		// Test missing target in ShowConfig
		_, err = svc.ShowConfig(context.Background(), &forwardpb.ShowConfigRequest{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target module cannot be nil")
	})

	t.Run("missing module name", func(t *testing.T) {
		// Test empty module name in EnableL2Forward
		_, err := svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
			Target:   &commonpb.TargetModule{DataplaneInstance: 0},
			SrcDevId: "1",
			DstDevId: "2",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")

		// Test empty module name in RemoveL3Forward
		_, err = svc.RemoveL3Forward(context.Background(), &forwardpb.RemoveL3ForwardRequest{
			Target:   &commonpb.TargetModule{DataplaneInstance: 0},
			SrcDevId: "1",
			Network:  "192.168.1.0/24",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")

		// Test empty module name in AddForward
		_, err = svc.AddL3Forward(context.Background(), &forwardpb.AddL3ForwardRequest{
			Target:   &commonpb.TargetModule{DataplaneInstance: 0},
			SrcDevId: "1",
			Forward: &forwardpb.L3ForwardEntry{
				Network:  "192.168.1.0/24",
				DstDevId: "2",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")

		// Test empty module name in RemoveForward
		_, err = svc.RemoveL3Forward(context.Background(), &forwardpb.RemoveL3ForwardRequest{
			Target:   &commonpb.TargetModule{DataplaneInstance: 0},
			SrcDevId: "1",
			Network:  "192.168.1.0/24",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")

		// Test empty module name in ShowConfig
		_, err = svc.ShowConfig(context.Background(), &forwardpb.ShowConfigRequest{
			Target: &commonpb.TargetModule{DataplaneInstance: 0},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")
	})

	t.Run("invalid network in forward", func(t *testing.T) {
		// Add a device to test with
		_, err := svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
			Target:   &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 0},
			SrcDevId: "1",
			DstDevId: "2",
		})
		require.NoError(t, err)

		// Add a target device
		_, err = svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
			Target:   &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 0},
			SrcDevId: "2",
			DstDevId: "2",
		})
		require.NoError(t, err)

		// Test invalid network format in AddForward
		_, err = svc.AddL3Forward(context.Background(), &forwardpb.AddL3ForwardRequest{
			Target:   &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 0},
			SrcDevId: "1",
			Forward: &forwardpb.L3ForwardEntry{
				Network:  "invalid-network",
				DstDevId: "2",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse network")

		// Test invalid network format in RemoveForward
		_, err = svc.RemoveL3Forward(context.Background(), &forwardpb.RemoveL3ForwardRequest{
			Target:   &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 0},
			SrcDevId: "1",
			Network:  "invalid-network",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse network")
	})

	t.Run("missing forward entry", func(t *testing.T) {
		// Test nil forward entry in AddForward
		_, err := svc.AddL3Forward(context.Background(), &forwardpb.AddL3ForwardRequest{
			Target:   &commonpb.TargetModule{ConfigName: "test-module", DataplaneInstance: 0},
			SrcDevId: "1",
			Forward:  nil,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "forward entry cannot be nil")
	})
}
