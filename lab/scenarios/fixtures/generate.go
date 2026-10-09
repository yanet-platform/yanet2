//go:build ignore

package main

import (
	"encoding/binary"
	"hash/crc32"
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

	encapInput, encapExpected := vxlanEncapPackets()
	writePCAP("lab/scenarios/vxlan/encap-input.pcap", encapInput)
	writePCAP("lab/scenarios/vxlan/encap-expected.pcap", encapExpected)

	decapInput, decapExpected = vxlanDecapPackets(vxlanVNI)
	writePCAP("lab/scenarios/vxlan/decap-input.pcap", decapInput)
	writePCAP("lab/scenarios/vxlan/decap-expected.pcap", decapExpected)

	foreignInput, _ := vxlanDecapPackets(vxlanVNI + 1)
	writePCAP("lab/scenarios/vxlan/foreign-vni-input.pcap", foreignInput)
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

// The vxlan device of the vxlan scenario: the DUT is the local endpoint.
const (
	vxlanLocalIP  = "192.0.2.1"
	vxlanRemoteIP = "198.51.100.7"
	vxlanVNI      = 0x1234
)

// vxlanInner is the frame the tunnel carries: an IPv4 UDP datagram to
// 203.0.113.10, which the scenario steers into the vxlan output.
func vxlanInner() []byte {
	ethernet := &layers.Ethernet{SrcMAC: framework.MustParseMAC(framework.SrcMAC), DstMAC: framework.MustParseMAC(framework.DstMAC), EthernetType: layers.EthernetTypeIPv4}
	ip := &layers.IPv4{Version: 4, IHL: 5, Id: 5, TTL: 64, Protocol: layers.IPProtocolUDP, SrcIP: net.ParseIP("10.0.0.1"), DstIP: net.ParseIP("203.0.113.10")}
	udp := &layers.UDP{SrcPort: 12345, DstPort: 8080}
	if err := udp.SetNetworkLayerForChecksum(ip); err != nil {
		log.Fatal(err)
	}
	return serialize(ethernet, ip, udp, gopacket.Payload("yanet2-vxlan"))
}

// vxlanFrame encapsulates inner as the endpoint at src sends it to dst:
// IPv4 with DF and TTL 64, UDP to 4789 with a zero checksum, VXLAN with
// the I flag (RFC 7348 section 5).
func vxlanFrame(srcMAC, dstMAC, src, dst string, sourcePort uint16, vni uint32, inner []byte) []byte {
	ethernet := &layers.Ethernet{SrcMAC: framework.MustParseMAC(srcMAC), DstMAC: framework.MustParseMAC(dstMAC), EthernetType: layers.EthernetTypeIPv4}
	ip := &layers.IPv4{Version: 4, IHL: 5, TTL: 64, Flags: layers.IPv4DontFragment, Protocol: layers.IPProtocolUDP, SrcIP: net.ParseIP(src), DstIP: net.ParseIP(dst)}
	header := make([]byte, 16)
	binary.BigEndian.PutUint16(header[0:], sourcePort)
	binary.BigEndian.PutUint16(header[2:], 4789)
	binary.BigEndian.PutUint16(header[4:], uint16(16+len(inner)))
	header[8] = 0x08
	binary.BigEndian.PutUint32(header[12:], vni<<8)
	return serialize(ethernet, ip, gopacket.Payload(append(header, inner...)))
}

// rawCRC32C folds bytes into a CRC-32C without the pre and post inversion,
// as the dataplane flow hash does.
func rawCRC32C(hash uint32, data []byte) uint32 {
	return ^crc32.Update(^hash, crc32.MakeTable(crc32.Castagnoli), data)
}

// vxlanSourcePort is the outer source port the device derives from the
// parser's flow hash over the inner addresses and UDP ports.
func vxlanSourcePort(inner []byte) uint16 {
	hash := rawCRC32C(0, inner[26:30])
	hash = rawCRC32C(hash, inner[30:34])
	hash = rawCRC32C(hash, inner[34:36])
	hash = rawCRC32C(hash, inner[36:38])
	return 0xc000 | uint16(hash^hash>>16)&0x3fff
}

func vxlanEncapPackets() ([]byte, []byte) {
	inner := vxlanInner()
	expected := vxlanFrame(framework.DstMAC, framework.SrcMAC, vxlanLocalIP, vxlanRemoteIP, vxlanSourcePort(inner), vxlanVNI, inner)
	return inner, expected
}

func vxlanDecapPackets(vni uint32) ([]byte, []byte) {
	inner := vxlanInner()
	return vxlanFrame(framework.SrcMAC, framework.DstMAC, vxlanRemoteIP, vxlanLocalIP, 50000, vni, inner), inner
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
