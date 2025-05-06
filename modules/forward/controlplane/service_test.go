package forward

import (
	"context"
	"net/netip"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/numa"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

// Override ForwardSerivce updater to avoid FFI calls in tests
func testUpdateModuleConfigs(m *ForwardService, name string, numaMap numa.NUMAMap) error {
	m.log.Debugw("Skip FFI calls in tests", zap.String("module", name), zap.Uint32s("numa", slices.Collect(numaMap.Iter())))
	return nil
}

func newTestService(agents []*ffi.Agent, deviceCount uint16) *ForwardService {
	logger, _ := zap.NewDevelopment()
	svc := NewForwardService(agents, logger.Sugar(), deviceCount)
	svc.updater = testUpdateModuleConfigs
	return svc
}

func TestEnableL2Forward(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 1), 8)

	var srcId uint16 = 3
	var dstId uint16 = 4
	// Enable L2 forward from device 3 to 4
	req := &forwardpb.L2ForwardEnableRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		SrcDevId: uint32(srcId),
		DstDevId: uint32(dstId),
	}

	_, err := svc.EnableL2Forward(context.Background(), req)
	require.NoError(t, err)

	// Check that dstDevId is set
	key := instanceKey{name: "test-module", numaIdx: 0}
	config := svc.configs[key]
	require.NotNil(t, config)
	require.Equal(t, dstId, uint16(config[srcId].DstDevId))

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
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		SrcDevId: 1,
		Forward: &forwardpb.L3ForwardEntry{
			Network:  "192.168.1.0/24",
			DstDevId: 2,
		},
	}
	_, err := svc.AddL3Forward(context.Background(), addForwardReq)
	require.NoError(t, err)

	// Check that the rule was added
	key := instanceKey{name: "test-module", numaIdx: 0}
	config := svc.configs[key]
	device := &config[1]
	prefix := netip.MustParsePrefix("192.168.1.0/24")
	targetID, exists := device.Forwards[prefix]
	require.True(t, exists, "Forward rule should exist")
	require.Equal(t, DeviceID(2), targetID)

	// Error: non-existent target device
	invalidTargetReq := &forwardpb.AddL3ForwardRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		SrcDevId: 1,
		Forward: &forwardpb.L3ForwardEntry{
			Network:  "10.0.0.0/8",
			DstDevId: 999,
		},
	}
	_, err = svc.AddL3Forward(context.Background(), invalidTargetReq)
	require.Error(t, err)
	require.Contains(t, err.Error(), "destination device ID 999 is out of range")

	// Error: invalid network
	invalidReq := &forwardpb.AddL3ForwardRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		SrcDevId: 1,
		Forward: &forwardpb.L3ForwardEntry{
			Network:  "invalid",
			DstDevId: 2,
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
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		SrcDevId: 1,
		Forward: &forwardpb.L3ForwardEntry{
			Network:  "192.168.1.0/24",
			DstDevId: 2,
		},
	})
	require.NoError(t, err)

	// Remove L3 forward
	removeReq := &forwardpb.RemoveL3ForwardRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		SrcDevId: 1,
		Network:  "192.168.1.0/24",
	}
	_, err = svc.RemoveL3Forward(context.Background(), removeReq)
	require.NoError(t, err)

	// Check that the rule was removed
	key := instanceKey{name: "test-module", numaIdx: 0}
	config := svc.configs[key]
	device := &config[1]
	prefix := netip.MustParsePrefix("192.168.1.0/24")
	_, exists := device.Forwards[prefix]
	require.False(t, exists, "Forward rule should be removed")
}

func getDevice(t *testing.T, SrcDevId uint32, cfg *forwardpb.InstanceConfig) *forwardpb.ForwardDeviceConfig {
	devIdx := slices.IndexFunc(cfg.Devices, func(d *forwardpb.ForwardDeviceConfig) bool {
		return d.SrcDevId == SrcDevId
	})
	require.NotEqual(t, -1, devIdx)
	return cfg.Devices[devIdx]

}

func TestShowConfig(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 2), 8) // Create service with 2 agents for testing multiple NUMA nodes

	// Setup: Add devices and forward rules
	// Set up default config for NUMA 0
	_, err := svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
		Target:   &forwardpb.TargetModule{ModuleName: "test-module", Numa: 0b1},
		SrcDevId: 1,
		DstDevId: 7,
	})
	require.NoError(t, err)

	key := instanceKey{name: "test-module", numaIdx: 0}

	// Add forward rules directly to the config
	forward1 := netip.MustParsePrefix("192.168.1.0/24")
	forward2 := netip.MustParsePrefix("10.0.0.0/8")
	forward3 := netip.MustParsePrefix("172.16.0.0/12")
	forward4 := netip.MustParsePrefix("2001:db8::/32")
	svc.configs[key][1].Forwards = map[netip.Prefix]DeviceID{
		forward1: 2,
		forward2: 2,
	}
	svc.configs[key][3].Forwards = map[netip.Prefix]DeviceID{
		forward3: 4,
		forward4: 5,
	}

	// Do not set up the config for NUMA node 1; we should see the default config.

	// Test scenario 1: Show configuration for all NUMA nodes
	t.Run("ShowAllNUMA", func(t *testing.T) {
		resp, err := svc.ShowConfig(context.Background(), &forwardpb.ShowConfigRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
				// Don't specify NUMA, let it use all nodes
			},
		})

		require.NoError(t, err, "ShowConfig should not return an error")
		require.NotNil(t, resp, "Response should not be nil")
		require.Len(t, resp.Configs, 2, "Should return configs for both NUMA nodes")

		// Verify NUMA index 0 configuration
		numa0Found := false
		for _, cfg := range resp.Configs {
			if cfg.Numa == 0 {
				numa0Found = true
				require.Len(t, cfg.Devices, int(svc.deviceCount), "Should have %d devices in NUMA 0", svc.deviceCount)

				// Check device 1 has 2 forward rules
				var srcDevId uint32 = 1
				dev1 := getDevice(t, srcDevId, cfg)
				require.Equal(t, uint32(7), dev1.DstDevId, "Device 1 should forward to device 7")
				require.Len(t, dev1.Forwards, 2, "Device 1 should have 2 forwards")

				// Check device 3 has 2 forward rules
				srcDevId = 3
				dev3 := getDevice(t, srcDevId, cfg)
				require.Equal(t, srcDevId, dev3.DstDevId, "Device 3 should forward to itself")
				require.Len(t, dev3.Forwards, 2, "Device 3 should have two forwards")

				networksFound := 0
				for _, fwd := range dev3.Forwards {
					if fwd.Network == forward3.String() {
						networksFound++
						require.Equal(t, uint32(4), fwd.DstDevId, "Target device should match")
					} else if fwd.Network == forward4.String() {
						networksFound++
						require.Equal(t, uint32(5), fwd.DstDevId, "Target device should match")
					}
				}
				require.Equal(t, 2, networksFound, "Device 3 should have networks %s and %s", forward3, forward4)
			}
		}
		require.True(t, numa0Found, "NUMA 0 should be in the response")

		// Verify NUMA index 1 configuration
		numa1Found := false
		for _, cfg := range resp.Configs {
			if cfg.Numa == 1 {
				numa1Found = true
				require.Len(t, cfg.Devices, int(svc.deviceCount), "Should have %d devices in NUMA 1", svc.deviceCount)

				// All devices should forward L2 to itself
				for _, dev := range cfg.Devices {
					require.Equal(t, dev.SrcDevId, dev.DstDevId, "Should forward to itself")
				}
				break
			}
		}
		require.True(t, numa1Found, "NUMA 1 should be in the response")
	})

	// Test scenario 2: Show configuration for specific NUMA node
	t.Run("ShowSpecificNUMA", func(t *testing.T) {
		resp, err := svc.ShowConfig(context.Background(), &forwardpb.ShowConfigRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
				Numa:       0b10, // Specifically request NUMA 1
			},
		})

		require.NoError(t, err, "ShowConfig should not return an error")
		require.NotNil(t, resp, "Response should not be nil")
		require.Len(t, resp.Configs, 1, "Should return config for only NUMA 1")
		require.Equal(t, uint32(1), resp.Configs[0].Numa, "Response should be for NUMA 1")
		require.Len(t, resp.Configs[0].Devices, int(svc.deviceCount), "Should return %d devices", svc.deviceCount)
	})

	// Test scenario 4: Show for non-existent config
	t.Run("ShowNonExistentConfig", func(t *testing.T) {
		resp, err := svc.ShowConfig(context.Background(), &forwardpb.ShowConfigRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "non-existent-module",
				// Don't specify NUMA, let it use all nodes
			},
		})

		require.NoError(t, err, "ShowConfig should not return an error for non-existent module")
		require.NotNil(t, resp, "Response should not be nil")
		require.Len(t, resp.Configs, 2, "Should return configs for both NUMA nodes")
		for _, cfg := range resp.Configs {
			require.Len(t, cfg.Devices, int(svc.deviceCount), "Should return %d devices for NUMA %d", svc.deviceCount, cfg.Numa)
		}
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
		require.Contains(t, err.Error(), "target cannot be nil")

		// Test missing target in RemoveDevice
		_, err = svc.RemoveL3Forward(context.Background(), &forwardpb.RemoveL3ForwardRequest{
			SrcDevId: 1,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target cannot be nil")

		// Test missing target in AddForward
		_, err = svc.AddL3Forward(context.Background(), &forwardpb.AddL3ForwardRequest{
			SrcDevId: 1,
			Forward: &forwardpb.L3ForwardEntry{
				Network:  "192.168.1.0/24",
				DstDevId: 2,
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target cannot be nil")

		// Test missing target in RemoveForward
		_, err = svc.RemoveL3Forward(context.Background(), &forwardpb.RemoveL3ForwardRequest{
			SrcDevId: 1,
			Network:  "192.168.1.0/24",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target cannot be nil")

		// Test missing target in ShowConfig
		_, err = svc.ShowConfig(context.Background(), &forwardpb.ShowConfigRequest{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target cannot be nil")
	})

	t.Run("missing module name", func(t *testing.T) {
		// Test empty module name in AddDevice
		_, err := svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
			Target:   &forwardpb.TargetModule{},
			SrcDevId: 1,
			DstDevId: 2,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")

		// Test empty module name in RemoveDevice
		_, err = svc.RemoveL3Forward(context.Background(), &forwardpb.RemoveL3ForwardRequest{
			Target:   &forwardpb.TargetModule{},
			SrcDevId: 1,
			Network:  "192.168.1.0/24",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")

		// Test empty module name in AddForward
		_, err = svc.AddL3Forward(context.Background(), &forwardpb.AddL3ForwardRequest{
			Target:   &forwardpb.TargetModule{},
			SrcDevId: 1,
			Forward: &forwardpb.L3ForwardEntry{
				Network:  "192.168.1.0/24",
				DstDevId: 2,
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")

		// Test empty module name in RemoveForward
		_, err = svc.RemoveL3Forward(context.Background(), &forwardpb.RemoveL3ForwardRequest{
			Target:   &forwardpb.TargetModule{},
			SrcDevId: 1,
			Network:  "192.168.1.0/24",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")

		// Test empty module name in ShowConfig
		_, err = svc.ShowConfig(context.Background(), &forwardpb.ShowConfigRequest{
			Target: &forwardpb.TargetModule{},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")
	})

	t.Run("invalid network in forward", func(t *testing.T) {
		// Add a device to test with
		_, err := svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			SrcDevId: 1,
			DstDevId: 2,
		})
		require.NoError(t, err)

		// Add a target device
		_, err = svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			SrcDevId: 2,
			DstDevId: 2,
		})
		require.NoError(t, err)

		// Test invalid network format in AddForward
		_, err = svc.AddL3Forward(context.Background(), &forwardpb.AddL3ForwardRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			SrcDevId: 1,
			Forward: &forwardpb.L3ForwardEntry{
				Network:  "invalid-network",
				DstDevId: 2,
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse network")

		// Test invalid network format in RemoveForward
		_, err = svc.RemoveL3Forward(context.Background(), &forwardpb.RemoveL3ForwardRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			SrcDevId: 1,
			Network:  "invalid-network",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse network")
	})

	t.Run("missing forward entry", func(t *testing.T) {
		// Test nil forward entry in AddForward
		_, err := svc.AddL3Forward(context.Background(), &forwardpb.AddL3ForwardRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			SrcDevId: 1,
			Forward:  nil,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "forward entry cannot be nil")
	})

	t.Run("nonexistent source device", func(t *testing.T) {
		// Set up a clean environment
		svc := newTestService(make([]*ffi.Agent, 1), 8)

		// Add target device
		_, err := svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			SrcDevId: 2,
			DstDevId: 2,
		})
		require.NoError(t, err)

		// Test AddForward with a non-existent source device
		_, err = svc.AddL3Forward(context.Background(), &forwardpb.AddL3ForwardRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			SrcDevId: 999, // Non-existent source device
			Forward: &forwardpb.L3ForwardEntry{
				Network:  "192.168.1.0/24",
				DstDevId: 2,
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "out of range")
	})

	t.Run("nonexistent target device", func(t *testing.T) {
		// Set up a clean environment
		svc := newTestService(make([]*ffi.Agent, 1), 8)

		// Add a source device
		_, err := svc.EnableL2Forward(context.Background(), &forwardpb.L2ForwardEnableRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			SrcDevId: 1,
			DstDevId: 1,
		})
		require.NoError(t, err)

		// Test AddForward with a non-existent target device
		_, err = svc.AddL3Forward(context.Background(), &forwardpb.AddL3ForwardRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			SrcDevId: 1, // Existing source device
			Forward: &forwardpb.L3ForwardEntry{
				Network:  "192.168.1.0/24",
				DstDevId: 999, // Non-existent target device
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "destination device ID 999 is out of range")
	})
}
