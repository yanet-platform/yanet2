package balancer

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	mock "github.com/yanet-platform/yanet2/mock/go"
	moduleBalancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"github.com/yanet-platform/yanet2/tests/go/common"
)

// test gre, fix mss, encap, not standard packets
// UDP
// test packet source address is shuffled

////////////////////////////////////////////////////////////////////////////////

func allCombinationsConfig() (*moduleBalancer.ModuleInstanceConfig, *moduleBalancer.SessionsTimeouts) {
	serviceConfigs := make([]moduleBalancer.VirtualService, 0, 2*2*2*2*2)
	for _, vsAddrVersion := range []int{4, 6} {
		for _, proto := range []moduleBalancer.TransportProto{moduleBalancer.TransportProtoTcp, moduleBalancer.TransportProtoUdp} {
			for _, greEnabled := range []bool{false, true} {
				for _, fixMssEnabled := range []bool{false, true} {
					for _, realAddr := range []netip.Addr{IpAddr("10.1.1.1"), IpAddr("fe80::1")} {
						counter := len(serviceConfigs) + 1
						vsAddr := IpAddr(fmt.Sprintf("10.12.1.%d", counter))
						allowed := IpPrefix("10.0.1.0/24")
						if vsAddrVersion == 6 {
							vsAddr = IpAddr(fmt.Sprintf("2001:db8::%d", counter))
							allowed = IpPrefix("ffff::0/16")
						}
						serviceConfig := moduleBalancer.VirtualService{
							Address: vsAddr,
							Proto:   proto,
							Port:    8080,
							AllowedSrc: []netip.Prefix{
								allowed,
							},
							Reals: []moduleBalancer.Real{
								{
									Weight:  1,
									DstAddr: realAddr,
									SrcAddr: realAddr,
									SrcMask: realAddr,
									Enabled: true,
								},
							},
							Flags: moduleBalancer.VsFlags{
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
	return &moduleBalancer.ModuleInstanceConfig{
			Services: serviceConfigs,
		}, &moduleBalancer.SessionsTimeouts{
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
	VsIp   uint64 // 4 or 6
	Proto  moduleBalancer.TransportProto
	Gre    bool
	FixMSS bool
	RealIp uint64 // 4 or 6
}

func (vs *VsSelector) Json() string {
	if b, err := json.MarshalIndent(vs, "", "  "); err != nil {
		return ""
	} else {
		return string(b)
	}
}

func SendPacket(
	t *testing.T,
	mock *mock.YanetMock,
	b *moduleBalancer.ModuleInstance,
	selector VsSelector,
) (*framework.PacketInfo, *moduleBalancer.VirtualService) {
	t.Log("send packet to vs:", selector.Json())
	virtualServices := b.GetConfig().Services
	for vsIdx := range virtualServices {
		vs := &virtualServices[vsIdx]
		if (vs.Address.Is4() && selector.VsIp == 4) || (vs.Address.Is6() && selector.VsIp == 6) {
			if vs.Proto == selector.Proto {
				flags := &vs.Flags
				if flags.FixMSS == selector.FixMSS && flags.GRE == selector.Gre {
					real := &vs.Reals[0]
					if (real.DstAddr.Is4() && selector.RealIp == 4) ||
						(real.DstAddr.Is6() && selector.RealIp == 6) {
						// found
						resultPacket := SendPacketToVs(t, mock, b, vs)
						return resultPacket, vs
					}
				}
			}
		}
	}
	t.Error("failed to select vs")
	return nil, nil
}

func SendPacketToVs(
	t *testing.T,
	mock *mock.YanetMock,
	b *moduleBalancer.ModuleInstance,
	vs *moduleBalancer.VirtualService,
) *framework.PacketInfo {
	clientAddr := clientIpv4()
	if vs.Address.Is6() {
		clientAddr = clientIpv6()
	}
	clientPort := uint16(40441)

	vsAddr := vs.Address
	vsPort := vs.Port

	tcp := &layers.TCP{SYN: true}
	if vs.Proto == moduleBalancer.TransportProtoUdp {
		tcp = nil
	}
	layers := MakePacketLayers(clientAddr, clientPort, vsAddr, vsPort, tcp)
	packet := common.LayersToPacket(t, layers...)
	result, err := mock.HandlePackets(packet)
	assert.Nil(t, err)
	assert.Equal(t, 1, len(result.Output))
	assert.Empty(t, result.Drop)
	resultPacket := result.Output[0]
	ValidatePacket(t, b.GetConfig(), packet, resultPacket)
	return resultPacket
}

////////////////////////////////////////////////////////////////////////////////

func TestPacketGRE(t *testing.T) {
	setup := allCombinationsSetup(t)
	defer setup.Free()

	mock := setup.mock
	balancer := setup.balancer

	SendPacket(t, mock, balancer, VsSelector{
		VsIp:   4,
		Proto:  moduleBalancer.TransportProtoTcp,
		RealIp: 4,
		Gre:    true,
		FixMSS: false,
	})
}

////////////////////////////////////////////////////////////////////////////////

func TestPacketFixMSS(t *testing.T) {

}

////////////////////////////////////////////////////////////////////////////////

func TestPacketFixMssPlusGre(t *testing.T) {

}

////////////////////////////////////////////////////////////////////////////////
