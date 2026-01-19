package balancer

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
)

// TestBuildConfigWeightsMap tests the helper function that builds config weights lookup map
func TestBuildConfigWeightsMap(t *testing.T) {
	tests := []struct {
		name     string
		config   *ffi.BalancerManagerConfig
		expected map[vsRealKey]uint16
	}{
		{
			name: "Empty virtual services",
			config: &ffi.BalancerManagerConfig{
				Balancer: ffi.BalancerConfig{
					Handler: ffi.PacketHandlerConfig{
						VirtualServices: []ffi.VsConfig{},
					},
				},
			},
			expected: map[vsRealKey]uint16{},
		},
		{
			name: "Single VS with single real",
			config: &ffi.BalancerManagerConfig{
				Balancer: ffi.BalancerConfig{
					Handler: ffi.PacketHandlerConfig{
						VirtualServices: []ffi.VsConfig{
							{
								Identifier: ffi.VsIdentifier{
									Addr: netip.MustParseAddr(
										"10.0.0.1",
									),
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
			},
			expected: map[vsRealKey]uint16{
				{
					vsAddr:   "10.0.0.1",
					vsPort:   80,
					vsProto:  ffi.VsTransportProtoTcp,
					realAddr: "192.168.1.1",
					realPort: 8080,
				}: 100,
			},
		},
		{
			name: "Multiple VS with multiple reals",
			config: &ffi.BalancerManagerConfig{
				Balancer: ffi.BalancerConfig{
					Handler: ffi.PacketHandlerConfig{
						VirtualServices: []ffi.VsConfig{
							{
								Identifier: ffi.VsIdentifier{
									Addr: netip.MustParseAddr(
										"10.0.0.1",
									),
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
									{
										Identifier: ffi.RelativeRealIdentifier{
											Addr: netip.MustParseAddr(
												"192.168.1.2",
											),
											Port: 8080,
										},
										Weight: 200,
									},
								},
							},
							{
								Identifier: ffi.VsIdentifier{
									Addr: netip.MustParseAddr(
										"10.0.0.2",
									),
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
										Weight: 150,
									},
								},
							},
						},
					},
				},
			},
			expected: map[vsRealKey]uint16{
				{
					vsAddr:   "10.0.0.1",
					vsPort:   80,
					vsProto:  ffi.VsTransportProtoTcp,
					realAddr: "192.168.1.1",
					realPort: 8080,
				}: 100,
				{
					vsAddr:   "10.0.0.1",
					vsPort:   80,
					vsProto:  ffi.VsTransportProtoTcp,
					realAddr: "192.168.1.2",
					realPort: 8080,
				}: 200,
				{
					vsAddr:   "10.0.0.2",
					vsPort:   443,
					vsProto:  ffi.VsTransportProtoTcp,
					realAddr: "192.168.2.1",
					realPort: 8443,
				}: 150,
			},
		},
		{
			name: "IPv6 addresses",
			config: &ffi.BalancerManagerConfig{
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
										Weight: 250,
									},
								},
							},
						},
					},
				},
			},
			expected: map[vsRealKey]uint16{
				{
					vsAddr:   "2001:db8::1",
					vsPort:   80,
					vsProto:  ffi.VsTransportProtoTcp,
					realAddr: "2001:db8::100",
					realPort: 8080,
				}: 250,
			},
		},
		{
			name: "UDP transport protocol",
			config: &ffi.BalancerManagerConfig{
				Balancer: ffi.BalancerConfig{
					Handler: ffi.PacketHandlerConfig{
						VirtualServices: []ffi.VsConfig{
							{
								Identifier: ffi.VsIdentifier{
									Addr: netip.MustParseAddr(
										"10.0.0.1",
									),
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
										Weight: 75,
									},
								},
							},
						},
					},
				},
			},
			expected: map[vsRealKey]uint16{
				{
					vsAddr:   "10.0.0.1",
					vsPort:   53,
					vsProto:  ffi.VsTransportProtoUdp,
					realAddr: "192.168.1.1",
					realPort: 5353,
				}: 75,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildConfigWeightsMap(tt.config)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestConvertGraphToProtoWithConfig tests the main Graph conversion function
// that merges FFI graph with config to produce protobuf Graph with correct
// Weight (from config) and EffectiveWeight (from graph)
func TestConvertGraphToProtoWithConfig(t *testing.T) {
	tests := []struct {
		name   string
		graph  *ffi.BalancerGraph
		config *ffi.BalancerManagerConfig
		verify func(t *testing.T, result *balancerpb.Graph)
	}{
		{
			name: "Empty graph returns empty VirtualServices",
			graph: &ffi.BalancerGraph{
				VirtualServices: []ffi.GraphVs{},
			},
			config: &ffi.BalancerManagerConfig{
				Balancer: ffi.BalancerConfig{
					Handler: ffi.PacketHandlerConfig{
						VirtualServices: []ffi.VsConfig{},
					},
				},
			},
			verify: func(t *testing.T, result *balancerpb.Graph) {
				assert.NotNil(t, result)
				assert.Empty(t, result.VirtualServices)
			},
		},
		{
			name: "Basic single VS with single real - Weight from config, EffectiveWeight from graph",
			graph: &ffi.BalancerGraph{
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
								Weight:  150, // Effective weight (e.g., after WLC adjustment)
								Enabled: true,
							},
						},
					},
				},
			},
			config: &ffi.BalancerManagerConfig{
				Balancer: ffi.BalancerConfig{
					Handler: ffi.PacketHandlerConfig{
						VirtualServices: []ffi.VsConfig{
							{
								Identifier: ffi.VsIdentifier{
									Addr: netip.MustParseAddr(
										"10.0.0.1",
									),
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
										Weight: 100, // Config weight
									},
								},
							},
						},
					},
				},
			},
			verify: func(t *testing.T, result *balancerpb.Graph) {
				require.Len(t, result.VirtualServices, 1)
				vs := result.VirtualServices[0]
				require.Len(t, vs.Reals, 1)
				real := vs.Reals[0]

				// Weight should come from config
				assert.Equal(
					t,
					uint32(100),
					real.Weight,
					"Weight should be from config",
				)
				// EffectiveWeight should come from graph
				assert.Equal(
					t,
					uint32(150),
					real.EffectiveWeight,
					"EffectiveWeight should be from graph",
				)
				assert.True(t, real.Enabled)
			},
		},
		{
			name: "Multiple reals with different weights",
			graph: &ffi.BalancerGraph{
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
								Weight:  120, // Effective weight
								Enabled: true,
							},
							{
								Identifier: ffi.RelativeRealIdentifier{
									Addr: netip.MustParseAddr("192.168.1.2"),
									Port: 8080,
								},
								Weight:  180, // Effective weight
								Enabled: true,
							},
							{
								Identifier: ffi.RelativeRealIdentifier{
									Addr: netip.MustParseAddr("192.168.1.3"),
									Port: 8081,
								},
								Weight:  75, // Effective weight
								Enabled: false,
							},
						},
					},
				},
			},
			config: &ffi.BalancerManagerConfig{
				Balancer: ffi.BalancerConfig{
					Handler: ffi.PacketHandlerConfig{
						VirtualServices: []ffi.VsConfig{
							{
								Identifier: ffi.VsIdentifier{
									Addr: netip.MustParseAddr(
										"10.0.0.1",
									),
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
										Weight: 100, // Config weight
									},
									{
										Identifier: ffi.RelativeRealIdentifier{
											Addr: netip.MustParseAddr(
												"192.168.1.2",
											),
											Port: 8080,
										},
										Weight: 200, // Config weight
									},
									{
										Identifier: ffi.RelativeRealIdentifier{
											Addr: netip.MustParseAddr(
												"192.168.1.3",
											),
											Port: 8081,
										},
										Weight: 50, // Config weight
									},
								},
							},
						},
					},
				},
			},
			verify: func(t *testing.T, result *balancerpb.Graph) {
				require.Len(t, result.VirtualServices, 1)
				vs := result.VirtualServices[0]
				require.Len(t, vs.Reals, 3)

				// Real 1: Config=100, Effective=120
				assert.Equal(t, uint32(100), vs.Reals[0].Weight)
				assert.Equal(t, uint32(120), vs.Reals[0].EffectiveWeight)
				assert.True(t, vs.Reals[0].Enabled)

				// Real 2: Config=200, Effective=180
				assert.Equal(t, uint32(200), vs.Reals[1].Weight)
				assert.Equal(t, uint32(180), vs.Reals[1].EffectiveWeight)
				assert.True(t, vs.Reals[1].Enabled)

				// Real 3: Config=50, Effective=75, Disabled
				assert.Equal(t, uint32(50), vs.Reals[2].Weight)
				assert.Equal(t, uint32(75), vs.Reals[2].EffectiveWeight)
				assert.False(t, vs.Reals[2].Enabled)
			},
		},
		{
			name: "Multiple virtual services",
			graph: &ffi.BalancerGraph{
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
								Weight:  150,
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
								Weight:  250,
								Enabled: true,
							},
							{
								Identifier: ffi.RelativeRealIdentifier{
									Addr: netip.MustParseAddr("192.168.2.2"),
									Port: 8443,
								},
								Weight:  300,
								Enabled: true,
							},
						},
					},
				},
			},
			config: &ffi.BalancerManagerConfig{
				Balancer: ffi.BalancerConfig{
					Handler: ffi.PacketHandlerConfig{
						VirtualServices: []ffi.VsConfig{
							{
								Identifier: ffi.VsIdentifier{
									Addr: netip.MustParseAddr(
										"10.0.0.1",
									),
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
									Addr: netip.MustParseAddr(
										"10.0.0.2",
									),
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
									{
										Identifier: ffi.RelativeRealIdentifier{
											Addr: netip.MustParseAddr(
												"192.168.2.2",
											),
											Port: 8443,
										},
										Weight: 300,
									},
								},
							},
						},
					},
				},
			},
			verify: func(t *testing.T, result *balancerpb.Graph) {
				require.Len(t, result.VirtualServices, 2)

				// VS 1
				vs1 := result.VirtualServices[0]
				require.Len(t, vs1.Reals, 1)
				assert.Equal(t, uint32(100), vs1.Reals[0].Weight)
				assert.Equal(t, uint32(150), vs1.Reals[0].EffectiveWeight)

				// VS 2
				vs2 := result.VirtualServices[1]
				require.Len(t, vs2.Reals, 2)
				assert.Equal(t, uint32(200), vs2.Reals[0].Weight)
				assert.Equal(t, uint32(250), vs2.Reals[0].EffectiveWeight)
				assert.Equal(t, uint32(300), vs2.Reals[1].Weight)
				assert.Equal(t, uint32(300), vs2.Reals[1].EffectiveWeight)
			},
		},
		{
			name: "Real in graph but not in config - Weight defaults to 0",
			graph: &ffi.BalancerGraph{
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
								Weight:  150,
								Enabled: true,
							},
							{
								Identifier: ffi.RelativeRealIdentifier{
									Addr: netip.MustParseAddr(
										"192.168.1.99",
									), // Not in config
									Port: 9999,
								},
								Weight:  200,
								Enabled: true,
							},
						},
					},
				},
			},
			config: &ffi.BalancerManagerConfig{
				Balancer: ffi.BalancerConfig{
					Handler: ffi.PacketHandlerConfig{
						VirtualServices: []ffi.VsConfig{
							{
								Identifier: ffi.VsIdentifier{
									Addr: netip.MustParseAddr(
										"10.0.0.1",
									),
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
									// Note: 192.168.1.99:9999 is NOT in config
								},
							},
						},
					},
				},
			},
			verify: func(t *testing.T, result *balancerpb.Graph) {
				require.Len(t, result.VirtualServices, 1)
				vs := result.VirtualServices[0]
				require.Len(t, vs.Reals, 2)

				// Real 1: In config
				assert.Equal(t, uint32(100), vs.Reals[0].Weight)
				assert.Equal(t, uint32(150), vs.Reals[0].EffectiveWeight)

				// Real 2: NOT in config - Weight should be 0
				assert.Equal(
					t,
					uint32(0),
					vs.Reals[1].Weight,
					"Weight should be 0 for real not in config",
				)
				assert.Equal(
					t,
					uint32(200),
					vs.Reals[1].EffectiveWeight,
					"EffectiveWeight should still come from graph",
				)
			},
		},
		{
			name: "IPv6 addresses",
			graph: &ffi.BalancerGraph{
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
								Weight:  180,
								Enabled: true,
							},
						},
					},
				},
			},
			config: &ffi.BalancerManagerConfig{
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
										Weight: 150,
									},
								},
							},
						},
					},
				},
			},
			verify: func(t *testing.T, result *balancerpb.Graph) {
				require.Len(t, result.VirtualServices, 1)
				vs := result.VirtualServices[0]

				// Verify VS identifier is IPv6
				vsAddr, _ := netip.AddrFromSlice(vs.Identifier.Addr.Bytes)
				assert.True(t, vsAddr.Is6())
				assert.Equal(t, "2001:db8::1", vsAddr.String())

				require.Len(t, vs.Reals, 1)
				real := vs.Reals[0]

				// Verify real identifier is IPv6
				realAddr, _ := netip.AddrFromSlice(real.Identifier.Ip.Bytes)
				assert.True(t, realAddr.Is6())
				assert.Equal(t, "2001:db8::100", realAddr.String())

				assert.Equal(t, uint32(150), real.Weight)
				assert.Equal(t, uint32(180), real.EffectiveWeight)
			},
		},
		{
			name: "UDP transport protocol",
			graph: &ffi.BalancerGraph{
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
								Weight:  90,
								Enabled: true,
							},
						},
					},
				},
			},
			config: &ffi.BalancerManagerConfig{
				Balancer: ffi.BalancerConfig{
					Handler: ffi.PacketHandlerConfig{
						VirtualServices: []ffi.VsConfig{
							{
								Identifier: ffi.VsIdentifier{
									Addr: netip.MustParseAddr(
										"10.0.0.1",
									),
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
										Weight: 75,
									},
								},
							},
						},
					},
				},
			},
			verify: func(t *testing.T, result *balancerpb.Graph) {
				require.Len(t, result.VirtualServices, 1)
				vs := result.VirtualServices[0]

				assert.Equal(
					t,
					balancerpb.TransportProto_UDP,
					vs.Identifier.Proto,
				)
				require.Len(t, vs.Reals, 1)
				assert.Equal(t, uint32(75), vs.Reals[0].Weight)
				assert.Equal(t, uint32(90), vs.Reals[0].EffectiveWeight)
			},
		},
		{
			name: "Disabled real with zero effective weight",
			graph: &ffi.BalancerGraph{
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
								Weight:  0, // Disabled real may have 0 effective weight
								Enabled: false,
							},
						},
					},
				},
			},
			config: &ffi.BalancerManagerConfig{
				Balancer: ffi.BalancerConfig{
					Handler: ffi.PacketHandlerConfig{
						VirtualServices: []ffi.VsConfig{
							{
								Identifier: ffi.VsIdentifier{
									Addr: netip.MustParseAddr(
										"10.0.0.1",
									),
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
										Weight: 100, // Config weight is still set
									},
								},
							},
						},
					},
				},
			},
			verify: func(t *testing.T, result *balancerpb.Graph) {
				require.Len(t, result.VirtualServices, 1)
				vs := result.VirtualServices[0]
				require.Len(t, vs.Reals, 1)
				real := vs.Reals[0]

				// Config weight should still be present
				assert.Equal(
					t,
					uint32(100),
					real.Weight,
					"Config weight should be preserved for disabled real",
				)
				// Effective weight is 0 because real is disabled
				assert.Equal(
					t,
					uint32(0),
					real.EffectiveWeight,
					"EffectiveWeight should be 0 for disabled real",
				)
				assert.False(t, real.Enabled)
			},
		},
		{
			name: "WLC adjusted weights - effective weight differs from config",
			graph: &ffi.BalancerGraph{
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
								Weight:  250, // WLC increased weight (less loaded)
								Enabled: true,
							},
							{
								Identifier: ffi.RelativeRealIdentifier{
									Addr: netip.MustParseAddr("192.168.1.2"),
									Port: 8080,
								},
								Weight:  50, // WLC decreased weight (more loaded)
								Enabled: true,
							},
						},
					},
				},
			},
			config: &ffi.BalancerManagerConfig{
				Balancer: ffi.BalancerConfig{
					Handler: ffi.PacketHandlerConfig{
						VirtualServices: []ffi.VsConfig{
							{
								Identifier: ffi.VsIdentifier{
									Addr: netip.MustParseAddr(
										"10.0.0.1",
									),
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
										Weight: 100, // Both have same config weight
									},
									{
										Identifier: ffi.RelativeRealIdentifier{
											Addr: netip.MustParseAddr(
												"192.168.1.2",
											),
											Port: 8080,
										},
										Weight: 100, // Both have same config weight
									},
								},
							},
						},
					},
				},
			},
			verify: func(t *testing.T, result *balancerpb.Graph) {
				require.Len(t, result.VirtualServices, 1)
				vs := result.VirtualServices[0]
				require.Len(t, vs.Reals, 2)

				// Both reals have same config weight but different effective weights
				assert.Equal(
					t,
					uint32(100),
					vs.Reals[0].Weight,
					"Config weight should be same",
				)
				assert.Equal(
					t,
					uint32(250),
					vs.Reals[0].EffectiveWeight,
					"WLC increased weight for less loaded server",
				)

				assert.Equal(
					t,
					uint32(100),
					vs.Reals[1].Weight,
					"Config weight should be same",
				)
				assert.Equal(
					t,
					uint32(50),
					vs.Reals[1].EffectiveWeight,
					"WLC decreased weight for more loaded server",
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertGraphToProtoWithConfig(tt.graph, tt.config)
			tt.verify(t, result)
		})
	}
}
