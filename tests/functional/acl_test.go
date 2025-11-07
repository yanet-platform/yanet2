package functional

import (
	"net"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

////////////////////////////////////////////////////////////////////////////////

func createUdpPacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte) []byte {
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.DstMAC),
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip4 := layers.IPv4{
		Version:  4,
		IHL:      5,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolUDP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	udp := layers.UDP{
		SrcPort: layers.UDPPort(srcPort),
		DstPort: layers.UDPPort(dstPort),
	}
	err := udp.SetNetworkLayerForChecksum(&ip4)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, opts, &eth, &ip4, &udp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

////////////////////////////////////////////////////////////////////////////////

func TestACL(t *testing.T) {
	fw := globalFramework
	require.NotNil(t, fw, "Global framework should be initialized")

	t.Run("Configure_ACL_Module", func(t *testing.T) {
		// Forward-specific configuration
		commands := []string{
			// Configure module
			"/mnt/target/release/yanet-cli-acl enable --cfg acl0 --rules /mnt/yanet2/acl.yaml",

			// Configure functions
			"/mnt/target/release/yanet-cli-function update --name=test --chains ch0:2=acl:acl0,route:route0 --instance=0",

			// Configure pipelines
			"/mnt/target/release/yanet-cli-pipeline update --name=test --functions test --instance=0",
		}

		_, err := fw.CLI.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure acl module")
	})

	t.Run("Test_IPv4_Packet", func(t *testing.T) {
		packet := createUdpPacket(
			net.ParseIP("192.0.2.2"),
			net.ParseIP("192.0.3.1"),
			150, 600,
			[]byte("test acl"),
		)
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		t.Log("inputPacket", inputPacket)
		t.Log("outputPacket", outputPacket)
		require.NoError(t, err, "Failed to send packet")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		require.Equal(t, inputPacket.SrcIP, outputPacket.SrcIP)
		require.Equal(t, inputPacket.DstIP, outputPacket.DstIP)
		require.Equal(t, inputPacket.SrcPort, outputPacket.SrcPort)
		require.Equal(t, inputPacket.DstPort, outputPacket.DstPort)
	})
}
