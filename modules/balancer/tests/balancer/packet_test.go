package balancer_test

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	mock "github.com/yanet-platform/yanet2/mock/go"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane/agent"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	test_utils "github.com/yanet-platform/yanet2/modules/balancer/tests/balancer"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"google.golang.org/protobuf/types/known/durationpb"
)

// test gre, fix mss, encap, not standard packets
// UDP: <--- todo
// test packet source address is shuffled: <--- todo

////////////////////////////////////////////////////////////////////////////////

func allCombinationsConfig() (*balancerpb.ModuleConfig, *balancerpb.SessionsTimeouts) {
	serviceConfigs := make([]*balancerpb.VirtualService, 0, 2*2*2*2*2)
	for _, vsAddrVersion := range []int{4, 6} {
		for _, proto := range []balancerpb.TransportProto{balancerpb.TransportProto_TCP, balancerpb.TransportProto_UDP} {
			for _, greEnabled := range []bool{false, true} {
				for _, fixMssEnabled := range []bool{false, true} {
					for _, realAddr := range []netip.Addr{test_utils.IpAddr("10.1.1.1"), test_utils.IpAddr("fe80::1")} {
						counter := len(serviceConfigs) + 1
						vsAddr := test_utils.IpAddr(fmt.Sprintf("10.12.1.%d", counter))
						allowed := test_utils.IpPrefix("10.0.1.0/24")
						if vsAddrVersion == 6 {
							vsAddr = test_utils.IpAddr(
								fmt.Sprintf("2001:db8::%d", counter),
							)
							allowed = test_utils.IpPrefix("ffff::0/16")
						}
						serviceConfig := &balancerpb.VirtualService{
							Addr:  vsAddr.AsSlice(),
							Proto: proto,
							Port:  8080,
							AllowedSrcs: []*balancerpb.Subnet{
								{
									Addr: allowed.Addr().AsSlice(),
									Size: uint32(allowed.Bits()),
								},
							},
							Flags: &balancerpb.VsFlags{
								Gre:    greEnabled,
								Ops:    false,
								PureL3: false,
								FixMss: fixMssEnabled,
							},
							Scheduler: balancerpb.VsScheduler_PRR,
							Reals: []*balancerpb.Real{
								{
									Weight:  1,
									DstAddr: realAddr.AsSlice(),
									SrcAddr: realAddr.AsSlice(),
									SrcMask: realAddr.AsSlice(),
								},
							},
						}
						serviceConfigs = append(serviceConfigs, serviceConfig)
					}
				}
			}
		}
	}
	return &balancerpb.ModuleConfig{
			SourceAddressV4: test_utils.IpAddr("5.5.5.5").AsSlice(),
			SourceAddressV6: test_utils.IpAddr("fe80::5").AsSlice(),
			VirtualServices: serviceConfigs,
			SessionsTimeouts: &balancerpb.SessionsTimeouts{
				TcpSynAck: 10,
				TcpSyn:    10,
				TcpFin:    10,
				Tcp:       10,
				Udp:       10,
				Default:   10,
			},
			Wlc: &balancerpb.WlcConfig{
				WlcPower:      10,
				MaxRealWeight: 1000,
				UpdatePeriod:  durationpb.New(0),
			},
		}, &balancerpb.SessionsTimeouts{
			TcpSynAck: 10,
			TcpSyn:    10,
			TcpFin:    10,
			Tcp:       10,
			Udp:       10,
			Default:   10,
		}
}

////////////////////////////////////////////////////////////////////////////////

func allCombinationsTestConfig() *test_utils.TestConfig {
	moduleConfig, _ := allCombinationsConfig()
	return &test_utils.TestConfig{
		ModuleConfig: moduleConfig,
		StateConfig: &balancerpb.ModuleStateConfig{
			SessionTableCapacity:      100,
			SessionTableScanPeriod:    durationpb.New(0),
			SessionTableMaxLoadFactor: 0.5,
		},
	}
}

func allCombinationsSetup(t *testing.T) *test_utils.TestSetup {
	config := allCombinationsTestConfig()
	setup, err := test_utils.SetupTest(config)
	require.NoError(t, err)
	return setup
}

////////////////////////////////////////////////////////////////////////////////

func clientIpv4() netip.Addr {
	return test_utils.IpAddr("10.0.1.1")
}

func clientIpv6() netip.Addr {
	return test_utils.IpAddr("ffff::1")
}

////////////////////////////////////////////////////////////////////////////////

type VsSelector struct {
	VsIp   int // 4 or 6
	Proto  balancerpb.TransportProto
	Gre    bool
	FixMSS bool
	RealIp int // 4 or 6
}

func (vs *VsSelector) Json() string {
	if b, err := json.Marshal(vs); err != nil {
		return ""
	} else {
		return string(b)
	}
}

func SendAndValidatePacket(
	t *testing.T,
	mock *mock.YanetMock,
	b *balancer.Balancer,
	options *PacketOptions,
	selector VsSelector,
) (*framework.PacketInfo, *balancerpb.VirtualService) {
	moduleConfig, _ := b.GetConfig()
	virtualServices := moduleConfig.VirtualServices

	for vsIdx := range virtualServices {
		vs := virtualServices[vsIdx]
		vsAddr, _ := netip.AddrFromSlice(vs.Addr)
		if (vsAddr.Is4() && selector.VsIp == 4) ||
			(vsAddr.Is6() && selector.VsIp == 6) {
			if vs.Proto == selector.Proto {
				flags := vs.Flags
				if flags.FixMss == selector.FixMSS &&
					flags.Gre == selector.Gre {
					if len(vs.Reals) > 0 {
						real := vs.Reals[0]
						realAddr, _ := netip.AddrFromSlice(real.DstAddr)
						if (realAddr.Is4() && selector.RealIp == 4) ||
							(realAddr.Is6() && selector.RealIp == 6) {
							// found
							resultPacket := SendPacketToVsAndValidate(
								t,
								mock,
								b,
								options,
								vs,
							)
							return resultPacket, vs
						}
					}
				}
			}
		}
	}
	t.Error("failed to select vs")
	return nil, nil
}

type PacketOptions struct {
	MSS uint16
}

func SendPacketToVsAndValidate(
	t *testing.T,
	mock *mock.YanetMock,
	b *balancer.Balancer,
	options *PacketOptions,
	vs *balancerpb.VirtualService,
) *framework.PacketInfo {
	clientAddr := clientIpv4()
	vsAddr, _ := netip.AddrFromSlice(vs.Addr)
	if vsAddr.Is6() {
		clientAddr = clientIpv6()
	}
	clientPort := uint16(40441)

	vsPort := uint16(vs.Port)

	tcp := &layers.TCP{SYN: true}
	if vs.Proto == balancerpb.TransportProto_UDP {
		tcp = nil
	}
	packetLayers := test_utils.MakePacketLayers(
		clientAddr,
		clientPort,
		vsAddr,
		vsPort,
		tcp,
	)
	packet := xpacket.LayersToPacket(t, packetLayers...)
	if tcp != nil && options != nil {
		p, err := test_utils.InsertOrUpdateMSS(packet, options.MSS)
		require.Nil(t, err, "failed to insert mss")
		packet = *p
	}
	result, err := mock.HandlePackets(packet)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(result.Output))
	assert.Empty(t, result.Drop)

	if len(result.Output) > 0 {
		resultPacket := result.Output[0]
		moduleConfig, _ := b.GetConfig()
		test_utils.ValidatePacket(t, moduleConfig, packet, resultPacket)
		return resultPacket
	} else {
		return nil
	}
}

////////////////////////////////////////////////////////////////////////////////

func TestPacketEncapGreMSS(t *testing.T) {
	setup := allCombinationsSetup(t)
	defer setup.Free()

	mock := setup.Mock
	bal := setup.Balancer

	// test packet encapsulation without GRE and MSS

	t.Run("Encap", func(t *testing.T) {
		for _, proto := range []balancerpb.TransportProto{balancerpb.TransportProto_TCP, balancerpb.TransportProto_UDP} {
			for _, vsIp := range []int{4, 6} {
				for _, realIp := range []int{4, 6} {
					selector := VsSelector{
						VsIp:   vsIp,
						Proto:  proto,
						RealIp: realIp,
						Gre:    false,
						FixMSS: false,
					}
					t.Logf(
						"send packet: vsIp=%d, realIp=%d, proto=%s",
						selector.VsIp,
						selector.RealIp,
						selector.Proto.String(),
					)

					result, vs := SendAndValidatePacket(
						t,
						mock,
						bal,
						nil,
						selector,
					)

					assert.NotNil(t, result)
					assert.NotNil(t, vs)
				}
			}
		}
	})

	// test GRE tunneling

	t.Run("GRE", func(t *testing.T) {
		for _, proto := range []balancerpb.TransportProto{balancerpb.TransportProto_TCP, balancerpb.TransportProto_UDP} {
			for _, vsIp := range []int{4, 6} {
				for _, realIp := range []int{4, 6} {
					selector := VsSelector{
						VsIp:   vsIp,
						Proto:  proto,
						RealIp: realIp,
						Gre:    true,
						FixMSS: false,
					}

					t.Logf(
						"send packet to GRE service: vsIp=%d, realIp=%d, proto=%s",
						selector.VsIp,
						selector.RealIp,
						selector.Proto.String(),
					)

					result, vs := SendAndValidatePacket(
						t,
						mock,
						bal,
						nil,
						selector,
					)

					assert.NotNil(t, result)
					assert.NotNil(t, vs)

					if result != nil {
						assert.True(t, result.IsTunneled)
					}

					if vs != nil {
						assert.True(t, vs.Flags.Gre)
					}
				}
			}
		}
	})

	// test mss fix works

	t.Run("FixMSS", func(t *testing.T) {
		for _, mss := range []uint16{0, 500, 1200, 1400, 1460} {
			for _, realIp := range []int{4, 6} {
				selector := VsSelector{
					VsIp:   6,
					Proto:  balancerpb.TransportProto_TCP,
					RealIp: realIp,
					Gre:    false,
					FixMSS: true,
				}
				t.Logf(
					"send packet: vsIp=%d, realIp=%d, proto=%s, mss=%d",
					selector.VsIp,
					selector.RealIp,
					selector.Proto.String(),
					mss,
				)

				options := &PacketOptions{
					MSS: mss,
				}
				if mss == 0 {
					options = nil
				}

				result, vs := SendAndValidatePacket(
					t,
					mock,
					bal,
					options,
					selector,
				)

				assert.NotNil(t, result)
				assert.NotNil(t, vs)
			}
		}
	})

	// mss + gre

	t.Run("FixMSS_GRE", func(t *testing.T) {
		for _, mss := range []uint16{0, 500, 1200, 1400} {
			for _, realIp := range []int{4, 6} {
				selector := VsSelector{
					VsIp:   6,
					Proto:  balancerpb.TransportProto_TCP,
					RealIp: realIp,
					Gre:    true,
					FixMSS: true,
				}
				t.Logf(
					"send packet to GRE service: vsIp=%d, realIp=%d, proto=%s, mss=%d",
					selector.VsIp,
					selector.RealIp,
					selector.Proto.String(),
					mss,
				)

				options := &PacketOptions{
					MSS: mss,
				}
				if mss == 0 {
					options = nil
				}

				result, vs := SendAndValidatePacket(
					t,
					mock,
					bal,
					options,
					selector,
				)

				assert.NotNil(t, result)
				assert.NotNil(t, vs)
			}
		}
	})
}
