package test_balancer

import (
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	cp "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/tests/go/common"
	test_utils "github.com/yanet-platform/yanet2/tests/utils/go"
)

////////////////////////////////////////////////////////////////////////////////

func TestPacketPlusUpdateReals(t *testing.T) {
	mock, err := test_utils.NewYanetMock(1<<20, 1<<27, []string{"balancer"})
	require.Nil(t, err, "failed to create mock: %w", err)
	defer mock.Free()

	agent, err := mock.AttachAgent("balancer", 1<<24)
	require.Nil(t, err, "failed to create agent: %w", err)

	config := cp.ModuleInstanceConfig{
		Services: []cp.VirtualService{
			{
				Address: IpAddr("192.166.13.22"),
				Port:    1000,
				Flags: cp.VsFlags{
					GRE:    false,
					OPS:    false,
					PureL3: false,
					FixMSS: false,
				},
				Proto: cp.VsProtoTcp,
				AllowedSrc: []netip.Prefix{
					IpPrefix("10.12.0.0/8"),
				},
				Reals: []cp.Real{
					{
						Weight:  1,
						DstAddr: IpAddr("1.1.1.1"),
						SrcAddr: IpAddr("3.3.3.3"),
						SrcMask: IpAddr("255.240.255.0"),
						Enabled: true,
					},
				},
			},
		},
	}

	err = mock.PrepareForCpUpdate()
	require.Nil(t, err, "failed to prepare mock for cp update before create balancer instance")

	balancer, err := cp.NewModuleInstance(agent, "balancer", &config, 100)
	require.Nil(t, err, "failed to create new balancer instance")
	defer balancer.Free()

	inLayers := MakeTCPPacket("10.12.15.1", 1005, "192.166.13.22", 1000, &layers.TCP{SYN: true})
	originPacket := common.LayersToPacket(t, inLayers...)
	t.Log("Origin packet", originPacket)

	result, err := HandlePackets(balancer, mock, originPacket)
	require.Nil(t, err, "failed to handle packet1: %s", err)

	require.True(t, len(result.Output) == 1, "failed to handle packet #1")
	require.True(t, len(result.Input) == 0)
	require.True(t, len(result.Drop) == 0)

	resultPacket := result.Output[0]
	t.Log("Result packet", resultPacket)
	require.True(t, resultPacket.IsTunneled, "result packet is not tunneled")

	err = mock.PrepareForCpUpdate()
	require.Nil(t, err, "failed to prepare mock for cp update before update reals")

	err = balancer.UpdateReals([]*balancerpb.RealUpdate{
		{
			VirtualIp: []byte("192.166.13.22"),
			Proto:     "TCP",
			Port:      1000,
			RealIp:    []byte("1.1.1.1"),
			Weight:    5,
			Enable:    true,
		},
	}, true)
	require.NoError(t, err, "failed to handle real update")

	flushed, err := balancer.FlushRealUpdatesBuffer()
	require.NoError(t, err, "failed to flush real updates buffer")
	require.Equal(t, uint32(1), flushed)

	require.Equal(t, uint16(5), balancer.GetConfig().Services[0].Reals[0].Weight)
}

////////////////////////////////////////////////////////////////////////////////

func TestGRE(t *testing.T) {
	mock, err := test_utils.NewYanetMock(1<<20, 1<<27, []string{"balancer"})
	require.Nil(t, err, "failed to create mock: %w", err)
	defer mock.Free()

	agent, err := mock.AttachAgent("balancer", 1<<24)
	require.Nil(t, err, "failed to attach agent: %w", err)

	config := cp.ModuleInstanceConfig{
		Services: []cp.VirtualService{
			{
				Address: IpAddr("192.166.13.22"),
				Port:    1000,
				Flags: cp.VsFlags{
					GRE:    true,
					OPS:    false,
					PureL3: false,
					FixMSS: false,
				},
				Proto: cp.VsProtoTcp,
				AllowedSrc: []netip.Prefix{
					IpPrefix("10.12.0.0/8"),
				},
				Reals: []cp.Real{
					{
						Weight:  1,
						DstAddr: IpAddr("1.1.1.1"),
						SrcAddr: IpAddr("3.3.3.3"),
						SrcMask: IpAddr("255.240.255.0"),
						Enabled: true,
					},
				},
			},
		},
	}

	err = mock.PrepareForCpUpdate()
	require.Nil(t, err, "failed to prepare for cp update")

	balancer, err := cp.NewModuleInstance(agent, "balancer", &config, 100)
	require.Nil(t, err, "failed to create new balancer instance")
	defer balancer.Free()

	inLayers := MakeTCPPacket("10.12.15.1", 1005, "192.166.13.22", 1000, &layers.TCP{SYN: true})
	originPacket := common.LayersToPacket(t, inLayers...)
	t.Log("Origin packet", originPacket)

	result, err := HandlePackets(balancer, mock, originPacket)
	require.Nil(t, err, "failed to handle packet1: %s", err)

	require.True(t, len(result.Output) == 1, "failed to handle packet #1")
	require.True(t, len(result.Input) == 0)
	require.True(t, len(result.Drop) == 0)

	resultPacket := result.Output[0]
	require.True(t, resultPacket.IsTunneled, "result packet is not tunneled")
	require.Equal(t, resultPacket.TunnelType, "gre-ip4", "tunnel type must be GRE")

	require.Equal(t, resultPacket.Protocol, layers.IPProtocolGRE)
	require.Equal(t, resultPacket.DstIP.String(), "1.1.1.1")
	require.Equal(t, resultPacket.DstPort, uint16(1000))

	require.Equal(t, resultPacket.InnerPacket.DstIP.String(), "192.166.13.22")
	require.Equal(t, resultPacket.InnerPacket.SrcIP.String(), "10.12.15.1")
	require.Equal(t, resultPacket.InnerPacket.Protocol, layers.IPProtocolTCP)
}
