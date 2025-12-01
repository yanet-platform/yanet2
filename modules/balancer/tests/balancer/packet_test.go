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
	serviceConfigs := make([]mbalancer.VirtualServiceConfig, 0, 2*2*2*2*2)
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
						serviceConfig := mbalancer.VirtualServiceConfig{
							Info: mbalancer.VirtualServiceInfo{
								Address: vsAddr,
								Proto:   proto,
								Port:    8080,
								AllowedSrc: []netip.Prefix{
									allowed,
								},
								Flags: mbalancer.VsFlags{
									GRE:    greEnabled,
									OPS:    false,
									PureL3: false,
									FixMSS: fixMssEnabled,
								},
							},
							Reals: []mbalancer.RealConfig{
								{
									Weight:  1,
									DstAddr: realAddr,
									SrcAddr: realAddr,
									SrcMask: realAddr,
									Enabled: true,
								},
							}}
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
	b *mbalancer.ModuleInstance,
	options *PacketOptions,
	selector VsSelector,
) (*framework.PacketInfo, *mbalancer.VirtualServiceConfig) {
	virtualServices := b.GetConfig().Services
	for vsIdx := range virtualServices {
		vs := &virtualServices[vsIdx]
		vsInfo := vs.Info
		if (vsInfo.Address.Is4() && selector.VsIp == 4) ||
			(vsInfo.Address.Is6() && selector.VsIp == 6) {
			if vsInfo.Proto == selector.Proto {
				flags := &vsInfo.Flags
				if flags.FixMSS == selector.FixMSS &&
					flags.GRE == selector.Gre {
					real := &vs.Reals[0]
					if (real.DstAddr.Is4() && selector.RealIp == 4) ||
						(real.DstAddr.Is6() && selector.RealIp == 6) {
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
	t.Error("failed to select vs")
	return nil, nil
}

type PacketOptions struct {
	MSS uint16
}

func SendPacketToVsAndValidate(
	t *testing.T,
	mock *mock.YanetMock,
	balancer *mbalancer.ModuleInstance,
	options *PacketOptions,
	vs *mbalancer.VirtualServiceConfig,
) *framework.PacketInfo {
	clientAddr := clientIpv4()
	if vs.Info.Address.Is6() {
		clientAddr = clientIpv6()
	}
	clientPort := uint16(40441)

	vsAddr := vs.Info.Address
	vsPort := vs.Info.Port

	tcp := &layers.TCP{SYN: true}
	if vs.Info.Proto == mbalancer.Udp {
		tcp = nil
	}
	layers := MakePacketLayers(clientAddr, clientPort, vsAddr, vsPort, tcp)
	packet := xpacket.LayersToPacket(t, layers...)
	if tcp != nil && options != nil {
		p, err := InsertOrUpdateMSS(packet, options.MSS)
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

func TestPacketEncapGreMSS(t *testing.T) {
	setup := allCombinationsSetup(t)
	defer setup.Free()

	mock := setup.mock
	balancer := setup.balancer

	// test packet encapsulation without GRE and MSS

	t.Run("Encap", func(t *testing.T) {
		for _, proto := range []mbalancer.TransportProto{mbalancer.Tcp, mbalancer.Udp} {
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
						selector.Proto.IntoProto().String(),
					)

					result, vs := SendAndValidatePacket(
						t,
						mock,
						balancer,
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
		for _, proto := range []mbalancer.TransportProto{mbalancer.Tcp, mbalancer.Udp} {
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
						selector.Proto.IntoProto().String(),
					)

					result, vs := SendAndValidatePacket(t, mock, balancer, nil, selector)

					assert.NotNil(t, result)
					assert.NotNil(t, vs)

					if result != nil {
						assert.True(t, result.IsTunneled)
					}

					if vs != nil {
						assert.True(t, vs.Info.Flags.GRE)
					}
				}
			}
		}
	})

	// test mss fix works

	t.Run("FixMSS", func(t *testing.T) {
		for _, mss := range []uint16{0, 500, 1200, 1400} {
			for _, realIp := range []int{4, 6} {
				selector := VsSelector{
					VsIp:   6,
					Proto:  mbalancer.Tcp,
					RealIp: realIp,
					Gre:    false,
					FixMSS: true,
				}
				t.Logf(
					"send packet: vsIp=%d, realIp=%d, proto=%s, mss=%d",
					selector.VsIp,
					selector.RealIp,
					selector.Proto.IntoProto().String(),
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
					balancer,
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
					Proto:  mbalancer.Tcp,
					RealIp: realIp,
					Gre:    true,
					FixMSS: true,
				}
				t.Logf(
					"send packet to GRE service: vsIp=%d, realIp=%d, proto=%s, mss=%d",
					selector.VsIp,
					selector.RealIp,
					selector.Proto.IntoProto().String(),
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
					balancer,
					options,
					selector,
				)

				assert.NotNil(t, result)
				assert.NotNil(t, vs)
			}
		}
	})
}
