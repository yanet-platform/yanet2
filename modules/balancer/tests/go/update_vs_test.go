package balancer_test

// TestUpdateVSAndDeleteVS provides comprehensive testing for UpdateVS and DeleteVS methods,
// verifying configuration updates, WLC index management, ACL reuse filtering, and idempotent operations.

import (
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Test addresses
var (
	// Virtual Services
	testVs1IP   = netip.MustParseAddr("10.1.1.1")
	testVs1Port = uint16(80)
	testVs2IP   = netip.MustParseAddr("10.1.2.1")
	testVs2Port = uint16(80)
	testVs3IP   = netip.MustParseAddr("10.1.3.1")
	testVs3Port = uint16(80)
	testVs4IP   = netip.MustParseAddr("10.1.4.1")
	testVs4Port = uint16(80)

	// Real servers
	testReal1IP = netip.MustParseAddr("192.168.1.1")
	testReal2IP = netip.MustParseAddr("192.168.1.2")
	testReal3IP = netip.MustParseAddr("192.168.1.3")
	testReal4IP = netip.MustParseAddr("192.168.2.1")
	testReal5IP = netip.MustParseAddr("192.168.2.2")
	testReal6IP = netip.MustParseAddr("192.168.3.1")
	testReal7IP = netip.MustParseAddr("192.168.3.2")
	testReal8IP = netip.MustParseAddr("192.168.4.1")
	testReal9IP = netip.MustParseAddr("192.168.4.2")
)

// createTestReal creates a Real configuration
func createTestReal(ip netip.Addr, weight uint32) *balancerpb.Real {
	return &balancerpb.Real{
		Id: &balancerpb.RelativeRealIdentifier{
			Ip:   &balancerpb.Addr{Bytes: ip.AsSlice()},
			Port: 0,
		},
		Weight: weight,
		SrcAddr: &balancerpb.Addr{
			Bytes: ip.AsSlice(),
		},
		SrcMask: &balancerpb.Addr{
			Bytes: netip.MustParseAddr("255.255.255.255").AsSlice(),
		},
	}
}

// createTestVS creates a VirtualService configuration
func createTestVS(
	ip netip.Addr,
	port uint16,
	wlc bool,
	reals []*balancerpb.Real,
) *balancerpb.VirtualService {
	return &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr:  &balancerpb.Addr{Bytes: ip.AsSlice()},
			Port:  uint32(port),
			Proto: balancerpb.TransportProto_TCP,
		},
		Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
		AllowedSrcs: []*balancerpb.AllowedSources{
			{
				Nets: []*balancerpb.Net{{
					Addr: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("0.0.0.0").AsSlice(),
					},
					Mask: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("0.0.0.0").AsSlice(),
					},
				}},
			},
		},
		Flags: &balancerpb.VsFlags{
			Gre:    false,
			FixMss: false,
			Ops:    false,
			PureL3: false,
			Wlc:    wlc,
		},
		Reals: reals,
		Peers: []*balancerpb.Addr{},
	}
}

// createTestVSWithACL creates a VirtualService with specific ACL configuration
func createTestVSWithACL(
	ip netip.Addr,
	port uint16,
	wlc bool,
	reals []*balancerpb.Real,
	allowedNets []*balancerpb.Net,
) *balancerpb.VirtualService {
	return &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr:  &balancerpb.Addr{Bytes: ip.AsSlice()},
			Port:  uint32(port),
			Proto: balancerpb.TransportProto_TCP,
		},
		Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
		AllowedSrcs: []*balancerpb.AllowedSources{
			{
				Nets: allowedNets,
			},
		},
		Flags: &balancerpb.VsFlags{
			Gre:    false,
			FixMss: false,
			Ops:    false,
			PureL3: false,
			Wlc:    wlc,
		},
		Reals: reals,
		Peers: []*balancerpb.Addr{},
	}
}

// createInitialTestConfig creates the initial balancer configuration
func createInitialTestConfig() *balancerpb.BalancerConfig {
	return &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				createTestVS(testVs1IP, testVs1Port, true, []*balancerpb.Real{
					createTestReal(testReal1IP, 1),
					createTestReal(testReal2IP, 2),
				}),
				createTestVS(testVs2IP, testVs2Port, false, []*balancerpb.Real{
					createTestReal(testReal3IP, 1),
				}),
			},
			DecapAddresses: []*balancerpb.Addr{},
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 60,
				TcpSyn:    60,
				TcpFin:    60,
				Tcp:       60,
				Udp:       60,
				Default:   60,
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(1000); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.8); return &v }(),
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}
}

// findVSInConfig finds a VS in config by IP address
func findVSInConfig(
	config *balancerpb.BalancerConfig,
	vsIP netip.Addr,
) *balancerpb.VirtualService {
	for _, vs := range config.PacketHandler.Vs {
		addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
		if addr == vsIP {
			return vs
		}
	}
	return nil
}

// verifyWLCConfig verifies that WLC configuration is correctly set for specified VSs
func verifyWLCConfig(
	t *testing.T,
	config *balancerpb.BalancerConfig,
	expectedWLCVSs []netip.Addr,
) {
	t.Helper()

	require.NotNil(t, config.State, "State config should not be nil")
	require.NotNil(t, config.State.Wlc, "WLC config should not be nil")

	// Build map of expected WLC VSs
	expectedWLC := make(map[netip.Addr]bool)
	for _, vsIP := range expectedWLCVSs {
		expectedWLC[vsIP] = true
	}

	// Verify each VS has correct WLC flag
	for _, vs := range config.PacketHandler.Vs {
		addr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
		require.NotNil(t, vs.Flags, "VS flags should not be nil for %s", addr)

		if expectedWLC[addr] {
			assert.True(t, vs.Flags.Wlc, "VS %s should have WLC enabled", addr)
		} else {
			assert.False(t, vs.Flags.Wlc, "VS %s should have WLC disabled", addr)
		}
	}
}

// TestUpdateVSBasicOperations tests basic UpdateVS operations
func TestUpdateVSBasicOperations(t *testing.T) {
	config := createInitialTestConfig()

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			memory := 16 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	t.Run("AddNewVS", func(t *testing.T) {
		// Add VS3 with WLC enabled
		newVS := createTestVS(testVs3IP, testVs3Port, true, []*balancerpb.Real{
			createTestReal(testReal4IP, 1),
			createTestReal(testReal5IP, 1),
		})

		updateInfo, err := ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{newVS},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)
		require.NotNil(t, updateInfo)

		// Verify config
		updatedConfig := ts.Balancer.Config()
		require.NotNil(t, updatedConfig)
		assert.Equal(
			t,
			3,
			len(updatedConfig.PacketHandler.Vs),
			"should have 3 VS",
		)

		// Verify VS3 exists
		vs3 := findVSInConfig(updatedConfig, testVs3IP)
		require.NotNil(t, vs3, "VS3 should exist")
		assert.True(t, vs3.Flags.Wlc, "VS3 should have WLC enabled")
		assert.Equal(t, 2, len(vs3.Reals), "VS3 should have 2 reals")

		// Verify WLC: VS1 and VS3 should have WLC enabled
		verifyWLCConfig(t, updatedConfig, []netip.Addr{testVs1IP, testVs3IP})
	})

	t.Run("UpdateExistingVS", func(t *testing.T) {
		// Update VS1: change from WLC=true to WLC=false
		updatedVS1 := createTestVS(
			testVs1IP,
			testVs1Port,
			false,
			[]*balancerpb.Real{
				createTestReal(testReal6IP, 1),
			},
		)

		updateInfo, err := ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{updatedVS1},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)
		require.NotNil(t, updateInfo)

		// Verify config
		updatedConfig := ts.Balancer.Config()
		require.NotNil(t, updatedConfig)
		assert.Equal(
			t,
			3,
			len(updatedConfig.PacketHandler.Vs),
			"should still have 3 VS",
		)

		// Verify VS1 updated
		vs1 := findVSInConfig(updatedConfig, testVs1IP)
		require.NotNil(t, vs1, "VS1 should exist")
		assert.False(t, vs1.Flags.Wlc, "VS1 should have WLC disabled")
		assert.Equal(t, 1, len(vs1.Reals), "VS1 should have 1 real")

		// Verify WLC: only VS3 should have WLC enabled now
		verifyWLCConfig(t, updatedConfig, []netip.Addr{testVs3IP})
	})

	t.Run("UpdateMultipleVS", func(t *testing.T) {
		// Update VS2 to enable WLC and add VS4 with WLC
		updatedVS2 := createTestVS(
			testVs2IP,
			testVs2Port,
			true,
			[]*balancerpb.Real{
				createTestReal(testReal7IP, 2),
				createTestReal(testReal8IP, 1),
			},
		)
		newVS4 := createTestVS(testVs4IP, testVs4Port, true, []*balancerpb.Real{
			createTestReal(testReal9IP, 1),
		})

		updateInfo, err := ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{updatedVS2, newVS4},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)
		require.NotNil(t, updateInfo)

		// Verify config
		updatedConfig := ts.Balancer.Config()
		require.NotNil(t, updatedConfig)
		assert.Equal(
			t,
			4,
			len(updatedConfig.PacketHandler.Vs),
			"should have 4 VS",
		)

		// Verify VS2 updated
		vs2 := findVSInConfig(updatedConfig, testVs2IP)
		require.NotNil(t, vs2, "VS2 should exist")
		assert.True(t, vs2.Flags.Wlc, "VS2 should have WLC enabled")
		assert.Equal(t, 2, len(vs2.Reals), "VS2 should have 2 reals")

		// Verify VS4 added
		vs4 := findVSInConfig(updatedConfig, testVs4IP)
		require.NotNil(t, vs4, "VS4 should exist")
		assert.True(t, vs4.Flags.Wlc, "VS4 should have WLC enabled")

		// Verify WLC: VS2, VS3, VS4 should have WLC enabled
		verifyWLCConfig(
			t,
			updatedConfig,
			[]netip.Addr{testVs2IP, testVs3IP, testVs4IP},
		)
	})
}

// TestDeleteVSBasicOperations tests basic DeleteVS operations
func TestDeleteVSBasicOperations(t *testing.T) {
	config := createInitialTestConfig()

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			memory := 16 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	t.Run("DeleteSingleVS", func(t *testing.T) {
		// Delete VS1
		vsToDelete := createTestVS(testVs1IP, testVs1Port, false, nil)

		updateInfo, err := ts.Balancer.DeleteVS(
			[]*balancerpb.VirtualService{vsToDelete},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)
		require.NotNil(t, updateInfo)

		// Verify ACL reuse list is empty for delete
		assert.Empty(
			t,
			updateInfo.ACLReusedVs,
			"ACL reuse list should be empty for delete",
		)

		// Verify config
		updatedConfig := ts.Balancer.Config()
		require.NotNil(t, updatedConfig)
		assert.Equal(
			t,
			1,
			len(updatedConfig.PacketHandler.Vs),
			"should have 1 VS",
		)

		// Verify VS1 deleted
		vs1 := findVSInConfig(updatedConfig, testVs1IP)
		assert.Nil(t, vs1, "VS1 should be deleted")

		// Verify VS2 still exists
		vs2 := findVSInConfig(updatedConfig, testVs2IP)
		require.NotNil(t, vs2, "VS2 should still exist")

		// Verify WLC: no WLC-enabled VS remaining
		verifyWLCConfig(t, updatedConfig, []netip.Addr{})
	})

	t.Run("DeleteMultipleVS", func(t *testing.T) {
		// Re-add VS1 and VS3
		vs1 := createTestVS(testVs1IP, testVs1Port, true, []*balancerpb.Real{
			createTestReal(testReal1IP, 1),
		})
		vs3 := createTestVS(testVs3IP, testVs3Port, false, []*balancerpb.Real{
			createTestReal(testReal4IP, 1),
		})

		_, err := ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{vs1, vs3},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)

		// Delete VS2 and VS3
		vsToDelete := []*balancerpb.VirtualService{
			createTestVS(testVs2IP, testVs2Port, false, nil),
			createTestVS(testVs3IP, testVs3Port, false, nil),
		}

		updateInfo, err := ts.Balancer.DeleteVS(
			vsToDelete,
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)
		require.NotNil(t, updateInfo)

		// Verify config
		updatedConfig := ts.Balancer.Config()
		require.NotNil(t, updatedConfig)
		assert.Equal(
			t,
			1,
			len(updatedConfig.PacketHandler.Vs),
			"should have 1 VS",
		)

		// Verify only VS1 remains
		vs1Found := findVSInConfig(updatedConfig, testVs1IP)
		require.NotNil(t, vs1Found, "VS1 should exist")

		// Verify WLC: VS1 should have WLC enabled
		verifyWLCConfig(t, updatedConfig, []netip.Addr{testVs1IP})
	})

	t.Run("IdempotentDelete", func(t *testing.T) {
		// Try to delete non-existent VS
		vsToDelete := createTestVS(testVs4IP, testVs4Port, false, nil)

		updateInfo, err := ts.Balancer.DeleteVS(
			[]*balancerpb.VirtualService{vsToDelete},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err, "deleting non-existent VS should not error")
		require.NotNil(t, updateInfo)

		// Verify config unchanged
		updatedConfig := ts.Balancer.Config()
		require.NotNil(t, updatedConfig)
		assert.Equal(
			t,
			1,
			len(updatedConfig.PacketHandler.Vs),
			"should still have 1 VS",
		)
	})
}

// TestUpdateVSAndDeleteVSWorkflow tests complex workflows
func TestUpdateVSAndDeleteVSWorkflow(t *testing.T) {
	config := createInitialTestConfig()

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			memory := 16 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	// Enable all reals
	utils.EnableAllReals(t, ts)

	t.Run("ComplexWorkflow", func(t *testing.T) {
		// Step 1: Add VS3 and VS4
		vs3 := createTestVS(testVs3IP, testVs3Port, true, []*balancerpb.Real{
			createTestReal(testReal4IP, 1),
		})
		vs4 := createTestVS(testVs4IP, testVs4Port, false, []*balancerpb.Real{
			createTestReal(testReal5IP, 1),
		})

		_, err := ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{vs3, vs4},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)

		config := ts.Balancer.Config()
		assert.Equal(t, 4, len(config.PacketHandler.Vs), "should have 4 VS")
		verifyWLCConfig(t, config, []netip.Addr{testVs1IP, testVs3IP})

		// Step 2: Send packets to VS1
		clientIP := netip.MustParseAddr("3.3.3.1")
		packetLayers := utils.MakeTCPPacket(
			clientIP,
			1000,
			testVs1IP,
			testVs1Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		require.Equal(t, 1, len(result.Output))

		// Step 3: Delete VS2
		vsToDelete := createTestVS(testVs2IP, testVs2Port, false, nil)
		_, err = ts.Balancer.DeleteVS(
			[]*balancerpb.VirtualService{vsToDelete},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)

		config = ts.Balancer.Config()
		assert.Equal(t, 3, len(config.PacketHandler.Vs), "should have 3 VS")
		verifyWLCConfig(t, config, []netip.Addr{testVs1IP, testVs3IP})

		// Step 4: Update VS1 to disable WLC
		updatedVS1 := createTestVS(
			testVs1IP,
			testVs1Port,
			false,
			[]*balancerpb.Real{
				createTestReal(testReal1IP, 1),
				createTestReal(testReal2IP, 1),
			},
		)
		_, err = ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{updatedVS1},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)

		config = ts.Balancer.Config()
		verifyWLCConfig(t, config, []netip.Addr{testVs3IP})

		// Step 5: Delete all remaining VS
		allVS := []*balancerpb.VirtualService{
			createTestVS(testVs1IP, testVs1Port, false, nil),
			createTestVS(testVs3IP, testVs3Port, false, nil),
			createTestVS(testVs4IP, testVs4Port, false, nil),
		}
		_, err = ts.Balancer.DeleteVS(allVS, ts.Mock.CurrentTime())
		require.NoError(t, err)

		config = ts.Balancer.Config()
		assert.Equal(t, 0, len(config.PacketHandler.Vs), "should have 0 VS")
		verifyWLCConfig(t, config, []netip.Addr{})
	})
}

// TestWLCIndexRecalculation tests WLC index management during updates/deletes
func TestWLCIndexRecalculation(t *testing.T) {
	config := createInitialTestConfig()

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			memory := 16 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	t.Run("WLCIndexRecalculationOnUpdate", func(t *testing.T) {
		// Initial: VS1 (WLC=true, index 0), VS2 (WLC=false, index 1)
		initialConfig := ts.Balancer.Config()
		verifyWLCConfig(t, initialConfig, []netip.Addr{testVs1IP})

		// Add VS3 with WLC=true
		vs3 := createTestVS(testVs3IP, testVs3Port, true, []*balancerpb.Real{
			createTestReal(testReal4IP, 1),
		})
		_, err := ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{vs3},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)

		// Now: VS1 (index 0, WLC), VS2 (index 1, no WLC), VS3 (index 2, WLC)
		config := ts.Balancer.Config()
		verifyWLCConfig(t, config, []netip.Addr{testVs1IP, testVs3IP})

		// Update VS2 to enable WLC
		updatedVS2 := createTestVS(
			testVs2IP,
			testVs2Port,
			true,
			[]*balancerpb.Real{
				createTestReal(testReal3IP, 1),
			},
		)
		_, err = ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{updatedVS2},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)

		// Now all 3 VS have WLC
		config = ts.Balancer.Config()
		verifyWLCConfig(
			t,
			config,
			[]netip.Addr{testVs1IP, testVs2IP, testVs3IP},
		)
	})

	t.Run("WLCIndexRecalculationOnDelete", func(t *testing.T) {
		// Current: VS1 (index 0, WLC), VS2 (index 1, WLC), VS3 (index 2, WLC)

		// Delete VS2 (middle VS with WLC)
		vsToDelete := createTestVS(testVs2IP, testVs2Port, false, nil)
		_, err := ts.Balancer.DeleteVS(
			[]*balancerpb.VirtualService{vsToDelete},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)

		// Now: VS1 (index 0, WLC), VS3 (index 1, WLC) - indices shifted
		config := ts.Balancer.Config()
		verifyWLCConfig(t, config, []netip.Addr{testVs1IP, testVs3IP})

		// Verify VS3 is now at index 1
		assert.Equal(t, 2, len(config.PacketHandler.Vs))
		vs3Addr, _ := netip.AddrFromSlice(
			config.PacketHandler.Vs[1].Id.Addr.Bytes,
		)
		assert.Equal(t, testVs3IP, vs3Addr, "VS3 should be at index 1")
	})
}

// TestACLRebuildVerification tests that ACL filters are properly rebuilt on VS update
func TestACLRebuildVerification(t *testing.T) {
	// Create initial config with VS1 allowing only 10.0.0.0/8
	initialConfig := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("5.5.5.5").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("fe80::5").AsSlice(),
			},
			Vs: []*balancerpb.VirtualService{
				createTestVSWithACL(
					testVs1IP,
					testVs1Port,
					false,
					[]*balancerpb.Real{
						createTestReal(testReal1IP, 1),
					},
					[]*balancerpb.Net{{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.0.0.0").AsSlice(),
						},
						Mask: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("255.0.0.0").AsSlice(),
						},
					}},
				),
			},
			DecapAddresses: []*balancerpb.Addr{},
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 60,
				TcpSyn:    60,
				TcpFin:    60,
				Tcp:       60,
				Udp:       60,
				Default:   60,
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(1000); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.8); return &v }(),
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: initialConfig,
		AgentMemory: func() *datasize.ByteSize {
			memory := 16 * datasize.MB
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	// Enable all reals
	utils.EnableAllReals(t, ts)

	t.Run("InitialACL_AllowsOnly10Network", func(t *testing.T) {
		// Packet from 10.0.0.1 should be allowed
		allowedClient := netip.MustParseAddr("10.0.0.1")
		packetLayers := utils.MakeTCPPacket(
			allowedClient,
			1000,
			testVs1IP,
			testVs1Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"packet from 10.0.0.1 should be allowed",
		)
		assert.Empty(t, result.Drop, "packet should not be dropped")

		// Packet from 192.168.1.1 should be denied
		deniedClient := netip.MustParseAddr("192.168.1.100")
		packetLayers = utils.MakeTCPPacket(
			deniedClient,
			1001,
			testVs1IP,
			testVs1Port,
			&layers.TCP{SYN: true},
		)
		packet = xpacket.LayersToPacket(t, packetLayers...)
		result, err = ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Empty(
			t,
			result.Output,
			"packet from 192.168.1.100 should be denied",
		)
		assert.Equal(t, 1, len(result.Drop), "packet should be dropped")
	})

	t.Run("UpdateACL_AllowsOnly192Network", func(t *testing.T) {
		// Update VS1 to allow only 192.168.0.0/16
		updatedVS1 := createTestVSWithACL(
			testVs1IP,
			testVs1Port,
			false,
			[]*balancerpb.Real{
				createTestReal(testReal1IP, 1),
			},
			[]*balancerpb.Net{{
				Addr: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("192.168.0.0").AsSlice(),
				},
				Mask: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("255.255.0.0").AsSlice(),
				},
			}},
		)

		updateInfo, err := ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{updatedVS1},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)
		require.NotNil(t, updateInfo)

		// Now packet from 10.0.0.1 should be denied
		deniedClient := netip.MustParseAddr("10.0.0.1")
		packetLayers := utils.MakeTCPPacket(
			deniedClient,
			1002,
			testVs1IP,
			testVs1Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Empty(
			t,
			result.Output,
			"packet from 10.0.0.1 should now be denied",
		)
		assert.Equal(t, 1, len(result.Drop), "packet should be dropped")

		// Packet from 192.168.1.1 should now be allowed
		allowedClient := netip.MustParseAddr("192.168.1.100")
		packetLayers = utils.MakeTCPPacket(
			allowedClient,
			1003,
			testVs1IP,
			testVs1Port,
			&layers.TCP{SYN: true},
		)
		packet = xpacket.LayersToPacket(t, packetLayers...)
		result, err = ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"packet from 192.168.1.100 should now be allowed",
		)
		assert.Empty(t, result.Drop, "packet should not be dropped")
	})

	t.Run("UpdateACL_AllowsAllNetworks", func(t *testing.T) {
		// Update VS1 to allow all networks (0.0.0.0/0)
		updatedVS1 := createTestVSWithACL(
			testVs1IP,
			testVs1Port,
			false,
			[]*balancerpb.Real{
				createTestReal(testReal1IP, 1),
			},
			[]*balancerpb.Net{{
				Addr: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("0.0.0.0").AsSlice(),
				},
				Mask: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("0.0.0.0").AsSlice(),
				},
			}},
		)

		updateInfo, err := ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{updatedVS1},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)
		require.NotNil(t, updateInfo)

		// Both packets should now be allowed
		client1 := netip.MustParseAddr("10.0.0.1")
		packetLayers := utils.MakeTCPPacket(
			client1,
			1004,
			testVs1IP,
			testVs1Port,
			&layers.TCP{SYN: true},
		)
		packet := xpacket.LayersToPacket(t, packetLayers...)
		result, err := ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"packet from 10.0.0.1 should be allowed",
		)
		assert.Empty(t, result.Drop)

		client2 := netip.MustParseAddr("192.168.1.100")
		packetLayers = utils.MakeTCPPacket(
			client2,
			1005,
			testVs1IP,
			testVs1Port,
			&layers.TCP{SYN: true},
		)
		packet = xpacket.LayersToPacket(t, packetLayers...)
		result, err = ts.Mock.HandlePackets(packet)
		require.NoError(t, err)
		assert.Equal(
			t,
			1,
			len(result.Output),
			"packet from 192.168.1.100 should be allowed",
		)
		assert.Empty(t, result.Drop)
	})

	t.Run("VerifyACLReuseReporting", func(t *testing.T) {
		// Update VS1 with same ACL - should report ACL reuse
		sameACLVS := createTestVSWithACL(
			testVs1IP,
			testVs1Port,
			false,
			[]*balancerpb.Real{
				createTestReal(testReal1IP, 1),
			},
			[]*balancerpb.Net{{
				Addr: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("0.0.0.0").AsSlice(),
				},
				Mask: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("0.0.0.0").AsSlice(),
				},
			}},
		)

		updateInfo, err := ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{sameACLVS},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)
		require.NotNil(t, updateInfo)

		// Should report ACL reuse for VS1
		assert.NotEmpty(
			t,
			updateInfo.ACLReusedVs,
			"ACL should be reused when unchanged",
		)

		// Update VS1 with different ACL - should NOT report ACL reuse
		differentACLVS := createTestVSWithACL(
			testVs1IP,
			testVs1Port,
			false,
			[]*balancerpb.Real{
				createTestReal(testReal1IP, 1),
			},
			[]*balancerpb.Net{{
				Addr: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("172.16.0.0").AsSlice(),
				},
				Mask: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("255.240.0.0").AsSlice(),
				},
			}},
		)

		updateInfo, err = ts.Balancer.UpdateVS(
			[]*balancerpb.VirtualService{differentACLVS},
			ts.Mock.CurrentTime(),
		)
		require.NoError(t, err)
		require.NotNil(t, updateInfo)

		// Should NOT report ACL reuse for VS1
		assert.Empty(
			t,
			updateInfo.ACLReusedVs,
			"ACL should not be reused when changed",
		)
	})
}
