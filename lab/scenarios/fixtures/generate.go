//go:build ignore

package main

import (
	"log"
	"net"
	"os"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

func main() {
	input := packet(framework.SrcMAC, framework.DstMAC, 64)
	expected := packet(framework.DstMAC, framework.SrcMAC, 63)
	writePCAP("lab/scenarios/forward-route/input.pcap", input)
	writePCAP("lab/scenarios/forward-route/expected.pcap", expected)
}

func packet(sourceMAC, destinationMAC string, ttl uint8) []byte {
	ethernet := &layers.Ethernet{
		SrcMAC:       framework.MustParseMAC(sourceMAC),
		DstMAC:       framework.MustParseMAC(destinationMAC),
		EthernetType: layers.EthernetTypeIPv4,
	}
	ipv4 := &layers.IPv4{
		Version: 4, IHL: 5, Id: 1, TTL: ttl, Protocol: layers.IPProtocolUDP,
		SrcIP: net.ParseIP("192.0.2.10"), DstIP: net.ParseIP("198.51.100.20"),
	}
	udp := &layers.UDP{SrcPort: 12345, DstPort: 8080}
	if err := udp.SetNetworkLayerForChecksum(ipv4); err != nil {
		log.Fatal(err)
	}
	buffer := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buffer, gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}, ethernet, ipv4, udp, gopacket.Payload("yanet2-lab")); err != nil {
		log.Fatal(err)
	}
	return buffer.Bytes()
}

func writePCAP(path string, packet []byte) {
	file, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()
	writer := pcapgo.NewWriter(file)
	if err := writer.WriteFileHeader(65535, layers.LinkTypeEthernet); err != nil {
		log.Fatal(err)
	}
	if err := writer.WritePacket(gopacket.CaptureInfo{Timestamp: time.Unix(0, 0), CaptureLength: len(packet), Length: len(packet)}, packet); err != nil {
		log.Fatal(err)
	}
}
