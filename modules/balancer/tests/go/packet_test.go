package balancer_test

// TestPacketProcessing is a comprehensive test suite for packet processing in the balancer module that covers:
//
// # Packet Encapsulation
// - Basic encapsulation without GRE or MSS fixing
// - IPv4 and IPv6 virtual services
// - TCP and UDP protocols
// - IPv4 and IPv6 real servers
//
// # GRE Tunneling
// - GRE encapsulation for IPv4 and IPv6
// - Proper tunnel type identification
// - All protocol and IP version combinations
//
// # MSS Fixing
// - MSS option insertion when missing
// - MSS option update when present
// - MSS clamping to maximum value (1220)
// - Default MSS value (536) when no MSS option present
//
// # Combined Features
// - GRE tunneling with MSS fixing
// - All combinations of features working together
//
// The test validates:
// - Correct packet encapsulation
// - ToS/TrafficClass preservation
// - Protocol consistency
// - Tunnel type correctness
// - MSS option handling

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/modules/balancer/tests/go/utils"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Test addresses
var (
	clientIPv4 = netip.MustParseAddr("10.0.1.1")
	clientIPv6 = netip.MustParseAddr("ffff::1")

	balancerSrcIPv4 = netip.MustParseAddr("5.5.5.5")
	balancerSrcIPv6 = netip.MustParseAddr("fe80::5")
)

// createPacketTestConfig creates a balancer configuration with all combinations of:
// - VS IP version (IPv4, IPv6)
// - Protocol (TCP, UDP)
// - GRE enabled/disabled
// - FixMSS enabled/disabled
// - Real IP version (IPv4, IPv6)
func createPacketTestConfig() *balancerpb.BalancerConfig {
	var virtualServices []*balancerpb.VirtualService
	counter := 1

	for _, vsIPVersion := range []int{4, 6} {
		for _, proto := range []balancerpb.TransportProto{
			balancerpb.TransportProto_TCP,
			balancerpb.TransportProto_UDP,
		} {
			for _, greEnabled := range []bool{false, true} {
				for _, fixMssEnabled := range []bool{false, true} {
					for _, realIPVersion := range []int{4, 6} {
						// Create VS address
						var vsAddr netip.Addr
						var allowedSrc *balancerpb.Net
						if vsIPVersion == 4 {
							vsAddr = netip.MustParseAddr(
								fmt.Sprintf("10.12.1.%d", counter),
							)
							allowedSrc = &balancerpb.Net{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("10.0.1.0").
										AsSlice(),
								},
								Size: 24,
							}
						} else {
							vsAddr = netip.MustParseAddr(fmt.Sprintf("2001:db8::%d", counter))
							allowedSrc = &balancerpb.Net{
								Addr: &balancerpb.Addr{
									Bytes: netip.MustParseAddr("ffff::0").AsSlice(),
								},
								Size: 16,
							}
						}

						// Create real address
						var realAddr netip.Addr
						if realIPVersion == 4 {
							realAddr = netip.MustParseAddr("10.1.1.1")
						} else {
							realAddr = netip.MustParseAddr("fe80::1")
						}

						vs := &balancerpb.VirtualService{
							Id: &balancerpb.VsIdentifier{
								Addr: &balancerpb.Addr{
									Bytes: vsAddr.AsSlice(),
								},
								Port:  8080,
								Proto: proto,
							},
							Scheduler: balancerpb.VsScheduler_ROUND_ROBIN,
							AllowedSrcs: []*balancerpb.Net{
								allowedSrc,
							},
							Flags: &balancerpb.VsFlags{
								Gre:    greEnabled,
								FixMss: fixMssEnabled,
								Ops:    false,
								PureL3: false,
								Wlc:    false,
							},
							Reals: []*balancerpb.Real{
								{
									Id: &balancerpb.RelativeRealIdentifier{
										Ip: &balancerpb.Addr{
											Bytes: realAddr.AsSlice(),
										},
										Port: 0,
									},
									Weight: 1,
									SrcAddr: &balancerpb.Addr{
										Bytes: realAddr.AsSlice(),
									},
									SrcMask: &balancerpb.Addr{
										Bytes: realAddr.AsSlice(),
									},
								},
							},
							Peers: []*balancerpb.Addr{},
						}

						virtualServices = append(virtualServices, vs)
						counter++
					}
				}
			}
		}
	}

	return &balancerpb.BalancerConfig{
		PacketHandler: &balancerpb.PacketHandlerConfig{
			SourceAddressV4: &balancerpb.Addr{
				Bytes: balancerSrcIPv4.AsSlice(),
			},
			SourceAddressV6: &balancerpb.Addr{
				Bytes: balancerSrcIPv6.AsSlice(),
			},
			Vs:             virtualServices,
			DecapAddresses: []*balancerpb.Addr{},
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 10,
				TcpSyn:    10,
				TcpFin:    10,
				Tcp:       10,
				Udp:       10,
				Default:   10,
			},
		},
		State: &balancerpb.StateConfig{
			SessionTableCapacity:      func() *uint64 { v := uint64(100); return &v }(),
			SessionTableMaxLoadFactor: func() *float32 { v := float32(0.5); return &v }(),
			RefreshPeriod:             durationpb.New(0),
			Wlc: &balancerpb.WlcConfig{
				Power:     func() *uint64 { v := uint64(10); return &v }(),
				MaxWeight: func() *uint32 { v := uint32(1000); return &v }(),
			},
		},
	}
}

// VsSelector helps select a specific virtual service based on its characteristics
type VsSelector struct {
	VsIPVersion   int // 4 or 6
	Proto         balancerpb.TransportProto
	Gre           bool
	FixMSS        bool
	RealIPVersion int // 4 or 6
}

// findMatchingVS finds a virtual service that matches the selector criteria
func findMatchingVS(
	config *balancerpb.BalancerConfig,
	selector VsSelector,
) *balancerpb.VirtualService {
	for _, vs := range config.PacketHandler.Vs {
		vsAddr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
		vsIsIPv4 := vsAddr.Is4()
		vsIsIPv6 := vsAddr.Is6()

		if (vsIsIPv4 && selector.VsIPVersion == 4) ||
			(vsIsIPv6 && selector.VsIPVersion == 6) {
			if vs.Id.Proto == selector.Proto {
				if vs.Flags.FixMss == selector.FixMSS &&
					vs.Flags.Gre == selector.Gre {
					if len(vs.Reals) > 0 {
						real := vs.Reals[0]
						realAddr, _ := netip.AddrFromSlice(real.Id.Ip.Bytes)
						if (realAddr.Is4() && selector.RealIPVersion == 4) ||
							(realAddr.Is6() && selector.RealIPVersion == 6) {
							return vs
						}
					}
				}
			}
		}
	}
	return nil
}

// sendPacketToVS sends a packet to a virtual service and returns the result
func sendPacketToVS(
	t *testing.T,
	ts *utils.TestSetup,
	vs *balancerpb.VirtualService,
	mss *uint16,
) *framework.PacketInfo {
	t.Helper()

	vsAddr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
	clientAddr := clientIPv4
	if vsAddr.Is6() {
		clientAddr = clientIPv6
	}
	clientPort := uint16(40441)
	vsPort := uint16(vs.Id.Port)

	var tcp *layers.TCP
	if vs.Id.Proto == balancerpb.TransportProto_TCP {
		tcp = &layers.TCP{SYN: true}
	}

	packetLayers := utils.MakePacketLayers(
		clientAddr,
		clientPort,
		vsAddr,
		vsPort,
		tcp,
	)

	packet := xpacket.LayersToPacket(t, packetLayers...)

	// Add MSS option if requested and TCP
	if tcp != nil && mss != nil {
		modifiedPacket, err := insertOrUpdateMSS(packet, *mss)
		require.NoError(t, err, "failed to insert/update MSS")
		packet = *modifiedPacket
	}

	result, err := ts.Mock.HandlePackets(packet)
	require.NoError(t, err, "failed to handle packet")
	require.Equal(t, 1, len(result.Output), "expected 1 output packet")
	require.Empty(t, result.Drop, "expected no dropped packets")

	if len(result.Output) > 0 {
		resultPacket := result.Output[0]
		utils.ValidatePacket(t, ts.Balancer.Config(), packet, resultPacket)
		return resultPacket
	}

	return nil
}

// insertOrUpdateMSS inserts or updates the MSS option in a TCP packet
func insertOrUpdateMSS(
	p gopacket.Packet,
	newMSS uint16,
) (*gopacket.Packet, error) {
	return utils.InsertOrUpdateMSS(p, newMSS)
}

// TestPacketProcessing is the main test function
func TestPacketProcessing(t *testing.T) {
	config := createPacketTestConfig()

	ts, err := utils.Make(&utils.TestConfig{
		Mock:     utils.SingleWorkerMockConfig(64*datasize.MB, 4*datasize.MB),
		Balancer: config,
		AgentMemory: func() *datasize.ByteSize {
			memory := datasize.MB * 16
			return &memory
		}(),
	})
	require.NoError(t, err)
	defer ts.Free()

	// Enable all reals
	utils.EnableAllReals(t, ts)

	// Test basic encapsulation
	t.Run("Encapsulation", func(t *testing.T) {
		testEncapsulation(t, ts)
	})

	// Test GRE tunneling
	t.Run("GRE_Tunneling", func(t *testing.T) {
		testGRETunneling(t, ts)
	})

	// Test MSS fixing
	t.Run("MSS_Fixing", func(t *testing.T) {
		testMSSFixing(t, ts)
	})

	// Test GRE + MSS fixing
	t.Run("GRE_MSS_Combined", func(t *testing.T) {
		testGREMSSCombined(t, ts)
	})
}

// testEncapsulation tests basic packet encapsulation without GRE or MSS fixing
func testEncapsulation(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	config := ts.Balancer.Config()

	for _, proto := range []balancerpb.TransportProto{
		balancerpb.TransportProto_TCP,
		balancerpb.TransportProto_UDP,
	} {
		for _, vsIPVersion := range []int{4, 6} {
			for _, realIPVersion := range []int{4, 6} {
				selector := VsSelector{
					VsIPVersion:   vsIPVersion,
					Proto:         proto,
					RealIPVersion: realIPVersion,
					Gre:           false,
					FixMSS:        false,
				}

				vs := findMatchingVS(config, selector)
				require.NotNil(
					t,
					vs,
					"failed to find VS for selector: %+v",
					selector,
				)

				t.Logf(
					"Testing encapsulation: vsIP=v%d, realIP=v%d, proto=%s",
					selector.VsIPVersion,
					selector.RealIPVersion,
					selector.Proto.String(),
				)

				result := sendPacketToVS(t, ts, vs, nil)
				assert.NotNil(t, result, "expected result packet")
				assert.True(t, result.IsTunneled, "packet should be tunneled")
			}
		}
	}
}

// testGRETunneling tests GRE tunneling functionality
func testGRETunneling(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	config := ts.Balancer.Config()

	for _, proto := range []balancerpb.TransportProto{
		balancerpb.TransportProto_TCP,
		balancerpb.TransportProto_UDP,
	} {
		for _, vsIPVersion := range []int{4, 6} {
			for _, realIPVersion := range []int{4, 6} {
				selector := VsSelector{
					VsIPVersion:   vsIPVersion,
					Proto:         proto,
					RealIPVersion: realIPVersion,
					Gre:           true,
					FixMSS:        false,
				}

				vs := findMatchingVS(config, selector)
				require.NotNil(
					t,
					vs,
					"failed to find VS for selector: %+v",
					selector,
				)

				t.Logf(
					"Testing GRE: vsIP=v%d, realIP=v%d, proto=%s",
					selector.VsIPVersion,
					selector.RealIPVersion,
					selector.Proto.String(),
				)

				result := sendPacketToVS(t, ts, vs, nil)
				assert.NotNil(t, result, "expected result packet")
				assert.True(t, result.IsTunneled, "packet should be tunneled")

				// Verify GRE tunnel type
				expectedTunnelType := "gre-ip4"
				vsAddr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
				if vsAddr.Is6() {
					expectedTunnelType = "gre-ip6"
				}
				assert.Equal(
					t,
					expectedTunnelType,
					result.TunnelType,
					"tunnel type should be GRE",
				)
			}
		}
	}
}

// testMSSFixing tests MSS fixing functionality
func testMSSFixing(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	config := ts.Balancer.Config()

	// Test with different MSS values
	for _, mssValue := range []uint16{0, 500, 1200, 1400, 1460} {
		for _, realIPVersion := range []int{4, 6} {
			selector := VsSelector{
				VsIPVersion:   6, // Use IPv6 VS
				Proto:         balancerpb.TransportProto_TCP,
				RealIPVersion: realIPVersion,
				Gre:           false,
				FixMSS:        true,
			}

			vs := findMatchingVS(config, selector)
			require.NotNil(
				t,
				vs,
				"failed to find VS for selector: %+v",
				selector,
			)

			t.Logf(
				"Testing MSS fixing: vsIP=v%d, realIP=v%d, mss=%d",
				selector.VsIPVersion,
				selector.RealIPVersion,
				mssValue,
			)

			var mssPtr *uint16
			if mssValue > 0 {
				mssPtr = &mssValue
			}

			result := sendPacketToVS(t, ts, vs, mssPtr)
			assert.NotNil(t, result, "expected result packet")

			// Verify MSS was fixed
			if mssValue > 0 {
				// MSS should be clamped to min(original, 1220)
				expectedMSS := min(mssValue, 1220)
				actualMSS := extractMSS(t, result)
				assert.Equal(
					t,
					expectedMSS,
					actualMSS,
					"MSS should be fixed to %d",
					expectedMSS,
				)
			} else {
				// No MSS option in original, should insert default (536)
				actualMSS := extractMSS(t, result)
				assert.Equal(t, uint16(536), actualMSS, "MSS should be default 536")
			}
		}
	}
}

// testGREMSSCombined tests GRE tunneling combined with MSS fixing
func testGREMSSCombined(t *testing.T, ts *utils.TestSetup) {
	t.Helper()

	config := ts.Balancer.Config()

	for _, mssValue := range []uint16{0, 500, 1200, 1400} {
		for _, realIPVersion := range []int{4, 6} {
			selector := VsSelector{
				VsIPVersion:   6,
				Proto:         balancerpb.TransportProto_TCP,
				RealIPVersion: realIPVersion,
				Gre:           true,
				FixMSS:        true,
			}

			vs := findMatchingVS(config, selector)
			require.NotNil(
				t,
				vs,
				"failed to find VS for selector: %+v",
				selector,
			)

			t.Logf(
				"Testing GRE+MSS: vsIP=v%d, realIP=v%d, mss=%d",
				selector.VsIPVersion,
				selector.RealIPVersion,
				mssValue,
			)

			var mssPtr *uint16
			if mssValue > 0 {
				mssPtr = &mssValue
			}

			result := sendPacketToVS(t, ts, vs, mssPtr)
			assert.NotNil(t, result, "expected result packet")
			assert.True(t, result.IsTunneled, "packet should be tunneled")

			// Verify GRE tunnel type
			vsAddr, _ := netip.AddrFromSlice(vs.Id.Addr.Bytes)
			expectedTunnelType := "gre-ip4"
			if vsAddr.Is6() {
				expectedTunnelType = "gre-ip6"
			}
			assert.Equal(
				t,
				expectedTunnelType,
				result.TunnelType,
				"tunnel type should be GRE",
			)

			// Verify MSS was fixed
			if mssValue > 0 {
				expectedMSS := min(mssValue, 1220)
				actualMSS := extractMSS(t, result)
				assert.Equal(
					t,
					expectedMSS,
					actualMSS,
					"MSS should be fixed to %d",
					expectedMSS,
				)
			} else {
				actualMSS := extractMSS(t, result)
				assert.Equal(t, uint16(536), actualMSS, "MSS should be default 536")
			}
		}
	}
}

// extractMSS extracts the MSS value from a packet
func extractMSS(t *testing.T, packet *framework.PacketInfo) uint16 {
	t.Helper()

	p := gopacket.NewPacket(
		packet.RawData,
		layers.LayerTypeEthernet,
		gopacket.Default,
	)

	mss, err := xpacket.PacketMSS(p)
	require.NoError(t, err, "failed to extract MSS from packet")

	return mss
}
