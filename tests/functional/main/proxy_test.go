package functional

import (
	"net"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

func createProxyPacket(srcIP, dstIP net.IP, srcPort, dstPort layers.TCPPort, syn, ack bool, payload []byte) []byte {
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
		SrcPort: srcPort,
		DstPort: dstPort,
		Seq:     1,
		Ack:     1,
		Window:  1024,
		PSH:     true,
		ACK:     ack,
		SYN:     syn,
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

func handlePcap(t *testing.T, fw *framework.F, sendPath, expectPath string) {
	sendHandle, err := pcap.OpenOffline(sendPath)
	if err != nil {
		panic(err)
	}
	defer sendHandle.Close()
	expectHandle, err := pcap.OpenOffline(expectPath)
	if err != nil {
		panic(err)
	}
	defer expectHandle.Close()

	sendPacketSource := gopacket.NewPacketSource(sendHandle, sendHandle.LinkType())
	expectPacketSource := gopacket.NewPacketSource(expectHandle, expectHandle.LinkType())

	buf := gopacket.NewSerializeBuffer()
	for send := range sendPacketSource.Packets() {
		expect, err := expectPacketSource.NextPacket()
		require.NoError(t, err, "Failed to read expected packet")

		err = gopacket.SerializePacket(buf, gopacket.SerializeOptions{}, send)
		require.NoError(t, err, "Failed to serialize packet")

		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, buf.Bytes(), 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		packet := gopacket.NewPacket(outputPacket.RawData, layers.LinkTypeEthernet, gopacket.Default)
		if packet.ErrorLayer() != nil {
			require.NoError(t, packet.ErrorLayer().Error())
		}
		assert.Equal(t, expect.Data(), packet.Data())
	}
}

func TestProxy(t *testing.T) {
	fw := globalFramework.ForTest(t)
	require.NotNil(t, fw, "Global framework should be initialized")

	t.Run("Configure_Proxy_Module", func(t *testing.T) {
		// Proxy-specific configuration
		commands := []string{
			"/mnt/target/release/yanet-cli-proxy conn-table-size set --cfg proxy0 --size 1024",

			"/mnt/target/release/yanet-cli-function update --name=test --chains ch0:5=proxy:proxy0,route:route0",
			// Configure pipelines
			"/mnt/target/release/yanet-cli-pipeline update --name=test --functions test",
		}

		_, err := fw.ExecuteCommands(commands...)
		require.NoError(t, err, "Failed to configure proxy module")
	})

	t.Run("Test_Proxying", func(t *testing.T) {
		// handlePcap(t, fw, "proxy/001-send.pcap", "proxy/001-expect.pcap")

		client_ip := net.ParseIP("10.0.2.1")
		client_port := 12345
		server_ip := net.ParseIP("10.0.1.1")
		server_port := 80
		local_ip := net.ParseIP("10.0.0.0")
		local_port := 32768

		// Client SYN
		packet := createProxyPacket(
			client_ip,
			server_ip,
			layers.TCPPort(client_port),
			layers.TCPPort(server_port),
			true, false,
			nil,
		)
		inputPacket, outputPacket, err := fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		assert.Equal(t, local_ip.String(), outputPacket.SrcIP.String(), "Source IP should be modified")
		assert.Equal(t, local_port, int(outputPacket.SrcPort), "Source port should be modified")
		assert.Equal(t, server_ip.String(), outputPacket.DstIP.String(), "Destination IP should be preserved")

		// Server SYN-ACK
		packet = createProxyPacket(
			server_ip,
			local_ip,
			layers.TCPPort(server_port),
			layers.TCPPort(local_port),
			true, true,
			nil,
		)
		inputPacket, outputPacket, err = fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		assert.Equal(t, server_ip.String(), outputPacket.SrcIP.String(), "Source IP should be modified")
		assert.Equal(t, 80, int(outputPacket.SrcPort), "Source port should be modified")
		assert.Equal(t, client_ip.String(), outputPacket.DstIP.String(), "Destination IP should be preserved")

		// Client ACK
		packet = createProxyPacket(
			client_ip,
			server_ip,
			layers.TCPPort(client_port),
			layers.TCPPort(server_port),
			false, true,
			nil,
		)
		inputPacket, outputPacket, err = fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		assert.Equal(t, local_ip.String(), outputPacket.SrcIP.String(), "Source IP should be modified")
		assert.Equal(t, local_port, int(outputPacket.SrcPort), "Source port should be modified")
		assert.Equal(t, server_ip.String(), outputPacket.DstIP.String(), "Destination IP should be preserved")

		// Server ACK
		packet = createProxyPacket(
			server_ip,
			local_ip,
			layers.TCPPort(server_port),
			layers.TCPPort(local_port),
			false, true,
			nil,
		)
		inputPacket, outputPacket, err = fw.SendPacketAndParse(0, 0, packet, 100*time.Millisecond)
		require.NoError(t, err, "Failed to send packet")
		require.NotNil(t, inputPacket, "Input packet should be parsed")
		require.NotNil(t, outputPacket, "Output packet should be parsed")

		assert.Equal(t, server_ip.String(), outputPacket.SrcIP.String(), "Source IP should be modified")
		assert.Equal(t, server_port, int(outputPacket.SrcPort), "Source port should be modified")
		assert.Equal(t, client_ip.String(), outputPacket.DstIP.String(), "Destination IP should be preserved")
	})
}
