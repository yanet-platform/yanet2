package forward

import (
	"context"
	"maps"
	"net/netip"
	"slices"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/modules/forward/forwardpb"
)

// Override ForwardSerivce updater to avoid FFI calls in tests
func testUpdateModuleConfigs(m *ForwardService, name string, numaIndices []uint32) error {
	m.log.Debugw("Skip FFI calls in tests", zap.String("module", name), zap.Uint32s("numa", numaIndices))
	return nil
}

func newTestService(agents []*ffi.Agent) *ForwardService {
	logger, _ := zap.NewDevelopment()
	svc := NewForwardService(agents, logger.Sugar())

	key := instanceKey{name: "test-module", numaIdx: 0}
	svc.configs[key] = &ForwardConfig{
		DeviceForwards: []ForwardDeviceConfig{},
	}
	svc.updater = testUpdateModuleConfigs

	return svc
}

func TestAddDevice(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 1))

	// Test adding a device
	req := &forwardpb.AddDeviceRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 3,
	}

	_, err := svc.AddDevice(context.Background(), req)
	require.NoError(t, err)

	// Verify device was added
	key := instanceKey{name: "test-module", numaIdx: 0}
	config := svc.configs[key]
	require.NotNil(t, config)

	// Find the added device
	found := false
	for _, device := range config.DeviceForwards {
		if uint32(device.L2ForwardDeviceID) == 3 {
			found = true
			break
		}
	}
	require.True(t, found, "Device with ID 3 should be added")

	// Test adding with duplicate ID fails
	_, err = svc.AddDevice(context.Background(), req)
	require.Error(t, err, "Adding duplicate device should fail")
	require.Contains(t, err.Error(), "already exists")
}

func TestAddForward(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 1))

	// First add source device
	_, err := svc.AddDevice(context.Background(), &forwardpb.AddDeviceRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 1,
	})
	require.NoError(t, err)

	// Now add target device
	_, err = svc.AddDevice(context.Background(), &forwardpb.AddDeviceRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 2,
	})
	require.NoError(t, err)

	// Verify devices were added
	key := instanceKey{name: "test-module", numaIdx: 0}
	config := svc.configs[key]

	// Check for source device
	device1Found := false
	for _, dev := range config.DeviceForwards {
		if uint32(dev.L2ForwardDeviceID) == 1 {
			device1Found = true
			break
		}
	}
	require.True(t, device1Found, "Source device with ID 1 should be added")

	// Check for target device
	device2Found := false
	for _, dev := range config.DeviceForwards {
		if uint32(dev.L2ForwardDeviceID) == 2 {
			device2Found = true
			break
		}
	}
	require.True(t, device2Found, "Target device with ID 2 should be added")

	// Now add a forward rule
	addForwardReq := &forwardpb.AddForwardRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 1,
		Forward: &forwardpb.ForwardEntry{
			Network:  "192.168.1.0/24",
			DeviceId: 2,
		},
	}

	_, err = svc.AddForward(context.Background(), addForwardReq)
	require.NoError(t, err)

	// Verify forward rule was added
	config = svc.configs[key] // Re-get config in case it was replaced

	// Find the source device
	var device *ForwardDeviceConfig
	for i := range config.DeviceForwards {
		if uint32(config.DeviceForwards[i].L2ForwardDeviceID) == 1 {
			device = &config.DeviceForwards[i]
			break
		}
	}
	require.NotNil(t, device, "Source device with ID 1 should exist")

	// Verify forward rule exists
	prefix := netip.MustParsePrefix("192.168.1.0/24")
	targetID, exists := device.Forwards[prefix]
	require.True(t, exists, "Forward rule should exist")
	require.Equal(t, ForwardDeviceID(2), targetID)

	// Test adding forward to non-existent target device should fail
	invalidTargetReq := &forwardpb.AddForwardRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 1,
		Forward: &forwardpb.ForwardEntry{
			Network:  "10.0.0.0/8",
			DeviceId: 999, // Non-existent target device
		},
	}

	_, err = svc.AddForward(context.Background(), invalidTargetReq)
	require.Error(t, err)
	require.Contains(t, err.Error(), "target device with ID 999 not found in NUMA node 0")

	// Test invalid network prefix
	invalidReq := &forwardpb.AddForwardRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 1,
		Forward: &forwardpb.ForwardEntry{
			Network:  "invalid",
			DeviceId: 2,
		},
	}

	_, err = svc.AddForward(context.Background(), invalidReq)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to parse network")
}

func TestRemoveForward(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 1))

	// Setup: Add source and target devices
	// Add source device
	_, err := svc.AddDevice(context.Background(), &forwardpb.AddDeviceRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 1,
	})
	require.NoError(t, err)

	// Add target device
	_, err = svc.AddDevice(context.Background(), &forwardpb.AddDeviceRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 2,
	})
	require.NoError(t, err)

	// Verify devices were added
	key := instanceKey{name: "test-module", numaIdx: 0}
	config := svc.configs[key]

	// Verify source device was added
	sourceDeviceFound := false
	for _, dev := range config.DeviceForwards {
		if uint32(dev.L2ForwardDeviceID) == 1 {
			sourceDeviceFound = true
			break
		}
	}
	require.True(t, sourceDeviceFound, "Source device with ID 1 should be added")

	// Verify target device was added
	targetDeviceFound := false
	for _, dev := range config.DeviceForwards {
		if uint32(dev.L2ForwardDeviceID) == 2 {
			targetDeviceFound = true
			break
		}
	}
	require.True(t, targetDeviceFound, "Target device with ID 2 should be added")

	// Add a forward rule
	addForwardReq := &forwardpb.AddForwardRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 1,
		Forward: &forwardpb.ForwardEntry{
			Network:  "192.168.1.0/24",
			DeviceId: 2,
		},
	}
	_, err = svc.AddForward(context.Background(), addForwardReq)
	require.NoError(t, err)

	// Verify forward rule was added
	config = svc.configs[key]
	var device *ForwardDeviceConfig
	for i := range config.DeviceForwards {
		if uint32(config.DeviceForwards[i].L2ForwardDeviceID) == 1 {
			device = &config.DeviceForwards[i]
			break
		}
	}
	require.NotNil(t, device, "Source device should exist")

	prefix := netip.MustParsePrefix("192.168.1.0/24")
	_, exists := device.Forwards[prefix]
	require.True(t, exists, "Forward rule should exist")

	// Test: Remove the forward rule
	removeReq := &forwardpb.RemoveForwardRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 1,
		Network:  "192.168.1.0/24",
	}

	_, err = svc.RemoveForward(context.Background(), removeReq)
	require.NoError(t, err)

	// Verify the forward rule was removed
	config = svc.configs[key]

	// Find the device
	device = nil
	for i := range config.DeviceForwards {
		if uint32(config.DeviceForwards[i].L2ForwardDeviceID) == 1 {
			device = &config.DeviceForwards[i]
			break
		}
	}
	require.NotNil(t, device, "Device should still exist")

	// Verify forward rule no longer exists
	_, exists = device.Forwards[prefix]
	require.False(t, exists, "Forward rule should be removed")
}

func TestRemoveDevice(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 2))

	// Setup: Add a device
	addDeviceReq := &forwardpb.AddDeviceRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 1,
	}
	_, err := svc.AddDevice(context.Background(), addDeviceReq)
	require.NoError(t, err)

	// Verify device was added
	key := instanceKey{name: "test-module", numaIdx: 0}
	config := svc.configs[key]
	deviceFound := false
	for _, dev := range config.DeviceForwards {
		if uint32(dev.L2ForwardDeviceID) == 1 {
			deviceFound = true
			break
		}
	}
	require.True(t, deviceFound, "Device with ID 1 should be added")

	// Test: Remove the device
	removeReq := &forwardpb.RemoveDeviceRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 1,
	}

	_, err = svc.RemoveDevice(context.Background(), removeReq)
	require.NoError(t, err)

	// Verify the device was removed
	config = svc.configs[key]

	// Check that the device no longer exists
	deviceFound = false
	for _, device := range config.DeviceForwards {
		if uint32(device.L2ForwardDeviceID) == 1 {
			deviceFound = true
			break
		}
	}
	require.False(t, deviceFound, "Device should be removed")
}

func TestCascadingDeviceRemoval(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 2))

	// Setup: Add devices and forward rules
	// Add device 1 (source device)
	_, err := svc.AddDevice(context.Background(), &forwardpb.AddDeviceRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 1,
	})
	require.NoError(t, err)

	// Add device 2 (target device for device 1)
	_, err = svc.AddDevice(context.Background(), &forwardpb.AddDeviceRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 2,
	})
	require.NoError(t, err)

	// Add device 3 (another source device)
	_, err = svc.AddDevice(context.Background(), &forwardpb.AddDeviceRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 3,
	})
	require.NoError(t, err)

	// Verify all devices were added
	key := instanceKey{name: "test-module", numaIdx: 0}
	config := svc.configs[key]

	// Check device 1 exists
	device1Found := false
	for _, dev := range config.DeviceForwards {
		if uint32(dev.L2ForwardDeviceID) == 1 {
			device1Found = true
			break
		}
	}
	require.True(t, device1Found, "Device 1 should be added")

	// Check device 2 exists
	device2Found := false
	for _, dev := range config.DeviceForwards {
		if uint32(dev.L2ForwardDeviceID) == 2 {
			device2Found = true
			break
		}
	}
	require.True(t, device2Found, "Device 2 should be added")

	// Check device 3 exists
	device3Found := false
	for _, dev := range config.DeviceForwards {
		if uint32(dev.L2ForwardDeviceID) == 3 {
			device3Found = true
			break
		}
	}
	require.True(t, device3Found, "Device 3 should be added")

	// Add forward rules from device 1 to device 2
	_, err = svc.AddForward(context.Background(), &forwardpb.AddForwardRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 1,
		Forward: &forwardpb.ForwardEntry{
			Network:  "192.168.1.0/24",
			DeviceId: 2,
		},
	})
	require.NoError(t, err)

	_, err = svc.AddForward(context.Background(), &forwardpb.AddForwardRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 1,
		Forward: &forwardpb.ForwardEntry{
			Network:  "10.0.0.0/8",
			DeviceId: 2,
		},
	})
	require.NoError(t, err)

	// Add forward rule from device 3 to device 2
	_, err = svc.AddForward(context.Background(), &forwardpb.AddForwardRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 3,
		Forward: &forwardpb.ForwardEntry{
			Network:  "172.16.0.0/12",
			DeviceId: 2,
		},
	})
	require.NoError(t, err)

	// Verify forward rules were added
	var device1Config, device3Config ForwardDeviceConfig
	for _, dev := range config.DeviceForwards {
		if uint32(dev.L2ForwardDeviceID) == 1 {
			device1Config = dev
		} else if uint32(dev.L2ForwardDeviceID) == 3 {
			device3Config = dev
		}
	}

	// Verify device 1 has 2 forward rules
	require.Len(t, device1Config.Forwards, 2, "Device 1 should have 2 forward rules")

	// Verify device 3 has 1 forward rule
	require.Len(t, device3Config.Forwards, 1, "Device 3 should have 1 forward rule")

	// Test cascade deletion - removing device 2 should remove all forward rules to it
	_, err = svc.RemoveDevice(context.Background(), &forwardpb.RemoveDeviceRequest{
		Target: &forwardpb.TargetModule{
			ModuleName: "test-module",
		},
		DeviceId: 2,
	})
	require.NoError(t, err)

	// Verify device 2 was removed
	config = svc.configs[key]
	device2Found = false
	for _, dev := range config.DeviceForwards {
		if uint32(dev.L2ForwardDeviceID) == 2 {
			device2Found = true
			break
		}
	}
	require.False(t, device2Found, "Device 2 should be removed")

	// Verify all forward rules to device 2 were removed
	for _, dev := range config.DeviceForwards {
		if uint32(dev.L2ForwardDeviceID) == 1 {
			// Device 1 should have no forward rules now
			require.Empty(t, dev.Forwards, "Device 1 should have no forward rules")
		} else if uint32(dev.L2ForwardDeviceID) == 3 {
			// Device 3 should have no forward rules now
			require.Empty(t, dev.Forwards, "Device 3 should have no forward rules")
		}
	}
}

func TestShowConfig(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 2)) // Create service with 2 agents for testing multiple NUMA nodes

	// Setup: Add devices and forward rules
	// Set up config for NUMA 0 with devices 1, 2, 3, and 4
	key := instanceKey{name: "test-module", numaIdx: 0}
	svc.configs[key] = &ForwardConfig{
		DeviceForwards: []ForwardDeviceConfig{
			{
				L2ForwardDeviceID: ForwardDeviceID(1),
				Forwards:          make(map[netip.Prefix]ForwardDeviceID),
			},
			{
				L2ForwardDeviceID: ForwardDeviceID(2),
				Forwards:          make(map[netip.Prefix]ForwardDeviceID),
			},
			{
				L2ForwardDeviceID: ForwardDeviceID(3),
				Forwards:          make(map[netip.Prefix]ForwardDeviceID),
			},
			{
				L2ForwardDeviceID: ForwardDeviceID(4),
				Forwards:          make(map[netip.Prefix]ForwardDeviceID),
			},
		},
	}

	// Add forward rules directly to the config
	// Add forward rule from device 1 to device 2 for 192.168.1.0/24
	forward1 := netip.MustParsePrefix("192.168.1.0/24")
	svc.configs[key].DeviceForwards[0].Forwards[forward1] = ForwardDeviceID(2)

	// Add forward rule from device 1 to device 2 for 10.0.0.0/8
	forward2 := netip.MustParsePrefix("10.0.0.0/8")
	svc.configs[key].DeviceForwards[0].Forwards[forward2] = ForwardDeviceID(2)

	// Add forward rule from device 3 to device 4 for 172.16.0.0/12
	forward3 := netip.MustParsePrefix("172.16.0.0/12")
	svc.configs[key].DeviceForwards[2].Forwards[forward3] = ForwardDeviceID(4)

	// Add IPv6 forward rule from device 3 to device 4 for 2001:db8::/32
	forward4 := netip.MustParsePrefix("2001:db8::/32")
	svc.configs[key].DeviceForwards[2].Forwards[forward4] = ForwardDeviceID(4)

	// Set up config for NUMA 1 with devices 5 and 6
	key1 := instanceKey{name: "test-module", numaIdx: 1}
	svc.configs[key1] = &ForwardConfig{
		DeviceForwards: []ForwardDeviceConfig{
			{
				L2ForwardDeviceID: ForwardDeviceID(5),
				Forwards:          make(map[netip.Prefix]ForwardDeviceID),
			},
			{
				L2ForwardDeviceID: ForwardDeviceID(6),
				Forwards:          make(map[netip.Prefix]ForwardDeviceID),
			},
		},
	}

	// Add a forward from device 5 to device 6
	forward5to6 := netip.MustParsePrefix("192.168.5.0/24")
	svc.configs[key1].DeviceForwards[0].Forwards[forward5to6] = ForwardDeviceID(6)

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
				require.Len(t, cfg.Devices, 4, "Should have 4 devices in NUMA 0")

				// Check device 1 has 2 forward rules
				device1Found := false
				for _, device := range cfg.Devices {
					if device.DeviceId == 1 {
						device1Found = true
						require.Len(t, device.Forwards, 2, "Device 1 should have 2 forwards")
						break
					}
				}
				require.True(t, device1Found, "Device 1 should be in the config")

				// Check device 3 has 2 forward rules
				device3Found := false
				for _, device := range cfg.Devices {
					if device.DeviceId == 3 {
						device3Found = true
						require.Len(t, device.Forwards, 2, "Device 3 should have 2 forwards")
						break
					}
				}
				require.True(t, device3Found, "Device 3 should be in the config")
				break
			}
		}
		require.True(t, numa0Found, "NUMA 0 should be in the response")

		// Verify NUMA index 1 configuration
		numa1Found := false
		for _, cfg := range resp.Configs {
			if cfg.Numa == 1 {
				numa1Found = true
				require.Len(t, cfg.Devices, 2, "Should have 2 devices in NUMA 1")

				// Check device 5 has 1 forward rule
				device5Found := false
				for _, device := range cfg.Devices {
					if device.DeviceId == 5 {
						device5Found = true
						require.Len(t, device.Forwards, 1, "Device 5 should have 1 forward")
						require.Equal(t, "192.168.5.0/24", device.Forwards[0].Network, "Network should match")
						require.Equal(t, uint32(6), device.Forwards[0].DeviceId, "Target device should match")
						break
					}
				}
				require.True(t, device5Found, "Device 5 should be in the config")
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
				Numa:       []uint32{1}, // Specifically request NUMA 1
			},
		})

		require.NoError(t, err, "ShowConfig should not return an error")
		require.NotNil(t, resp, "Response should not be nil")
		require.Len(t, resp.Configs, 1, "Should return config for only NUMA 1")
		require.Equal(t, uint32(1), resp.Configs[0].Numa, "Response should be for NUMA 1")
		require.Len(t, resp.Configs[0].Devices, 2, "Should have 2 devices")

		// Check that both devices are present
		deviceIDs := []uint32{
			resp.Configs[0].Devices[0].DeviceId,
			resp.Configs[0].Devices[1].DeviceId,
		}
		slices.Sort(deviceIDs)
		require.Equal(t, uint32(5), deviceIDs[0], "Should have device 5")
		require.Equal(t, uint32(6), deviceIDs[1], "Should have device 6")
	})

	// Test scenario 3: Show configuration for empty config
	t.Run("ShowEmptyConfig", func(t *testing.T) {
		// Clear the config for NUMA 0
		svc.configs[key] = &ForwardConfig{
			DeviceForwards: []ForwardDeviceConfig{},
		}

		resp, err := svc.ShowConfig(context.Background(), &forwardpb.ShowConfigRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
				Numa:       []uint32{0}, // Only request NUMA 0 which is now empty
			},
		})

		require.NoError(t, err, "ShowConfig should not return an error even with empty config")
		require.NotNil(t, resp, "Response should not be nil")
		require.Len(t, resp.Configs, 1, "Should return config for NUMA 0")
		require.Equal(t, uint32(0), resp.Configs[0].Numa, "Response should be for NUMA 0")
		require.Empty(t, resp.Configs[0].Devices, "Devices should be empty for NUMA 0")
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

		// All configs should be empty
		for _, cfg := range resp.Configs {
			require.Empty(t, cfg.Devices, "Devices should be empty for non-existent module")
		}
	})

	// Test scenario 5: Sort order of forwards
	t.Run("SortOrderOfForwards", func(t *testing.T) {
		// Create a config with multiple forwards that would need sorting
		// First add the devices
		sortKey := instanceKey{name: "sort-test-module", numaIdx: 0}
		svc.configs[sortKey] = &ForwardConfig{
			DeviceForwards: []ForwardDeviceConfig{
				{
					L2ForwardDeviceID: ForwardDeviceID(10),
					Forwards:          make(map[netip.Prefix]ForwardDeviceID),
				},
				{
					L2ForwardDeviceID: ForwardDeviceID(20),
					Forwards:          make(map[netip.Prefix]ForwardDeviceID),
				},
				{
					L2ForwardDeviceID: ForwardDeviceID(30),
					Forwards:          make(map[netip.Prefix]ForwardDeviceID),
				},
			},
		}

		// Add forwards between existing devices
		forwards := map[netip.Prefix]ForwardDeviceID{
			netip.MustParsePrefix("192.168.1.0/24"): ForwardDeviceID(20),
			netip.MustParsePrefix("192.168.2.0/24"): ForwardDeviceID(30),
			netip.MustParsePrefix("10.0.0.0/8"):     ForwardDeviceID(20),
			netip.MustParsePrefix("172.16.0.0/12"):  ForwardDeviceID(20),
		}

		// Apply the forwards to device 10
		maps.Copy(svc.configs[sortKey].DeviceForwards[0].Forwards, forwards)

		resp, err := svc.ShowConfig(context.Background(), &forwardpb.ShowConfigRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "sort-test-module",
			},
		})

		require.NoError(t, err, "ShowConfig should not return an error")
		require.NotNil(t, resp, "Response should not be nil")
		require.Len(t, resp.Configs, 2, "Should return configs for both NUMA nodes")

		// Find the NUMA 0 config
		var device10 *forwardpb.ForwardDeviceConfig
		for _, cfg := range resp.Configs {
			if cfg.Numa == 0 {
				require.Len(t, cfg.Devices, 3, "Should have 3 devices")

				for _, device := range cfg.Devices {
					if device.DeviceId == 10 {
						device10 = device
						break
					}
				}
				break
			}
		}

		require.NotNil(t, device10, "Device 10 should be in the config")
		require.Len(t, device10.Forwards, 4, "Device 10 should have 4 forwards")

		// Check the sort order - first by DeviceId, then by Network
		// Forwards with same DeviceId should be sorted by Network
		require.Equal(t, uint32(20), device10.Forwards[0].DeviceId)
		require.Equal(t, uint32(20), device10.Forwards[1].DeviceId)
		require.Equal(t, uint32(20), device10.Forwards[2].DeviceId)
		require.Equal(t, uint32(30), device10.Forwards[3].DeviceId)

		// Networks should be alphabetically sorted within each DeviceId group
		networks := []string{
			device10.Forwards[0].Network,
			device10.Forwards[1].Network,
			device10.Forwards[2].Network,
		}
		sorted := make([]string, len(networks))
		copy(sorted, networks)
		sort.Strings(sorted)
		require.Equal(t, sorted, networks, "Networks with same target DeviceId should be sorted")
	})
}

func TestInputValidation(t *testing.T) {
	svc := newTestService(make([]*ffi.Agent, 1))

	// Test AddForward validation
	t.Run("AddForward", func(t *testing.T) {
		// Nil target
		_, err := svc.AddForward(context.Background(), &forwardpb.AddForwardRequest{
			Target: nil,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target cannot be nil")

		// Test missing target in RemoveDevice
		_, err = svc.RemoveDevice(context.Background(), &forwardpb.RemoveDeviceRequest{
			DeviceId: 1,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target cannot be nil")

		// Test missing target in AddForward
		_, err = svc.AddForward(context.Background(), &forwardpb.AddForwardRequest{
			DeviceId: 1,
			Forward: &forwardpb.ForwardEntry{
				Network:  "192.168.1.0/24",
				DeviceId: 2,
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target cannot be nil")

		// Test missing target in RemoveForward
		_, err = svc.RemoveForward(context.Background(), &forwardpb.RemoveForwardRequest{
			DeviceId: 1,
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
		_, err := svc.AddDevice(context.Background(), &forwardpb.AddDeviceRequest{
			Target:   &forwardpb.TargetModule{},
			DeviceId: 1,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")

		// Test empty module name in RemoveDevice
		_, err = svc.RemoveDevice(context.Background(), &forwardpb.RemoveDeviceRequest{
			Target:   &forwardpb.TargetModule{},
			DeviceId: 1,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")

		// Test empty module name in AddForward
		_, err = svc.AddForward(context.Background(), &forwardpb.AddForwardRequest{
			Target:   &forwardpb.TargetModule{},
			DeviceId: 1,
			Forward: &forwardpb.ForwardEntry{
				Network:  "192.168.1.0/24",
				DeviceId: 2,
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "module name is required")

		// Test empty module name in RemoveForward
		_, err = svc.RemoveForward(context.Background(), &forwardpb.RemoveForwardRequest{
			Target:   &forwardpb.TargetModule{},
			DeviceId: 1,
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
		_, err := svc.AddDevice(context.Background(), &forwardpb.AddDeviceRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			DeviceId: 1,
		})
		require.NoError(t, err)

		// Add a target device
		_, err = svc.AddDevice(context.Background(), &forwardpb.AddDeviceRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			DeviceId: 2,
		})
		require.NoError(t, err)

		// Test invalid network format in AddForward
		_, err = svc.AddForward(context.Background(), &forwardpb.AddForwardRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			DeviceId: 1,
			Forward: &forwardpb.ForwardEntry{
				Network:  "invalid-network",
				DeviceId: 2,
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse network")

		// Test invalid network format in RemoveForward
		_, err = svc.RemoveForward(context.Background(), &forwardpb.RemoveForwardRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			DeviceId: 1,
			Network:  "invalid-network",
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse network")
	})

	t.Run("missing forward entry", func(t *testing.T) {
		// Test nil forward entry in AddForward
		_, err := svc.AddForward(context.Background(), &forwardpb.AddForwardRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			DeviceId: 1,
			Forward:  nil,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "forward entry cannot be nil")
	})

	t.Run("nonexistent source device", func(t *testing.T) {
		// Set up a clean environment
		svc := newTestService(make([]*ffi.Agent, 1))

		// Add target device
		_, err := svc.AddDevice(context.Background(), &forwardpb.AddDeviceRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			DeviceId: 2,
		})
		require.NoError(t, err)

		// Test AddForward with a non-existent source device
		_, err = svc.AddForward(context.Background(), &forwardpb.AddForwardRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			DeviceId: 999, // Non-existent source device
			Forward: &forwardpb.ForwardEntry{
				Network:  "192.168.1.0/24",
				DeviceId: 2,
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "source device with ID 999 not found")
	})

	t.Run("nonexistent target device", func(t *testing.T) {
		// Set up a clean environment
		svc := newTestService(make([]*ffi.Agent, 1))

		// Add a source device
		_, err := svc.AddDevice(context.Background(), &forwardpb.AddDeviceRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			DeviceId: 1,
		})
		require.NoError(t, err)

		// Test AddForward with a non-existent target device
		_, err = svc.AddForward(context.Background(), &forwardpb.AddForwardRequest{
			Target: &forwardpb.TargetModule{
				ModuleName: "test-module",
			},
			DeviceId: 1, // Existing source device
			Forward: &forwardpb.ForwardEntry{
				Network:  "192.168.1.0/24",
				DeviceId: 999, // Non-existent target device
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "target device with ID 999 not found")
	})
}
