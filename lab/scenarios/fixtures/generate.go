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
	input := routedPacket(framework.SrcMAC, framework.DstMAC, 64)
	expected := routedPacket(framework.DstMAC, framework.SrcMAC, 63)
	writePCAP("lab/scenarios/forward-route/input.pcap", input)
	writePCAP("lab/scenarios/forward-route/expected.pcap", expected)

	decapInput, decapExpected := decapPackets()
	writePCAP("lab/scenarios/decap/input.pcap", decapInput)
	writePCAP("lab/scenarios/decap/expected.pcap", decapExpected)

	nat64Input, nat64Expected := nat64Packets()
	writePCAP("lab/scenarios/nat64/input.pcap", nat64Input)
	writePCAP("lab/scenarios/nat64/expected.pcap", nat64Expected)
}

func routedPacket(sourceMAC, destinationMAC string, ttl uint8) []byte {
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
	return serialize(ethernet, ipv4, udp, gopacket.Payload("yanet2-lab"))
}

func decapPackets() ([]byte, []byte) {
	inputEthernet := &layers.Ethernet{SrcMAC: framework.MustParseMAC(framework.SrcMAC), DstMAC: framework.MustParseMAC(framework.DstMAC), EthernetType: layers.EthernetTypeIPv4}
	outer := &layers.IPv4{Version: 4, IHL: 5, Id: 2, TTL: 64, Protocol: layers.IPProtocolIPv4, SrcIP: net.ParseIP("192.0.2.1"), DstIP: net.ParseIP("4.5.6.7")}
	inner := &layers.IPv4{Version: 4, IHL: 5, Id: 3, TTL: 64, Protocol: layers.IPProtocolUDP, SrcIP: net.ParseIP("192.0.2.10"), DstIP: net.ParseIP("198.51.100.20")}
	inputUDP := &layers.UDP{SrcPort: 12345, DstPort: 8080}
	if err := inputUDP.SetNetworkLayerForChecksum(inner); err != nil {
		log.Fatal(err)
	}
	input := serialize(inputEthernet, outer, inner, inputUDP, gopacket.Payload("yanet2-decap"))

	expectedEthernet := &layers.Ethernet{SrcMAC: framework.MustParseMAC(framework.DstMAC), DstMAC: framework.MustParseMAC(framework.SrcMAC), EthernetType: layers.EthernetTypeIPv4}
	expectedIP := *inner
	expectedIP.TTL--
	expectedUDP := &layers.UDP{SrcPort: 12345, DstPort: 8080}
	if err := expectedUDP.SetNetworkLayerForChecksum(&expectedIP); err != nil {
		log.Fatal(err)
	}
	return input, serialize(expectedEthernet, &expectedIP, expectedUDP, gopacket.Payload("yanet2-decap"))
}

func nat64Packets() ([]byte, []byte) {
	inputEthernet := &layers.Ethernet{SrcMAC: framework.MustParseMAC(framework.SrcMAC), DstMAC: framework.MustParseMAC(framework.DstMAC), EthernetType: layers.EthernetTypeIPv4}
	inputIP := &layers.IPv4{Version: 4, IHL: 5, Id: 4, TTL: 64, Protocol: layers.IPProtocolUDP, SrcIP: net.ParseIP("192.0.2.34"), DstIP: net.ParseIP("198.51.100.2")}
	inputUDP := &layers.UDP{SrcPort: 12345, DstPort: 53}
	if err := inputUDP.SetNetworkLayerForChecksum(inputIP); err != nil {
		log.Fatal(err)
	}
	input := serialize(inputEthernet, inputIP, inputUDP, gopacket.Payload("yanet2-nat64"))

	expectedEthernet := &layers.Ethernet{SrcMAC: framework.MustParseMAC(framework.DstMAC), DstMAC: framework.MustParseMAC(framework.SrcMAC), EthernetType: layers.EthernetTypeIPv6}
	expectedIP := &layers.IPv6{Version: 6, HopLimit: 63, NextHeader: layers.IPProtocolUDP, SrcIP: net.ParseIP("2001:db8::c000:222"), DstIP: net.ParseIP("2001:db8::3")}
	expectedUDP := &layers.UDP{SrcPort: 12345, DstPort: 53}
	if err := expectedUDP.SetNetworkLayerForChecksum(expectedIP); err != nil {
		log.Fatal(err)
	}
	return input, serialize(expectedEthernet, expectedIP, expectedUDP, gopacket.Payload("yanet2-nat64"))
}

func serialize(serializableLayers ...gopacket.SerializableLayer) []byte {
	buffer := gopacket.NewSerializeBuffer()
	if err := gopacket.SerializeLayers(buffer, gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}, serializableLayers...); err != nil {
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
