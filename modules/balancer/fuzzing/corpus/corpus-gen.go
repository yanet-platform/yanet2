package main

import (
	"fmt"
	"net"
	"os"
	"path"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"

	"github.com/yanet-platform/yanet2/tests/go/common"
)

var dirName = "."

func main() {

	if len(os.Args) > 1 {
		dirName = os.Args[1]
		stat, err := os.Stat(dirName)
		if err != nil {
			panic(err)
		}
		if !stat.IsDir() {
			panic("directory name as a single argument is expected")
		}
	}

	eth := layers.Ethernet{
		SrcMAC:       common.Unwrap(net.ParseMAC("00:00:00:00:00:01")),
		DstMAC:       common.Unwrap(net.ParseMAC("00:11:22:33:44:55")),
		EthernetType: layers.EthernetTypeIPv4,
	}

	ip4 := layers.IPv4{
		Version:  4,
		Id:       1,
		TTL:      64,
		Protocol: layers.IPProtocolTCP,
		SrcIP:    net.ParseIP("192.168.2.1"),
		DstIP:    net.ParseIP("10.10.10.10"),
	}

	tcp := layers.TCP{
		SrcPort: 25525,
		DstPort: 443,
	}
	if err := tcp.SetNetworkLayerForChecksum(&ip4); err != nil {
		panic(err)
	}
	writePacket("tcp-v4.pkt", &eth, &ip4, &tcp, gopacket.Payload([]byte("YANET")))

	eth.EthernetType = layers.EthernetTypeIPv6
	ip6 := layers.IPv6{
		Version:    6,
		NextHeader: layers.IPProtocolUDP,
		HopLimit:   64,
		SrcIP:      net.ParseIP("2a01:db8::aaaa:1"),
		DstIP:      net.ParseIP("2a01:db8::853a:0:3"),
	}
	udp := layers.UDP{
		SrcPort: 7777,
		DstPort: 443,
	}
	if err := udp.SetNetworkLayerForChecksum(&ip6); err != nil {
		panic(err)
	}
	writePacket("udp-v6.pkt", &eth, &ip6, &udp, gopacket.Payload([]byte("YANET")))

}

func writePacket(name string, layers ...gopacket.SerializableLayer) {
	opts := gopacket.SerializeOptions{
		FixLengths:       true,
		ComputeChecksums: true,
	}
	buf := gopacket.NewSerializeBuffer()

	if err := gopacket.SerializeLayers(buf, opts, layers...); err != nil {
		panic(err)
	}

	path := path.Join(dirName, name)
	if err := os.WriteFile(path, buf.Bytes(), 0640); err != nil {
		panic(err)
	}
	fmt.Println("Data is written to the file:", path)
}
