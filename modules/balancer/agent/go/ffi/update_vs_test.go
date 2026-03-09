package ffi

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUpdateVsWLCIndicesInConfig tests that WLC indices are correctly set in BalancerManagerConfig
func TestUpdateVsWLCIndicesInConfig(t *testing.T) {
	t.Run("NoWLCEnabled", func(t *testing.T) {
		config := &BalancerManagerConfig{
			Balancer: BalancerConfig{
				Handler: PacketHandlerConfig{
					VirtualServices: []VsConfig{
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.2"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
					},
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{}, // No WLC indices
			},
		}

		assert.Empty(
			t,
			config.Wlc.Vs,
			"WLC indices should be empty when no VS has WLC enabled",
		)
	})

	t.Run("SingleWLCEnabled", func(t *testing.T) {
		config := &BalancerManagerConfig{
			Balancer: BalancerConfig{
				Handler: PacketHandlerConfig{
					VirtualServices: []VsConfig{
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.2"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
					},
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{1}, // VS at index 1 has WLC
			},
		}

		require.Len(t, config.Wlc.Vs, 1, "WLC indices should contain 1 entry")
		assert.Equal(
			t,
			uint32(1),
			config.Wlc.Vs[0],
			"WLC index should be 1 (second VS)",
		)
	})

	t.Run("MultipleWLCEnabled", func(t *testing.T) {
		config := &BalancerManagerConfig{
			Balancer: BalancerConfig{
				Handler: PacketHandlerConfig{
					VirtualServices: []VsConfig{
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.2"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.3"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.4"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
					},
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs: []uint32{
					0,
					2,
					3,
				}, // VS at indices 0, 2, 3 have WLC
			},
		}

		require.Len(t, config.Wlc.Vs, 3, "WLC indices should contain 3 entries")
		assert.Equal(
			t,
			uint32(0),
			config.Wlc.Vs[0],
			"First WLC index should be 0",
		)
		assert.Equal(
			t,
			uint32(2),
			config.Wlc.Vs[1],
			"Second WLC index should be 2",
		)
		assert.Equal(
			t,
			uint32(3),
			config.Wlc.Vs[2],
			"Third WLC index should be 3",
		)
	})

	t.Run("AllWLCEnabled", func(t *testing.T) {
		config := &BalancerManagerConfig{
			Balancer: BalancerConfig{
				Handler: PacketHandlerConfig{
					VirtualServices: []VsConfig{
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.2"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.3"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
					},
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0, 1, 2}, // All VS have WLC
			},
		}

		require.Len(t, config.Wlc.Vs, 3, "WLC indices should contain 3 entries")
		assert.Equal(
			t,
			uint32(0),
			config.Wlc.Vs[0],
			"First WLC index should be 0",
		)
		assert.Equal(
			t,
			uint32(1),
			config.Wlc.Vs[1],
			"Second WLC index should be 1",
		)
		assert.Equal(
			t,
			uint32(2),
			config.Wlc.Vs[2],
			"Third WLC index should be 2",
		)
	})
}

// TestUpdateVsWLCIndicesRecalculationScenarios tests WLC index recalculation scenarios
func TestUpdateVsWLCIndicesRecalculationScenarios(t *testing.T) {
	t.Run("AddWLCEnabledVS", func(t *testing.T) {
		// Initial config: VS0 (WLC=true), VS1 (WLC=false)
		initialConfig := &BalancerManagerConfig{
			Balancer: BalancerConfig{
				Handler: PacketHandlerConfig{
					VirtualServices: []VsConfig{
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.2"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
					},
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0},
			},
		}

		// After adding VS2 with WLC=true
		updatedConfig := &BalancerManagerConfig{
			Balancer: BalancerConfig{
				Handler: PacketHandlerConfig{
					VirtualServices: append(
						initialConfig.Balancer.Handler.VirtualServices,
						VsConfig{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.3"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
					),
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0, 2}, // VS0 and VS2 have WLC
			},
		}

		require.Len(
			t,
			updatedConfig.Wlc.Vs,
			2,
			"Should have 2 WLC indices after adding WLC-enabled VS",
		)
		assert.Equal(
			t,
			uint32(0),
			updatedConfig.Wlc.Vs[0],
			"First WLC index should be 0",
		)
		assert.Equal(
			t,
			uint32(2),
			updatedConfig.Wlc.Vs[1],
			"Second WLC index should be 2",
		)
	})

	t.Run("RemoveWLCEnabledVS", func(t *testing.T) {
		// Initial config: VS0 (WLC=true), VS1 (WLC=true), VS2 (WLC=false)
		initialConfig := &BalancerManagerConfig{
			Balancer: BalancerConfig{
				Handler: PacketHandlerConfig{
					VirtualServices: []VsConfig{
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.2"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.3"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
					},
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0, 1},
			},
		}

		// After removing VS1 (middle VS with WLC=true)
		// New list: VS0 (WLC=true), VS2 (WLC=false) -> indices shift
		updatedConfig := &BalancerManagerConfig{
			Balancer: BalancerConfig{
				Handler: PacketHandlerConfig{
					VirtualServices: []VsConfig{
						initialConfig.Balancer.Handler.VirtualServices[0],
						initialConfig.Balancer.Handler.VirtualServices[2],
					},
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs: []uint32{
					0,
				}, // Only VS0 has WLC now (at new index 0)
			},
		}

		require.Len(
			t,
			updatedConfig.Wlc.Vs,
			1,
			"Should have 1 WLC index after removing WLC-enabled VS",
		)
		assert.Equal(
			t,
			uint32(0),
			updatedConfig.Wlc.Vs[0],
			"WLC index should be 0",
		)
	})

	t.Run("UpdateVSToEnableWLC", func(t *testing.T) {
		// Initial config: VS0 (WLC=false), VS1 (WLC=false)
		initialConfig := &BalancerManagerConfig{
			Balancer: BalancerConfig{
				Handler: PacketHandlerConfig{
					VirtualServices: []VsConfig{
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.2"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
					},
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{},
			},
		}

		// After updating VS1 to enable WLC
		updatedConfig := &BalancerManagerConfig{
			Balancer: BalancerConfig{
				Handler: PacketHandlerConfig{
					VirtualServices: []VsConfig{
						initialConfig.Balancer.Handler.VirtualServices[0],
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.2"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
					},
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{1}, // VS1 now has WLC
			},
		}

		require.Len(
			t,
			updatedConfig.Wlc.Vs,
			1,
			"Should have 1 WLC index after enabling WLC on VS",
		)
		assert.Equal(
			t,
			uint32(1),
			updatedConfig.Wlc.Vs[0],
			"WLC index should be 1",
		)
	})

	t.Run("UpdateVSToDisableWLC", func(t *testing.T) {
		// Initial config: VS0 (WLC=true), VS1 (WLC=true), VS2 (WLC=true)
		initialConfig := &BalancerManagerConfig{
			Balancer: BalancerConfig{
				Handler: PacketHandlerConfig{
					VirtualServices: []VsConfig{
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.2"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.3"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
					},
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0, 1, 2},
			},
		}

		// After updating VS1 to disable WLC
		updatedConfig := &BalancerManagerConfig{
			Balancer: BalancerConfig{
				Handler: PacketHandlerConfig{
					VirtualServices: []VsConfig{
						initialConfig.Balancer.Handler.VirtualServices[0],
						{
							Identifier: VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.2"),
								Port:           80,
								TransportProto: VsTransportProtoTCP,
							},
						},
						initialConfig.Balancer.Handler.VirtualServices[2],
					},
				},
			},
			Wlc: BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0, 2}, // VS0 and VS2 have WLC
			},
		}

		require.Len(
			t,
			updatedConfig.Wlc.Vs,
			2,
			"Should have 2 WLC indices after disabling WLC on VS",
		)
		assert.Equal(
			t,
			uint32(0),
			updatedConfig.Wlc.Vs[0],
			"First WLC index should be 0",
		)
		assert.Equal(
			t,
			uint32(2),
			updatedConfig.Wlc.Vs[1],
			"Second WLC index should be 2",
		)
	})
}
