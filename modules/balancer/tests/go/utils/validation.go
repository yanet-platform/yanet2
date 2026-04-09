package utils

import (
	"fmt"
	"math"
	"net"
	"net/netip"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// ValidatePacket validates that a packet has been properly processed by the balancer.
func ValidatePacket(
	t *testing.T,
	config *balancerpb.BalancerConfig,
	originalGoPacket gopacket.Packet,
	resultPacket *framework.PacketInfo,
) {
	t.Helper()

	parser := framework.NewPacketParser()
	originalPacket, err := parser.ParsePacket(originalGoPacket.Data())
	require.NoError(t, err, "failed to parse original packet")

	validateTunnelStructure(t, originalPacket, resultPacket, originalGoPacket)
	validateTosPreservation(t, originalPacket, originalGoPacket, resultPacket)
	packetProto := validateProtocol(t, originalPacket, resultPacket)
	validateServiceAndReal(t, config, originalPacket, resultPacket, packetProto)
	validateTunnelSourceAddress(t, config, originalPacket, resultPacket)
}

func validateTunnelStructure(
	t *testing.T,
	originalPacket *framework.PacketInfo,
	resultPacket *framework.PacketInfo,
	originalGoPacket gopacket.Packet,
) {
	t.Helper()

	require.True(t, resultPacket.IsTunneled, "result packet is not tunneled")

	resultInner := resultPacket.InnerPacket
	require.NotNil(t, resultInner, "no inner packet in result")

	assert.Equal(t,
		originalPacket.DstIP.String(),
		resultInner.DstIP.String(),
		"encapsulated packet dst ip mismatch",
	)
	assert.Equal(t,
		originalPacket.SrcIP.String(),
		resultInner.SrcIP.String(),
		"encapsulated packet src ip mismatch",
	)
	assert.Equal(t,
		originalGoPacket.ApplicationLayer().Payload(),
		resultPacket.Payload,
		"payload mismatch",
	)
}

func validateTosPreservation(
	t *testing.T,
	originalPacket *framework.PacketInfo,
	originalGoPacket gopacket.Packet,
	resultPacket *framework.PacketInfo,
) {
	t.Helper()

	originalToS := getOriginalTos(t, originalPacket, originalGoPacket)
	if originalToS == nil {
		return
	}

	tunneled := gopacket.NewPacket(
		resultPacket.RawData,
		layers.LayerTypeEthernet,
		gopacket.Default,
	)
	if tunneled.ErrorLayer() != nil {
		t.Errorf("failed to parse tunneled packet: %v", tunneled.ErrorLayer().Error())
		return
	}

	outerToS := getOuterTos(t, resultPacket, tunneled)
	if outerToS == nil {
		return
	}

	innerToS := getInnerTos(t, tunneled)
	if innerToS == nil {
		return
	}

	assert.Equal(t, *originalToS, *outerToS,
		"outer packet ToS/TrafficClass mismatch with original")
	assert.Equal(t, *originalToS, *innerToS,
		"inner packet ToS/TrafficClass mismatch with original")
}

func getOriginalTos(
	t *testing.T,
	originalPacket *framework.PacketInfo,
	originalGoPacket gopacket.Packet,
) *uint8 {
	t.Helper()

	var tos uint8
	if originalPacket.IsIPv4 {
		if ipv4 := originalGoPacket.Layer(layers.LayerTypeIPv4); ipv4 != nil {
			tos = ipv4.(*layers.IPv4).TOS
		} else {
			t.Error("no IPv4 layer in original packet")
			return nil
		}
	} else if originalPacket.IsIPv6 {
		if ipv6 := originalGoPacket.Layer(layers.LayerTypeIPv6); ipv6 != nil {
			tos = ipv6.(*layers.IPv6).TrafficClass
		} else {
			t.Error("no IPv6 layer in original packet")
			return nil
		}
	}
	return &tos
}

func getOuterTos(
	t *testing.T,
	resultPacket *framework.PacketInfo,
	tunneled gopacket.Packet,
) *uint8 {
	t.Helper()

	var tos uint8
	if resultPacket.IsIPv4 {
		if ipv4 := tunneled.Layer(layers.LayerTypeIPv4); ipv4 != nil {
			tos = ipv4.(*layers.IPv4).TOS
		} else {
			t.Error("no outer IPv4 layer")
			return nil
		}
	} else if resultPacket.IsIPv6 {
		if ipv6 := tunneled.Layer(layers.LayerTypeIPv6); ipv6 != nil {
			tos = ipv6.(*layers.IPv6).TrafficClass
		} else {
			t.Error("no outer IPv6 layer")
			return nil
		}
	} else {
		t.Error("unknown outer IP version")
		return nil
	}
	return &tos
}

func getInnerTos(t *testing.T, tunneled gopacket.Packet) *uint8 {
	t.Helper()

	var innerToS uint8
	ipCount := 0
	found := false

	for _, l := range tunneled.Layers() {
		switch l.LayerType() {
		case layers.LayerTypeIPv4:
			ipCount++
			if ipCount == 2 {
				innerToS = l.(*layers.IPv4).TOS
				found = true
			}
		case layers.LayerTypeIPv6:
			ipCount++
			if ipCount == 2 {
				innerToS = l.(*layers.IPv6).TrafficClass
				found = true
			}
		}
		if found {
			break
		}
	}

	if !found {
		t.Error("failed to locate inner IP header")
		return nil
	}
	return &innerToS
}

func validateProtocol(
	t *testing.T,
	originalPacket *framework.PacketInfo,
	resultPacket *framework.PacketInfo,
) balancerpb.TransportProto {
	t.Helper()

	resultInner := resultPacket.InnerPacket
	var originPacketProto layers.IPProtocol

	if originalPacket.IsIPv4 {
		assert.Equal(t, originalPacket.Protocol, resultInner.Protocol,
			"encapsulated packet protocol mismatch")
		originPacketProto = originalPacket.Protocol
	} else {
		assert.Equal(t, originalPacket.NextHeader, resultInner.NextHeader,
			"encapsulated packet protocol mismatch")
		originPacketProto = originalPacket.NextHeader
	}

	if originPacketProto.LayerType() == layers.LayerTypeTCP {
		return balancerpb.TransportProto_TCP
	}
	if originPacketProto.LayerType() == layers.LayerTypeUDP {
		return balancerpb.TransportProto_UDP
	}
	t.Errorf("invalid packet protocol: %s", originPacketProto.String())
	return balancerpb.TransportProto_TCP
}

func validateServiceAndReal(
	t *testing.T,
	config *balancerpb.BalancerConfig,
	originalPacket *framework.PacketInfo,
	resultPacket *framework.PacketInfo,
	packetProto balancerpb.TransportProto,
) {
	t.Helper()

	if config.PacketHandler == nil {
		t.Error("packet handler config is nil")
		return
	}

	originalDstIP := netip.MustParseAddr(originalPacket.DstIP.String())

	for _, service := range config.PacketHandler.Vs {
		vsAddr, _ := netip.AddrFromSlice(service.Id.Addr)

		if vsAddr.Compare(originalDstIP) == 0 &&
			(service.Id.Port == uint32(originalPacket.DstPort) || service.Flags.PureL3) &&
			service.Id.Proto == packetProto {

			validateTunnelType(t, service, vsAddr, resultPacket)

			if findMatchingReal(t, service, resultPacket) {
				return
			}

			t.Error("no real found that matches packet destination")
			t.Logf("original: %v", originalPacket)
			t.Logf("result: %v", resultPacket)
			return
		}
	}

	t.Error("no service found that matches packet")
	t.Logf("original: %v", originalPacket)
	t.Logf("result: %v", resultPacket)
}

func validateTunnelType(
	t *testing.T,
	service *balancerpb.VirtualService,
	vsAddr netip.Addr,
	resultPacket *framework.PacketInfo,
) {
	t.Helper()

	if service.Flags.Gre {
		expectedTunnelType := "gre-ip4"
		if vsAddr.Is6() {
			expectedTunnelType = "gre-ip6"
		}
		assert.Equal(t, expectedTunnelType, resultPacket.TunnelType,
			"packet tunnel type must be gre")
	}
}

func findMatchingReal(
	t *testing.T,
	service *balancerpb.VirtualService,
	resultPacket *framework.PacketInfo,
) bool {
	t.Helper()

	resultDstIP := netip.MustParseAddr(resultPacket.DstIP.String())

	for _, real := range service.Reals {
		realAddr, _ := netip.AddrFromSlice(real.Id.Ip)
		if realAddr.Compare(resultDstIP) == 0 {
			return true
		}
	}
	return false
}

// ExtractDestinationReal extracts the destination IP (real server) from a tunneled packet.
func ExtractDestinationReal(packet *framework.PacketInfo) (netip.Addr, error) {
	if !packet.IsTunneled {
		return netip.Addr{}, fmt.Errorf("packet is not tunneled")
	}

	dstIP, ok := netip.AddrFromSlice(packet.DstIP)
	if !ok {
		return netip.Addr{}, fmt.Errorf("failed to parse destination IP: %v", packet.DstIP)
	}
	return dstIP, nil
}

// CountPacketsPerReal counts how many packets went to each real server.
func CountPacketsPerReal(packets []*framework.PacketInfo) map[netip.Addr]int {
	counts := make(map[netip.Addr]int)
	for _, packet := range packets {
		realIP, err := ExtractDestinationReal(packet)
		if err != nil {
			continue
		}
		counts[realIP]++
	}
	return counts
}

// ValidateWeightDistribution checks if packet distribution matches expected weights.
func ValidateWeightDistribution(
	t *testing.T,
	counts map[netip.Addr]int,
	expectedWeights map[netip.Addr]uint32,
	tolerance float64,
) {
	t.Helper()

	totalPackets := 0
	for _, count := range counts {
		totalPackets += count
	}

	totalWeight := uint32(0)
	for _, weight := range expectedWeights {
		totalWeight += weight
	}

	if totalPackets == 0 {
		t.Error("no packets to validate")
		return
	}
	if totalWeight == 0 {
		t.Error("total weight is zero")
		return
	}

	for realIP, expectedWeight := range expectedWeights {
		actualCount := counts[realIP]
		expectedRatio := float64(expectedWeight) / float64(totalWeight)
		actualRatio := float64(actualCount) / float64(totalPackets)

		diff := math.Abs(actualRatio - expectedRatio)
		if diff > tolerance {
			t.Errorf(
				"weight distribution mismatch for real %s: expected ratio %.3f (weight %d/%d), got %.3f (%d/%d packets), diff %.3f > tolerance %.3f",
				realIP, expectedRatio, expectedWeight, totalWeight,
				actualRatio, actualCount, totalPackets, diff, tolerance,
			)
		}
	}
}

// AllPacketsToSameReal checks if all packets went to the same real server.
func AllPacketsToSameReal(packets []*framework.PacketInfo) (netip.Addr, bool) {
	if len(packets) == 0 {
		return netip.Addr{}, false
	}

	var firstReal netip.Addr
	firstSet := false

	for _, packet := range packets {
		realIP, err := ExtractDestinationReal(packet)
		if err != nil {
			return netip.Addr{}, false
		}
		if !firstSet {
			firstReal = realIP
			firstSet = true
		} else if firstReal != realIP {
			return netip.Addr{}, false
		}
	}

	return firstReal, true
}

// PacketsDistributedAcrossReals checks if packets are distributed across multiple reals.
func PacketsDistributedAcrossReals(packets []*framework.PacketInfo) bool {
	counts := CountPacketsPerReal(packets)
	return len(counts) > 1
}

func validateTunnelSourceAddress(
	t *testing.T,
	config *balancerpb.BalancerConfig,
	originalPacket *framework.PacketInfo,
	resultPacket *framework.PacketInfo,
) {
	t.Helper()

	if !resultPacket.IsTunneled {
		return
	}

	clientIP := originalPacket.SrcIP
	if clientIP == nil {
		t.Error("original packet has no source IP")
		return
	}

	tunnelSrcIP := resultPacket.SrcIP
	if tunnelSrcIP == nil {
		t.Error("result packet has no source IP")
		return
	}

	originalDstIP := netip.MustParseAddr(originalPacket.DstIP.String())
	resultDstIP := netip.MustParseAddr(resultPacket.DstIP.String())

	var packetProto balancerpb.TransportProto
	if originalPacket.IsIPv4 {
		if originalPacket.Protocol.LayerType() == layers.LayerTypeTCP {
			packetProto = balancerpb.TransportProto_TCP
		} else if originalPacket.Protocol.LayerType() == layers.LayerTypeUDP {
			packetProto = balancerpb.TransportProto_UDP
		}
	} else if originalPacket.IsIPv6 {
		if originalPacket.NextHeader.LayerType() == layers.LayerTypeTCP {
			packetProto = balancerpb.TransportProto_TCP
		} else if originalPacket.NextHeader.LayerType() == layers.LayerTypeUDP {
			packetProto = balancerpb.TransportProto_UDP
		}
	}

	if config.PacketHandler == nil {
		t.Error("packet handler config is nil")
		return
	}

	for _, service := range config.PacketHandler.Vs {
		vsAddr, _ := netip.AddrFromSlice(service.Id.Addr)

		if vsAddr.Compare(originalDstIP) == 0 &&
			(service.Id.Port == uint32(originalPacket.DstPort) || service.Flags.PureL3) &&
			service.Id.Proto == packetProto {

			for _, real := range service.Reals {
				realAddr, _ := netip.AddrFromSlice(real.Id.Ip)
				if realAddr.Compare(resultDstIP) == 0 {
					validateSourceAddressCalculation(t, clientIP, tunnelSrcIP, real)
					return
				}
			}
		}
	}
}

func validateSourceAddressCalculation(
	t *testing.T,
	clientIP net.IP,
	tunnelSrcIP net.IP,
	real *balancerpb.Real,
) {
	t.Helper()

	if real.Src == nil {
		t.Error("real server has no Src configured")
		return
	}

	realSrc := real.Src.Addr
	realMask := real.Src.Mask
	realIP := real.Id.Ip

	realIsIPv6 := len(realIP) == 16
	realIsIPv4 := len(realIP) == 4

	if !realIsIPv4 && !realIsIPv6 {
		t.Errorf("unexpected real IP address length: %d", len(realIP))
		return
	}

	var clientIPBytes []byte
	if len(clientIP) == 4 || (len(clientIP) == 16 && clientIP.To4() != nil) {
		clientIPv4 := clientIP.To4()
		if clientIPv4 == nil {
			t.Error("failed to convert client IP to IPv4")
			return
		}
		clientIPBytes = []byte(clientIPv4)
	} else if len(clientIP) == 16 {
		clientIPBytes = []byte(clientIP)
	} else {
		t.Errorf("unexpected client IP address length: %d", len(clientIP))
		return
	}

	if realIsIPv6 {
		if len(tunnelSrcIP) != 16 || tunnelSrcIP.To4() != nil {
			t.Errorf("tunnel source IP should be IPv6 for IPv6 real, got %s", tunnelSrcIP)
			return
		}

		expectedSrc := make([]byte, 16)
		clientLen := len(clientIPBytes)
		if clientLen > 16 {
			clientLen = 16
		}
		for i := 0; i < 16; i++ {
			var clientByte byte
			if i < clientLen {
				clientByte = clientIPBytes[i]
			}
			expectedSrc[i] = (clientByte & ^realMask[i]) | (realSrc[i] & realMask[i])
		}

		if !tunnelSrcIP.Equal(net.IP(expectedSrc)) {
			t.Errorf("tunnel source address mismatch: expected %s, got %s (client=%s, src=%s, mask=%s)",
				net.IP(expectedSrc), tunnelSrcIP, clientIP, net.IP(realSrc), net.IP(realMask))
		}
	} else {
		tunnelSrcIPv4 := tunnelSrcIP.To4()
		if tunnelSrcIPv4 == nil {
			t.Errorf("tunnel source IP should be IPv4 for IPv4 real, got %s", tunnelSrcIP)
			return
		}

		expectedSrc := make([]byte, 4)
		for i := 0; i < 4; i++ {
			var clientByte byte
			if i < len(clientIPBytes) {
				clientByte = clientIPBytes[i]
			}
			expectedSrc[i] = (clientByte & ^realMask[i]) | (realSrc[i] & realMask[i])
		}

		if !tunnelSrcIPv4.Equal(net.IP(expectedSrc)) {
			t.Errorf("tunnel source address mismatch: expected %s, got %s (client=%s, src=%s, mask=%s)",
				net.IP(expectedSrc), tunnelSrcIPv4, clientIP, net.IP(realSrc), net.IP(realMask))
		}
	}
}
