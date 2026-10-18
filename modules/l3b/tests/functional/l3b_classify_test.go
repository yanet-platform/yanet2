package l3b_test

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/xnetip"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/l3b/bindings/go/cl3b"
	cl3bobject "github.com/yanet-platform/yanet2/objects/l3b/bindings/go/cl3bobject"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// classifyFrame builds an unpadded frame with declared transport and IP lengths.
//
// Checksums remain zero because the harness does not validate them.
func classifyFrame(
	source, destination string,
	sourcePort, destinationPort uint16,
	protocol, subtype byte,
	payload []byte,
) []byte {
	transportLength := 8
	if protocol == 6 {
		transportLength = 20
	}
	header := make([]byte, transportLength)
	switch protocol {
	case 6, 17:
		binary.BigEndian.PutUint16(header[0:2], sourcePort)
		binary.BigEndian.PutUint16(header[2:4], destinationPort)
		if protocol == 6 {
			header[12] = 5 << 4
			header[13] = subtype
			binary.BigEndian.PutUint16(header[14:16], 1024)
		} else {
			binary.BigEndian.PutUint16(header[4:6], uint16(transportLength+len(payload)))
		}
	case 1, 58:
		header[0] = subtype
		binary.BigEndian.PutUint16(header[4:6], 0x1234)
		binary.BigEndian.PutUint16(header[6:8], 7)
	}
	sourceAddress := netip.MustParseAddr(source)
	destinationAddress := netip.MustParseAddr(destination)
	networkLength := 20
	if sourceAddress.Is6() {
		networkLength = 40
	}
	frame := make([]byte, ethHeaderLen+networkLength+transportLength+len(payload))
	copy(frame[0:6], []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66})
	copy(frame[6:12], []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff})
	if sourceAddress.Is4() {
		binary.BigEndian.PutUint16(frame[12:14], 0x0800)
		frame[14] = 0x45
		binary.BigEndian.PutUint16(frame[16:18], uint16(networkLength+transportLength+len(payload)))
		frame[22], frame[23] = 64, protocol
		copy(frame[26:30], sourceAddress.AsSlice())
		copy(frame[30:34], destinationAddress.AsSlice())
	} else {
		binary.BigEndian.PutUint16(frame[12:14], 0x86dd)
		frame[14] = 0x60
		binary.BigEndian.PutUint16(frame[18:20], uint16(transportLength+len(payload)))
		frame[20], frame[21] = protocol, 64
		copy(frame[22:38], sourceAddress.AsSlice())
		copy(frame[38:54], destinationAddress.AsSlice())
	}
	copy(frame[ethHeaderLen+networkLength:], header)
	copy(frame[ethHeaderLen+networkLength+transportLength:], payload)
	return frame
}

// publishClassifyService publishes a service with one usable IPv4 real and ring.
func publishClassifyService(
	t *testing.T,
	agent *ffi.Agent,
	name, realIP string,
	rules []cl3bobject.SourceFilterRule,
) *cl3bobject.VirtualServiceObject {
	t.Helper()
	object, _ := newVirtualService(t, agent, name, cl3bobject.VirtualServiceConfig{
		SourceFilterRules: rules,
		RealServers: []cl3bobject.RealServer{{
			Type:               cl3bobject.IPv4,
			DestinationAddress: xerror.Unwrap(netip.ParseAddr(realIP)),
			SourceNet:          xnetip.MustParseNetwork("192.0.2.0/24"),
		}},
		HashMask:     0,
		IndexMask:    0,
		RingCapacity: 1000,
	})
	require.NoError(t, object.UpdateRing([]uint32{0}))
	require.NoError(t, object.Publish(agent))
	return object
}

// publishClassifyModule installs destination rules in the published pipeline.
func publishClassifyModule(
	t *testing.T,
	agent *ffi.Agent,
	configName string,
	rules []cl3b.DestinationFilterRule,
) *cl3b.ModuleConfig {
	t.Helper()
	module, err := cl3b.NewModuleConfig(agent, configName)
	require.NoError(t, err)
	require.NoError(t, module.Update(rules))
	require.NoError(t, agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}))
	t.Cleanup(func() { _ = module.Free() })
	return module
}

// counterOf reads a published service's packet and byte counter pair.
func counterOf(t *testing.T, harness *dataplaneut.Harness, service, name string) (uint64, uint64) {
	t.Helper()
	ctx, err := harness.PublishedExecutionContext(0)
	require.NoError(t, err)
	packets, byteCount, err := cl3bobject.ReadServiceCounter(ctx, service, name)
	require.NoError(t, err)
	return packets, byteCount
}

// Test_L3b_ClassifyOmittedSourcePortRanges verifies that omitted ports admit
// every service port while restrictive source networks still reject outsiders.
func Test_L3b_ClassifyOmittedSourcePortRanges(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("10.0.0.0/24")},
		Net6s: []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:1::/48")},
	}})
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.168.1.0/24")},
		Net6s: []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:2::/48")},
		ProtoRanges: filter.ProtoRanges{
			filter.NewProtoRange(6, filter.AnySubtype()),
			filter.NewProtoRange(17, filter.AnySubtype()),
		},
		VirtualService: "svc",
	}})
	for _, tc := range []struct {
		name, allowed, foreign, destination string
	}{
		{"IPv4", "10.0.0.1", "10.9.9.1", "192.168.1.1"},
		{"IPv6", "2001:db8:1::1", "2001:db8:9::1", "2001:db8:2::1"},
	} {
		for _, protocol := range []byte{6, 17} {
			for _, port := range []uint16{0, 80, 40000, 65535} {
				t.Run(fmt.Sprintf("%s/protocol%d/servicePort%d", tc.name, protocol, port), func(t *testing.T) {
					allowed := classifyFrame(tc.allowed, tc.destination, 12345, port, protocol, 2, nil)
					foreign := classifyFrame(tc.foreign, tc.destination, 12345, port, protocol, 2, nil)
					before, _ := counterOf(t, harness, "svc", "filter_rejected")
					result, err := harness.HandleSegmentedPackets([][]byte{allowed}, [][]byte{foreign})
					require.NoError(t, err)
					require.Len(t, result.Output, 1)
					require.Equal(t, [][]byte{foreign}, result.Drop)
					info, err := framework.NewPacketParser().ParsePacket(result.Output[0])
					require.NoError(t, err)
					require.True(t, info.IsTunneled)
					require.Equal(t, "172.16.0.10", info.DstIP.String())
					after, _ := counterOf(t, harness, "svc", "filter_rejected")
					require.Equal(t, before+1, after)
				})
			}
		}
	}
}

// Test_L3b_ClassifyRestrictedSourcePortRanges verifies that source admission
// restricts the destination service port, not the client's source port.
func Test_L3b_ClassifyRestrictedSourcePortRanges(t *testing.T) {
	for _, tc := range []struct {
		name, source, destination string
	}{
		{"IPv4", "10.0.0.1", "192.168.1.1"},
		{"IPv6", "2001:db8:1::1", "2001:db8:2::1"},
	} {
		for _, protocol := range []byte{6, 17} {
			t.Run(fmt.Sprintf("%s/protocol%d", tc.name, protocol), func(t *testing.T) {
				harness, agent := setupL3bHarness(t, "port0", "test")
				wirePipeline(t, agent, "port0", "test")
				publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
					Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
					Net6s:      []xnetip.BiContiguous{filter.UnspecifiedIPv6},
					PortRanges: filter.PortRanges{{From: 80, To: 80}},
				}})
				publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
					Net4s:          []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
					Net6s:          []xnetip.BiContiguous{filter.UnspecifiedIPv6},
					ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(protocol, filter.AnySubtype())},
					VirtualService: "svc",
				}})
				allowed := classifyFrame(tc.source, tc.destination, 12345, 80, protocol, 2, nil)
				foreign := classifyFrame(tc.source, tc.destination, 80, 12345, protocol, 2, nil)
				result, err := harness.HandleSegmentedPackets([][]byte{allowed}, [][]byte{foreign})
				require.NoError(t, err)
				require.Len(t, result.Output, 1)
				require.Equal(t, [][]byte{foreign}, result.Drop)
				packets, _ := counterOf(t, harness, "svc", "filter_rejected")
				require.EqualValues(t, 1, packets)
			})
		}
	}
}

// Test_L3b_DirectPathPortRanges verifies that reversed ranges cover nothing
// and do not interfere with valid ranges through the direct binding.
func Test_L3b_DirectPathPortRanges(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ranges   filter.PortRanges
		admitted bool
	}{
		{"mixed valid and reversed", filter.PortRanges{{From: 100, To: 200}, {From: 100, To: 50}}, true},
		{"reversed only", filter.PortRanges{{From: 100, To: 50}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness, agent := setupL3bHarness(t, "port0", "test")
			wirePipeline(t, agent, "port0", "test")
			publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
				Net4s:      []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				Net6s:      []xnetip.BiContiguous{filter.UnspecifiedIPv6},
				PortRanges: tc.ranges,
			}})
			publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
				Net4s:          []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
				Net6s:          []xnetip.BiContiguous{filter.UnspecifiedIPv6},
				ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype()), filter.NewProtoRange(17, filter.AnySubtype())},
				VirtualService: "svc",
			}})
			for _, family := range []struct{ source, destination string }{
				{"10.0.0.1", "192.168.1.1"}, {"2001:db8:1::1", "2001:db8:2::1"},
			} {
				for _, protocol := range []byte{6, 17} {
					for _, port := range []uint16{0, 50, 60, 99, 100, 150, 200, 201, 65535} {
						frame := classifyFrame(family.source, family.destination, 12345, port, protocol, 2, nil)
						result, err := harness.HandleSegmentedPackets([][]byte{frame})
						require.NoError(t, err)
						if tc.admitted && port >= 100 && port <= 200 {
							require.Len(t, result.Output, 1)
							require.Empty(t, result.Drop)
						} else {
							require.Empty(t, result.Output)
							require.Equal(t, [][]byte{frame}, result.Drop)
						}
					}
				}
			}
		})
	}
}

// Test_L3b_ClassifyEmptyPredicates verifies that missing networks and empty
// destination protocols match no traffic.
//
// Rule-less services reject ordinary transport at source admission.
func Test_L3b_ClassifyEmptyPredicates(t *testing.T) {
	for _, tc := range []struct {
		name             string
		sourceRules      []cl3bobject.SourceFilterRule
		destination      cl3b.DestinationFilterRule
		filterRejections uint64
	}{
		{"rule-less source service", nil, cl3b.DestinationFilterRule{
			Net4s:       []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s:       []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			ProtoRanges: filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype()), filter.NewProtoRange(17, filter.AnySubtype())},
		}, 4},
		{"empty destination protocols", []cl3bobject.SourceFilterRule{{
			Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		}}, cl3b.DestinationFilterRule{
			Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		}, 0},
		{"missing destination families", []cl3bobject.SourceFilterRule{{
			Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		}}, cl3b.DestinationFilterRule{
			ProtoRanges: filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype()), filter.NewProtoRange(17, filter.AnySubtype())},
		}, 0},
		{"missing source families", []cl3bobject.SourceFilterRule{{PortRanges: filter.PortRanges{{From: 0, To: 65535}}}}, cl3b.DestinationFilterRule{
			Net4s:       []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s:       []xnetip.BiContiguous{filter.UnspecifiedIPv6},
			ProtoRanges: filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype()), filter.NewProtoRange(17, filter.AnySubtype())},
		}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			harness, agent := setupL3bHarness(t, "port0", "test")
			wirePipeline(t, agent, "port0", "test")
			publishClassifyService(t, agent, "svc", "172.16.0.10", tc.sourceRules)
			tc.destination.VirtualService = "svc"
			publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{tc.destination})
			for _, family := range []struct{ source, destination string }{
				{"10.0.0.1", "192.168.1.1"}, {"2001:db8:1::1", "2001:db8:2::1"},
			} {
				for _, protocol := range []byte{6, 17} {
					frame := classifyFrame(family.source, family.destination, 12345, 80, protocol, 2, nil)
					result, err := harness.HandleSegmentedPackets([][]byte{frame})
					require.NoError(t, err)
					require.Empty(t, result.Output)
					require.Equal(t, [][]byte{frame}, result.Drop)
				}
			}
			packets, _ := counterOf(t, harness, "svc", "filter_rejected")
			require.Equal(t, tc.filterRejections, packets)
		})
	}
}

// Test_L3b_FirstMatchBatchBoundary verifies that first-match service decoding
// survives the 256-packet family boundary in an interleaved dual-family batch.
func Test_L3b_FirstMatchBatchBoundary(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	for _, tc := range []struct{ name, real string }{{"narrow", "172.16.0.10"}, {"broad", "172.16.0.11"}} {
		publishClassifyService(t, agent, tc.name, tc.real, []cl3bobject.SourceFilterRule{{
			Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		}})
	}
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{
		{
			Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.168.1.1/32")},
			Net6s:          []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:2::1/128")},
			ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
			VirtualService: "narrow",
		},
		{
			Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.168.1.0/24")},
			Net6s:          []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:2::/48")},
			ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
			VirtualService: "broad",
		},
	})
	packets := make([][][]byte, 0, 514)
	for idx := range 257 {
		destination4, destination6 := "192.168.1.1", "2001:db8:2::1"
		if idx%2 != 0 || idx == 256 {
			destination4, destination6 = "192.168.1.2", "2001:db8:2::2"
		}
		packets = append(packets,
			[][]byte{classifyFrame("10.0.0.1", destination4, uint16(10000+idx), 80, 6, 2, nil)},
			[][]byte{classifyFrame("2001:db8:1::1", destination6, uint16(10000+idx), 80, 6, 2, nil)},
		)
	}
	result, err := harness.HandleSegmentedPackets(packets...)
	require.NoError(t, err)
	require.Empty(t, result.Drop)
	require.Len(t, result.Output, 514)
	for idx, frame := range result.Output {
		info, err := framework.NewPacketParser().ParsePacket(frame)
		require.NoError(t, err)
		require.True(t, info.IsTunneled)
		// Packet order identifies the service expected at each family boundary.
		expected := "172.16.0.10"
		if (idx/2)%2 != 0 || idx/2 == 256 {
			expected = "172.16.0.11"
		}
		require.Equal(t, expected, info.DstIP.String(), "packet %d", idx)
	}
}

// Test_L3b_TCPFlagsSubtypeMatching verifies that readable SYN and ACK flags
// select distinct services while flags zero match neither rule.
func Test_L3b_TCPFlagsSubtypeMatching(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	for _, tc := range []struct{ name, real string }{{"syn", "172.16.0.10"}, {"ack", "172.16.0.11"}} {
		publishClassifyService(t, agent, tc.name, tc.real, []cl3bobject.SourceFilterRule{{
			Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
			Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		}})
	}
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{
		{Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4}, Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6}, ProtoRanges: filter.ProtoRanges{filter.NewProtoRange(6, filter.ExactSubtype(2))}, VirtualService: "syn"},
		{Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4}, Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6}, ProtoRanges: filter.ProtoRanges{filter.NewProtoRange(6, filter.ExactSubtype(16))}, VirtualService: "ack"},
	})
	for _, family := range []struct{ source, destination string }{
		{"10.0.0.1", "192.168.1.1"}, {"2001:db8:1::1", "2001:db8:2::1"},
	} {
		for _, tc := range []struct {
			name, real string
			flags      byte
		}{
			{"SYN", "172.16.0.10", 2}, {"ACK", "172.16.0.11", 16}, {"zero flags", "", 0},
		} {
			t.Run(family.source+"/"+tc.name, func(t *testing.T) {
				frame := classifyFrame(family.source, family.destination, 12345+uint16(tc.flags), 80, 6, tc.flags, nil)
				result, err := harness.HandleSegmentedPackets([][]byte{frame})
				require.NoError(t, err)
				if tc.real == "" {
					require.Empty(t, result.Output)
					require.Equal(t, [][]byte{frame}, result.Drop)
				} else {
					require.Empty(t, result.Drop)
					require.Len(t, result.Output, 1)
					info, err := framework.NewPacketParser().ParsePacket(result.Output[0])
					require.NoError(t, err)
					require.Equal(t, tc.real, info.DstIP.String())
				}
			})
		}
	}
}

// Test_L3b_SegmentedHeadersFailClosed verifies that nonresident headers reject
// while full-header controls retain their dispositions in the same batch.
//
// Raw observations expose only the head segment, not chained tail bytes.
func Test_L3b_SegmentedHeadersFailClosed(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
	}})
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		ProtoRanges: filter.ProtoRanges{
			filter.NewProtoRange(6, filter.AnySubtype()), filter.NewProtoRange(17, filter.AnySubtype()),
			filter.NewProtoRange(1, filter.ExactSubtype(8)), filter.NewProtoRange(58, filter.ExactSubtype(128)),
		},
		VirtualService: "svc",
	}})
	for _, tc := range []struct {
		name, source, destination string
		protocol, subtype         byte
		splitOffset               int
	}{
		{"IPv4 TCP IP header", "10.0.0.1", "192.168.1.1", 6, 2, 24},
		{"IPv6 TCP IP header", "2001:db8:1::1", "2001:db8:2::1", 6, 2, 24},
		{"IPv4 TCP flags", "10.0.0.1", "192.168.1.1", 6, 2, 47},
		{"IPv6 TCP flags", "2001:db8:1::1", "2001:db8:2::1", 6, 2, 67},
		{"IPv4 TCP fixed header 19 of 20 bytes", "10.0.0.1", "192.168.1.1", 6, 2, 53},
		{"IPv6 TCP fixed header 19 of 20 bytes", "2001:db8:1::1", "2001:db8:2::1", 6, 2, 73},
		{"IPv4 UDP ports", "10.0.0.1", "192.168.1.1", 17, 0, 36},
		{"IPv6 UDP ports", "2001:db8:1::1", "2001:db8:2::1", 17, 0, 56},
		{"IPv4 UDP fixed header 7 of 8 bytes", "10.0.0.1", "192.168.1.1", 17, 0, 41},
		{"IPv6 UDP fixed header 7 of 8 bytes", "2001:db8:1::1", "2001:db8:2::1", 17, 0, 61},
		{"IPv4 ICMP echo", "10.0.0.1", "192.168.1.1", 1, 8, 36},
		{"IPv6 ICMP echo", "2001:db8:1::1", "2001:db8:2::1", 58, 128, 56},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := classifyFrame(tc.source, tc.destination, 12345, 80, tc.protocol, tc.subtype, nil)
			result, err := harness.HandleSegmentedPackets([][]byte{frame[:tc.splitOffset], frame[tc.splitOffset:]}, [][]byte{frame})
			require.NoError(t, err)
			require.Equal(t, [][]byte{frame[:tc.splitOffset]}, result.Drop)
			require.Len(t, result.Output, 1)
			info, err := framework.NewPacketParser().ParsePacket(result.Output[0])
			require.NoError(t, err)
			if tc.protocol == 1 || tc.protocol == 58 {
				require.Equal(t, tc.destination, info.SrcIP.String())
			} else {
				require.True(t, info.IsTunneled)
				require.Equal(t, "172.16.0.10", info.DstIP.String())
			}
		})
	}
	// Echo only rewrites resident headers, making its payload chain a safe control.
	for _, tc := range []struct {
		name, source, destination string
		protocol, subtype         byte
		headerLength              int
	}{
		{"IPv4 echo payload", "10.0.0.1", "192.168.1.1", 1, 8, 42},
		{"IPv6 echo payload", "2001:db8:1::1", "2001:db8:2::1", 58, 128, 62},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte{0xab, 0xcd, 1, 2}
			frame := classifyFrame(tc.source, tc.destination, 0, 0, tc.protocol, tc.subtype, payload)
			result, err := harness.HandleSegmentedPackets([][]byte{frame[:tc.headerLength], frame[tc.headerLength:]})
			require.NoError(t, err)
			require.Empty(t, result.Drop)
			require.Len(t, result.Output, 1)
			outputHead := result.Output[0]
			require.Len(t, outputHead, tc.headerLength)
			if tc.protocol == 1 {
				require.Equal(t, netip.MustParseAddr(tc.destination).AsSlice(), outputHead[26:30])
				require.Equal(t, netip.MustParseAddr(tc.source).AsSlice(), outputHead[30:34])
				require.EqualValues(t, 0, outputHead[34])
				require.EqualValues(t, len(frame)-ethHeaderLen, binary.BigEndian.Uint16(outputHead[16:18]))
			} else {
				require.Equal(t, netip.MustParseAddr(tc.destination).AsSlice(), outputHead[22:38])
				require.Equal(t, netip.MustParseAddr(tc.source).AsSlice(), outputHead[38:54])
				require.EqualValues(t, 129, outputHead[54])
				require.EqualValues(t, 8+len(payload), binary.BigEndian.Uint16(outputHead[18:20]))
			}
			require.Zero(t, outputHead[tc.headerLength-7])
			require.Equal(t, frame[tc.headerLength-4:tc.headerLength], outputHead[tc.headerLength-4:])
		})
	}
}

// Test_L3b_SegmentedICMPErrorsPassthrough verifies that readable non-Echo types
// pass unchanged, absent types drop, and following TCP still tunnels.
//
// Chained packet comparisons cover only the observable head bytes.
func Test_L3b_SegmentedICMPErrorsPassthrough(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
	}})
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		ProtoRanges: filter.ProtoRanges{
			filter.NewProtoRange(1, filter.AnySubtype()),
			filter.NewProtoRange(58, filter.AnySubtype()),
			filter.NewProtoRange(6, filter.AnySubtype()),
		},
		VirtualService: "svc",
	}})
	for _, tc := range []struct {
		name, source, destination string
		protocol, subtype, code   byte
		networkLength             int
	}{
		{"IPv4 fragmentation needed", "10.0.0.1", "192.168.1.1", 1, 3, 4, 20},
		{"IPv6 packet too big", "2001:db8:1::1", "2001:db8:2::1", 58, 2, 0, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quoted := classifyFrame(tc.destination, tc.source, 80, 12345, 6, 2, nil)
			frame := classifyFrame(
				tc.source, tc.destination, 0, 0, tc.protocol, tc.subtype,
				quoted[ethHeaderLen:],
			)
			transportOffset := ethHeaderLen + tc.networkLength
			frame[transportOffset+1] = tc.code
			binary.BigEndian.PutUint32(frame[transportOffset+4:transportOffset+8], 1280)
			packets := [][][]byte{}
			for _, headBytes := range []int{1, 4, 7} {
				splitOffset := transportOffset + headBytes
				packets = append(packets, [][]byte{frame[:splitOffset], frame[splitOffset:]})
			}
			control := classifyFrame(tc.source, tc.destination, 12345, 80, 6, 2, nil)
			packets = append(packets,
				[][]byte{frame[:transportOffset], frame[transportOffset:]},
				[][]byte{frame},
				[][]byte{control},
			)
			result, err := harness.HandleSegmentedPackets(packets...)
			require.NoError(t, err)
			require.Equal(t, [][]byte{frame[:transportOffset]}, result.Drop)
			require.Len(t, result.Output, 5)
			for idx, headBytes := range []int{1, 4, 7} {
				require.Equal(t, frame[:transportOffset+headBytes], result.Output[idx], "ICMP bytes in head: %d", headBytes)
			}
			require.Equal(t, frame, result.Output[3])
			info, err := framework.NewPacketParser().ParsePacket(result.Output[4])
			require.NoError(t, err)
			require.True(t, info.IsTunneled)
			require.Equal(t, "172.16.0.10", info.DstIP.String())
		})
	}
}

// Test_L3b_ClassifyExemptPacketsPassthrough verifies that globally short ICMP
// and unread protocols pass unchanged even with a published matching service.
func Test_L3b_ClassifyExemptPacketsPassthrough(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	publishClassifyService(t, agent, "svc", "172.16.0.10", nil)
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
		Net4s:          []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Net6s:          []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(1, filter.AnySubtype()), filter.NewProtoRange(58, filter.AnySubtype()), filter.NewProtoRange(47, filter.AnySubtype())},
		VirtualService: "svc",
	}})
	for _, tc := range []struct {
		name, source, destination string
		protocol                  byte
		networkLength             int
		splitOffset               int
	}{
		{"IPv4 short ICMP", "10.0.0.1", "192.168.1.1", 1, 20, 0},
		{"IPv6 short ICMP", "2001:db8:1::1", "2001:db8:2::1", 58, 40, 0},
		{"IPv4 unread protocol", "10.0.0.1", "192.168.1.1", 47, 20, 0},
		{"IPv6 unread protocol", "2001:db8:1::1", "2001:db8:2::1", 47, 40, 0},
		{"IPv4 unread protocol IP header split", "10.0.0.1", "192.168.1.1", 47, 20, 24},
		{"IPv6 unread protocol IP header split", "2001:db8:1::1", "2001:db8:2::1", 47, 40, 24},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := classifyFrame(tc.source, tc.destination, 0, 0, tc.protocol, 0, nil)
			frame = frame[:ethHeaderLen+tc.networkLength]
			if tc.networkLength == 20 {
				binary.BigEndian.PutUint16(frame[16:18], 20)
			} else {
				binary.BigEndian.PutUint16(frame[18:20], 0)
			}
			input := [][][]byte{{frame}}
			if tc.splitOffset != 0 {
				input = append(input, [][]byte{frame[:tc.splitOffset], frame[tc.splitOffset:]})
			}
			result, err := harness.HandleSegmentedPackets(input...)
			require.NoError(t, err)
			require.Empty(t, result.Drop)
			require.Len(t, result.Output, len(input))
			for idx, output := range result.Output {
				// Only the readable head is observable for chained passthrough.
				require.Equal(t, input[idx][0], output)
			}
		})
	}
	packets, _ := counterOf(t, harness, "svc", "filter_rejected")
	require.Zero(t, packets)
}

// Test_L3b_ClassifyMatchesByDestination verifies that reversing source and
// destination networks does not select a service for outbound traffic.
func Test_L3b_ClassifyMatchesByDestination(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	publishClassifyService(t, agent, "svc", "172.16.0.10", []cl3bobject.SourceFilterRule{{
		Net4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Net6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
	}})
	publishClassifyModule(t, agent, "test", []cl3b.DestinationFilterRule{{
		Net4s:          []xnetip.Contiguous[xnetip.Network4]{xnetip.MustParseContiguous4("192.168.1.0/24")},
		Net6s:          []xnetip.BiContiguous{xnetip.MustParseBiContiguous("2001:db8:2::/48")},
		ProtoRanges:    filter.ProtoRanges{filter.NewProtoRange(6, filter.AnySubtype())},
		VirtualService: "svc",
	}})
	for _, tc := range []struct{ source, destination string }{
		{"10.0.0.1", "192.168.1.1"}, {"2001:db8:1::1", "2001:db8:2::1"},
	} {
		inbound := classifyFrame(tc.source, tc.destination, 12345, 80, 6, 2, nil)
		outbound := classifyFrame(tc.destination, tc.source, 12345, 80, 6, 2, nil)
		result, err := harness.HandleSegmentedPackets([][]byte{inbound}, [][]byte{outbound})
		require.NoError(t, err)
		require.Len(t, result.Output, 1)
		require.Equal(t, [][]byte{outbound}, result.Drop)
	}
}

// Test_L3b_RulelessConfigDropsTcp verifies that an empty destination config
// drops ordinary transport without querying an uninitialized classifier.
func Test_L3b_RulelessConfigDropsTcp(t *testing.T) {
	harness, agent := setupL3bHarness(t, "port0", "test")
	wirePipeline(t, agent, "port0", "test")
	publishClassifyModule(t, agent, "test", nil)
	frame := classifyFrame("10.0.0.1", "192.168.1.1", 12345, 80, 6, 2, nil)
	result, err := harness.HandleSegmentedPackets([][]byte{frame})
	require.NoError(t, err)
	require.Empty(t, result.Output)
	require.Equal(t, [][]byte{frame}, result.Drop)
}
