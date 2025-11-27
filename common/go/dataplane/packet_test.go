package dataplane

import (
	"net"
	"runtime"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
)

////////////////////////////////////////////////////////////////////////////////

func TestPacket(t *testing.T) {
	eth := &layers.Ethernet{
		SrcMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		DstMAC:       net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip := &layers.IPv4{
		Version:  4,
		IHL:      5,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.IP{192, 168, 1, 10},
		DstIP:    net.IP{192, 168, 1, 20},
	}

	tcp := &layers.TCP{
		SrcPort: layers.TCPPort(12345),
		DstPort: layers.TCPPort(80),
		SYN:     true,
		Seq:     1105024978,
		Window:  14600,
	}
	tcp.SetNetworkLayerForChecksum(ip)

	payload := gopacket.Payload("hello")

	gopacket := xpacket.LayersToPacket(t, eth, ip, tcp, payload)

	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	gopacketData := gopacket.Data()
	pinner.Pin(&gopacketData[0])

	data := PacketData{
		Payload:    gopacketData,
		TxDeviceId: 1,
		RxDeviceId: 2,
	}
	packet, err := NewPacketFromData(data)
	require.NoError(t, err, "failed to create new packet from data")

	defer packet.Free()

	packetData := packet.Data()
	assert.Equal(t, data, packetData)

	packetInfo := packet.Info()
	assert.Equal(t, packetInfo.DstIP, ip.DstIP)
	assert.Equal(t, packetInfo.SrcIP, ip.SrcIP)
	assert.Equal(t, packetInfo.SrcPort, uint16(tcp.SrcPort))
	assert.Equal(t, packetInfo.DstPort, uint16(tcp.DstPort))
	assert.Equal(t, []byte("hello"), packetInfo.Payload)
	assert.Equal(t, packetInfo.SrcMAC, eth.SrcMAC)
	assert.Equal(t, packetInfo.DstMAC, eth.DstMAC)
}

////////////////////////////////////////////////////////////////////////////////

func genPackets(t *testing.T, pinner *runtime.Pinner, count uint64) []*Packet {
	packets := make([]*Packet, 0, count)

	for idx := range count {
		eth := &layers.Ethernet{
			SrcMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			DstMAC:       net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
			EthernetType: layers.EthernetTypeIPv4,
		}

		ip := &layers.IPv4{
			Version:  4,
			IHL:      5,
			TTL:      64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.IP{192, 168, 1, byte(idx % 255)},
			DstIP:    net.IP{192, 168, 1, byte((idx * 17) % 255)},
		}

		tcp := &layers.TCP{
			SrcPort: layers.TCPPort(12345),
			DstPort: layers.TCPPort(80),
			SYN:     true,
			Seq:     uint32(idx),
			Window:  14600,
		}
		tcp.SetNetworkLayerForChecksum(ip)

		var payload gopacket.SerializableLayer
		if idx%2 == 0 {
			payload = gopacket.Payload("hello")
		} else {
			payload = gopacket.Payload("hello5555")
		}

		data := xpacket.LayersToPacket(t, eth, ip, tcp, payload).Data()
		pinner.Pin(&data[0])

		packet, err := NewPacketFromData(PacketData{
			Payload:    data,
			TxDeviceId: uint16(idx % 1000),
			RxDeviceId: uint16(idx * 13 % 1000),
		})

		require.NoError(t, err)

		pinner.Pin(packet)

		packets = append(packets, packet)
	}

	pinner.Pin(&packets[0])

	return packets
}

////////////////////////////////////////////////////////////////////////////////

func TestPacketList(t *testing.T) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	packets := genPackets(t, &pinner, 5)

	packetList := NewPacketList(&pinner, packets)
	defer packetList.Free()

	packet := packetList.First()
	for idx := 0; packet != nil; idx += 1 {
		assert.Equal(t, packet, packets[idx])
		packet = packet.Next()
	}
}

////////////////////////////////////////////////////////////////////////////////

func TestPacketFront(t *testing.T) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	packets := genPackets(t, &pinner, 2)

	packetList := NewPacketList(&pinner, packets)

	pf := NewPacketFront(&pinner, packetList, nil, nil)
	defer pf.Free()

	assert.Equal(t, pf.InputList(), packetList)
}
