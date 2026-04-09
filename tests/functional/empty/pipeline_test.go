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

func createLoopTestPacket(srcIP, dstIP net.IP, payload []byte) []byte {
	// Framework skip packets with MAC not equal to the framework one
	eth := layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(framework.SrcMAC),
		DstMAC:       framework.MustParseMAC(framework.SrcMAC),
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

// Test checks that no packet will be forwarded through device if
// pipeline assigment map is zero
func TestNoPackets(t *testing.T) {
	fw := globalFramework.ForTest(t)
	require.NotNil(t, fw, "Global framework should be initialized")

	fw.Run("Configure_Device_No_Pipelines", func(fw *framework.F, t *testing.T) {
		commands := []string{
			framework.CLIPipeline + " update --name=dummy-in",
			framework.CLIPipeline + " update --name=dummy-out",
			framework.CLIDevicePlain + " update --name=virtio_user_kni0 --input dummy-in:1 --output dummy-out:1",
		}

		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure forward module")
	})

	fw.Run("Test_Packet_Processing", func(fw *framework.F, t *testing.T) {

		packet := createLoopTestPacket(
			net.ParseIP("192.0.2.100"), // src IP
			net.ParseIP("192.16.0.10"), // dst IP
			[]byte(make([]byte, 1000)),
		)

		// Try to receive packet on interface 0
		inputPacket, outputPacket0, err := fw.SendPacketAndParse(0, 0, packet, 200*time.Millisecond)
		require.Error(t, err, "Should get error when packet is dropped")
		var netErr0 net.Error
		require.ErrorAs(t, err, &netErr0, "Error should be a net.Error")
		require.True(t, netErr0.Timeout(), "Error should be a timeout")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.Nil(t, outputPacket0, "Packet should be dropped - no pipelines")

	})
}
