package ffi

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
)

// Test helper to compare netip.Addr
func compareAddr(a, b netip.Addr) bool {
	return a.Compare(b) == 0
}

// Test helper to compare netip.Prefix
func comparePrefix(a, b netip.Prefix) bool {
	return a.Addr().Compare(b.Addr()) == 0 && a.Bits() == b.Bits()
}

// Test helper to compare xnetip.NetWithMask
func compareNetWithMask(a, b xnetip.NetWithMask) bool {
	if !compareAddr(a.Addr, b.Addr) {
		return false
	}
	if len(a.Mask) != len(b.Mask) {
		return false
	}
	for i := range a.Mask {
		if a.Mask[i] != b.Mask[i] {
			return false
		}
	}
	return true
}

// TestNetAddrConversion tests round-trip conversion of network addresses
func TestNetAddrConversion(t *testing.T) {
	tests := []struct {
		name string
		addr netip.Addr
		isV4 bool
	}{
		{
			name: "IPv4 localhost",
			addr: netip.MustParseAddr("127.0.0.1"),
			isV4: true,
		},
		{
			name: "IPv4 zero",
			addr: netip.MustParseAddr("0.0.0.0"),
			isV4: true,
		},
		{
			name: "IPv4 broadcast",
			addr: netip.MustParseAddr("255.255.255.255"),
			isV4: true,
		},
		{
			name: "IPv4 typical",
			addr: netip.MustParseAddr("192.168.1.100"),
			isV4: true,
		},
		{
			name: "IPv6 localhost",
			addr: netip.MustParseAddr("::1"),
			isV4: false,
		},
		{
			name: "IPv6 zero",
			addr: netip.MustParseAddr("::"),
			isV4: false,
		},
		{
			name: "IPv6 typical",
			addr: netip.MustParseAddr("2001:db8::1"),
			isV4: false,
		},
		{
			name: "IPv6 full",
			addr: netip.MustParseAddr(
				"2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			),
			isV4: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert Go -> C
			cAddr := goToC_NetAddr(tt.addr)

			// Convert C -> Go
			result := cToGo_NetAddr(cAddr, tt.isV4)

			// Compare
			if !compareAddr(tt.addr, result) {
				t.Errorf(
					"Round-trip conversion failed: got %v, want %v",
					result,
					tt.addr,
				)
			}
		})
	}
}

// TestNetConversion tests round-trip conversion of network prefixes
func TestNetConversion(t *testing.T) {
	tests := []struct {
		name string
		net  xnetip.NetWithMask
		isV4 bool
	}{
		{
			name: "IPv4 /32",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("192.168.1.1/32")),
			isV4: true,
		},
		{
			name: "IPv4 /24",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("192.168.1.0/24")),
			isV4: true,
		},
		{
			name: "IPv4 /17",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("192.168.0.0/17")),
			isV4: true,
		},
		{
			name: "IPv4 /16",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("192.168.0.0/16")),
			isV4: true,
		},
		{
			name: "IPv4 /11",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("10.0.0.0/11")),
			isV4: true,
		},
		{
			name: "IPv4 /8",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("10.0.0.0/8")),
			isV4: true,
		},
		{
			name: "IPv4 /0",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("0.0.0.0/0")),
			isV4: true,
		},
		{
			name: "IPv6 /128",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("2001:db8::1/128")),
			isV4: false,
		},
		{
			name: "IPv6 /73",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("2001:db8::/73")),
			isV4: false,
		},
		{
			name: "IPv6 /64",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("2001:db8::/64")),
			isV4: false,
		},
		{
			name: "IPv6 /49",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("2001:db8::/49")),
			isV4: false,
		},
		{
			name: "IPv6 /48",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("2001:db8::/48")),
			isV4: false,
		},
		{
			name: "IPv6 /37",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("2001:db8::/37")),
			isV4: false,
		},
		{
			name: "IPv6 /0",
			net:  xnetip.FromPrefix(netip.MustParsePrefix("::/0")),
			isV4: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert Go -> C
			cNet := goToC_Net(tt.net)

			// Convert C -> Go
			result := cToGo_Net(cNet, tt.isV4)

			// Compare
			if !compareNetWithMask(tt.net, result) {
				t.Errorf(
					"Round-trip conversion failed: got %v, want %v",
					result,
					tt.net,
				)
			}
		})
	}
}

// TestVsIdentifierConversion tests round-trip conversion of VS identifiers
func TestVsIdentifierConversion(t *testing.T) {
	tests := []struct {
		name string
		id   VsIdentifier
	}{
		{
			name: "IPv4 TCP VS",
			id: VsIdentifier{
				Addr: netip.MustParseAddr("192.168.1.100"),

				Port:           80,
				TransportProto: VsTransportProtoTcp,
			},
		},
		{
			name: "IPv4 UDP VS",
			id: VsIdentifier{
				Addr: netip.MustParseAddr("192.168.1.100"),

				Port:           53,
				TransportProto: VsTransportProtoUdp,
			},
		},
		{
			name: "IPv6 TCP VS",
			id: VsIdentifier{
				Addr: netip.MustParseAddr("2001:db8::1"),

				Port:           443,
				TransportProto: VsTransportProtoTcp,
			},
		},
		{
			name: "IPv6 UDP VS",
			id: VsIdentifier{
				Addr: netip.MustParseAddr("2001:db8::1"),

				Port:           53,
				TransportProto: VsTransportProtoUdp,
			},
		},
		{
			name: "Port zero (PureL3)",
			id: VsIdentifier{
				Addr: netip.MustParseAddr("10.0.0.1"),

				Port:           0,
				TransportProto: VsTransportProtoTcp,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert Go -> C
			cId := goToC_VsIdentifier(tt.id)

			// Convert C -> Go
			result := cToGo_VsIdentifier(cId)

			// Compare
			if diff := cmp.Diff(tt.id, result, cmp.Comparer(compareAddr)); diff != "" {
				t.Errorf(
					"Round-trip conversion mismatch (-want +got):\n%s",
					diff,
				)
			}
		})
	}
}

// TestRelativeRealIdentifierConversion tests round-trip conversion of relative real identifiers
func TestRelativeRealIdentifierConversion(t *testing.T) {
	tests := []struct {
		name string
		id   RelativeRealIdentifier
	}{
		{
			name: "IPv4 real",
			id: RelativeRealIdentifier{
				Addr: netip.MustParseAddr("10.0.0.1"),

				Port: 8080,
			},
		},
		{
			name: "IPv6 real",
			id: RelativeRealIdentifier{
				Addr: netip.MustParseAddr("2001:db8::100"),

				Port: 8080,
			},
		},
		{
			name: "Port zero",
			id: RelativeRealIdentifier{
				Addr: netip.MustParseAddr("192.168.1.1"),

				Port: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert Go -> C
			cId := goToC_RelativeRealIdentifier(tt.id)

			// Convert C -> Go
			result := cToGo_RelativeRealIdentifier(cId)

			// Compare
			if diff := cmp.Diff(tt.id, result, cmp.Comparer(compareAddr)); diff != "" {
				t.Errorf(
					"Round-trip conversion mismatch (-want +got):\n%s",
					diff,
				)
			}
		})
	}
}

// TestRealIdentifierConversion tests round-trip conversion of real identifiers
func TestRealIdentifierConversion(t *testing.T) {
	tests := []struct {
		name string
		id   RealIdentifier
	}{
		{
			name: "Complete IPv4 real",
			id: RealIdentifier{
				VsIdentifier: VsIdentifier{
					Addr: netip.MustParseAddr("192.168.1.100"),

					Port:           80,
					TransportProto: VsTransportProtoTcp,
				},
				Relative: RelativeRealIdentifier{
					Addr: netip.MustParseAddr("10.0.0.1"),

					Port: 8080,
				},
			},
		},
		{
			name: "Complete IPv6 real",
			id: RealIdentifier{
				VsIdentifier: VsIdentifier{
					Addr: netip.MustParseAddr("2001:db8::1"),

					Port:           443,
					TransportProto: VsTransportProtoUdp,
				},
				Relative: RelativeRealIdentifier{
					Addr: netip.MustParseAddr("2001:db8::100"),

					Port: 8443,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert Go -> C
			cId := goToC_RealIdentifier(tt.id)

			// Convert C -> Go
			result := cToGo_RealIdentifier(cId)

			// Compare
			if diff := cmp.Diff(tt.id, result, cmp.Comparer(compareAddr)); diff != "" {
				t.Errorf(
					"Round-trip conversion mismatch (-want +got):\n%s",
					diff,
				)
			}
		})
	}
}

// TestTimestampConversion tests round-trip conversion of timestamps
func TestTimestampConversion(t *testing.T) {
	tests := []struct {
		name string
		ts   time.Time
	}{
		{
			name: "Unix epoch",
			ts:   time.Unix(0, 0),
		},
		{
			name: "Current time",
			ts:   time.Unix(1700000000, 0),
		},
		{
			name: "Future time",
			ts:   time.Unix(2000000000, 0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert Go -> C
			cTs := goToC_Timestamp(tt.ts)

			// Convert C -> Go
			result := cToGo_Timestamp(cTs)

			// Compare (only seconds precision)
			if result.Unix() != tt.ts.Unix() {
				t.Errorf(
					"Round-trip conversion failed: got %v, want %v",
					result.Unix(),
					tt.ts.Unix(),
				)
			}
		})
	}
}

// TestVsConfigConversion tests round-trip conversion of VS configuration
func TestVsConfigConversion(t *testing.T) {
	tests := []struct {
		name   string
		config VsConfig
	}{
		{
			name: "Simple IPv4 VS with one real",
			config: VsConfig{
				Identifier: VsIdentifier{
					Addr: netip.MustParseAddr("192.168.1.100"),

					Port:           80,
					TransportProto: VsTransportProtoTcp,
				},
				Flags: VsFlags{
					PureL3: false,
					FixMSS: true,
					GRE:    false,
					OPS:    false,
				},
				Scheduler: VsSchedulerSourceHash,
				Reals: []RealConfig{
					{
						Identifier: RelativeRealIdentifier{
							Addr: netip.MustParseAddr("10.0.0.1"),
							Port: 8080,
						},
						Src: xnetip.FromPrefix(
							netip.MustParsePrefix("172.16.0.0/24"),
						),
						Weight: 100,
					},
				},
				AllowedSrc: []netip.Prefix{
					netip.MustParsePrefix("192.168.0.0/16"),
				},
				PeersV4: []netip.Addr{
					netip.MustParseAddr("192.168.1.1"),
					netip.MustParseAddr("192.168.1.2"),
				},
				PeersV6: []netip.Addr{},
			},
		},
		{
			name: "IPv6 VS with multiple reals",
			config: VsConfig{
				Identifier: VsIdentifier{
					Addr: netip.MustParseAddr("2001:db8::1"),

					Port:           443,
					TransportProto: VsTransportProtoTcp,
				},
				Flags: VsFlags{
					PureL3: false,
					FixMSS: false,
					GRE:    true,
					OPS:    false,
				},
				Scheduler: VsSchedulerRoundRobin,
				Reals: []RealConfig{
					{
						Identifier: RelativeRealIdentifier{
							Addr: netip.MustParseAddr("2001:db8::100"),
							Port: 8443,
						},
						Src: xnetip.FromPrefix(
							netip.MustParsePrefix("2001:db8:1::/64"),
						),
						Weight: 50,
					},
					{
						Identifier: RelativeRealIdentifier{
							Addr: netip.MustParseAddr("2001:db8::101"),
							Port: 8443,
						},
						Src: xnetip.FromPrefix(
							netip.MustParsePrefix("2001:db8:2::/64"),
						),
						Weight: 150,
					},
				},
				AllowedSrc: []netip.Prefix{
					netip.MustParsePrefix("2001:db8::/32"),
				},
				PeersV4: []netip.Addr{},
				PeersV6: []netip.Addr{
					netip.MustParseAddr("2001:db8::2"),
				},
			},
		},
		{
			name: "VS with PureL3 flag and multiple reals",
			config: VsConfig{
				Identifier: VsIdentifier{
					Addr: netip.MustParseAddr("10.0.0.1"),

					Port:           0,
					TransportProto: VsTransportProtoTcp,
				},
				Flags: VsFlags{
					PureL3: true,
					FixMSS: false,
					GRE:    false,
					OPS:    false,
				},
				Scheduler: VsSchedulerRoundRobin,
				Reals: []RealConfig{
					{
						Identifier: RelativeRealIdentifier{
							Addr: netip.MustParseAddr("10.0.0.10"),
							Port: 0,
						},
						Src: xnetip.FromPrefix(
							netip.MustParsePrefix("172.16.0.0/24"),
						),
						Weight: 100,
					},
					{
						Identifier: RelativeRealIdentifier{
							Addr: netip.MustParseAddr("10.0.0.11"),
							Port: 0,
						},
						Src: xnetip.FromPrefix(
							netip.MustParsePrefix("172.16.1.0/24"),
						),
						Weight: 150,
					},
					{
						Identifier: RelativeRealIdentifier{
							Addr: netip.MustParseAddr("10.0.0.12"),
							Port: 0,
						},
						Src: xnetip.FromPrefix(
							netip.MustParsePrefix("172.16.2.0/24"),
						),
						Weight: 200,
					},
				},
				AllowedSrc: []netip.Prefix{
					netip.MustParsePrefix("0.0.0.0/0"),
					netip.MustParsePrefix("10.0.0.0/8"),
					netip.MustParsePrefix("192.168.0.0/16"),
				},
				PeersV4: []netip.Addr{
					netip.MustParseAddr("10.0.0.2"),
					netip.MustParseAddr("10.0.0.3"),
					netip.MustParseAddr("10.0.0.4"),
				},
				PeersV6: []netip.Addr{
					netip.MustParseAddr("::1"),
					netip.MustParseAddr("2001:db8::2"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert Go -> C
			cConfig, err := goToC_VsConfig(&tt.config)
			if err != nil {
				t.Fatalf("Failed to convert to C: %v", err)
			}
			defer freeC_VsConfig(cConfig)

			// Convert C -> Go
			result := cToGo_VsConfig(cConfig)

			// Compare
			if diff := cmp.Diff(&tt.config, result, cmp.Comparer(compareAddr), cmp.Comparer(comparePrefix), cmp.Comparer(compareNetWithMask)); diff != "" {
				t.Errorf(
					"Round-trip conversion mismatch (-want +got):\n%s",
					diff,
				)
			}
		})
	}
}

// TestPacketHandlerConfigConversion tests round-trip conversion of packet handler configuration
func TestPacketHandlerConfigConversion(t *testing.T) {
	tests := []struct {
		name   string
		config PacketHandlerConfig
	}{
		{
			name: "Complete configuration",
			config: PacketHandlerConfig{
				SessionsTimeouts: SessionsTimeouts{
					TcpSynAck: 60,
					TcpSyn:    30,
					TcpFin:    30,
					Tcp:       300,
					Udp:       120,
					Default:   60,
				},
				VirtualServices: []VsConfig{
					{
						Identifier: VsIdentifier{
							Addr: netip.MustParseAddr("192.168.1.100"),

							Port:           80,
							TransportProto: VsTransportProtoTcp,
						},
						Flags:     VsFlags{FixMSS: true},
						Scheduler: VsSchedulerSourceHash,
						Reals: []RealConfig{
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.0.1.1"),
									Port: 8080,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.16.0.0/24"),
								),
								Weight: 100,
							},
						},
						AllowedSrc: []netip.Prefix{
							netip.MustParsePrefix("192.168.0.0/16"),
						},
						PeersV4: []netip.Addr{
							netip.MustParseAddr("192.168.1.1"),
						},
						PeersV6: []netip.Addr{
							netip.MustParseAddr("::1"),
						},
					},
				},
				SourceV4: netip.MustParseAddr("10.0.0.1"),
				SourceV6: netip.MustParseAddr("2001:db8::1"),
				DecapV4: []netip.Addr{
					netip.MustParseAddr("192.168.1.1"),
					netip.MustParseAddr("192.168.1.2"),
				},
				DecapV6: []netip.Addr{
					netip.MustParseAddr("2001:db8::100"),
				},
			},
		},
		{
			name: "Configuration with multiple VS",
			config: PacketHandlerConfig{
				SessionsTimeouts: SessionsTimeouts{
					TcpSynAck: 60,
					TcpSyn:    30,
					TcpFin:    30,
					Tcp:       300,
					Udp:       120,
					Default:   60,
				},
				VirtualServices: []VsConfig{
					{
						Identifier: VsIdentifier{
							Addr: netip.MustParseAddr("192.168.1.100"),

							Port:           80,
							TransportProto: VsTransportProtoTcp,
						},
						Flags:     VsFlags{FixMSS: true},
						Scheduler: VsSchedulerSourceHash,
						Reals: []RealConfig{
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.0.1.1"),
									Port: 8080,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.16.0.0/24"),
								),
								Weight: 100,
							},
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.0.1.2"),
									Port: 8080,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.16.1.0/24"),
								),
								Weight: 100,
							},
						},
						AllowedSrc: []netip.Prefix{
							netip.MustParsePrefix("192.168.0.0/16"),
							netip.MustParsePrefix("10.0.0.0/8"),
						},
						PeersV4: []netip.Addr{
							netip.MustParseAddr("192.168.1.1"),
							netip.MustParseAddr("192.168.1.2"),
						},
						PeersV6: []netip.Addr{
							netip.MustParseAddr("::1"),
						},
					},
					{
						Identifier: VsIdentifier{
							Addr: netip.MustParseAddr("2001:db8::1"),

							Port:           443,
							TransportProto: VsTransportProtoTcp,
						},
						Flags:     VsFlags{GRE: true},
						Scheduler: VsSchedulerRoundRobin,
						Reals: []RealConfig{
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("2001:db8::100"),
									Port: 8443,
								},
								Src: xnetip.FromPrefix(netip.MustParsePrefix(
									"2001:db8:1::/64",
								)),
								Weight: 50,
							},
						},
						AllowedSrc: []netip.Prefix{
							netip.MustParsePrefix("2001:db8::/32"),
						},
						PeersV4: []netip.Addr{
							netip.MustParseAddr("192.168.1.1"),
						},
						PeersV6: []netip.Addr{
							netip.MustParseAddr("2001:db8::2"),
							netip.MustParseAddr("2001:db8::3"),
							netip.MustParseAddr("2001:db8::4"),
						},
					},
				},
				SourceV4: netip.MustParseAddr("10.0.0.1"),
				SourceV6: netip.MustParseAddr("2001:db8::1"),
				DecapV4: []netip.Addr{
					netip.MustParseAddr("192.168.1.1"),
					netip.MustParseAddr("192.168.1.2"),
				},
				DecapV6: []netip.Addr{
					netip.MustParseAddr("2001:db8::100"),
					netip.MustParseAddr("2001:db8::101"),
					netip.MustParseAddr("2001:db8::102"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert Go -> C
			cConfig, err := goToC_PacketHandlerConfig(&tt.config)
			if err != nil {
				t.Fatalf("Failed to convert to C: %v", err)
			}
			defer freeC_PacketHandlerConfig(cConfig)

			// Convert C -> Go
			result := cToGo_PacketHandlerConfig(cConfig)

			// Compare
			if diff := cmp.Diff(&tt.config, result, cmp.Comparer(compareAddr), cmp.Comparer(comparePrefix), cmp.Comparer(compareNetWithMask)); diff != "" {
				t.Errorf(
					"Round-trip conversion mismatch (-want +got):\n%s",
					diff,
				)
			}
		})
	}
}

// TestBalancerConfigConversion tests round-trip conversion of balancer configuration
func TestBalancerConfigConversion(t *testing.T) {
	tests := []struct {
		name   string
		config BalancerConfig
	}{
		{
			name: "Complete balancer config",
			config: BalancerConfig{
				Handler: PacketHandlerConfig{
					SessionsTimeouts: SessionsTimeouts{
						TcpSynAck: 60,
						TcpSyn:    30,
						TcpFin:    30,
						Tcp:       300,
						Udp:       120,
						Default:   60,
					},
					VirtualServices: []VsConfig{
						{
							Identifier: VsIdentifier{
								Addr: netip.MustParseAddr("192.168.1.100"),

								Port:           80,
								TransportProto: VsTransportProtoTcp,
							},
							Flags:     VsFlags{FixMSS: true},
							Scheduler: VsSchedulerSourceHash,
							Reals: []RealConfig{
								{
									Identifier: RelativeRealIdentifier{
										Addr: netip.MustParseAddr("10.0.1.1"),
										Port: 8080,
									},
									Src: xnetip.FromPrefix(
										netip.MustParsePrefix(
											"172.16.0.0/24",
										),
									),
									Weight: 100,
								},
								{
									Identifier: RelativeRealIdentifier{
										Addr: netip.MustParseAddr("10.0.1.2"),
										Port: 8080,
									},
									Src: xnetip.FromPrefix(
										netip.MustParsePrefix(
											"172.16.1.0/24",
										),
									),
									Weight: 150,
								},
							},
							AllowedSrc: []netip.Prefix{
								netip.MustParsePrefix("192.168.0.0/16"),
								netip.MustParsePrefix("10.0.0.0/8"),
							},
							PeersV4: []netip.Addr{
								netip.MustParseAddr("192.168.1.1"),
								netip.MustParseAddr("192.168.1.2"),
							},
							PeersV6: []netip.Addr{
								netip.MustParseAddr("::1"),
								netip.MustParseAddr("2001:db8::2"),
							},
						},
					},
					SourceV4: netip.MustParseAddr("10.0.0.1"),
					SourceV6: netip.MustParseAddr("2001:db8::1"),
					DecapV4: []netip.Addr{
						netip.MustParseAddr("192.168.1.1"),
						netip.MustParseAddr("192.168.1.2"),
						netip.MustParseAddr("192.168.1.3"),
						netip.MustParseAddr("192.168.1.4"),
					},
					DecapV6: []netip.Addr{
						netip.MustParseAddr("2001:db8::100"),
						netip.MustParseAddr("2001:db8::101"),
						netip.MustParseAddr("2001:db8::102"),
						netip.MustParseAddr("2001:db8::103"),
						netip.MustParseAddr("2001:db8::104"),
					},
				},
				State: StateConfig{
					TableCapacity: 10000,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert Go -> C
			cConfig, err := goToC_BalancerConfig(&tt.config)
			if err != nil {
				t.Fatalf("Failed to convert to C: %v", err)
			}
			defer freeC_BalancerConfig(cConfig)

			// Convert C -> Go
			result := cToGo_BalancerConfig(cConfig)

			// Compare
			if diff := cmp.Diff(&tt.config, result, cmp.Comparer(compareAddr), cmp.Comparer(comparePrefix), cmp.Comparer(compareNetWithMask)); diff != "" {
				t.Errorf(
					"Round-trip conversion mismatch (-want +got):\n%s",
					diff,
				)
			}
		})
	}
}

// TestBalancerManagerConfigConversion tests round-trip conversion of manager configuration
func TestBalancerManagerConfigConversion(t *testing.T) {
	tests := []struct {
		name   string
		config BalancerManagerConfig
	}{
		{
			name: "Complete manager config",
			config: BalancerManagerConfig{
				Balancer: BalancerConfig{
					Handler: PacketHandlerConfig{
						SessionsTimeouts: SessionsTimeouts{
							TcpSynAck: 60,
							TcpSyn:    30,
							TcpFin:    30,
							Tcp:       300,
							Udp:       120,
							Default:   60,
						},
						VirtualServices: []VsConfig{
							{
								Identifier: VsIdentifier{
									Addr: netip.MustParseAddr("192.168.1.100"),

									Port:           80,
									TransportProto: VsTransportProtoTcp,
								},
								Flags:     VsFlags{FixMSS: true},
								Scheduler: VsSchedulerSourceHash,
								Reals: []RealConfig{
									{
										Identifier: RelativeRealIdentifier{
											Addr: netip.MustParseAddr(
												"10.0.1.1",
											),
											Port: 8080,
										},
										Src: xnetip.FromPrefix(
											netip.MustParsePrefix(
												"172.16.0.0/24",
											),
										),
										Weight: 100,
									},
								},
								AllowedSrc: []netip.Prefix{
									netip.MustParsePrefix("192.168.0.0/16"),
									netip.MustParsePrefix("10.0.0.0/8"),
								},
								PeersV4: []netip.Addr{
									netip.MustParseAddr("192.168.1.1"),
									netip.MustParseAddr("192.168.1.2"),
									netip.MustParseAddr("192.168.1.3"),
								},
								PeersV6: []netip.Addr{
									netip.MustParseAddr("::1"),
									netip.MustParseAddr("2001:db8::2"),
								},
							},
						},
						SourceV4: netip.MustParseAddr("10.0.0.1"),
						SourceV6: netip.MustParseAddr("2001:db8::1"),
						DecapV4: []netip.Addr{
							netip.MustParseAddr("192.168.1.1"),
							netip.MustParseAddr("192.168.1.2"),
							netip.MustParseAddr("192.168.1.3"),
							netip.MustParseAddr("192.168.1.4"),
						},
						DecapV6: []netip.Addr{
							netip.MustParseAddr("2001:db8::100"),
							netip.MustParseAddr("2001:db8::101"),
							netip.MustParseAddr("2001:db8::102"),
							netip.MustParseAddr("2001:db8::103"),
							netip.MustParseAddr("2001:db8::104"),
						},
					},
					State: StateConfig{
						TableCapacity: 10000,
					},
				},
				Wlc: BalancerManagerWlcConfig{
					Power:         2,
					MaxRealWeight: 1000,
					Vs:            []uint32{1, 2, 3, 4, 5, 6, 7},
				},
				RefreshPeriod: 5 * time.Second,
				MaxLoadFactor: 0.75,
			},
		},
		{
			name: "Manager config with different WLC settings",
			config: BalancerManagerConfig{
				Balancer: BalancerConfig{
					Handler: PacketHandlerConfig{
						SessionsTimeouts: SessionsTimeouts{
							TcpSynAck: 60,
							TcpSyn:    30,
							TcpFin:    30,
							Tcp:       300,
							Udp:       120,
							Default:   60,
						},
						VirtualServices: []VsConfig{
							{
								Identifier: VsIdentifier{
									Addr: netip.MustParseAddr("192.168.1.100"),

									Port:           80,
									TransportProto: VsTransportProtoTcp,
								},
								Flags:     VsFlags{FixMSS: true},
								Scheduler: VsSchedulerSourceHash,
								Reals: []RealConfig{
									{
										Identifier: RelativeRealIdentifier{
											Addr: netip.MustParseAddr(
												"10.0.1.1",
											),
											Port: 8080,
										},
										Src: xnetip.FromPrefix(
											netip.MustParsePrefix(
												"172.16.0.0/24",
											),
										),
										Weight: 100,
									},
								},
								AllowedSrc: []netip.Prefix{
									netip.MustParsePrefix("192.168.0.0/16"),
									netip.MustParsePrefix("10.0.0.0/8"),
								},
								PeersV4: []netip.Addr{
									netip.MustParseAddr("192.168.1.1"),
									netip.MustParseAddr("192.168.1.2"),
									netip.MustParseAddr("192.168.1.3"),
								},
								PeersV6: []netip.Addr{
									netip.MustParseAddr("::1"),
									netip.MustParseAddr("2001:db8::2"),
								},
							},
						},
						SourceV4: netip.MustParseAddr("10.0.0.1"),
						SourceV6: netip.MustParseAddr("2001:db8::1"),
						DecapV4: []netip.Addr{
							netip.MustParseAddr("192.168.1.1"),
							netip.MustParseAddr("192.168.1.2"),
							netip.MustParseAddr("192.168.1.3"),
							netip.MustParseAddr("192.168.1.4"),
							netip.MustParseAddr("192.168.1.5"),
						},
						DecapV6: []netip.Addr{
							netip.MustParseAddr("2001:db8::100"),
							netip.MustParseAddr("2001:db8::101"),
							netip.MustParseAddr("2001:db8::102"),
						},
					},
					State: StateConfig{
						TableCapacity: 5000,
					},
				},
				Wlc: BalancerManagerWlcConfig{
					Power:         1,
					MaxRealWeight: 500,
					Vs:            []uint32{10, 20, 30},
				},
				RefreshPeriod: 10 * time.Second,
				MaxLoadFactor: 0.9,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert Go -> C
			cConfig, err := goToC_BalancerManagerConfig(&tt.config)
			if err != nil {
				t.Fatalf("Failed to convert to C: %v", err)
			}
			defer freeC_BalancerManagerConfig(cConfig)

			// Convert C -> Go
			result := cToGo_BalancerManagerConfig(cConfig)

			// Compare
			if diff := cmp.Diff(&tt.config, result, cmp.Comparer(compareAddr), cmp.Comparer(comparePrefix), cmp.Comparer(compareNetWithMask)); diff != "" {
				t.Errorf(
					"Round-trip conversion mismatch (-want +got):\n%s",
					diff,
				)
			}
		})
	}
}

// TestEdgeCases tests edge cases and boundary conditions
func TestEdgeCases(t *testing.T) {
	t.Run("Empty arrays in VsConfig", func(t *testing.T) {
		config := VsConfig{
			Identifier: VsIdentifier{
				Addr: netip.MustParseAddr("192.168.1.100"),

				Port:           80,
				TransportProto: VsTransportProtoTcp,
			},
			Flags:      VsFlags{},
			Scheduler:  VsSchedulerSourceHash,
			Reals:      []RealConfig{},
			AllowedSrc: []netip.Prefix{},
			PeersV4:    []netip.Addr{},
			PeersV6:    []netip.Addr{},
		}

		cConfig, err := goToC_VsConfig(&config)
		if err != nil {
			t.Fatalf("Failed to convert: %v", err)
		}
		defer freeC_VsConfig(cConfig)

		result := cToGo_VsConfig(cConfig)

		if len(result.Reals) != 0 {
			t.Errorf("Expected empty Reals, got %d items", len(result.Reals))
		}
		if len(result.AllowedSrc) != 0 {
			t.Errorf(
				"Expected empty AllowedSrc, got %d items",
				len(result.AllowedSrc),
			)
		}
	})

	t.Run("Zero port (PureL3 mode)", func(t *testing.T) {
		id := VsIdentifier{
			Addr: netip.MustParseAddr("192.168.1.100"),

			Port:           0,
			TransportProto: 6,
		}

		cId := goToC_VsIdentifier(id)
		result := cToGo_VsIdentifier(cId)

		if result.Port != 0 {
			t.Errorf("Port should be 0, got %v", result.Port)
		}
	})

	t.Run("IPv4 vs IPv6 distinction", func(t *testing.T) {
		v4Addr := netip.MustParseAddr("192.168.1.1")
		v6Addr := netip.MustParseAddr("2001:db8::1")

		cV4 := goToC_NetAddr(v4Addr)
		cV6 := goToC_NetAddr(v6Addr)

		resultV4 := cToGo_NetAddr(cV4, true)
		resultV6 := cToGo_NetAddr(cV6, false)

		if !resultV4.Is4() {
			t.Errorf("Expected IPv4 address, got %v", resultV4)
		}
		if !resultV6.Is6() {
			t.Errorf("Expected IPv6 address, got %v", resultV6)
		}
	})

	t.Run("Nil config pointer", func(t *testing.T) {
		result := cToGo_BalancerConfig(nil)
		if result != nil {
			t.Errorf("Expected nil result for nil input, got %v", result)
		}
	})
}

// TestComplexScenario tests a realistic complex scenario
func TestComplexScenario(t *testing.T) {
	config := BalancerManagerConfig{
		Balancer: BalancerConfig{
			Handler: PacketHandlerConfig{
				SessionsTimeouts: SessionsTimeouts{
					TcpSynAck: 60,
					TcpSyn:    30,
					TcpFin:    30,
					Tcp:       300,
					Udp:       120,
					Default:   60,
				},
				VirtualServices: []VsConfig{
					{
						Identifier: VsIdentifier{
							Addr: netip.MustParseAddr("192.168.1.100"),

							Port:           80,
							TransportProto: VsTransportProtoTcp,
						},
						Flags: VsFlags{
							PureL3: false,
							FixMSS: true,
							GRE:    false,
							OPS:    false,
						},
						Scheduler: VsSchedulerSourceHash,
						Reals: []RealConfig{
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.0.0.1"),
									Port: 8080,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.16.0.0/24"),
								),
								Weight: 100,
							},
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("10.0.0.2"),
									Port: 8080,
								},
								Src: xnetip.FromPrefix(
									netip.MustParsePrefix("172.16.1.0/24"),
								),
								Weight: 150,
							},
						},
						AllowedSrc: []netip.Prefix{
							netip.MustParsePrefix("192.168.0.0/16"),
							netip.MustParsePrefix("10.0.0.0/8"),
						},
						PeersV4: []netip.Addr{
							netip.MustParseAddr("192.168.1.1"),
							netip.MustParseAddr("192.168.1.2"),
						},
						PeersV6: []netip.Addr{},
					},
					{
						Identifier: VsIdentifier{
							Addr: netip.MustParseAddr("2001:db8::1"),

							Port:           443,
							TransportProto: VsTransportProtoTcp,
						},
						Flags: VsFlags{
							PureL3: false,
							FixMSS: false,
							GRE:    true,
							OPS:    false,
						},
						Scheduler: VsSchedulerRoundRobin,
						Reals: []RealConfig{
							{
								Identifier: RelativeRealIdentifier{
									Addr: netip.MustParseAddr("2001:db8::100"),
									Port: 8443,
								},
								Src: xnetip.FromPrefix(netip.MustParsePrefix(
									"2001:db8:1::/64",
								)),
								Weight: 100,
							},
						},
						AllowedSrc: []netip.Prefix{
							netip.MustParsePrefix("2001:db8::/32"),
						},
						PeersV4: []netip.Addr{},
						PeersV6: []netip.Addr{
							netip.MustParseAddr("2001:db8::2"),
							netip.MustParseAddr("2001:db8::3"),
						},
					},
				},
				SourceV4: netip.MustParseAddr("10.0.0.1"),
				SourceV6: netip.MustParseAddr("2001:db8::1"),
				DecapV4: []netip.Addr{
					netip.MustParseAddr("192.168.1.1"),
					netip.MustParseAddr("192.168.1.2"),
				},
				DecapV6: []netip.Addr{
					netip.MustParseAddr("2001:db8::100"),
					netip.MustParseAddr("2001:db8::101"),
				},
			},
			State: StateConfig{
				TableCapacity: 100000,
			},
		},
		Wlc: BalancerManagerWlcConfig{
			Power:         2,
			MaxRealWeight: 1000,
			Vs:            []uint32{1, 2, 3, 4, 5},
		},
		RefreshPeriod: 5 * time.Second,
		MaxLoadFactor: 0.75,
	}

	// Convert Go -> C
	cConfig, err := goToC_BalancerManagerConfig(&config)
	if err != nil {
		t.Fatalf("Failed to convert to C: %v", err)
	}
	defer freeC_BalancerManagerConfig(cConfig)

	// Convert C -> Go
	result := cToGo_BalancerManagerConfig(cConfig)

	// Compare
	if diff := cmp.Diff(&config, result, cmp.Comparer(compareAddr), cmp.Comparer(comparePrefix), cmp.Comparer(compareNetWithMask)); diff != "" {
		t.Errorf("Round-trip conversion mismatch (-want +got):\n%s", diff)
	}
}

// TestLargeScaleConversion tests conversion performance with a large configuration
func TestLargeScaleConversion(t *testing.T) {
	// Build a large configuration with 100 virtual services
	config := PacketHandlerConfig{
		SessionsTimeouts: SessionsTimeouts{
			TcpSynAck: 60,
			TcpSyn:    30,
			TcpFin:    30,
			Tcp:       300,
			Udp:       120,
			Default:   60,
		},
		VirtualServices: make([]VsConfig, 100),
		SourceV4:        netip.MustParseAddr("10.0.0.1"),
		SourceV6:        netip.MustParseAddr("2001:db8::1"),
		DecapV4:         make([]netip.Addr, 100),
		DecapV6:         make([]netip.Addr, 100),
	}

	// Generate 100 DecapV4 addresses
	for i := 0; i < 100; i++ {
		config.DecapV4[i] = netip.MustParseAddr(
			fmt.Sprintf("192.168.%d.%d", i/256, i%256),
		)
	}

	// Generate 100 DecapV6 addresses
	for i := 0; i < 100; i++ {
		config.DecapV6[i] = netip.MustParseAddr(
			fmt.Sprintf("2001:db8::%x", i+1),
		)
	}

	// Generate 100 virtual services
	for vsIdx := 0; vsIdx < 100; vsIdx++ {
		vs := VsConfig{
			Identifier: VsIdentifier{
				Addr: netip.MustParseAddr(
					fmt.Sprintf("10.0.%d.%d", vsIdx/256, vsIdx%256),
				),

				Port:           uint16(8000 + vsIdx),
				TransportProto: VsTransportProtoTcp,
			},
			Flags: VsFlags{
				FixMSS: vsIdx%2 == 0,
				GRE:    vsIdx%3 == 0,
			},
			Scheduler: VsScheduler(vsIdx % 2),
			Reals:     make([]RealConfig, 100),
		}

		// Generate 100 reals for each VS
		for realIdx := 0; realIdx < 100; realIdx++ {
			vs.Reals[realIdx] = RealConfig{
				Identifier: RelativeRealIdentifier{
					Addr: netip.MustParseAddr(
						fmt.Sprintf("172.16.%d.%d", realIdx/256, realIdx%256),
					),
					Port: uint16(9000 + realIdx),
				},
				Src: xnetip.FromPrefix(netip.MustParsePrefix(
					fmt.Sprintf("172.16.%d.0/24", realIdx/256)),
				),
				Weight: uint16(100 + realIdx%900),
			}
		}

		// Generate 10-100 allowed sources (varies per VS)
		allowedCount := 10 + (vsIdx % 91)
		vs.AllowedSrc = make([]netip.Prefix, allowedCount)
		for i := 0; i < allowedCount; i++ {
			vs.AllowedSrc[i] = netip.MustParsePrefix(
				fmt.Sprintf("10.%d.%d.0/24", i/256, i%256),
			)
		}

		// Generate 10-100 IPv4 peers (varies per VS)
		peersV4Count := 10 + (vsIdx % 91)
		vs.PeersV4 = make([]netip.Addr, peersV4Count)
		for i := 0; i < peersV4Count; i++ {
			vs.PeersV4[i] = netip.MustParseAddr(
				fmt.Sprintf("192.168.%d.%d", i/256, i%256),
			)
		}

		// Generate 10-100 IPv6 peers (varies per VS)
		peersV6Count := 10 + ((vsIdx + 50) % 91)
		vs.PeersV6 = make([]netip.Addr, peersV6Count)
		for i := 0; i < peersV6Count; i++ {
			vs.PeersV6[i] = netip.MustParseAddr(
				fmt.Sprintf("2001:db8::%x", i+1),
			)
		}

		config.VirtualServices[vsIdx] = vs
	}

	// Measure Go -> C conversion time
	startGoToC := time.Now()
	cConfig, err := goToC_PacketHandlerConfig(&config)
	goToCDuration := time.Since(startGoToC)
	if err != nil {
		t.Fatalf("Failed to convert Go -> C: %v", err)
	}
	defer freeC_PacketHandlerConfig(cConfig)

	// Measure C -> Go conversion time
	startCToGo := time.Now()
	result := cToGo_PacketHandlerConfig(cConfig)
	cToGoDuration := time.Since(startCToGo)

	// Measure round-trip time
	totalDuration := goToCDuration + cToGoDuration

	// Output timing results
	t.Logf("Large-scale conversion performance:")
	t.Logf(
		"  Configuration: 100 VS × 100 reals each + varying peers/allowed sources",
	)
	t.Logf("  Go -> C conversion: %v", goToCDuration)
	t.Logf("  C -> Go conversion: %v", cToGoDuration)
	t.Logf("  Total round-trip:   %v", totalDuration)

	// Basic validation
	if len(result.VirtualServices) != 100 {
		t.Errorf(
			"Expected 100 virtual services, got %d",
			len(result.VirtualServices),
		)
	}
	if len(result.DecapV4) != 100 {
		t.Errorf("Expected 100 DecapV4 addresses, got %d", len(result.DecapV4))
	}
	if len(result.DecapV6) != 100 {
		t.Errorf("Expected 100 DecapV6 addresses, got %d", len(result.DecapV6))
	}

	// Validate first VS has 100 reals
	if len(result.VirtualServices) > 0 &&
		len(result.VirtualServices[0].Reals) != 100 {
		t.Errorf(
			"Expected first VS to have 100 reals, got %d",
			len(result.VirtualServices[0].Reals),
		)
	}
}
