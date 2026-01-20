package balancer

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
)

// TestCalcWlcWeight tests the core WLC weight calculation algorithm
func TestCalcWlcWeight(t *testing.T) {
	tests := []struct {
		name           string
		wlc            *ffi.BalancerManagerWlcConfig
		weight         uint16
		connections    uint64
		weightSum      uint64
		connectionsSum uint64
		expected       uint16
	}{
		{
			name: "Zero weight returns zero",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
			},
			weight:         0,
			connections:    100,
			weightSum:      200,
			connectionsSum: 500,
			expected:       0,
		},
		{
			name: "Zero weightSum returns original weight",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
			},
			weight:         100,
			connections:    50,
			weightSum:      0,
			connectionsSum: 500,
			expected:       100,
		},
		{
			name: "connectionsSum less than weightSum returns original weight",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
			},
			weight:         100,
			connections:    50,
			weightSum:      200,
			connectionsSum: 100,
			expected:       100,
		},
		{
			name: "connectionsSum equals weightSum - algorithm proceeds",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
			},
			weight:         100,
			connections:    50,
			weightSum:      200,
			connectionsSum: 200,
			// scaledConnections = 50 * 200 = 10000
			// scaledWeight = 200 * 100 = 20000
			// connectionsRatio = 10000 / 20000 = 0.5
			// wlcRatio = max(1.0, 10 * (1.0 - 0.5)) = max(1.0, 5.0) = 5.0
			// newWeight = round(100 * 5.0) = 500
			expected: 500,
		},
		{
			name: "Equal distribution - connections proportional to weight",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
			},
			weight:         100,
			connections:    200, // connections/connectionsSum = 200/400 = 0.5 = weight/weightSum = 100/200
			weightSum:      200,
			connectionsSum: 400,
			expected:       100, // ratio = 1.0, wlcRatio = max(1.0, 10*(1-1)) = 1.0
		},
		{
			name: "Underloaded server gets higher weight",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
			},
			weight:         100,
			connections:    100, // connections/connectionsSum = 100/400 = 0.25, but weight/weightSum = 100/200 = 0.5
			weightSum:      200,
			connectionsSum: 400,
			// scaledConnections = 100 * 200 = 20000
			// scaledWeight = 400 * 100 = 40000
			// connectionsRatio = 20000 / 40000 = 0.5
			// wlcRatio = max(1.0, 10 * (1.0 - 0.5)) = max(1.0, 5.0) = 5.0
			// newWeight = round(100 * 5.0) = 500
			expected: 500,
		},
		{
			name: "Overloaded server keeps minimum weight (ratio >= 1)",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
			},
			weight:         100,
			connections:    300, // connections/connectionsSum = 300/400 = 0.75, but weight/weightSum = 100/200 = 0.5
			weightSum:      200,
			connectionsSum: 400,
			// scaledConnections = 300 * 200 = 60000
			// scaledWeight = 400 * 100 = 40000
			// connectionsRatio = 60000 / 40000 = 1.5
			// wlcRatio = max(1.0, 10 * (1.0 - 1.5)) = max(1.0, -5.0) = 1.0
			// newWeight = round(100 * 1.0) = 100
			expected: 100,
		},
		{
			name: "Weight capped at MaxRealWeight",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         20,
				MaxRealWeight: 200,
			},
			weight:         100,
			connections:    50, // Very underloaded
			weightSum:      200,
			connectionsSum: 400,
			// scaledConnections = 50 * 200 = 10000
			// scaledWeight = 400 * 100 = 40000
			// connectionsRatio = 10000 / 40000 = 0.25
			// wlcRatio = max(1.0, 20 * (1.0 - 0.25)) = max(1.0, 15.0) = 15.0
			// newWeight = round(100 * 15.0) = 1500, but capped at 200
			expected: 200,
		},
		{
			name: "Power factor of 1",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         1,
				MaxRealWeight: 1000,
			},
			weight:         100,
			connections:    100,
			weightSum:      200,
			connectionsSum: 400,
			// connectionsRatio = 0.5
			// wlcRatio = max(1.0, 1 * (1.0 - 0.5)) = max(1.0, 0.5) = 1.0
			expected: 100,
		},
		{
			name: "Power factor of 2 with underloaded server",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         2,
				MaxRealWeight: 1000,
			},
			weight:         100,
			connections:    100,
			weightSum:      200,
			connectionsSum: 400,
			// connectionsRatio = 0.5
			// wlcRatio = max(1.0, 2 * (1.0 - 0.5)) = max(1.0, 1.0) = 1.0
			expected: 100,
		},
		{
			name: "Power factor of 4 with underloaded server",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         4,
				MaxRealWeight: 1000,
			},
			weight:         100,
			connections:    100,
			weightSum:      200,
			connectionsSum: 400,
			// connectionsRatio = 0.5
			// wlcRatio = max(1.0, 4 * (1.0 - 0.5)) = max(1.0, 2.0) = 2.0
			// newWeight = round(100 * 2.0) = 200
			expected: 200,
		},
		{
			name: "Zero connections (very underloaded)",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
			},
			weight:         100,
			connections:    0,
			weightSum:      200,
			connectionsSum: 400,
			// scaledConnections = 0 * 200 = 0
			// scaledWeight = 400 * 100 = 40000
			// connectionsRatio = 0 / 40000 = 0
			// wlcRatio = max(1.0, 10 * (1.0 - 0)) = max(1.0, 10.0) = 10.0
			// newWeight = round(100 * 10.0) = 1000
			expected: 1000,
		},
		{
			name: "Large values",
			wlc: &ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 65535,
			},
			weight:         1000,
			connections:    1000000,
			weightSum:      10000,
			connectionsSum: 5000000,
			// scaledConnections = 1000000 * 10000 = 10000000000
			// scaledWeight = 5000000 * 1000 = 5000000000
			// connectionsRatio = 10000000000 / 5000000000 = 2.0
			// wlcRatio = max(1.0, 10 * (1.0 - 2.0)) = max(1.0, -10.0) = 1.0
			// newWeight = round(1000 * 1.0) = 1000
			expected: 1000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calcWlcWeight(
				tt.wlc,
				tt.weight,
				tt.connections,
				tt.weightSum,
				tt.connectionsSum,
			)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestVsWlcUpdates tests the virtual service level WLC update calculation
func TestVsWlcUpdates(t *testing.T) {
	wlc := &ffi.BalancerManagerWlcConfig{
		Power:         10,
		MaxRealWeight: 1000,
	}

	vsIdentifier := ffi.VsIdentifier{
		Addr:           netip.MustParseAddr("10.0.0.1"),
		Port:           80,
		TransportProto: ffi.VsTransportProtoTcp,
	}

	t.Run("Empty reals returns no updates", func(t *testing.T) {
		vsConfig := &ffi.VsConfig{
			Identifier: vsIdentifier,
			Reals:      []ffi.RealConfig{},
		}
		vsGraph := &ffi.GraphVs{
			Identifier: vsIdentifier,
			Reals:      []ffi.GraphReal{},
		}
		vsInfo := &ffi.VsInfo{
			Identifier: vsIdentifier,
			Reals:      []ffi.RealInfo{},
		}

		updates := vsWlcUpdates(wlc, vsConfig, vsGraph, vsInfo)
		assert.Empty(t, updates)
	})

	t.Run("All disabled reals returns no updates", func(t *testing.T) {
		vsConfig := &ffi.VsConfig{
			Identifier: vsIdentifier,
			Reals: []ffi.RealConfig{
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.1"),
						Port: 8080,
					},
					Weight: 100,
				},
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.2"),
						Port: 8080,
					},
					Weight: 100,
				},
			},
		}
		vsGraph := &ffi.GraphVs{
			Identifier: vsIdentifier,
			Reals: []ffi.GraphReal{
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.1"),
						Port: 8080,
					},
					Weight:  100,
					Enabled: false,
				},
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.2"),
						Port: 8080,
					},
					Weight:  100,
					Enabled: false,
				},
			},
		}
		vsInfo := &ffi.VsInfo{
			Identifier: vsIdentifier,
			Reals: []ffi.RealInfo{
				{Dst: netip.MustParseAddr("192.168.1.1"), ActiveSessions: 50},
				{Dst: netip.MustParseAddr("192.168.1.2"), ActiveSessions: 50},
			},
		}

		updates := vsWlcUpdates(wlc, vsConfig, vsGraph, vsInfo)
		assert.Len(t, updates, 0)
	})

	t.Run("Single enabled real with no weight change", func(t *testing.T) {
		vsConfig := &ffi.VsConfig{
			Identifier: vsIdentifier,
			Reals: []ffi.RealConfig{
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.1"),
						Port: 8080,
					},
					Weight: 100,
				},
			},
		}
		vsGraph := &ffi.GraphVs{
			Identifier: vsIdentifier,
			Reals: []ffi.GraphReal{
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.1"),
						Port: 8080,
					},
					Weight:  100,
					Enabled: true,
				},
			},
		}
		vsInfo := &ffi.VsInfo{
			Identifier: vsIdentifier,
			Reals: []ffi.RealInfo{
				{Dst: netip.MustParseAddr("192.168.1.1"), ActiveSessions: 50},
			},
		}

		updates := vsWlcUpdates(wlc, vsConfig, vsGraph, vsInfo)
		// connectionsSum=50, weightSum=100, connectionsSum < weightSum, so weight unchanged
		assert.Empty(t, updates)
	})

	t.Run(
		"Multiple reals with unequal load generates updates",
		func(t *testing.T) {
			vsConfig := &ffi.VsConfig{
				Identifier: vsIdentifier,
				Reals: []ffi.RealConfig{
					{
						Identifier: ffi.RelativeRealIdentifier{
							Addr: netip.MustParseAddr("192.168.1.1"),
							Port: 8080,
						},
						Weight: 100,
					},
					{
						Identifier: ffi.RelativeRealIdentifier{
							Addr: netip.MustParseAddr("192.168.1.2"),
							Port: 8080,
						},
						Weight: 100,
					},
				},
			}
			vsGraph := &ffi.GraphVs{
				Identifier: vsIdentifier,
				Reals: []ffi.GraphReal{
					{
						Identifier: ffi.RelativeRealIdentifier{
							Addr: netip.MustParseAddr("192.168.1.1"),
							Port: 8080,
						},
						Weight:  100,
						Enabled: true,
					},
					{
						Identifier: ffi.RelativeRealIdentifier{
							Addr: netip.MustParseAddr("192.168.1.2"),
							Port: 8080,
						},
						Weight:  100,
						Enabled: true,
					},
				},
			}
			vsInfo := &ffi.VsInfo{
				Identifier: vsIdentifier,
				Reals: []ffi.RealInfo{
					{
						Dst:            netip.MustParseAddr("192.168.1.1"),
						ActiveSessions: 100,
					}, // Underloaded
					{
						Dst:            netip.MustParseAddr("192.168.1.2"),
						ActiveSessions: 300,
					}, // Overloaded
				},
			}

			updates := vsWlcUpdates(wlc, vsConfig, vsGraph, vsInfo)
			// connectionsSum=400, weightSum=200
			// Real 1: connections=100, weight=100
			//   scaledConnections = 100 * 200 = 20000
			//   scaledWeight = 400 * 100 = 40000
			//   connectionsRatio = 0.5
			//   wlcRatio = max(1.0, 10 * 0.5) = 5.0
			//   newWeight = 500
			// Real 2: connections=300, weight=100
			//   scaledConnections = 300 * 200 = 60000
			//   scaledWeight = 400 * 100 = 40000
			//   connectionsRatio = 1.5
			//   wlcRatio = max(1.0, 10 * -0.5) = 1.0
			//   newWeight = 100 (no change)

			require.Len(t, updates, 1) // Only real 1 has weight change
			assert.Equal(t, uint16(500), updates[0].Weight)
			assert.Equal(
				t,
				netip.MustParseAddr("192.168.1.1"),
				updates[0].Identifier.Relative.Addr,
			)
		},
	)

	t.Run("Update includes correct identifiers", func(t *testing.T) {
		vsConfig := &ffi.VsConfig{
			Identifier: vsIdentifier,
			Reals: []ffi.RealConfig{
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.1"),
						Port: 8080,
					},
					Weight: 100,
				},
			},
		}
		vsGraph := &ffi.GraphVs{
			Identifier: vsIdentifier,
			Reals: []ffi.GraphReal{
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.1"),
						Port: 8080,
					},
					Weight:  50, // Different from calculated weight
					Enabled: true,
				},
			},
		}
		vsInfo := &ffi.VsInfo{
			Identifier: vsIdentifier,
			Reals: []ffi.RealInfo{
				{Dst: netip.MustParseAddr("192.168.1.1"), ActiveSessions: 50},
			},
		}

		updates := vsWlcUpdates(wlc, vsConfig, vsGraph, vsInfo)
		// connectionsSum=50, weightSum=100, connectionsSum < weightSum
		// newWeight = 100 (original), but graph.Weight=50, so update generated

		require.Len(t, updates, 1)
		assert.Equal(t, vsIdentifier, updates[0].Identifier.VsIdentifier)
		assert.Equal(
			t,
			netip.MustParseAddr("192.168.1.1"),
			updates[0].Identifier.Relative.Addr,
		)
		assert.Equal(t, uint16(8080), updates[0].Identifier.Relative.Port)
		assert.Equal(t, ffi.DontUpdateRealEnabled, updates[0].Enabled)
	})

	t.Run(
		"Mismatched reals count panics - config vs graph",
		func(t *testing.T) {
			vsConfig := &ffi.VsConfig{
				Identifier: vsIdentifier,
				Reals: []ffi.RealConfig{
					{
						Identifier: ffi.RelativeRealIdentifier{
							Addr: netip.MustParseAddr("192.168.1.1"),
							Port: 8080,
						},
						Weight: 100,
					},
				},
			}
			vsGraph := &ffi.GraphVs{
				Identifier: vsIdentifier,
				Reals:      []ffi.GraphReal{}, // Mismatched count
			}
			vsInfo := &ffi.VsInfo{
				Identifier: vsIdentifier,
				Reals: []ffi.RealInfo{
					{
						Dst:            netip.MustParseAddr("192.168.1.1"),
						ActiveSessions: 50,
					},
				},
			}

			assert.Panics(t, func() {
				vsWlcUpdates(wlc, vsConfig, vsGraph, vsInfo)
			})
		},
	)

	t.Run("Mismatched reals count panics - config vs info", func(t *testing.T) {
		vsConfig := &ffi.VsConfig{
			Identifier: vsIdentifier,
			Reals: []ffi.RealConfig{
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.1"),
						Port: 8080,
					},
					Weight: 100,
				},
			},
		}
		vsGraph := &ffi.GraphVs{
			Identifier: vsIdentifier,
			Reals: []ffi.GraphReal{
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.1"),
						Port: 8080,
					},
					Weight:  100,
					Enabled: true,
				},
			},
		}
		vsInfo := &ffi.VsInfo{
			Identifier: vsIdentifier,
			Reals:      []ffi.RealInfo{}, // Mismatched count
		}

		assert.Panics(t, func() {
			vsWlcUpdates(wlc, vsConfig, vsGraph, vsInfo)
		})
	})

	t.Run("Mixed enabled and disabled reals", func(t *testing.T) {
		vsConfig := &ffi.VsConfig{
			Identifier: vsIdentifier,
			Reals: []ffi.RealConfig{
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.1"),
						Port: 8080,
					},
					Weight: 100,
				},
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.2"),
						Port: 8080,
					},
					Weight: 100,
				},
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.3"),
						Port: 8080,
					},
					Weight: 100,
				},
			},
		}
		vsGraph := &ffi.GraphVs{
			Identifier: vsIdentifier,
			Reals: []ffi.GraphReal{
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.1"),
						Port: 8080,
					},
					Weight:  100,
					Enabled: true,
				},
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.2"),
						Port: 8080,
					},
					Weight:  100,
					Enabled: false, // Disabled
				},
				{
					Identifier: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("192.168.1.3"),
						Port: 8080,
					},
					Weight:  100,
					Enabled: true,
				},
			},
		}
		vsInfo := &ffi.VsInfo{
			Identifier: vsIdentifier,
			Reals: []ffi.RealInfo{
				{Dst: netip.MustParseAddr("192.168.1.1"), ActiveSessions: 100},
				{
					Dst:            netip.MustParseAddr("192.168.1.2"),
					ActiveSessions: 50,
				}, // Disabled, but has sessions
				{Dst: netip.MustParseAddr("192.168.1.3"), ActiveSessions: 300},
			},
		}

		updates := vsWlcUpdates(wlc, vsConfig, vsGraph, vsInfo)

		// Expect updates for real 1 (weight change)
		require.Len(t, updates, 1)

		// Find updates by address
		var real1Update *ffi.RealUpdate
		for i := range updates {
			if updates[i].Identifier.Relative.Addr == netip.MustParseAddr(
				"192.168.1.1",
			) {
				real1Update = &updates[i]
			}
		}

		require.NotNil(t, real1Update)
		assert.Equal(t, uint16(500), real1Update.Weight)
	})
}

// TestWlcUpdates tests the main WLC updates function
func TestWlcUpdates(t *testing.T) {
	t.Run("No WLC virtual services returns empty updates", func(t *testing.T) {
		config := &ffi.BalancerManagerConfig{
			Balancer: ffi.BalancerConfig{
				Handler: ffi.PacketHandlerConfig{
					VirtualServices: []ffi.VsConfig{
						{
							Identifier: ffi.VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           80,
								TransportProto: ffi.VsTransportProtoTcp,
							},
							Reals: []ffi.RealConfig{
								{
									Identifier: ffi.RelativeRealIdentifier{
										Addr: netip.MustParseAddr(
											"192.168.1.1",
										),
										Port: 8080,
									},
									Weight: 100,
								},
							},
						},
					},
				},
			},
			Wlc: ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{}, // No WLC VS
			},
		}
		graph := &ffi.BalancerGraph{
			VirtualServices: []ffi.GraphVs{
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.1"),
						Port:           80,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.GraphReal{
						{
							Identifier: ffi.RelativeRealIdentifier{
								Addr: netip.MustParseAddr("192.168.1.1"),
								Port: 8080,
							},
							Weight:  50,
							Enabled: true,
						},
					},
				},
			},
		}
		info := &ffi.BalancerInfo{
			Vs: []ffi.VsInfo{
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.1"),
						Port:           80,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.RealInfo{
						{
							Dst:            netip.MustParseAddr("192.168.1.1"),
							ActiveSessions: 100,
						},
					},
				},
			},
		}

		updates := WlcUpdates(config, graph, info)
		assert.Empty(t, updates)
	})

	t.Run("Single WLC virtual service", func(t *testing.T) {
		config := &ffi.BalancerManagerConfig{
			Balancer: ffi.BalancerConfig{
				Handler: ffi.PacketHandlerConfig{
					VirtualServices: []ffi.VsConfig{
						{
							Identifier: ffi.VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           80,
								TransportProto: ffi.VsTransportProtoTcp,
							},
							Reals: []ffi.RealConfig{
								{
									Identifier: ffi.RelativeRealIdentifier{
										Addr: netip.MustParseAddr(
											"192.168.1.1",
										),
										Port: 8080,
									},
									Weight: 100,
								},
							},
						},
					},
				},
			},
			Wlc: ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0}, // VS index 0 has WLC
			},
		}
		graph := &ffi.BalancerGraph{
			VirtualServices: []ffi.GraphVs{
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.1"),
						Port:           80,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.GraphReal{
						{
							Identifier: ffi.RelativeRealIdentifier{
								Addr: netip.MustParseAddr("192.168.1.1"),
								Port: 8080,
							},
							Weight:  50, // Different from expected
							Enabled: true,
						},
					},
				},
			},
		}
		info := &ffi.BalancerInfo{
			Vs: []ffi.VsInfo{
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.1"),
						Port:           80,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.RealInfo{
						{
							Dst:            netip.MustParseAddr("192.168.1.1"),
							ActiveSessions: 50,
						},
					},
				},
			},
		}

		updates := WlcUpdates(config, graph, info)
		// connectionsSum=50, weightSum=100, connectionsSum < weightSum
		// newWeight = 100, but graph.Weight=50, so update generated
		require.Len(t, updates, 1)
		assert.Equal(t, uint16(100), updates[0].Weight)
	})

	t.Run("Multiple WLC virtual services", func(t *testing.T) {
		config := &ffi.BalancerManagerConfig{
			Balancer: ffi.BalancerConfig{
				Handler: ffi.PacketHandlerConfig{
					VirtualServices: []ffi.VsConfig{
						{
							Identifier: ffi.VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           80,
								TransportProto: ffi.VsTransportProtoTcp,
							},
							Reals: []ffi.RealConfig{
								{
									Identifier: ffi.RelativeRealIdentifier{
										Addr: netip.MustParseAddr(
											"192.168.1.1",
										),
										Port: 8080,
									},
									Weight: 100,
								},
							},
						},
						{
							Identifier: ffi.VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.2"),
								Port:           443,
								TransportProto: ffi.VsTransportProtoTcp,
							},
							Reals: []ffi.RealConfig{
								{
									Identifier: ffi.RelativeRealIdentifier{
										Addr: netip.MustParseAddr(
											"192.168.2.1",
										),
										Port: 8443,
									},
									Weight: 200,
								},
							},
						},
					},
				},
			},
			Wlc: ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0, 1}, // Both VS have WLC
			},
		}
		graph := &ffi.BalancerGraph{
			VirtualServices: []ffi.GraphVs{
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.1"),
						Port:           80,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.GraphReal{
						{
							Identifier: ffi.RelativeRealIdentifier{
								Addr: netip.MustParseAddr("192.168.1.1"),
								Port: 8080,
							},
							Weight:  50,
							Enabled: true,
						},
					},
				},
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.2"),
						Port:           443,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.GraphReal{
						{
							Identifier: ffi.RelativeRealIdentifier{
								Addr: netip.MustParseAddr("192.168.2.1"),
								Port: 8443,
							},
							Weight:  100,
							Enabled: true,
						},
					},
				},
			},
		}
		info := &ffi.BalancerInfo{
			Vs: []ffi.VsInfo{
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.1"),
						Port:           80,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.RealInfo{
						{
							Dst:            netip.MustParseAddr("192.168.1.1"),
							ActiveSessions: 50,
						},
					},
				},
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.2"),
						Port:           443,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.RealInfo{
						{
							Dst:            netip.MustParseAddr("192.168.2.1"),
							ActiveSessions: 100,
						},
					},
				},
			},
		}

		updates := WlcUpdates(config, graph, info)
		// Both VS should generate updates since graph weights differ from calculated
		require.Len(t, updates, 2)
	})

	t.Run("Mixed WLC and non-WLC virtual services", func(t *testing.T) {
		config := &ffi.BalancerManagerConfig{
			Balancer: ffi.BalancerConfig{
				Handler: ffi.PacketHandlerConfig{
					VirtualServices: []ffi.VsConfig{
						{
							Identifier: ffi.VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           80,
								TransportProto: ffi.VsTransportProtoTcp,
							},
							Reals: []ffi.RealConfig{
								{
									Identifier: ffi.RelativeRealIdentifier{
										Addr: netip.MustParseAddr(
											"192.168.1.1",
										),
										Port: 8080,
									},
									Weight: 100,
								},
							},
						},
						{
							Identifier: ffi.VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.2"),
								Port:           443,
								TransportProto: ffi.VsTransportProtoTcp,
							},
							Reals: []ffi.RealConfig{
								{
									Identifier: ffi.RelativeRealIdentifier{
										Addr: netip.MustParseAddr(
											"192.168.2.1",
										),
										Port: 8443,
									},
									Weight: 200,
								},
							},
						},
						{
							Identifier: ffi.VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.3"),
								Port:           8080,
								TransportProto: ffi.VsTransportProtoTcp,
							},
							Reals: []ffi.RealConfig{
								{
									Identifier: ffi.RelativeRealIdentifier{
										Addr: netip.MustParseAddr(
											"192.168.3.1",
										),
										Port: 9090,
									},
									Weight: 150,
								},
							},
						},
					},
				},
			},
			Wlc: ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs: []uint32{
					0,
					2,
				}, // Only VS 0 and 2 have WLC (not VS 1)
			},
		}
		graph := &ffi.BalancerGraph{
			VirtualServices: []ffi.GraphVs{
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.1"),
						Port:           80,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.GraphReal{
						{
							Identifier: ffi.RelativeRealIdentifier{
								Addr: netip.MustParseAddr("192.168.1.1"),
								Port: 8080,
							},
							Weight:  50,
							Enabled: true,
						},
					},
				},
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.2"),
						Port:           443,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.GraphReal{
						{
							Identifier: ffi.RelativeRealIdentifier{
								Addr: netip.MustParseAddr("192.168.2.1"),
								Port: 8443,
							},
							Weight:  50, // Different, but VS 1 is not WLC
							Enabled: true,
						},
					},
				},
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.3"),
						Port:           8080,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.GraphReal{
						{
							Identifier: ffi.RelativeRealIdentifier{
								Addr: netip.MustParseAddr("192.168.3.1"),
								Port: 9090,
							},
							Weight:  75,
							Enabled: true,
						},
					},
				},
			},
		}
		info := &ffi.BalancerInfo{
			Vs: []ffi.VsInfo{
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.1"),
						Port:           80,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.RealInfo{
						{
							Dst:            netip.MustParseAddr("192.168.1.1"),
							ActiveSessions: 50,
						},
					},
				},
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.2"),
						Port:           443,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.RealInfo{
						{
							Dst:            netip.MustParseAddr("192.168.2.1"),
							ActiveSessions: 100,
						},
					},
				},
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.3"),
						Port:           8080,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.RealInfo{
						{
							Dst:            netip.MustParseAddr("192.168.3.1"),
							ActiveSessions: 75,
						},
					},
				},
			},
		}

		updates := WlcUpdates(config, graph, info)
		// Only VS 0 and VS 2 should be processed (WLC enabled)
		// VS 1 should be skipped even though its weight differs
		require.Len(t, updates, 2)

		// Verify updates are for VS 0 and VS 2 only
		for _, update := range updates {
			vsAddr := update.Identifier.VsIdentifier.Addr
			assert.True(
				t,
				vsAddr == netip.MustParseAddr("10.0.0.1") ||
					vsAddr == netip.MustParseAddr("10.0.0.3"),
				"Update should be for VS 0 or VS 2, got VS with addr %s",
				vsAddr,
			)
		}
	})

	t.Run("Empty virtual services", func(t *testing.T) {
		config := &ffi.BalancerManagerConfig{
			Balancer: ffi.BalancerConfig{
				Handler: ffi.PacketHandlerConfig{
					VirtualServices: []ffi.VsConfig{},
				},
			},
			Wlc: ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0}, // WLC enabled for non-existent VS
			},
		}
		graph := &ffi.BalancerGraph{
			VirtualServices: []ffi.GraphVs{},
		}
		info := &ffi.BalancerInfo{
			Vs: []ffi.VsInfo{},
		}

		updates := WlcUpdates(config, graph, info)
		assert.Empty(t, updates)
	})

	t.Run("IPv6 virtual service", func(t *testing.T) {
		config := &ffi.BalancerManagerConfig{
			Balancer: ffi.BalancerConfig{
				Handler: ffi.PacketHandlerConfig{
					VirtualServices: []ffi.VsConfig{
						{
							Identifier: ffi.VsIdentifier{
								Addr: netip.MustParseAddr(
									"2001:db8::1",
								),
								Port:           80,
								TransportProto: ffi.VsTransportProtoTcp,
							},
							Reals: []ffi.RealConfig{
								{
									Identifier: ffi.RelativeRealIdentifier{
										Addr: netip.MustParseAddr(
											"2001:db8::100",
										),
										Port: 8080,
									},
									Weight: 100,
								},
							},
						},
					},
				},
			},
			Wlc: ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0},
			},
		}
		graph := &ffi.BalancerGraph{
			VirtualServices: []ffi.GraphVs{
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("2001:db8::1"),
						Port:           80,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.GraphReal{
						{
							Identifier: ffi.RelativeRealIdentifier{
								Addr: netip.MustParseAddr("2001:db8::100"),
								Port: 8080,
							},
							Weight:  50,
							Enabled: true,
						},
					},
				},
			},
		}
		info := &ffi.BalancerInfo{
			Vs: []ffi.VsInfo{
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("2001:db8::1"),
						Port:           80,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Reals: []ffi.RealInfo{
						{
							Dst: netip.MustParseAddr(
								"2001:db8::100",
							),
							ActiveSessions: 50,
						},
					},
				},
			},
		}

		updates := WlcUpdates(config, graph, info)
		require.Len(t, updates, 1)
		assert.Equal(
			t,
			netip.MustParseAddr("2001:db8::1"),
			updates[0].Identifier.VsIdentifier.Addr,
		)
		assert.Equal(
			t,
			netip.MustParseAddr("2001:db8::100"),
			updates[0].Identifier.Relative.Addr,
		)
	})

	t.Run("UDP transport protocol", func(t *testing.T) {
		config := &ffi.BalancerManagerConfig{
			Balancer: ffi.BalancerConfig{
				Handler: ffi.PacketHandlerConfig{
					VirtualServices: []ffi.VsConfig{
						{
							Identifier: ffi.VsIdentifier{
								Addr:           netip.MustParseAddr("10.0.0.1"),
								Port:           53,
								TransportProto: ffi.VsTransportProtoUdp,
							},
							Reals: []ffi.RealConfig{
								{
									Identifier: ffi.RelativeRealIdentifier{
										Addr: netip.MustParseAddr(
											"192.168.1.1",
										),
										Port: 5353,
									},
									Weight: 100,
								},
							},
						},
					},
				},
			},
			Wlc: ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0},
			},
		}
		graph := &ffi.BalancerGraph{
			VirtualServices: []ffi.GraphVs{
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.1"),
						Port:           53,
						TransportProto: ffi.VsTransportProtoUdp,
					},
					Reals: []ffi.GraphReal{
						{
							Identifier: ffi.RelativeRealIdentifier{
								Addr: netip.MustParseAddr("192.168.1.1"),
								Port: 5353,
							},
							Weight:  50,
							Enabled: true,
						},
					},
				},
			},
		}
		info := &ffi.BalancerInfo{
			Vs: []ffi.VsInfo{
				{
					Identifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.1"),
						Port:           53,
						TransportProto: ffi.VsTransportProtoUdp,
					},
					Reals: []ffi.RealInfo{
						{
							Dst:            netip.MustParseAddr("192.168.1.1"),
							ActiveSessions: 50,
						},
					},
				},
			},
		}

		updates := WlcUpdates(config, graph, info)
		require.Len(t, updates, 1)
		assert.Equal(
			t,
			ffi.VsTransportProtoUdp,
			updates[0].Identifier.VsIdentifier.TransportProto,
		)
	})
}
