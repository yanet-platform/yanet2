package balancer

import (
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
	"google.golang.org/protobuf/types/known/durationpb"
)

// TestNewRealUpdateFromProto_Valid tests valid real update conversion
func TestNewRealUpdateFromProto_Valid(t *testing.T) {
	tests := []struct {
		name     string
		proto    *balancerpb.RealUpdate
		expected *ffi.RealUpdate
	}{
		{
			name: "Complete update with weight and enable",
			proto: &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("192.168.1.100").
								AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
						},
						Port: 8080,
					},
				},
				Weight: ptrUint32(100),
				Enable: ptrBool(true),
			},
			expected: &ffi.RealUpdate{
				Identifier: ffi.RealIdentifier{
					VsIdentifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("192.168.1.100"),
						Port:           80,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Relative: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("10.0.0.1"),
						Port: 8080,
					},
				},
				Weight:  100,
				Enabled: 1,
			},
		},
		{
			name: "Update with only weight",
			proto: &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
						},
						Port:  443,
						Proto: balancerpb.TransportProto_UDP,
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("172.16.0.1").AsSlice(),
						},
						Port: 0,
					},
				},
				Weight: ptrUint32(200),
			},
			expected: &ffi.RealUpdate{
				Identifier: ffi.RealIdentifier{
					VsIdentifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.1"),
						Port:           443,
						TransportProto: ffi.VsTransportProtoUdp,
					},
					Relative: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("172.16.0.1"),
						Port: 0,
					},
				},
				Weight:  200,
				Enabled: ffi.DontUpdateRealEnabled,
			},
		},
		{
			name: "Update with only enable",
			proto: &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("2001:db8::1").AsSlice(),
						},
						Port:  53,
						Proto: balancerpb.TransportProto_UDP,
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("2001:db8::100").
								AsSlice(),
						},
						Port: 5353,
					},
				},
				Enable: ptrBool(false),
			},
			expected: &ffi.RealUpdate{
				Identifier: ffi.RealIdentifier{
					VsIdentifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("2001:db8::1"),
						Port:           53,
						TransportProto: ffi.VsTransportProtoUdp,
					},
					Relative: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("2001:db8::100"),
						Port: 5353,
					},
				},
				Weight:  ffi.DontUpdateRealWeight,
				Enabled: 0,
			},
		},
		{
			name: "Update with neither weight nor enable",
			proto: &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.10.10.10").AsSlice(),
						},
						Port:  8080,
						Proto: balancerpb.TransportProto_TCP,
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.10.10.11").AsSlice(),
						},
						Port: 9090,
					},
				},
			},
			expected: &ffi.RealUpdate{
				Identifier: ffi.RealIdentifier{
					VsIdentifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.10.10.10"),
						Port:           8080,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Relative: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("10.10.10.11"),
						Port: 9090,
					},
				},
				Weight:  ffi.DontUpdateRealWeight,
				Enabled: ffi.DontUpdateRealEnabled,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := NewRealUpdateFromProto(tt.proto)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestNewRealUpdateFromProto_Errors tests error cases
func TestNewRealUpdateFromProto_Errors(t *testing.T) {
	tests := []struct {
		name  string
		proto *balancerpb.RealUpdate
	}{
		{
			name: "Nil real_id",
			proto: &balancerpb.RealUpdate{
				RealId: nil,
			},
		},
		{
			name: "Nil VS in real_id",
			proto: &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs: nil,
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
						},
					},
				},
			},
		},
		{
			name: "Nil real in real_id",
			proto: &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Real: nil,
				},
			},
		},
		{
			name: "Invalid VS IP address",
			proto: &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("0.1.2.3").AsSlice()[:3],
						}, // Invalid length
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
						},
					},
				},
			},
		},
		{
			name: "Invalid real IP address",
			proto: &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("0.0.1.2").AsSlice()[:2],
						}, // Invalid length
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRealUpdateFromProto(tt.proto)
			assert.Error(t, err)
		})
	}
}

// TestProtoToFFIConfig_Valid tests valid config conversion
func TestProtoToFFIConfig_Valid(t *testing.T) {
	config := &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 10,
				TcpSyn:    20,
				TcpFin:    15,
				Tcp:       100,
				Udp:       50,
				Default:   30,
			},
			Vs: []*balancerpb.VirtualService{
				{
					Id: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("192.168.1.100").
								AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
					Reals: []*balancerpb.Real{
						{
							Id: &balancerpb.RelativeRealIdentifier{
								Ip: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.0.0.1").
										AsSlice(),
								},
								Port: 8080,
							},
							Weight: 100,
							SrcAddr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("172.16.0.0").
									AsSlice(),
							},
							SrcMask: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("255.255.255.0").
									AsSlice(),
							},
						},
					},
					Flags: &balancerpb.VsFlags{
						FixMss: true,
					},
				},
			},
			SourceAddressV4: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("2001:db8::1").AsSlice(),
			},
			DecapAddresses: []*balancerpb.Addr{
				{Bytes: netip.MustParseAddr("192.168.1.1").AsSlice()},
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      ptrUint64(1000),
			SessionTableMaxLoadFactor: ptrFloat32(0.75),
			RefreshPeriod:             durationpb.New(5 * time.Second),
		},
	}

	result, err := ProtoToFFIConfig(config)
	require.NoError(t, err)
	assert.Equal(t, uint(1000), result.State.TableCapacity)
	assert.Len(t, result.Handler.VirtualServices, 1)
	assert.Equal(t, netip.MustParseAddr("10.0.0.1"), result.Handler.SourceV4)
	assert.Equal(t, netip.MustParseAddr("2001:db8::1"), result.Handler.SourceV6)
}

// TestProtoToFFIConfig_MissingRequiredFields tests missing field validation
func TestProtoToFFIConfig_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		config *balancerpb.BalancerConfig
	}{
		{
			name: "Missing packet_handler",
			config: &balancerpb.BalancerConfig{
				State: &balancerpb.StateConfig{
					SessionTableCapacity:      ptrUint64(1000),
					SessionTableMaxLoadFactor: ptrFloat32(0.75),
					RefreshPeriod:             durationpb.New(5 * time.Second),
				},
			},
		},
		{
			name: "Missing state",
			config: &balancerpb.BalancerConfig{
				PacketHandler: &balancerpb.PacketHandlerConfig{
					SessionsTimeouts: &balancerpb.SessionsTimeouts{},
					Vs:               []*balancerpb.VirtualService{},
					SourceAddressV4: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
					},
					SourceAddressV6: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("::1").AsSlice(),
					},
					DecapAddresses: []*balancerpb.Addr{},
				},
			},
		},
		{
			name: "Missing session_table_capacity",
			config: &balancerpb.BalancerConfig{
				PacketHandler: &balancerpb.PacketHandlerConfig{
					SessionsTimeouts: &balancerpb.SessionsTimeouts{},
					Vs:               []*balancerpb.VirtualService{},
					SourceAddressV4: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
					},
					SourceAddressV6: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("::1").AsSlice(),
					},
					DecapAddresses: []*balancerpb.Addr{},
				},
				State: &balancerpb.StateConfig{
					SessionTableMaxLoadFactor: ptrFloat32(0.75),
					RefreshPeriod:             durationpb.New(5 * time.Second),
				},
			},
		},
		{
			name: "Missing session_table_max_load_factor",
			config: &balancerpb.BalancerConfig{
				PacketHandler: &balancerpb.PacketHandlerConfig{
					SessionsTimeouts: &balancerpb.SessionsTimeouts{},
					Vs:               []*balancerpb.VirtualService{},
					SourceAddressV4: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
					},
					SourceAddressV6: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("::1").AsSlice(),
					},
					DecapAddresses: []*balancerpb.Addr{},
				},
				State: &balancerpb.StateConfig{
					SessionTableCapacity: ptrUint64(1000),
					RefreshPeriod:        durationpb.New(5 * time.Second),
				},
			},
		},
		{
			name: "Missing refresh_period",
			config: &balancerpb.BalancerConfig{
				PacketHandler: &balancerpb.PacketHandlerConfig{
					SessionsTimeouts: &balancerpb.SessionsTimeouts{},
					Vs:               []*balancerpb.VirtualService{},
					SourceAddressV4: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
					},
					SourceAddressV6: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("::1").AsSlice(),
					},
					DecapAddresses: []*balancerpb.Addr{},
				},
				State: &balancerpb.StateConfig{
					SessionTableCapacity:      ptrUint64(1000),
					SessionTableMaxLoadFactor: ptrFloat32(0.75),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ProtoToFFIConfig(tt.config)
			assert.Error(t, err)
		})
	}
}

// TestProtoToManagerConfig_WLCValidation tests WLC field interdependencies
func TestProtoToManagerConfig_WLCValidation(t *testing.T) {
	baseConfig := func() *balancerpb.BalancerConfig {
		return &balancerpb.BalancerConfig{
			PacketHandler: &balancerpb.PacketHandlerConfig{
				SessionsTimeouts: &balancerpb.SessionsTimeouts{
					TcpSynAck: 10,
					TcpSyn:    20,
					TcpFin:    15,
					Tcp:       100,
					Udp:       50,
					Default:   30,
				},
				Vs: []*balancerpb.VirtualService{},
				SourceAddressV4: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
				},
				SourceAddressV6: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("::1").AsSlice(),
				},
				DecapAddresses: []*balancerpb.Addr{},
			},
			State: &balancerpb.StateConfig{
				SessionTableCapacity: ptrUint64(1000),
			},
		}
	}

	tests := []struct {
		name      string
		config    *balancerpb.BalancerConfig
		shouldErr bool
	}{
		{
			name: "All three WLC fields present - valid",
			config: func() *balancerpb.BalancerConfig {
				c := baseConfig()
				c.State.RefreshPeriod = durationpb.New(5 * time.Second)
				c.State.SessionTableMaxLoadFactor = ptrFloat32(0.75)
				c.State.Wlc = &balancerpb.WlcConfig{
					Power:     ptrUint64(2),
					MaxWeight: ptrUint32(1000),
				}
				return c
			}(),
			shouldErr: false,
		},
		{
			name: "None present - valid",
			config: func() *balancerpb.BalancerConfig {
				c := baseConfig()
				return c
			}(),
			shouldErr: false,
		},
		{
			name: "Only refresh_period - invalid",
			config: func() *balancerpb.BalancerConfig {
				c := baseConfig()
				c.State.RefreshPeriod = durationpb.New(5 * time.Second)
				return c
			}(),
			shouldErr: true,
		},
		{
			name: "Only max_load_factor - invalid",
			config: func() *balancerpb.BalancerConfig {
				c := baseConfig()
				c.State.SessionTableMaxLoadFactor = ptrFloat32(0.75)
				return c
			}(),
			shouldErr: true,
		},
		{
			name: "Only WLC - invalid",
			config: func() *balancerpb.BalancerConfig {
				c := baseConfig()
				c.State.Wlc = &balancerpb.WlcConfig{
					Power:     ptrUint64(2),
					MaxWeight: ptrUint32(1000),
				}
				return c
			}(),
			shouldErr: true,
		},
		{
			name: "refresh_period and max_load_factor without WLC - invalid",
			config: func() *balancerpb.BalancerConfig {
				c := baseConfig()
				c.State.RefreshPeriod = durationpb.New(5 * time.Second)
				c.State.SessionTableMaxLoadFactor = ptrFloat32(0.75)
				return c
			}(),
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ProtoToManagerConfig(tt.config)
			if tt.shouldErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestProtoToHandlerConfig_Valid tests handler config conversion
func TestProtoToHandlerConfig_Valid(t *testing.T) {
	config := &balancerpb.PacketHandlerConfig{
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 10,
			TcpSyn:    20,
			TcpFin:    15,
			Tcp:       100,
			Udp:       50,
			Default:   30,
		},
		Vs: []*balancerpb.VirtualService{
			{
				Id: &balancerpb.VsIdentifier{
					Addr: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("192.168.1.100").AsSlice(),
					},
					Port:  80,
					Proto: balancerpb.TransportProto_TCP,
				},
				Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
				Reals:     []*balancerpb.Real{},
			},
		},
		SourceAddressV4: &balancerpb.Addr{
			Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
		},
		SourceAddressV6: &balancerpb.Addr{
			Bytes: netip.MustParseAddr("2001:db8::1").AsSlice(),
		},
		DecapAddresses: []*balancerpb.Addr{
			{Bytes: netip.MustParseAddr("192.168.1.1").AsSlice()},
			{Bytes: netip.MustParseAddr("2001:db8::100").AsSlice()},
		},
	}

	result, err := ProtoToHandlerConfig(config)
	require.NoError(t, err)
	assert.Equal(t, uint32(10), result.SessionsTimeouts.TcpSynAck)
	assert.Len(t, result.VirtualServices, 1)
	assert.Len(t, result.DecapV4, 1)
	assert.Len(t, result.DecapV6, 1)
}

// TestProtoToHandlerConfig_MissingFields tests missing field validation
func TestProtoToHandlerConfig_MissingFields(t *testing.T) {
	tests := []struct {
		name   string
		config *balancerpb.PacketHandlerConfig
	}{
		{
			name: "Missing sessions_timeouts",
			config: &balancerpb.PacketHandlerConfig{
				Vs: []*balancerpb.VirtualService{},
				SourceAddressV4: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
				},
				SourceAddressV6: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("::1").AsSlice(),
				},
				DecapAddresses: []*balancerpb.Addr{},
			},
		},
		{
			name: "Missing source_address_v4",
			config: &balancerpb.PacketHandlerConfig{
				SessionsTimeouts: &balancerpb.SessionsTimeouts{},
				Vs:               []*balancerpb.VirtualService{},
				SourceAddressV6: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("::1").AsSlice(),
				},
				DecapAddresses: []*balancerpb.Addr{},
			},
		},
		{
			name: "Missing source_address_v6",
			config: &balancerpb.PacketHandlerConfig{
				SessionsTimeouts: &balancerpb.SessionsTimeouts{},
				Vs:               []*balancerpb.VirtualService{},
				SourceAddressV4: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
				},
				DecapAddresses: []*balancerpb.Addr{},
			},
		},
		{
			name: "Missing decap_addresses",
			config: &balancerpb.PacketHandlerConfig{
				SessionsTimeouts: &balancerpb.SessionsTimeouts{},
				Vs:               []*balancerpb.VirtualService{},
				SourceAddressV4: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
				},
				SourceAddressV6: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("::1").AsSlice(),
				},
			},
		},
		{
			name: "Missing vs",
			config: &balancerpb.PacketHandlerConfig{
				SessionsTimeouts: &balancerpb.SessionsTimeouts{},
				SourceAddressV4: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
				},
				SourceAddressV6: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("::1").AsSlice(),
				},
				DecapAddresses: []*balancerpb.Addr{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ProtoToHandlerConfig(tt.config)
			assert.Error(t, err)
		})
	}
}

// TestProtoToHandlerConfig_InvalidSourceAddresses tests invalid source address validation
func TestProtoToHandlerConfig_InvalidSourceAddresses(t *testing.T) {
	tests := []struct {
		name   string
		config *balancerpb.PacketHandlerConfig
	}{
		{
			name: "Invalid IPv4 source address length",
			config: &balancerpb.PacketHandlerConfig{
				SessionsTimeouts: &balancerpb.SessionsTimeouts{},
				Vs:               []*balancerpb.VirtualService{},
				SourceAddressV4: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("10.0.0.1").AsSlice()[:3],
				}, // Only 3 bytes
				SourceAddressV6: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("::1").AsSlice(),
				},
				DecapAddresses: []*balancerpb.Addr{},
			},
		},
		{
			name: "Invalid IPv6 source address length",
			config: &balancerpb.PacketHandlerConfig{
				SessionsTimeouts: &balancerpb.SessionsTimeouts{},
				Vs:               []*balancerpb.VirtualService{},
				SourceAddressV4: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
				},
				SourceAddressV6: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("0.0.0.0").AsSlice(),
				}, // Only 4 bytes
				DecapAddresses: []*balancerpb.Addr{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ProtoToHandlerConfig(tt.config)
			assert.Error(t, err)
		})
	}
}

// TestProtoToRealConfig_ZeroWeight tests weight validation
func TestProtoToRealConfig_ZeroWeight(t *testing.T) {
	real := &balancerpb.Real{
		Id: &balancerpb.RelativeRealIdentifier{
			Ip: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
			},
			Port: 8080,
		},
		Weight: 0, // Invalid
		SrcAddr: &balancerpb.Addr{
			Bytes: netip.MustParseAddr("172.16.0.0").AsSlice(),
		},
		SrcMask: &balancerpb.Addr{
			Bytes: netip.MustParseAddr("255.255.255.0").AsSlice(),
		},
	}

	_, err := protoToRealConfig(real)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "weight must be at least 1")
}

// TestCreateWlcConfig tests WLC config creation
func TestCreateWlcConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    *balancerpb.BalancerConfig
		expected  ffi.BalancerManagerWlcConfig
		shouldErr bool
	}{
		{
			name: "No WLC enabled",
			config: &balancerpb.BalancerConfig{
				PacketHandler: &balancerpb.PacketHandlerConfig{
					Vs: []*balancerpb.VirtualService{
						{
							Flags: &balancerpb.VsFlags{Wlc: false},
						},
					},
				},
				State: &balancerpb.StateConfig{},
			},
			expected: ffi.BalancerManagerWlcConfig{
				Power:         0,
				MaxRealWeight: 0,
				Vs:            []uint32{},
			},
			shouldErr: false,
		},
		{
			name: "WLC enabled with valid config",
			config: &balancerpb.BalancerConfig{
				PacketHandler: &balancerpb.PacketHandlerConfig{
					Vs: []*balancerpb.VirtualService{
						{
							Flags: &balancerpb.VsFlags{Wlc: true},
						},
						{
							Flags: &balancerpb.VsFlags{Wlc: false},
						},
						{
							Flags: &balancerpb.VsFlags{Wlc: true},
						},
					},
				},
				State: &balancerpb.StateConfig{
					Wlc: &balancerpb.WlcConfig{
						Power:     ptrUint64(2),
						MaxWeight: ptrUint32(1000),
					},
				},
			},
			expected: ffi.BalancerManagerWlcConfig{
				Power:         2,
				MaxRealWeight: 1000,
				Vs:            []uint32{0, 2},
			},
			shouldErr: false,
		},
		{
			name: "WLC enabled but missing power",
			config: &balancerpb.BalancerConfig{
				PacketHandler: &balancerpb.PacketHandlerConfig{
					Vs: []*balancerpb.VirtualService{
						{
							Flags: &balancerpb.VsFlags{Wlc: true},
						},
					},
				},
				State: &balancerpb.StateConfig{
					Wlc: &balancerpb.WlcConfig{
						MaxWeight: ptrUint32(1000),
					},
				},
			},
			shouldErr: true,
		},
		{
			name: "WLC enabled but missing max_weight",
			config: &balancerpb.BalancerConfig{
				PacketHandler: &balancerpb.PacketHandlerConfig{
					Vs: []*balancerpb.VirtualService{
						{
							Flags: &balancerpb.VsFlags{Wlc: true},
						},
					},
				},
				State: &balancerpb.StateConfig{
					Wlc: &balancerpb.WlcConfig{
						Power: ptrUint64(2),
					},
				},
			},
			shouldErr: true,
		},
		{
			name: "WLC enabled but no config",
			config: &balancerpb.BalancerConfig{
				PacketHandler: &balancerpb.PacketHandlerConfig{
					Vs: []*balancerpb.VirtualService{
						{
							Flags: &balancerpb.VsFlags{Wlc: true},
						},
					},
				},
				State: &balancerpb.StateConfig{},
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := createWlcConfig(tt.config)
			if tt.shouldErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

// TestMergeBalancerConfig tests config merging for UPDATE mode
func TestMergeBalancerConfig(t *testing.T) {
	currentConfig := &ffi.BalancerManagerConfig{
		Balancer: ffi.BalancerConfig{
			Handler: ffi.PacketHandlerConfig{
				SessionsTimeouts: ffi.SessionsTimeouts{
					TcpSynAck: 10,
					TcpSyn:    20,
					TcpFin:    15,
					Tcp:       100,
					Udp:       50,
					Default:   30,
				},
				VirtualServices: []ffi.VsConfig{},
				SourceV4:        netip.MustParseAddr("10.0.0.1"),
				SourceV6:        netip.MustParseAddr("::1"),
				DecapV4:         []netip.Addr{},
				DecapV6:         []netip.Addr{},
			},
			State: ffi.StateConfig{
				TableCapacity: 1000,
			},
		},
		RefreshPeriod: 5 * time.Second,
		MaxLoadFactor: 0.75,
		Wlc: ffi.BalancerManagerWlcConfig{
			Power:         2,
			MaxRealWeight: 1000,
			Vs:            []uint32{},
		},
	}

	tests := []struct {
		name      string
		newConfig *balancerpb.BalancerConfig
		verify    func(t *testing.T, result *balancerpb.BalancerConfig)
	}{
		{
			name: "Full update with all fields",
			newConfig: &balancerpb.BalancerConfig{
				PacketHandler: &balancerpb.PacketHandlerConfig{
					SessionsTimeouts: &balancerpb.SessionsTimeouts{
						TcpSynAck: 15,
						TcpSyn:    25,
						TcpFin:    20,
						Tcp:       120,
						Udp:       60,
						Default:   40,
					},
					Vs: []*balancerpb.VirtualService{},
					SourceAddressV4: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("192.168.1.1").AsSlice(),
					},
					SourceAddressV6: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("2001:db8::1").AsSlice(),
					},
					DecapAddresses: []*balancerpb.Addr{},
				},
				State: &balancerpb.StateConfig{
					SessionTableCapacity:      ptrUint64(2000),
					SessionTableMaxLoadFactor: ptrFloat32(0.85),
					RefreshPeriod:             durationpb.New(10 * time.Second),
					Wlc: &balancerpb.WlcConfig{
						Power:     ptrUint64(3),
						MaxWeight: ptrUint32(2000),
					},
				},
			},
			verify: func(t *testing.T, result *balancerpb.BalancerConfig) {
				assert.Equal(
					t,
					uint32(15),
					result.PacketHandler.SessionsTimeouts.TcpSynAck,
				)
				assert.Equal(
					t,
					uint64(2000),
					*result.State.SessionTableCapacity,
				)
				assert.Equal(
					t,
					float32(0.85),
					*result.State.SessionTableMaxLoadFactor,
				)
				assert.Equal(
					t,
					10*time.Second,
					result.State.RefreshPeriod.AsDuration(),
				)
			},
		},
		{
			name: "Partial update - only state fields",
			newConfig: &balancerpb.BalancerConfig{
				State: &balancerpb.StateConfig{
					SessionTableCapacity: ptrUint64(3000),
				},
			},
			verify: func(t *testing.T, result *balancerpb.BalancerConfig) {
				// Handler should be from current config
				assert.Equal(
					t,
					uint32(10),
					result.PacketHandler.SessionsTimeouts.TcpSynAck,
				)
				// State capacity should be updated
				assert.Equal(
					t,
					uint64(3000),
					*result.State.SessionTableCapacity,
				)
				// Other state fields should be from current config
				assert.Equal(
					t,
					float32(0.75),
					*result.State.SessionTableMaxLoadFactor,
				)
			},
		},
		{
			name: "Partial update - only handler",
			newConfig: &balancerpb.BalancerConfig{
				PacketHandler: &balancerpb.PacketHandlerConfig{
					SessionsTimeouts: &balancerpb.SessionsTimeouts{
						TcpSynAck: 25,
						TcpSyn:    35,
						TcpFin:    30,
						Tcp:       150,
						Udp:       70,
						Default:   50,
					},
					Vs: []*balancerpb.VirtualService{},
					SourceAddressV4: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("172.16.0.1").AsSlice(),
					},
					SourceAddressV6: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("fe80::1").AsSlice(),
					},
					DecapAddresses: []*balancerpb.Addr{},
				},
			},
			verify: func(t *testing.T, result *balancerpb.BalancerConfig) {
				// Handler should be updated
				assert.Equal(
					t,
					uint32(25),
					result.PacketHandler.SessionsTimeouts.TcpSynAck,
				)
				// State should be from current config
				assert.Equal(
					t,
					uint64(1000),
					*result.State.SessionTableCapacity,
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := mergeBalancerConfig(tt.newConfig, currentConfig)
			require.NoError(t, err)
			tt.verify(t, result)
		})
	}
}

// TestConvertFFIRealUpdateToProto tests FFI to proto conversion
func TestConvertFFIRealUpdateToProto(t *testing.T) {
	tests := []struct {
		name     string
		update   *ffi.RealUpdate
		expected *balancerpb.RealUpdate
	}{
		{
			name: "Update with weight and enabled",
			update: &ffi.RealUpdate{
				Identifier: ffi.RealIdentifier{
					VsIdentifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("192.168.1.100"),
						Port:           80,
						TransportProto: ffi.VsTransportProtoTcp,
					},
					Relative: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("10.0.0.1"),
						Port: 8080,
					},
				},
				Weight:  100,
				Enabled: 1,
			},
			expected: &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("192.168.1.100").
								AsSlice(),
						},
						Port:  80,
						Proto: balancerpb.TransportProto_TCP,
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
						},
						Port: 8080,
					},
				},
				Weight: ptrUint32(100),
				Enable: ptrBool(true),
			},
		},
		{
			name: "Update with DontUpdateRealWeight",
			update: &ffi.RealUpdate{
				Identifier: ffi.RealIdentifier{
					VsIdentifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("10.0.0.1"),
						Port:           443,
						TransportProto: ffi.VsTransportProtoUdp,
					},
					Relative: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("172.16.0.1"),
						Port: 8443,
					},
				},
				Weight:  ffi.DontUpdateRealWeight,
				Enabled: 0,
			},
			expected: &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
						},
						Port:  443,
						Proto: balancerpb.TransportProto_UDP,
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("172.16.0.1").AsSlice(),
						},
						Port: 8443,
					},
				},
				Weight: nil,
				Enable: ptrBool(false),
			},
		},
		{
			name: "Update with DontUpdateRealEnabled",
			update: &ffi.RealUpdate{
				Identifier: ffi.RealIdentifier{
					VsIdentifier: ffi.VsIdentifier{
						Addr:           netip.MustParseAddr("2001:db8::1"),
						Port:           53,
						TransportProto: ffi.VsTransportProtoUdp,
					},
					Relative: ffi.RelativeRealIdentifier{
						Addr: netip.MustParseAddr("2001:db8::100"),
						Port: 5353,
					},
				},
				Weight:  200,
				Enabled: ffi.DontUpdateRealEnabled,
			},
			expected: &balancerpb.RealUpdate{
				RealId: &balancerpb.RealIdentifier{
					Vs: &balancerpb.VsIdentifier{
						Addr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("2001:db8::1").AsSlice(),
						},
						Port:  53,
						Proto: balancerpb.TransportProto_UDP,
					},
					Real: &balancerpb.RelativeRealIdentifier{
						Ip: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("2001:db8::100").
								AsSlice(),
						},
						Port: 5353,
					},
				},
				Weight: ptrUint32(200),
				Enable: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ConvertFFIRealUpdateToProto(tt.update)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestProtoToVsConfig_PureL3Mode tests Pure L3 mode validation
func TestProtoToVsConfig_PureL3Mode(t *testing.T) {
	vs := &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
			},
			Port:  0, // Must be 0 for Pure L3
			Proto: balancerpb.TransportProto_TCP,
		},
		Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
		Reals:     []*balancerpb.Real{},
		Flags: &balancerpb.VsFlags{
			PureL3: true,
		},
	}

	result, err := protoToVsConfig(vs)
	require.NoError(t, err)
	assert.True(t, result.Flags.PureL3)
	assert.Equal(t, uint16(0), result.Identifier.Port)
}

// TestProtoToVsConfig_AllFlags tests all flag combinations
func TestProtoToVsConfig_AllFlags(t *testing.T) {
	tests := []struct {
		name  string
		flags *balancerpb.VsFlags
	}{
		{
			name: "All flags enabled",
			flags: &balancerpb.VsFlags{
				Gre:    true,
				FixMss: true,
				Ops:    true,
				PureL3: false, // Can't be true with port != 0
				Wlc:    true,
			},
		},
		{
			name: "All flags disabled",
			flags: &balancerpb.VsFlags{
				Gre:    false,
				FixMss: false,
				Ops:    false,
				PureL3: false,
				Wlc:    false,
			},
		},
		{
			name: "Mixed flags",
			flags: &balancerpb.VsFlags{
				Gre:    true,
				FixMss: false,
				Ops:    true,
				PureL3: false,
				Wlc:    false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := &balancerpb.VirtualService{
				Id: &balancerpb.VsIdentifier{
					Addr: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("192.168.1.100").AsSlice(),
					},
					Port:  80,
					Proto: balancerpb.TransportProto_TCP,
				},
				Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
				Reals:     []*balancerpb.Real{},
				Flags:     tt.flags,
			}

			result, err := protoToVsConfig(vs)
			require.NoError(t, err)
			assert.Equal(t, tt.flags.Gre, result.Flags.GRE)
			assert.Equal(t, tt.flags.FixMss, result.Flags.FixMSS)
			assert.Equal(t, tt.flags.Ops, result.Flags.OPS)
			assert.Equal(t, tt.flags.PureL3, result.Flags.PureL3)
		})
	}
}

// TestProtoToVsConfig_Schedulers tests both scheduler types
func TestProtoToVsConfig_Schedulers(t *testing.T) {
	tests := []struct {
		name      string
		scheduler balancerpb.VsScheduler
		expected  ffi.VsScheduler
	}{
		{
			name:      "SOURCE_HASH",
			scheduler: balancerpb.VsScheduler_SOURCE_HASH,
			expected:  ffi.VsSchedulerSourceHash,
		},
		{
			name:      "ROUND_ROBIN",
			scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
			expected:  ffi.VsSchedulerRoundRobin,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := &balancerpb.VirtualService{
				Id: &balancerpb.VsIdentifier{
					Addr: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("192.168.1.100").AsSlice(),
					},
					Port:  80,
					Proto: balancerpb.TransportProto_TCP,
				},
				Scheduler: tt.scheduler,
				Reals:     []*balancerpb.Real{},
			}

			result, err := protoToVsConfig(vs)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Scheduler)
		})
	}
}

// TestProtoToVsConfig_EmptyArrays tests with empty optional arrays
func TestProtoToVsConfig_EmptyArrays(t *testing.T) {
	vs := &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr: &balancerpb.Addr{
				Bytes: netip.MustParseAddr("192.168.1.100").AsSlice(),
			},
			Port:  80,
			Proto: balancerpb.TransportProto_TCP,
		},
		Scheduler:   balancerpb.VsScheduler_SOURCE_HASH,
		Reals:       []*balancerpb.Real{},
		AllowedSrcs: []*balancerpb.Net{},
		Peers:       []*balancerpb.Addr{},
	}

	result, err := protoToVsConfig(vs)
	require.NoError(t, err)
	assert.Empty(t, result.Reals)
	assert.Empty(t, result.AllowedSrc)
	assert.Empty(t, result.PeersV4)
	assert.Empty(t, result.PeersV6)
}

// TestProtoToRealConfig_SourcePrefix tests source prefix conversion
func TestProtoToRealConfig_SourcePrefix(t *testing.T) {
	tests := []struct {
		name     string
		srcAddr  []byte
		srcMask  []byte
		expected xnetip.NetWithMask
	}{
		{
			name:     "IPv4 /24",
			srcAddr:  netip.MustParseAddr("172.16.0.0").AsSlice(),
			srcMask:  []byte{255, 255, 255, 0},
			expected: xnetip.FromPrefix(netip.MustParsePrefix("172.16.0.0/24")),
		},
		{
			name:     "IPv4 /16",
			srcAddr:  netip.MustParseAddr("10.0.0.0").AsSlice(),
			srcMask:  []byte{255, 255, 0, 0},
			expected: xnetip.FromPrefix(netip.MustParsePrefix("10.0.0.0/16")),
		},
		{
			name:     "IPv6 /64",
			srcAddr:  netip.MustParseAddr("2001:db8::").AsSlice(),
			srcMask:  netip.MustParseAddr("ffff:ffff:ffff:ffff::").AsSlice(),
			expected: xnetip.FromPrefix(netip.MustParsePrefix("2001:db8::/64")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			real := &balancerpb.Real{
				Id: &balancerpb.RelativeRealIdentifier{
					Ip: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
					},
					Port: 8080,
				},
				Weight:  100,
				SrcAddr: &balancerpb.Addr{Bytes: tt.srcAddr},
				SrcMask: &balancerpb.Addr{Bytes: tt.srcMask},
			}

			result, err := protoToRealConfig(real)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result.Src)
		})
	}
}

// Helper functions
func ptrUint32(v uint32) *uint32 {
	return &v
}

func ptrUint64(v uint64) *uint64 {
	return &v
}

func ptrFloat32(v float32) *float32 {
	return &v
}

func ptrBool(v bool) *bool {
	return &v
}

// TestConvertPacketHandlerToProtoWithWlc tests WLC-aware packet handler conversion
func TestConvertPacketHandlerToProtoWithWlc(t *testing.T) {
	handler := &ffi.PacketHandlerConfig{
		SessionsTimeouts: ffi.SessionsTimeouts{
			TcpSynAck: 10,
			TcpSyn:    20,
			TcpFin:    15,
			Tcp:       100,
			Udp:       50,
			Default:   30,
		},
		VirtualServices: []ffi.VsConfig{
			{
				Identifier: ffi.VsIdentifier{
					Addr:           netip.MustParseAddr("192.168.1.100"),
					Port:           80,
					TransportProto: ffi.VsTransportProtoTcp,
				},
				Scheduler: ffi.VsSchedulerRoundRobin,
				Reals:     []ffi.RealConfig{},
			},
			{
				Identifier: ffi.VsIdentifier{
					Addr:           netip.MustParseAddr("192.168.1.101"),
					Port:           443,
					TransportProto: ffi.VsTransportProtoTcp,
				},
				Scheduler: ffi.VsSchedulerRoundRobin,
				Reals:     []ffi.RealConfig{},
			},
			{
				Identifier: ffi.VsIdentifier{
					Addr:           netip.MustParseAddr("192.168.1.102"),
					Port:           8080,
					TransportProto: ffi.VsTransportProtoTcp,
				},
				Scheduler: ffi.VsSchedulerRoundRobin,
				Reals:     []ffi.RealConfig{},
			},
		},
		SourceV4: netip.MustParseAddr("10.0.0.1"),
		SourceV6: netip.MustParseAddr("2001:db8::1"),
		DecapV4:  []netip.Addr{},
		DecapV6:  []netip.Addr{},
	}

	tests := []struct {
		name      string
		wlcConfig *ffi.BalancerManagerWlcConfig
		verify    func(t *testing.T, result *balancerpb.PacketHandlerConfig)
	}{
		{
			name:      "No WLC config",
			wlcConfig: nil,
			verify: func(t *testing.T, result *balancerpb.PacketHandlerConfig) {
				require.Len(t, result.Vs, 3)
				assert.False(
					t,
					result.Vs[0].Flags.Wlc,
					"VS0 should have WLC=false",
				)
				assert.False(
					t,
					result.Vs[1].Flags.Wlc,
					"VS1 should have WLC=false",
				)
				assert.False(
					t,
					result.Vs[2].Flags.Wlc,
					"VS2 should have WLC=false",
				)
			},
		},
		{
			name: "WLC enabled for VS 0 and 2",
			wlcConfig: &ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0, 2},
			},
			verify: func(t *testing.T, result *balancerpb.PacketHandlerConfig) {
				require.Len(t, result.Vs, 3)
				assert.True(
					t,
					result.Vs[0].Flags.Wlc,
					"VS0 should have WLC=true",
				)
				assert.False(
					t,
					result.Vs[1].Flags.Wlc,
					"VS1 should have WLC=false",
				)
				assert.True(
					t,
					result.Vs[2].Flags.Wlc,
					"VS2 should have WLC=true",
				)
			},
		},
		{
			name: "WLC enabled for all VSs",
			wlcConfig: &ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{0, 1, 2},
			},
			verify: func(t *testing.T, result *balancerpb.PacketHandlerConfig) {
				require.Len(t, result.Vs, 3)
				assert.True(
					t,
					result.Vs[0].Flags.Wlc,
					"VS0 should have WLC=true",
				)
				assert.True(
					t,
					result.Vs[1].Flags.Wlc,
					"VS1 should have WLC=true",
				)
				assert.True(
					t,
					result.Vs[2].Flags.Wlc,
					"VS2 should have WLC=true",
				)
			},
		},
		{
			name: "Empty WLC VS list",
			wlcConfig: &ffi.BalancerManagerWlcConfig{
				Power:         10,
				MaxRealWeight: 1000,
				Vs:            []uint32{},
			},
			verify: func(t *testing.T, result *balancerpb.PacketHandlerConfig) {
				require.Len(t, result.Vs, 3)
				assert.False(
					t,
					result.Vs[0].Flags.Wlc,
					"VS0 should have WLC=false",
				)
				assert.False(
					t,
					result.Vs[1].Flags.Wlc,
					"VS1 should have WLC=false",
				)
				assert.False(
					t,
					result.Vs[2].Flags.Wlc,
					"VS2 should have WLC=false",
				)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertPacketHandlerToProtoWithWlc(handler, tt.wlcConfig)
			require.NotNil(t, result)
			tt.verify(t, result)
		})
	}
}

// TestConvertVsConfigToProtoWithWlc tests WLC-aware VS config conversion
func TestConvertVsConfigToProtoWithWlc(t *testing.T) {
	vsConfig := &ffi.VsConfig{
		Identifier: ffi.VsIdentifier{
			Addr:           netip.MustParseAddr("192.168.1.100"),
			Port:           80,
			TransportProto: ffi.VsTransportProtoTcp,
		},
		Flags: ffi.VsFlags{
			GRE:    true,
			FixMSS: false,
			OPS:    true,
			PureL3: false,
		},
		Scheduler:  ffi.VsSchedulerRoundRobin,
		Reals:      []ffi.RealConfig{},
		AllowedSrc: []netip.Prefix{},
		PeersV4:    []netip.Addr{},
		PeersV6:    []netip.Addr{},
	}

	tests := []struct {
		name       string
		wlcEnabled bool
		verify     func(t *testing.T, result *balancerpb.VirtualService)
	}{
		{
			name:       "WLC disabled",
			wlcEnabled: false,
			verify: func(t *testing.T, result *balancerpb.VirtualService) {
				require.NotNil(t, result.Flags)
				assert.False(t, result.Flags.Wlc, "WLC should be false")
				assert.True(t, result.Flags.Gre, "GRE should be preserved")
				assert.True(t, result.Flags.Ops, "OPS should be preserved")
			},
		},
		{
			name:       "WLC enabled",
			wlcEnabled: true,
			verify: func(t *testing.T, result *balancerpb.VirtualService) {
				require.NotNil(t, result.Flags)
				assert.True(t, result.Flags.Wlc, "WLC should be true")
				assert.True(t, result.Flags.Gre, "GRE should be preserved")
				assert.True(t, result.Flags.Ops, "OPS should be preserved")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertVsConfigToProtoWithWlc(vsConfig, tt.wlcEnabled)
			require.NotNil(t, result)
			tt.verify(t, result)
		})
	}
}

// TestConvertBalancerConfigToProto_WithWlc tests full config conversion with WLC
func TestConvertBalancerConfigToProto_WithWlc(t *testing.T) {
	config := &ffi.BalancerManagerConfig{
		Balancer: ffi.BalancerConfig{
			Handler: ffi.PacketHandlerConfig{
				SessionsTimeouts: ffi.SessionsTimeouts{
					TcpSynAck: 10,
					TcpSyn:    20,
					TcpFin:    15,
					Tcp:       100,
					Udp:       50,
					Default:   30,
				},
				VirtualServices: []ffi.VsConfig{
					{
						Identifier: ffi.VsIdentifier{
							Addr: netip.MustParseAddr(
								"192.168.1.100",
							),
							Port:           80,
							TransportProto: ffi.VsTransportProtoTcp,
						},
						Scheduler: ffi.VsSchedulerRoundRobin,
						Reals:     []ffi.RealConfig{},
					},
					{
						Identifier: ffi.VsIdentifier{
							Addr: netip.MustParseAddr(
								"192.168.1.101",
							),
							Port:           443,
							TransportProto: ffi.VsTransportProtoTcp,
						},
						Scheduler: ffi.VsSchedulerRoundRobin,
						Reals:     []ffi.RealConfig{},
					},
				},
				SourceV4: netip.MustParseAddr("10.0.0.1"),
				SourceV6: netip.MustParseAddr("2001:db8::1"),
				DecapV4:  []netip.Addr{},
				DecapV6:  []netip.Addr{},
			},
			State: ffi.StateConfig{
				TableCapacity: 1000,
			},
		},
		RefreshPeriod: 5 * time.Second,
		MaxLoadFactor: 0.75,
		Wlc: ffi.BalancerManagerWlcConfig{
			Power:         10,
			MaxRealWeight: 1000,
			Vs:            []uint32{0}, // Only first VS has WLC enabled
		},
	}

	result := ConvertBalancerConfigToProto(config)
	require.NotNil(t, result)
	require.NotNil(t, result.PacketHandler)
	require.Len(t, result.PacketHandler.Vs, 2)

	// Verify WLC flags
	assert.True(
		t,
		result.PacketHandler.Vs[0].Flags.Wlc,
		"VS0 should have WLC=true",
	)
	assert.False(
		t,
		result.PacketHandler.Vs[1].Flags.Wlc,
		"VS1 should have WLC=false",
	)

	// Verify state config
	require.NotNil(t, result.State)
	assert.Equal(t, uint64(1000), *result.State.SessionTableCapacity)
	assert.Equal(t, float32(0.75), *result.State.SessionTableMaxLoadFactor)
	assert.Equal(t, 5*time.Second, result.State.RefreshPeriod.AsDuration())

	// Verify WLC config
	require.NotNil(t, result.State.Wlc)
	assert.Equal(t, uint64(10), *result.State.Wlc.Power)
	assert.Equal(t, uint32(1000), *result.State.Wlc.MaxWeight)
}
