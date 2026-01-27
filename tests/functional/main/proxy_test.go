package functional

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

func createProxyPacket(srcIP, dstIP net.IP, payload []byte) []byte {
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
		DstPort: 80,
		Seq:     1,
		Ack:     1,
		Window:  1024,
		PSH:     true,
		ACK:     true,
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

func TestProxy(t *testing.T) {
	fw := globalFramework.ForTest(t)
	require.NotNil(t, fw, "Global framework should be initialized")

	proxyAddr := "10.0.0.1"

	t.Run("Configure_Proxy_Module", func(t *testing.T) {
		// Proxy-specific configuration
		commands := []string{
			fmt.Sprintf("/mnt/target/release/yanet-cli-proxy addr set --cfg proxy0 --addr %s", proxyAddr),

			"/mnt/target/release/yanet-cli-function update --name=test --chains ch0:5=proxy:proxy0,route:route0",
			// Configure pipelines
			"/mnt/target/release/yanet-cli-pipeline update --name=test --functions test",
		}

		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure proxy module")
	})

	t.Run("Test_Proxying", func(t *testing.T) {
		packet := createProxyPacket(
			net.ParseIP("192.0.2.1"), // src IP (within 192.0.2.0/24)
			net.ParseIP("192.0.2.2"), // dst IP (within 192.0.2.0/24)
			[]byte("proxy test"),
		)

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")

		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		// Verify packet was forwarded with preserved addresses
		assert.Equal(t, proxyAddr, outputPacket.SrcIP.String(), "Source IP should be modified")
		assert.Equal(t, "192.0.2.2", outputPacket.DstIP.String(), "Destination IP should be preserved")
	})
}
