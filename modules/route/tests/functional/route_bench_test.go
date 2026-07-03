package route_test

import (
	"fmt"
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
)

// BenchmarkRouteForward measures IPv4 forwarding rounds against a FIB
// whose default route carries the given number of next hops.
//
// The payload-resetting bench runner is required here: the route module
// decrements TTL in place, so without the reset every round beyond the
// initial TTL would measure the drop path instead of forwarding.
func BenchmarkRouteForward(b *testing.B) {
	for _, nexthopCount := range []int{1, 2} {
		b.Run(fmt.Sprintf("nexthops=%d", nexthopCount), func(b *testing.B) {
			h, agent, backend := setupRouteHarness(b, "port0")

			nexthops := make([]FIBNexthop, 0, nexthopCount)
			for idx := range nexthopCount {
				nexthops = append(nexthops, FIBNexthop{
					DstMAC: xerror.Unwrap(net.ParseMAC(
						fmt.Sprintf("de:ad:be:ef:00:%02x", idx+1),
					)),
					SrcMAC: xerror.Unwrap(net.ParseMAC(
						fmt.Sprintf("ca:fe:ba:be:00:%02x", idx+1),
					)),
					Device: "port0",
				})
			}
			applyFIB(b, backend, "bench", []FIBEntry{{
				Prefix:   netip.MustParsePrefix("0.0.0.0/0"),
				Nexthops: nexthops,
			}})
			wirePipeline(b, agent, "port0", "bench")

			packets := make([]gopacket.Packet, 32)
			for idx := range packets {
				eth := layers.Ethernet{
					SrcMAC:       xerror.Unwrap(net.ParseMAC("aa:bb:cc:dd:ee:ff")),
					DstMAC:       xerror.Unwrap(net.ParseMAC("11:22:33:44:55:66")),
					EthernetType: layers.EthernetTypeIPv4,
				}
				ip4 := layers.IPv4{
					Version:  4,
					TTL:      64,
					Protocol: layers.IPProtocolUDP,
					SrcIP:    net.IP{10, 0, 0, byte(idx + 1)},
					DstIP:    net.IP{192, 168, byte(idx), 1},
				}
				udp := layers.UDP{
					SrcPort: layers.UDPPort(1024 + idx),
					DstPort: 443,
				}
				require.NoError(b, udp.SetNetworkLayerForChecksum(&ip4))
				packet, err := xpacket.LayersToPacketChecked(&eth, &ip4, &udp)
				require.NoError(b, err)
				packets[idx] = packet
			}

			h.Bench(b, packets, dataplaneut.WithPayloadReset())
		})
	}
}
