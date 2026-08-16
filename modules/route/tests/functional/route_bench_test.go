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
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// routeForwardShape names a single FIB shape swept under each counter mode.
//
// spread installs 32 /24 prefixes each with one per-idx nexthop. Otherwise a
// single 0.0.0.0/0 entry carries the given number of next hops. spread reuses
// the same 32 packets as the single-prefix shape, so each packet lands on
// its own prefix and nexthop.
type routeForwardShape struct {
	name string
	// spread selects the 32-prefix shape over the single-prefix one.
	spread bool
	// nexthops is the per-entry hop count for the single-prefix shape.
	nexthops int
}

var routeForwardShapes = []routeForwardShape{
	{name: "nexthops=1", nexthops: 1},
	{name: "nexthops=2", nexthops: 2},
	{name: "spread", spread: true},
}

// routeForwardCounterMode names one of the two RouteService configurations
// that BenchmarkRouteForward and TestRoute_NexthopCounterArms both build
// their arms from.
type routeForwardCounterMode struct {
	name string
	// disabled selects the operator opt-out over the shipped default.
	disabled bool
}

var routeForwardCounterModes = []routeForwardCounterMode{
	{name: "uncounted", disabled: true},
	{name: "counted"},
}

// serviceOptions returns the RouteServiceOption set that puts a service
// into this counter mode.
func (m routeForwardCounterMode) serviceOptions() []route.RouteServiceOption {
	if m.disabled {
		return []route.RouteServiceOption{route.WithNexthopCountersDisabled()}
	}
	return nil
}

// BenchmarkRouteForward measures IPv4 forwarding in both counter modes.
//
// See routeForwardCounterModes for what distinguishes the two arms.
//
// The payload-resetting bench runner is required: the route module
// decrements TTL in place, so without it every round past the initial TTL
// would measure the drop path instead of forwarding.
func BenchmarkRouteForward(b *testing.B) {
	for _, mode := range routeForwardCounterModes {
		b.Run("counter="+mode.name, func(b *testing.B) {
			for _, shape := range routeForwardShapes {
				b.Run(shape.name, func(b *testing.B) {
					runRouteForwardBench(b, shape, mode)
				})
			}
		})
	}
}

// runRouteForwardBench builds a per-shape harness + service + FIB + packets
// and runs the timed forward.
//
// Setup is excluded from timing: only h.Bench is measured, so the service
// path's extra setup work does not skew results.
func runRouteForwardBench(b *testing.B, shape routeForwardShape, mode routeForwardCounterMode) {
	b.Helper()

	h, agent, backend := setupRouteHarness(b, "port0")

	service := route.NewRouteService(backend, mode.serviceOptions()...)

	entries := buildRouteForwardFIB(shape)
	_, err := service.UpdateFIB(b.Context(), &routepb.UpdateFIBRequest{
		ModuleName: "bench",
		Entries:    toFIBEntries(b, entries),
	})
	require.NoError(b, err)

	// Route config must exist before the pipeline resolves chain references.
	wirePipeline(b, agent, "port0", "bench")

	packets := buildRouteForwardPackets(b)
	h.Bench(b, packets, dataplaneut.WithPayloadReset())
}

// buildRouteForwardFIB assembles the FIB entries for a bench shape.
//
// Nexthop counters are left empty in both modes.
func buildRouteForwardFIB(shape routeForwardShape) []FIBEntry {
	if shape.spread {
		entries := make([]FIBEntry, 32)
		for idx := range entries {
			hi, lo := byte(idx>>8), byte(idx)
			entries[idx] = FIBEntry{
				Prefix: netip.MustParsePrefix(fmt.Sprintf("192.168.%d.0/24", idx)),
				Nexthops: []FIBNexthop{{
					DstMAC: xerror.Unwrap(net.ParseMAC(
						fmt.Sprintf("de:ad:be:ef:%02x:%02x", hi, lo),
					)),
					SrcMAC: xerror.Unwrap(net.ParseMAC(
						fmt.Sprintf("ca:fe:ba:be:%02x:%02x", hi, lo),
					)),
					Device: "port0",
				}},
			}
		}
		return entries
	}

	nexthops := make([]FIBNexthop, 0, shape.nexthops)
	for idx := range shape.nexthops {
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
	return []FIBEntry{{
		Prefix:   netip.MustParsePrefix("0.0.0.0/0"),
		Nexthops: nexthops,
	}}
}

// buildRouteForwardPackets builds the 32 forward packets shared by all shapes.
//
// Packet idx has DstIP 192.168.<idx>.1, which falls under the spread prefix
// 192.168.<idx>.0/24 and under 0.0.0.0/0 for the single-prefix shape.
func buildRouteForwardPackets(tb testing.TB) []gopacket.Packet {
	tb.Helper()

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
		require.NoError(tb, udp.SetNetworkLayerForChecksum(&ip4))
		packet, err := xpacket.LayersToPacketChecked(&eth, &ip4, &udp)
		require.NoError(tb, err)
		packets[idx] = packet
	}
	return packets
}
