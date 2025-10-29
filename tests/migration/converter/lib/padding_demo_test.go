package lib

import (
	"encoding/hex"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// TestPaddingDemo demonstrates where Ethernet padding is added
func TestPaddingDemo(t *testing.T) {
	t.Log("=== Демонстрация добавления Ethernet padding ===\n")

	// Создаем маленький пакет: Ethernet + VLAN + IPv4 + TCP (без payload)
	// Ethernet: 14 байт
	// VLAN: 4 байта
	// IPv4: 20 байт
	// TCP: 20 байт
	// Итого: 58 байт (меньше минимума 60)

	t.Log("Шаг 1: Создаем пакет через NewPacket()")
	pkt, err := NewPacket(
		Ether(
			EtherSrc(framework.SrcMAC),
			EtherDst(framework.DstMAC),
		),
		Dot1Q(VLANId(200)),
		IP(
			IPSrc("192.168.1.1"),
			IPDst("192.168.1.2"),
			IPTTL(64),
		),
		TCP(
			TCPSport(12345),
			TCPDport(80),
		),
	)
	if err != nil {
		t.Fatalf("Failed to create packet: %v", err)
	}

	// Проверяем размер сериализованного пакета
	serializedBytes := pkt.Data()
	t.Logf("   Размер сериализованного пакета: %d байт", len(serializedBytes))
	t.Logf("   Hex dump:\n%s", hex.Dump(serializedBytes))

	// Проверяем, что gopacket НЕ добавил padding
	if len(serializedBytes) == 58 {
		t.Log("   ✅ gopacket НЕ добавил padding (58 байт)")
	} else if len(serializedBytes) == 60 {
		t.Log("   ⚠️  gopacket добавил padding до 60 байт")
	} else {
		t.Logf("   ⚠️  Неожиданный размер: %d байт", len(serializedBytes))
	}

	t.Log("\nШаг 2: Парсим пакет обратно")
	parsedPkt := gopacket.NewPacket(serializedBytes, layers.LayerTypeEthernet, gopacket.Default)

	// Проверяем слои
	ethLayer := parsedPkt.Layer(layers.LayerTypeEthernet)
	if ethLayer != nil {
		eth := ethLayer.(*layers.Ethernet)
		t.Logf("   Ethernet.BaseLayer.Contents: %d байт", len(eth.Contents))
		t.Logf("   Ethernet.BaseLayer.Payload:  %d байт", len(eth.Payload))
		t.Logf("   Payload hex:\n%s", hex.Dump(eth.Payload))
	}

	ipLayer := parsedPkt.Layer(layers.LayerTypeIPv4)
	if ipLayer != nil {
		ip := ipLayer.(*layers.IPv4)
		t.Logf("   IPv4.BaseLayer.Contents: %d байт", len(ip.Contents))
		t.Logf("   IPv4.BaseLayer.Payload:  %d байт", len(ip.Payload))
	}

	tcpLayer := parsedPkt.Layer(layers.LayerTypeTCP)
	if tcpLayer != nil {
		tcp := tcpLayer.(*layers.TCP)
		t.Logf("   TCP.BaseLayer.Contents: %d байт", len(tcp.Contents))
		t.Logf("   TCP.BaseLayer.Payload:  %d байт", len(tcp.Payload))
		if len(tcp.Payload) > 0 {
			t.Logf("   TCP Payload hex:\n%s", hex.Dump(tcp.Payload))
		}
	}

	t.Log("\nШаг 3: Симулируем добавление padding (как делает сетевой стек)")
	// Добавляем padding до 60 байт
	paddedBytes := make([]byte, len(serializedBytes))
	copy(paddedBytes, serializedBytes)

	if len(paddedBytes) < 60 {
		paddingSize := 60 - len(paddedBytes)
		t.Logf("   Добавляем %d байт padding", paddingSize)
		padding := make([]byte, paddingSize)
		paddedBytes = append(paddedBytes, padding...)
	}

	t.Logf("   Размер с padding: %d байт", len(paddedBytes))
	t.Logf("   Hex dump с padding:\n%s", hex.Dump(paddedBytes))

	t.Log("\nШаг 4: Парсим пакет с padding")
	paddedPkt := gopacket.NewPacket(paddedBytes, layers.LayerTypeEthernet, gopacket.Default)

	ethLayer2 := paddedPkt.Layer(layers.LayerTypeEthernet)
	if ethLayer2 != nil {
		eth := ethLayer2.(*layers.Ethernet)
		t.Logf("   Ethernet.BaseLayer.Payload: %d байт (было %d)",
			len(eth.Payload),
			len(ethLayer.(*layers.Ethernet).Payload))
	}

	tcpLayer2 := paddedPkt.Layer(layers.LayerTypeTCP)
	if tcpLayer2 != nil {
		tcp := tcpLayer2.(*layers.TCP)
		t.Logf("   TCP.BaseLayer.Payload: %d байт (было %d)",
			len(tcp.Payload),
			len(tcpLayer.(*layers.TCP).Payload))

		if len(tcp.Payload) > 0 {
			t.Logf("   ⚠️  TCP Payload содержит padding:")
			t.Logf("   Hex:\n%s", hex.Dump(tcp.Payload))
		}
	}

	t.Log("\n=== Выводы ===")
	t.Log("1. gopacket.SerializeLayers() НЕ добавляет padding")
	t.Log("2. Padding добавляется сетевым стеком при отправке")
	t.Log("3. gopacket.NewPacket() включает padding в BaseLayer.Payload")
	t.Log("4. Поэтому нужно игнорировать BaseLayer при сравнении пакетов")
}

// TestPaddingInRealPacket demonstrates padding in a real received packet
func TestPaddingInRealPacket(t *testing.T) {
	t.Log("=== Демонстрация padding в реальном пакете ===\n")

	// Это реальные байты из теста 009_nat64stateless (60 байт с padding)
	realPacketBytes := []byte{
		// Ethernet header (14 байт)
		0x52, 0x54, 0x00, 0x6b, 0xff, 0xa1, // Dst MAC
		0x52, 0x54, 0x00, 0x6b, 0xff, 0xa5, // Src MAC
		0x81, 0x00, // EtherType: 802.1Q

		// VLAN tag (4 байта)
		0x00, 0xc8, // VLAN ID: 200
		0x08, 0x00, // Type: IPv4

		// IPv4 header (20 байт)
		0x45, 0x00, 0x00, 0x28, // Version, IHL, TOS, Length
		0x00, 0x00, 0x00, 0x00, // ID, Flags, Fragment offset
		0x3f, 0x06, 0x7b, 0xd1, // TTL, Protocol (TCP), Checksum
		0x99, 0x99, 0x99, 0x99, // Src IP: 153.153.153.153
		0x66, 0x66, 0x66, 0x66, // Dst IP: 102.102.102.102

		// TCP header (20 байт)
		0x08, 0x00, // Src port: 2048
		0x00, 0x50, // Dst port: 80
		0x00, 0x00, 0x00, 0x00, // Seq
		0x00, 0x00, 0x00, 0x00, // Ack
		0x50, 0x02, // Data offset, Flags
		0x00, 0x00, // Window
		0xa7, 0x93, // Checksum
		0x00, 0x00, // Urgent

		// Padding (2 байта) - добавлено сетевым стеком
		0x00, 0x00,
	}

	t.Logf("Размер пакета: %d байт", len(realPacketBytes))
	t.Logf("Hex dump:\n%s", hex.Dump(realPacketBytes))

	// Парсим пакет
	pkt := gopacket.NewPacket(realPacketBytes, layers.LayerTypeEthernet, gopacket.Default)

	t.Log("\nСлои пакета:")
	for i, layer := range pkt.Layers() {
		t.Logf("  %d. %s", i, layer.LayerType())

		switch l := layer.(type) {
		case *layers.Ethernet:
			t.Logf("     Contents: %d байт", len(l.Contents))
			t.Logf("     Payload:  %d байт (VLAN + IP + TCP + padding)", len(l.Payload))

		case *layers.IPv4:
			t.Logf("     Contents: %d байт", len(l.Contents))
			t.Logf("     Payload:  %d байт (TCP)", len(l.Payload))
			t.Logf("     Length field: %d (указано в IP заголовке)", l.Length)

		case *layers.TCP:
			t.Logf("     Contents: %d байт", len(l.Contents))
			t.Logf("     Payload:  %d байт", len(l.Payload))

			if len(l.Payload) > 0 {
				t.Logf("     ⚠️  TCP Payload НЕ пустой! Это padding:")
				t.Logf("     Hex: %s", hex.EncodeToString(l.Payload))
			}
		}
	}

	t.Log("\n=== Объяснение ===")
	t.Log("Ethernet минимум: 60 байт (без FCS)")
	t.Log("Наш пакет без padding: 58 байт (14 + 4 + 20 + 20)")
	t.Log("Сетевой стек добавил: 2 байта padding (0x00 0x00)")
	t.Log("gopacket включил padding в TCP.BaseLayer.Payload")
	t.Log("\nПоэтому при сравнении нужно игнорировать BaseLayer!")
}
