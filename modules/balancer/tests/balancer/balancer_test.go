package balancer_test

////////////////////////////////////////////////////////////////////////////////

import (
	"net/netip"
	"os"
	"testing"
	"unsafe"

	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	cp "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	"github.com/yanet-platform/yanet2/tests/go/common"
)

////////////////////////////////////////////////////////////////////////////////

var arena unsafe.Pointer
var arenaSize uint64

////////////////////////////////////////////////////////////////////////////////

func TestMain(m *testing.M) {
	arenaSize = 1 << 27
	arena, _ = AllocateBalancerArena(arenaSize)
	if arena == nil {
		os.Exit(1)
	}

	exitCode := m.Run()
	os.Exit(exitCode)
}

////////////////////////////////////////////////////////////////////////////////

func TestMakeBalancerWorks(t *testing.T) {
	balancer, err := MakeBalancer(arena, arenaSize, 2, 100, cp.Timeouts{
		TcpSynAckTtl: 1,
		TcpSynTtl:    2,
		TcpFinTtl:    3,
		TcpTtl:       4,
		UdpTtl:       5,
		DefaultTtl:   6,
	})
	require.NotNil(t, balancer, "failed to make balancer: %s", err)
}

////////////////////////////////////////////////////////////////////////////////

func TestBasicIPv4(t *testing.T) {
	// Make balancer
	balancer, err := MakeBalancer(arena, arenaSize, 2, 100, cp.Timeouts{
		TcpSynAckTtl: 1,
		TcpSynTtl:    2,
		TcpFinTtl:    3,
		TcpTtl:       4,
		UdpTtl:       5,
		DefaultTtl:   6,
	})
	require.NotNil(t, balancer, "failed to make balancer: %s", err)

	// Add first service (1.1.1.0:10001 tcp)
	balancer.AddService(cp.Service{
		Addr:  IpAddr("1.1.1.0"),
		Port:  10001,
		Proto: cp.ServiceProtoTcp,
		Prefixes: []netip.Prefix{
			IpPrefix("10.0.0.0/12"),
			IpPrefix("10.240.0.0/12"),
		},
		Reals: []cp.Real{
			{
				Weight:  1,
				DstAddr: IpAddr("1.1.1.1"),
				SrcAddr: IpAddr("12.13.255.14"),
				SrcMask: IpAddr("255.0.127.255"),
			},
		},
		GRE:                false,
		FixMss:             false,
		OnePacketScheduler: false,
		PureL3:             false,
	})

	// Send syn packet to service
	inLayers := MakeTCPPacket("10.11.1.10", 1005, "1.1.1.0", 10001, &layers.TCP{SYN: true})
	originPacket := common.LayersToPacket(t, inLayers...)
	t.Log("Origin packet", originPacket)

	expectedPacket := Encap(t, inLayers, "12.11.127.14", "1.1.1.1")
	t.Log("Expected packet", expectedPacket)

	result, err := balancer.HandlePackets(0, originPacket)
	require.Nil(t, err, "failed to handle packet1: %s", err)

	require.True(t, len(result.Output) == 1, "failed to handle packet1")
	resultPacket := common.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPacket)

	// Ensure packets equal
	CheckPacketsEqual(t, resultPacket, expectedPacket)
}

////////////////////////////////////////////////////////////////////////////////

func TestBasicIPv6(t *testing.T) {
	// Make balancer
	balancer, err := MakeBalancer(arena, arenaSize, 2, 100, cp.Timeouts{
		TcpSynAckTtl: 1,
		TcpSynTtl:    2,
		TcpFinTtl:    3,
		TcpTtl:       4,
		UdpTtl:       5,
		DefaultTtl:   6,
	})
	require.NotNil(t, balancer, "failed to make balancer: %s", err)

	// Add first service (2001:db8:2:::10001 tcp)
	balancer.AddService(cp.Service{
		Addr:  IpAddr("2001:db8:2::"),
		Port:  10001,
		Proto: cp.ServiceProtoTcp,
		Prefixes: []netip.Prefix{
			IpPrefix("2001:db8::/32"),
		},
		Reals: []cp.Real{
			{
				Weight:  1,
				DstAddr: IpAddr("2001:db8:3::"),
				SrcAddr: IpAddr("2000::"),
				SrcMask: IpAddr("ff::"),
			},
		},
		GRE:                false,
		FixMss:             false,
		OnePacketScheduler: false,
		PureL3:             false,
	})

	// Send syn packet to service
	inLayers := MakeTCPPacket("2001:db8:1::", 1005, "2001:db8:2::", 10001, &layers.TCP{SYN: true})
	originPacket := common.LayersToPacket(t, inLayers...)
	t.Log("Origin packet", originPacket)

	expectedPacket := Encap(t, inLayers, "2000:db8:1::", "2001:db8:3::")
	t.Log("Expected packet", expectedPacket)

	result, err := balancer.HandlePackets(0, originPacket)
	require.Nil(t, err, "failed to handle packet1: %s", err)

	require.True(t, len(result.Output) == 1, "failed to handle packet1")
	resultPacket := common.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPacket)

	// Ensure packets equal
	CheckPacketsEqual(t, resultPacket, expectedPacket)
}

////////////////////////////////////////////////////////////////////////////////

func TestFixMssIPv6(t *testing.T) {
	// Make balancer
	balancer, err := MakeBalancer(arena, arenaSize, 2, 100, cp.Timeouts{
		TcpSynAckTtl: 1,
		TcpSynTtl:    2,
		TcpFinTtl:    3,
		TcpTtl:       4,
		UdpTtl:       5,
		DefaultTtl:   6,
	})
	require.NotNil(t, balancer, "failed to make balancer: %s", err)

	// Add first service (2001:db8:2:::10001 tcp)
	balancer.AddService(cp.Service{
		Addr:  IpAddr("2001:db8:2::"),
		Port:  10001,
		Proto: cp.ServiceProtoTcp,
		Prefixes: []netip.Prefix{
			IpPrefix("2001:db8::/32"),
		},
		Reals: []cp.Real{
			{
				Weight:  1,
				DstAddr: IpAddr("2001:db8:3::"),
				SrcAddr: IpAddr("2000::"),
				SrcMask: IpAddr("ff::"),
			},
		},
		GRE:                false,
		FixMss:             true,
		OnePacketScheduler: false,
		PureL3:             false,
	})

	// Send syn packet without MSS
	inLayers := MakeTCPPacket("2001:db8:1::", 1005, "2001:db8:2::", 10001, &layers.TCP{SYN: true})
	originPacket := common.LayersToPacket(t, inLayers...)
	t.Log("Origin packet", originPacket)

	expectedPacket := Encap(t, inLayers, "2000:db8:1::", "2001:db8:3::")
	expectedPacketPtr, err := InsertOrUpdateMSS(expectedPacket, 536)
	require.Nil(t, err, "failed to insert mss: %s", err)
	expectedPacket = *expectedPacketPtr
	t.Log("Expected packet", expectedPacket)

	result, err := balancer.HandlePackets(0, originPacket)
	require.Nil(t, err, "failed to handle packet1: %s", err)

	require.True(t, len(result.Output) == 1, "failed to handle packet1")
	resultPacket := common.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPacket)

	// Ensure packets equal
	CheckPacketsEqual(t, resultPacket, expectedPacket)

	// Send syn packet with big MSS

	originPacketPtr, err := InsertOrUpdateMSS(originPacket, 1440)
	require.Nil(t, err, "failed to insert mss: %s", err)
	originPacket = *originPacketPtr

	expectedPacket = Encap(t, inLayers, "2000:db8:1::", "2001:db8:3::")
	expectedPacketPtr, err = InsertOrUpdateMSS(expectedPacket, 1220)
	require.Nil(t, err, "failed to insert mss: %s", err)
	expectedPacket = *expectedPacketPtr
	t.Log("Expected packet", expectedPacket)

	result, err = balancer.HandlePackets(0, originPacket)
	require.Nil(t, err, "failed to handle packet1: %s", err)

	require.True(t, len(result.Output) == 1, "failed to handle packet1")
	resultPacket = common.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPacket)
}

////////////////////////////////////////////////////////////////////////////////

func TestFixMssIPv4(t *testing.T) {
	// Make balancer
	balancer, err := MakeBalancer(arena, arenaSize, 2, 100, cp.Timeouts{
		TcpSynAckTtl: 1,
		TcpSynTtl:    2,
		TcpFinTtl:    3,
		TcpTtl:       4,
		UdpTtl:       5,
		DefaultTtl:   6,
	})
	require.NotNil(t, balancer, "failed to make balancer: %s", err)

	// Add first service (1.1.1.0:10001 tcp)
	balancer.AddService(cp.Service{
		Addr:  IpAddr("1.1.1.0"),
		Port:  10001,
		Proto: cp.ServiceProtoTcp,
		Prefixes: []netip.Prefix{
			IpPrefix("10.0.0.0/12"),
			IpPrefix("10.240.0.0/12"),
		},
		Reals: []cp.Real{
			{
				Weight:  1,
				DstAddr: IpAddr("1.1.1.1"),
				SrcAddr: IpAddr("12.13.255.14"),
				SrcMask: IpAddr("255.0.127.255"),
			},
		},
		GRE:                false,
		FixMss:             true,
		OnePacketScheduler: false,
		PureL3:             false,
	})

	// Send syn packet to service
	inLayers := MakeTCPPacket("10.11.1.10", 1005, "1.1.1.0", 10001, &layers.TCP{SYN: true})
	originPacket := common.LayersToPacket(t, inLayers...)
	t.Log("Origin packet", originPacket)

	expectedPacket := Encap(t, inLayers, "12.11.127.14", "1.1.1.1")
	t.Log("Expected packet", expectedPacket)

	result, err := balancer.HandlePackets(0, originPacket)
	require.Nil(t, err, "failed to handle packet1: %s", err)

	require.True(t, len(result.Output) == 1, "failed to handle packet1")
	resultPacket := common.ParseEtherPacket(result.Output[0])
	t.Log("Result packet", resultPacket)

	// Ensure packets equal
	CheckPacketsEqual(t, resultPacket, expectedPacket)
}
