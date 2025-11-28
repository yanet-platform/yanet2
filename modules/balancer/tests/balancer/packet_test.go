package balancer

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
	mbalancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// test gre, fix mss, encap, not standard packets
// UDP
// test packet source address is shuffled

////////////////////////////////////////////////////////////////////////////////

func allCombinationsConfig() (*mbalancer.ModuleInstanceConfig, *mbalancer.SessionsTimeouts) {
	serviceConfigs := make([]mbalancer.VirtualService, 0, 2*2*2*2*2)
	for _, vsAddrVersion := range []int{4, 6} {
		for _, proto := range []mbalancer.TransportProto{mbalancer.Tcp, mbalancer.Udp} {
			for _, greEnabled := range []bool{false, true} {
				for _, fixMssEnabled := range []bool{false, true} {
					for _, realAddr := range []netip.Addr{IpAddr("10.1.1.1"), IpAddr("fe80::1")} {
						counter := len(serviceConfigs) + 1
						vsAddr := IpAddr(fmt.Sprintf("10.12.1.%d", counter))
						allowed := IpPrefix("10.0.1.0/24")
						if vsAddrVersion == 6 {
							vsAddr = IpAddr(
								fmt.Sprintf("2001:db8::%d", counter),
							)
							allowed = IpPrefix("ffff::0/16")
						}
						serviceConfig := mbalancer.VirtualService{
							Address: vsAddr,
							Proto:   proto,
							Port:    8080,
							AllowedSrc: []netip.Prefix{
								allowed,
							},
							Reals: []mbalancer.Real{
								{
									Weight:  1,
									DstAddr: realAddr,
									SrcAddr: realAddr,
									SrcMask: realAddr,
									Enabled: true,
								},
							},
							Flags: mbalancer.VsFlags{
								GRE:    greEnabled,
								OPS:    false,
								PureL3: false,
								FixMSS: fixMssEnabled,
							},
						}
						serviceConfigs = append(serviceConfigs, serviceConfig)
					}
				}
			}
		}
	}
	return &mbalancer.ModuleInstanceConfig{
			Services: serviceConfigs,
		}, &mbalancer.SessionsTimeouts{
			TcpSynAck: 10,
			TcpSyn:    10,
			TcpFin:    10,
			Tcp:       10,
			Udp:       10,
			Default:   10,
		}
}

////////////////////////////////////////////////////////////////////////////////

func allCombinationsTestConfig() *TestConfig {
	balancerConfig, timeouts := allCombinationsConfig()
	return &TestConfig{
		balancer:         balancerConfig,
		timeouts:         timeouts,
		sessionTableSize: 1024,
	}
}

func allCombinationsSetup(t *testing.T) *TestSetup {
	config := allCombinationsTestConfig()
	setup, err := SetupTest(config)
	require.NoError(t, err)
	return setup
}

////////////////////////////////////////////////////////////////////////////////

func clientIpv4() netip.Addr {
	return IpAddr("10.0.1.1")
}

func clientIpv6() netip.Addr {
	return IpAddr("ffff::1")
}

////////////////////////////////////////////////////////////////////////////////

type VsSelector struct {
	VsIp   int // 4 or 6
	Proto  mbalancer.TransportProto
	Gre    bool
	FixMSS int
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
	b *mbalancer.ModuleInstance,
	selector VsSelector,
) (*framework.PacketInfo, *mbalancer.VirtualService) {
	virtualServices := b.GetConfig().Services
	for vsIdx := range virtualServices {
		vs := &virtualServices[vsIdx]
		if (vs.Address.Is4() && selector.VsIp == 4) ||
			(vs.Address.Is6() && selector.VsIp == 6) {
			if vs.Proto == selector.Proto {
				flags := &vs.Flags
				if flags.FixMSS == (selector.FixMSS > 0) &&
					flags.GRE == selector.Gre {
					real := &vs.Reals[0]
					if (real.DstAddr.Is4() && selector.RealIp == 4) ||
						(real.DstAddr.Is6() && selector.RealIp == 6) {
						// found
						resultPacket := SendPacketToVsAndValidate(
							t,
							mock,
							b,
							vs,
							uint16(selector.FixMSS),
						)
						return resultPacket, vs
					}
				}
			}
		}
	}
	t.Error("failed to select vs")
	return nil, nil
}

func SendPacketToVsAndValidate(
	t *testing.T,
	mock *mock.YanetMock,
	balancer *mbalancer.ModuleInstance,
	vs *mbalancer.VirtualService,
	mss uint16,
) *framework.PacketInfo {
	clientAddr := clientIpv4()
	if vs.Address.Is6() {
		clientAddr = clientIpv6()
	}
	clientPort := uint16(40441)

	vsAddr := vs.Address
	vsPort := vs.Port

	tcp := &layers.TCP{SYN: true}
	if vs.Proto == mbalancer.Udp {
		tcp = nil
	}
	layers := MakePacketLayers(clientAddr, clientPort, vsAddr, vsPort, tcp)
	packet := xpacket.LayersToPacket(t, layers...)
	if tcp != nil {
		p, err := InsertOrUpdateMSS(packet, 1200)
		require.Nil(t, err, "failed to insert mss")
		packet = *p
	}
	result, err := mock.HandlePackets(packet)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(result.Output))
	assert.Empty(t, result.Drop)

	if len(result.Output) > 0 {
		resultPacket := result.Output[0]
		ValidatePacket(t, balancer.GetConfig(), packet, resultPacket)
		return resultPacket
	} else {
		return nil
	}
}

////////////////////////////////////////////////////////////////////////////////

func TestPacketBasic(t *testing.T) {
	setup := allCombinationsSetup(t)
	defer setup.Free()

	mock := setup.mock
	balancer := setup.balancer

	// t.Run("Send_GRE_IpV4_IpV6", func(t *testing.T) {
	// 	result, vs := SendAndValidatePacket(t, mock, balancer, VsSelector{
	// 		VsIp:   4,
	// 		Proto:  mbalancer.Tcp,
	// 		RealIp: 6,
	// 		Gre:    true,
	// 		FixMSS: 0,
	// 	})

	// 	assert.NotNil(t, result)
	// 	assert.NotNil(t, vs)

	// 	if result != nil {
	// 		assert.True(t, result.IsTunneled)
	// 		assert.Equal(t, result.TunnelType, "gre")
	// 	}

	// 	if vs != nil {
	// 		assert.True(t, vs.Flags.GRE)
	// 	}
	// })

	// test packet encapsulation

	t.Run("Encap", func(t *testing.T) {
		for _, proto := range []mbalancer.TransportProto{mbalancer.Tcp, mbalancer.Udp} {
			for _, vsIp := range []int{4, 6} {
				for _, realIp := range []int{4, 6} {
					selector := VsSelector{
						VsIp:   vsIp,
						Proto:  proto,
						RealIp: realIp,
						Gre:    false,
						FixMSS: 0,
					}
					t.Logf(
						"send packet: vsIp=%d, realIp=%d, proto=%s",
						selector.VsIp,
						selector.RealIp,
						selector.Proto.IntoProto().String(),
					)

					result, vs := SendAndValidatePacket(
						t,
						mock,
						balancer,
						selector,
					)

					assert.NotNil(t, result)
					assert.NotNil(t, vs)
				}
			}
		}
	})

	// test gre packets

	// t.Run("GRE", func(t *testing.T) {
	// 	result, vs := SendAndValidatePacket(t, mock, balancer, VsSelector{
	// 		VsIp:   4,
	// 		Proto:  mbalancer.Tcp,
	// 		RealIp: 4,
	// 		Gre:    true,
	// 		FixMSS: false,
	// 	})

	// 	assert.NotNil(t, result)
	// 	assert.NotNil(t, vs)

	// 	if result != nil {
	// 		assert.True(t, result.IsTunneled)
	// 		assert.Equal(t, result.TunnelType, "gre-ip4")
	// 	}

	// 	if vs != nil {
	// 		assert.True(t, vs.Flags.GRE)
	// 	}
	// })

	// t.Run("Send_GRE_IpV6_IpV4", func(t *testing.T) {
	// 	result, vs := SendPacket(t, mock, balancer, VsSelector{
	// 		VsIp:   6,
	// 		Proto:  moduleBalancer.TransportProtoTcp,
	// 		RealIp: 4,
	// 		Gre:    true,
	// 		FixMSS: false,
	// 	})
	// 	assert.NotNil(t, result)
	// 	assert.NotNil(t, vs)

	// 	if result != nil {
	// 		assert.True(t, result.IsTunneled)
	// 		assert.Equal(t, result.TunnelType, "gre-ip6")
	// 	}

	// 	if vs != nil {
	// 		assert.True(t, vs.Flags.GRE)
	// 	}
	// })

	// t.Run("Send_GRE_IpV6_IpV6", func(t *testing.T) {
	// 	result, vs := SendAndValidatePacket(t, mock, balancer, VsSelector{
	// 		VsIp:   6,
	// 		Proto:  mbalancer.Tcp,
	// 		RealIp: 6,
	// 		Gre:    true,
	// 		FixMSS: false,
	// 	})

	// 	assert.NotNil(t, result)
	// 	assert.NotNil(t, vs)

	// 	if result != nil {
	// 		assert.True(t, result.IsTunneled)
	// 		assert.Equal(t, result.TunnelType, "gre-ip6")
	// 	}

	// 	if vs != nil {
	// 		assert.True(t, vs.Flags.GRE)
	// 	}
	// })

	t.Run("FixMSS", func(t *testing.T) {
		for _, vsIp := range []int{4, 6} {
			for _, realIp := range []int{4, 6} {
				selector := VsSelector{
					VsIp:   vsIp,
					Proto:  mbalancer.Tcp,
					RealIp: realIp,
					Gre:    false,
					FixMSS: 1000,
				}
				t.Logf(
					"send packet: vsIp=%d, realIp=%d, proto=%s, mss=%d",
					selector.VsIp,
					selector.RealIp,
					selector.Proto.IntoProto().String(),
					selector.FixMSS,
				)

				result, vs := SendAndValidatePacket(
					t,
					mock,
					balancer,
					selector,
				)

				assert.NotNil(t, result)
				assert.NotNil(t, vs)
			}
		}
	})
}

////////////////////////////////////////////////////////////////////////////////

func TestPacketFixMSS(t *testing.T) {

}

////////////////////////////////////////////////////////////////////////////////

func TestPacketFixMssPlusGre(t *testing.T) {

}

////////////////////////////////////////////////////////////////////////////////
