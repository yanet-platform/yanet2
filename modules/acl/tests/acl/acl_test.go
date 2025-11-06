package test_acl

import (
	"testing"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
	"github.com/yanet-platform/yanet2/tests/go/common"
	test_utils "github.com/yanet-platform/yanet2/tests/utils/go"
)

////////////////////////////////////////////////////////////////////////////////

func TestAclBasic(t *testing.T) {
	mock, err := test_utils.NewYanetMock(1<<20, 1<<27, []string{"acl"})
	require.Nil(t, err, "failed to create new yanet mock")

	agent, err := mock.AttachAgent("acl", 1<<26)
	require.Nil(t, err, "failed to attach agent")

	// fix rules
	rules := []*aclpb.Rule{
		{
			Filter: &aclpb.Filter{
				ProtoRanges: []*aclpb.ProtoRange{{
					From: 6, To: 6, // TCP only
				}},
				SrcPortRanges: []*aclpb.PortRange{{
					From: 10, To: 20,
				}},
				DstPortRanges: []*aclpb.PortRange{{
					From: 10, To: 20,
				}},
				Src4S: []*aclpb.IPNet{
					{
						Ip:        []byte{10, 2, 0, 1},
						PrefixLen: 8,
					},
				},
				Dst4S: []*aclpb.IPNet{
					{
						Ip:        []byte{15, 1, 0, 1},
						PrefixLen: 16,
					},
				},
				Devices: []string{
					"device1", // todo: fixme
				},
			},
			Action: aclpb.ActionKind_ACTION_KIND_PASS,
		},
		{
			Filter: &aclpb.Filter{
				ProtoRanges: []*aclpb.ProtoRange{{
					From: 17, To: 17, // UDP only
				}},
				SrcPortRanges: []*aclpb.PortRange{{
					From: 1000, To: 2000,
				}},
				DstPortRanges: []*aclpb.PortRange{{
					From: 5000, To: 10000,
				}},
				Src6S: []*aclpb.IPNet{
					{
						Ip:        append([]byte{10, 2, 0, 1}, make([]byte, 12)...),
						PrefixLen: 24,
					},
				},
				Dst6S: []*aclpb.IPNet{
					{
						Ip:        append([]byte{15, 1, 0, 1}, make([]byte, 12)...),
						PrefixLen: 16,
					},
				},
				Devices: []string{
					"device1", // todo: fixme
				},
			},
			Action: aclpb.ActionKind_ACTION_KIND_PASS,
		},
	}

	// create module config
	acl, err := acl.NewModuleConfig(agent, "acl0", rules)
	require.Nil(t, err, "failed to create acl")

	// prepare for updates...
	err = mock.PrepareForCpUpdate()
	require.Nil(t, err, "failed to prepare for cp update")

	// check tcp packet passes
	t.Run("Check_TCP_IPv4_Packet_Passes", func(t *testing.T) {
		tcpPacketLayers := MakeTCPPacket("10.3.15.2", 15, "15.1.5.254", 10, &layers.TCP{})
		tcpPacket := common.LayersToPacket(t, tcpPacketLayers...)
		result, err := HandlePackets(mock, acl, tcpPacket)
		require.Nil(t, err, "failed to handle packets")

		require.True(t, len(result.Output) == 1, "expected output packet")
	})

	// check udp packet passes
	t.Run("Check_UDP_IPv6_Packet_Passes", func(t *testing.T) {
		udpPacketLayers := MakeUDPPacket(
			"a02:5:101:101:101:101:101:101",
			1505,
			"f01:5fe:101:101:101:101:101:101",
			6165,
		)
		udpPacket := common.LayersToPacket(t, udpPacketLayers...)
		result, err := HandlePackets(mock, acl, udpPacket)
		require.Nil(t, err, "failed to handle packets")

		require.True(t, len(result.Output) == 1, "expected output packet")
	})
}
