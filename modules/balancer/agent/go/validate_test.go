package balancer

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
)

// TestValidation_InvalidPortRange tests that port ranges with from > to are rejected
func TestValidation_InvalidPortRange(t *testing.T) {
	config := &balancerpb.PacketHandlerConfig{
		SessionsTimeouts: &balancerpb.SessionsTimeouts{
			TcpSynAck: 10,
			TcpSyn:    20,
			TcpFin:    15,
			Tcp:       100,
			Udp:       11,
			Default:   19,
		},
		SourceAddressV4: &balancerpb.Addr{
			Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
		},
		SourceAddressV6: &balancerpb.Addr{
			Bytes: netip.MustParseAddr("2001:db8::1").AsSlice(),
		},
		DecapAddresses: []*balancerpb.Addr{},
		Vs: []*balancerpb.VirtualService{
			{
				Id: &balancerpb.VsIdentifier{
					Addr: &balancerpb.Addr{
						Bytes: netip.MustParseAddr("10.0.0.100").AsSlice(),
					},
					Port:  80,
					Proto: balancerpb.TransportProto_TCP,
				},
				Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
				Reals: []*balancerpb.Real{
					{
						Id: &balancerpb.RelativeRealIdentifier{
							Ip: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.0.1.1").
									AsSlice(),
							},
							Port: 8080,
						},
						Weight: 100,
						SrcAddr: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("172.16.0.0").AsSlice(),
						},
						SrcMask: &balancerpb.Addr{
							Bytes: netip.MustParseAddr("255.255.255.0").
								AsSlice(),
						},
					},
				},
				AllowedSrcs: []*balancerpb.AllowedSources{
					{
						Nets: []*balancerpb.Net{
							{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("192.168.0.0").
										AsSlice(),
								},
								Mask: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("255.255.0.0").
										AsSlice(),
								},
							},
						},
						Ports: []*balancerpb.PortsRange{
							{
								From: 8080, // Invalid: from > to
								To:   80,
							},
						},
					},
				},
			},
		},
	}

	_, err := ProtoToHandlerConfig(config)
	require.Error(t, err, "Expected error for invalid port range")
	assert.Contains(
		t,
		err.Error(),
		"invalid range",
		"Error should mention invalid range",
	)
	assert.Contains(
		t,
		err.Error(),
		"from=8080",
		"Error should mention from value",
	)
	assert.Contains(t, err.Error(), "to=80", "Error should mention to value")
}

// TestValidation_TransportProtoTcpAndUdp tests that only TCP and UDP protocols are handled
func TestValidation_TransportProtoTcpAndUdp(t *testing.T) {
	testCases := []struct {
		name  string
		proto balancerpb.TransportProto
	}{
		{
			name:  "TCP protocol",
			proto: balancerpb.TransportProto_TCP,
		},
		{
			name:  "UDP protocol",
			proto: balancerpb.TransportProto_UDP,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &balancerpb.PacketHandlerConfig{
				SessionsTimeouts: &balancerpb.SessionsTimeouts{
					TcpSynAck: 10,
					TcpSyn:    20,
					TcpFin:    15,
					Tcp:       100,
					Udp:       11,
					Default:   19,
				},
				SourceAddressV4: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
				},
				SourceAddressV6: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("2001:db8::1").AsSlice(),
				},
				DecapAddresses: []*balancerpb.Addr{},
				Vs: []*balancerpb.VirtualService{
					{
						Id: &balancerpb.VsIdentifier{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr("10.0.0.100").
									AsSlice(),
							},
							Port:  80,
							Proto: tc.proto,
						},
						Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
						Reals: []*balancerpb.Real{
							{
								Id: &balancerpb.RelativeRealIdentifier{
									Ip: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("10.0.1.1").
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
						AllowedSrcs: []*balancerpb.AllowedSources{
							{
								Nets: []*balancerpb.Net{
									{
										Addr: &balancerpb.Addr{
											Bytes: netip.MustParseAddr("192.168.0.0").
												AsSlice(),
										},
										Mask: &balancerpb.Addr{
											Bytes: netip.MustParseAddr("255.255.0.0").
												AsSlice(),
										},
									},
								},
							},
						},
					},
				},
			}

			result, err := ProtoToHandlerConfig(config)
			require.NoError(
				t,
				err,
				"Valid TCP/UDP protocol should not produce error",
			)
			require.NotNil(t, result, "Result should not be nil")
			require.Len(
				t,
				result.VirtualServices,
				1,
				"Should have one virtual service",
			)
		})
	}
}

// TestValidation_AllowedSrcIPVersionMismatch tests that allowed_src networks must match VS IP version
func TestValidation_AllowedSrcIPVersionMismatch(t *testing.T) {
	testCases := []struct {
		name           string
		vsAddr         string
		allowedSrcAddr string
		allowedSrcMask string
		expectError    bool
		errorContains  string
	}{
		{
			name:           "IPv4 VS with IPv6 allowed_src",
			vsAddr:         "10.0.0.100",
			allowedSrcAddr: "2001:db8::",
			allowedSrcMask: "ffff:ffff::",
			expectError:    true,
			errorContains:  "IP version",
		},
		{
			name:           "IPv6 VS with IPv4 allowed_src",
			vsAddr:         "2001:db8::100",
			allowedSrcAddr: "192.168.0.0",
			allowedSrcMask: "255.255.0.0",
			expectError:    true,
			errorContains:  "IP version",
		},
		{
			name:           "IPv4 VS with IPv4 allowed_src (valid)",
			vsAddr:         "10.0.0.100",
			allowedSrcAddr: "192.168.0.0",
			allowedSrcMask: "255.255.0.0",
			expectError:    false,
		},
		{
			name:           "IPv6 VS with IPv6 allowed_src (valid)",
			vsAddr:         "2001:db8::100",
			allowedSrcAddr: "2001:db8:1::",
			allowedSrcMask: "ffff:ffff:ffff::",
			expectError:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := &balancerpb.PacketHandlerConfig{
				SessionsTimeouts: &balancerpb.SessionsTimeouts{
					TcpSynAck: 10,
					TcpSyn:    20,
					TcpFin:    15,
					Tcp:       100,
					Udp:       11,
					Default:   19,
				},
				SourceAddressV4: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("10.0.0.1").AsSlice(),
				},
				SourceAddressV6: &balancerpb.Addr{
					Bytes: netip.MustParseAddr("2001:db8::1").AsSlice(),
				},
				DecapAddresses: []*balancerpb.Addr{},
				Vs: []*balancerpb.VirtualService{
					{
						Id: &balancerpb.VsIdentifier{
							Addr: &balancerpb.Addr{
								Bytes: netip.MustParseAddr(tc.vsAddr).AsSlice(),
							},
							Port:  80,
							Proto: balancerpb.TransportProto_TCP,
						},
						Scheduler: balancerpb.VsScheduler_SOURCE_HASH,
						Reals: []*balancerpb.Real{
							{
								Id: &balancerpb.RelativeRealIdentifier{
									Ip: &balancerpb.Addr{
										Bytes: netip.MustParseAddr("10.0.1.1").
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
						AllowedSrcs: []*balancerpb.AllowedSources{
							{
								Nets: []*balancerpb.Net{
									{
										Addr: &balancerpb.Addr{
											Bytes: netip.MustParseAddr(tc.allowedSrcAddr).
												AsSlice(),
										},
										Mask: &balancerpb.Addr{
											Bytes: netip.MustParseAddr(tc.allowedSrcMask).
												AsSlice(),
										},
									},
								},
							},
						},
					},
				},
			}

			_, err := ProtoToHandlerConfig(config)

			if tc.expectError {
				require.Error(t, err, "Expected error for IP version mismatch")
				assert.Contains(
					t,
					err.Error(),
					tc.errorContains,
					"Error should mention IP version",
				)
			} else {
				require.NoError(t, err, "Valid IP version match should not produce error")
			}
		})
	}
}
