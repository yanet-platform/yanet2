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

func createTcpPacket(srcIP, dstIP net.IP, payload []byte, SYN bool) []byte {
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
		Protocol: layers.IPProtocolTCP,
		SrcIP:    srcIP,
		DstIP:    dstIP,
	}

	tcp := layers.TCP{
		SrcPort: 12345,
		DstPort: 5005,
		SYN:     SYN,
	}
	err := tcp.SetNetworkLayerForChecksum(&ip4)
	if err != nil {
		panic(err)
	}

	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	err = gopacket.SerializeLayers(buf, opts, &eth, &ip4, &tcp, gopacket.Payload(payload))
	if err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestBalancer(t *testing.T) {
	fw := globalFramework
	require.NotNil(t, fw, "Global framework should be initialized")

	t.Run("Configure_Balancer_Module", func(t *testing.T) {
		// Forward-specific configuration
		commands := []string{
			// Configure module
			"/mnt/target/release/yanet-cli-balancer enable --cfg balancer0 --services /mnt/yanet2/balancer.yaml",

			// Configure functions
			"/mnt/target/release/yanet-cli-function update --name=test --chains ch0:2=balancer:balancer0,route:route0 --instance=0",

			// Configure pipelines
			"/mnt/target/release/yanet-cli-pipeline update --name=test --functions test --instance=0",
		}

		_, err := fw.CLI.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure balancer module")
	})

	t.Run("Test_IPv4_Packet", func(t *testing.T) {
		packet := createTcpPacket(
			net.ParseIP("192.0.2.2"),
			net.ParseIP("192.0.2.1"),
			[]byte("test balancer"),
			true,
		)
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		t.Log("inputPacket", inputPacket)
		t.Log("outputPacket", outputPacket)
		require.NoError(t, err, "Failed to send packet")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")
		require.True(t, outputPacket.IsTunneled, "Output packet should be tunneled")
		require.Equal(t, outputPacket.DstIP.String(), "4.5.6.7")
		require.Equal(t, outputPacket.InnerPacket.SrcIP.String(), "192.0.2.2")
		require.True(t, outputPacket.InnerPacket.IsIPv4)
	})

	t.Run("Enable_Enabled_Real", func(t *testing.T) {
		commands := []string{
			// Enable already enabled real
			"/mnt/target/release/yanet-cli-balancer real enable --cfg balancer0 --virtual-ip \"192.0.2.1\" --proto \"TCP\" --virtual-port 5005 --real-ip \"4.5.6.7\" --real-weight 5",

			// Flush enable
			"/mnt/target/release/yanet-cli-balancer real flush --cfg balancer0",
		}

		_, err := fw.CLI.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to enable real")
	})
}
