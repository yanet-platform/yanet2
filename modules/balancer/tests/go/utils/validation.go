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
	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// ValidatePacket validates that a packet has been properly processed by the balancer.
// It checks that the packet is tunneled and that the inner packet matches the original.
func ValidatePacket(
	t *testing.T,
	config *balancerpb.BalancerConfig,
	originalGoPacket gopacket.Packet,
	resultPacket *framework.PacketInfo,
) {
	t.Helper()

	// Parse the original packet
	parser := framework.NewPacketParser()
	originalPacket, err := parser.ParsePacket(originalGoPacket.Data())
	require.NoError(t, err, "failed to parse original packet")

	// Validate basic tunnel structure
	validateTunnelStructure(t, originalPacket, resultPacket, originalGoPacket)

	// Validate ToS/TrafficClass preservation
	validateTosPreservation(t, originalPacket, originalGoPacket, resultPacket)

	// Validate protocol consistency
	packetProto := validateProtocol(t, originalPacket, resultPacket)

	// Find and validate matching service and real
	validateServiceAndReal(t, config, originalPacket, resultPacket, packetProto)

	// Validate tunnel source address
	validateTunnelSourceAddress(t, config, originalPacket, resultPacket)
}

// validateTunnelStructure checks that the packet is properly tunneled with correct inner packet.
func validateTunnelStructure(
	t *testing.T,
	originalPacket *framework.PacketInfo,
	resultPacket *framework.PacketInfo,
	originalGoPacket gopacket.Packet,
) {
	t.Helper()

	// Check that result packet is tunneled
	require.True(t, resultPacket.IsTunneled, "result packet is not tunneled")

	// Check that inner packet exists
	resultInner := resultPacket.InnerPacket
	require.NotNil(t, resultInner, "no inner packet in result")

	// Validate that inner packet matches original
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

// validateTosPreservation checks that ToS/TrafficClass is preserved through encapsulation.
func validateTosPreservation(
	t *testing.T,
	originalPacket *framework.PacketInfo,
	originalGoPacket gopacket.Packet,
	resultPacket *framework.PacketInfo,
) {
	t.Helper()

	// Get original ToS/TrafficClass
	originalToS := getOriginalTos(t, originalPacket, originalGoPacket)
	if originalToS == nil {
		return // Error already reported
	}

	// Parse the full tunneled packet
	tunneled := gopacket.NewPacket(
		resultPacket.RawData,
		layers.LayerTypeEthernet,
		gopacket.Default,
	)
	if tunneled.ErrorLayer() != nil {
		t.Errorf(
			"failed to parse tunneled packet for ToS/TrafficClass check: %v",
			tunneled.ErrorLayer().Error(),
		)
		return
	}

	// Get outer ToS/TrafficClass
	outerToS := getOuterTos(t, resultPacket, tunneled)
	if outerToS == nil {
		return // Error already reported
	}

	// Get inner ToS/TrafficClass
	innerToS := getInnerTos(t, tunneled)
	if innerToS == nil {
		return // Error already reported
	}

	// Verify ToS/TrafficClass preservation
	assert.Equal(t,
		*originalToS,
		*outerToS,
		"outer packet ToS/TrafficClass mismatch with original",
	)
	assert.Equal(t,
		*originalToS,
		*innerToS,
		"inner packet ToS/TrafficClass mismatch with original",
	)
}

// getOriginalTos extracts ToS/TrafficClass from the original packet.
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
			t.Error("no IPv4 layer in original packet to read TOS")
			return nil
		}
	} else if originalPacket.IsIPv6 {
		if ipv6 := originalGoPacket.Layer(layers.LayerTypeIPv6); ipv6 != nil {
			tos = ipv6.(*layers.IPv6).TrafficClass
		} else {
			t.Error("no IPv6 layer in original packet to read TrafficClass")
			return nil
		}
	}
	return &tos
}

// getOuterTos extracts ToS/TrafficClass from the outer packet header.
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
			t.Error("no outer IPv4 layer to read TOS")
			return nil
		}
	} else if resultPacket.IsIPv6 {
		if ipv6 := tunneled.Layer(layers.LayerTypeIPv6); ipv6 != nil {
			tos = ipv6.(*layers.IPv6).TrafficClass
		} else {
			t.Error("no outer IPv6 layer to read TrafficClass")
			return nil
		}
	} else {
		t.Error("unknown outer IP version for tunneled packet")
		return nil
	}
	return &tos
}

// getInnerTos extracts ToS/TrafficClass from the inner packet header.
func getInnerTos(t *testing.T, tunneled gopacket.Packet) *uint8 {
	t.Helper()

	var innerToS uint8
	ipCount := 0
	foundInner := false

	for _, l := range tunneled.Layers() {
		switch l.LayerType() {
		case layers.LayerTypeIPv4:
			ipCount++
			if ipCount == 2 {
				innerToS = l.(*layers.IPv4).TOS
				foundInner = true
			}
		case layers.LayerTypeIPv6:
			ipCount++
			if ipCount == 2 {
				innerToS = l.(*layers.IPv6).TrafficClass
				foundInner = true
			}
		}
		if foundInner {
			break
		}
	}

	if !foundInner {
		t.Error("failed to locate inner IP header to read ToS/TrafficClass")
		return nil
	}
	return &innerToS
}

// validateProtocol checks protocol consistency between original and encapsulated packet.
func validateProtocol(
	t *testing.T,
	originalPacket *framework.PacketInfo,
	resultPacket *framework.PacketInfo,
) balancerpb.TransportProto {
	t.Helper()

	resultInner := resultPacket.InnerPacket
	var originPacketProto layers.IPProtocol

	if originalPacket.IsIPv4 {
		assert.Equal(t,
			originalPacket.Protocol,
			resultInner.Protocol,
			"encapsulated packet protocol mismatch",
		)
		originPacketProto = originalPacket.Protocol
	} else {
		assert.Equal(t,
			originalPacket.NextHeader,
			resultInner.NextHeader,
			"encapsulated packet protocol mismatch",
		)
		originPacketProto = originalPacket.NextHeader
	}

	// Determine packet proto
	var packetProto balancerpb.TransportProto
	if originPacketProto.LayerType() == layers.LayerTypeTCP {
		packetProto = balancerpb.TransportProto_TCP
	} else if originPacketProto.LayerType() == layers.LayerTypeUDP {
		packetProto = balancerpb.TransportProto_UDP
	} else {
		t.Errorf("invalid packet protocol: %s", originPacketProto.String())
	}

	return packetProto
}

// validateServiceAndReal finds the matching virtual service and real server.
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

	for idx := range config.PacketHandler.Vs {
		service := config.PacketHandler.Vs[idx]
		vsAddr, _ := netip.AddrFromSlice(service.Id.Addr.Bytes)

		if vsAddr.Compare(originalDstIP) == 0 &&
			(service.Id.Port == uint32(originalPacket.DstPort) || service.Flags.PureL3) &&
			service.Id.Proto == packetProto {
			// Found matching service
			validateTunnelType(t, service, vsAddr, resultPacket)

			if findMatchingReal(t, service, resultPacket) {
				return // Success
			}

			t.Error("not found real which can accept packet sent by balancer")
			t.Logf("user packet: %v", originalPacket)
			t.Logf("balancer packet: %v", resultPacket)
			return
		}
	}

	t.Error("not found service which could serve packet")
	t.Logf("user packet: %v", originalPacket)
	t.Logf("balancer packet: %v", resultPacket)
}

// validateTunnelType checks that the tunnel type matches the service configuration.
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
		assert.Equal(t,
			expectedTunnelType,
			resultPacket.TunnelType,
			"packet tunnel type must be gre",
		)
	}
}

// findMatchingReal searches for a real server that matches the result packet destination.
func findMatchingReal(
	t *testing.T,
	service *balancerpb.VirtualService,
	resultPacket *framework.PacketInfo,
) bool {
	t.Helper()

	resultDstIP := netip.MustParseAddr(resultPacket.DstIP.String())

	for realIdx := range service.Reals {
		real := service.Reals[realIdx]
		realAddr, _ := netip.AddrFromSlice(real.Id.Ip.Bytes)

		if realAddr.Compare(resultDstIP) == 0 {
			return true // Found matching real
		}
	}

	return false
}

// ExtractDestinationReal extracts the destination IP (real server) from a tunneled packet.
// Returns the real server IP that the packet was forwarded to.
func ExtractDestinationReal(packet *framework.PacketInfo) (netip.Addr, error) {
	if !packet.IsTunneled {
		return netip.Addr{}, fmt.Errorf("packet is not tunneled")
	}

	// The destination IP of the outer packet is the real server
	dstIP, ok := netip.AddrFromSlice(packet.DstIP)
	if !ok {
		return netip.Addr{}, fmt.Errorf(
			"failed to parse destination IP: %v",
			packet.DstIP,
		)
	}

	return dstIP, nil
}

// CountPacketsPerReal counts how many packets went to each real server.
// Returns a map from real server IP to packet count.
func CountPacketsPerReal(packets []*framework.PacketInfo) map[netip.Addr]int {
	counts := make(map[netip.Addr]int)

	for _, packet := range packets {
		realIP, err := ExtractDestinationReal(packet)
		if err != nil {
			continue // Skip non-tunneled packets
		}
		counts[realIP]++
	}

	return counts
}

// ValidateWeightDistribution checks if packet distribution matches expected weights.
// Uses tolerance-based validation (e.g., 0.15 for 15% tolerance).
func ValidateWeightDistribution(
	t *testing.T,
	counts map[netip.Addr]int,
	expectedWeights map[netip.Addr]uint32,
	tolerance float64,
) {
	t.Helper()

	// Calculate total packets and total weight
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

	// Check each real's distribution
	for realIP, expectedWeight := range expectedWeights {
		actualCount := counts[realIP]
		expectedRatio := float64(expectedWeight) / float64(totalWeight)
		actualRatio := float64(actualCount) / float64(totalPackets)

		diff := math.Abs(actualRatio - expectedRatio)
		if diff > tolerance {
			t.Errorf(
				"weight distribution mismatch for real %s: expected ratio %.3f (weight %d/%d), got %.3f (%d/%d packets), diff %.3f > tolerance %.3f",
				realIP,
				expectedRatio,
				expectedWeight,
				totalWeight,
				actualRatio,
				actualCount,
				totalPackets,
				diff,
				tolerance,
			)
		}
	}
}

// AllPacketsToSameReal checks if all packets went to the same real server.
// Returns the real server IP and true if all packets went to the same real, or empty addr and false otherwise.
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
// Returns true if packets went to more than one real server.
func PacketsDistributedAcrossReals(packets []*framework.PacketInfo) bool {
	counts := CountPacketsPerReal(packets)
	return len(counts) > 1
}

// validateTunnelSourceAddress validates that the tunnel source address is correctly calculated
// according to the formula: tunnel_src = client_ip & !real_mask | real_src & real_mask
// This matches the implementation in modules/balancer/dataplane/tunnel.h
func validateTunnelSourceAddress(
	t *testing.T,
	config *balancerpb.BalancerConfig,
	originalPacket *framework.PacketInfo,
	resultPacket *framework.PacketInfo,
) {
	t.Helper()

	if !resultPacket.IsTunneled {
		return // Not a tunneled packet, nothing to validate
	}

	// Get the client IP (source of original packet)
	clientIP := originalPacket.SrcIP
	if clientIP == nil {
		t.Error("original packet has no source IP")
		return
	}

	// Get the tunnel source IP (source of outer packet)
	tunnelSrcIP := resultPacket.SrcIP
	if tunnelSrcIP == nil {
		t.Error("result packet has no source IP")
		return
	}

	// Find the matching virtual service and real
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

	// Find the matching virtual service
	for _, service := range config.PacketHandler.Vs {
		vsAddr, _ := netip.AddrFromSlice(service.Id.Addr.Bytes)

		if vsAddr.Compare(originalDstIP) == 0 &&
			(service.Id.Port == uint32(originalPacket.DstPort) || service.Flags.PureL3) &&
			service.Id.Proto == packetProto {

			// Find the matching real server
			for _, real := range service.Reals {
				realAddr, _ := netip.AddrFromSlice(real.Id.Ip.Bytes)

				if realAddr.Compare(resultDstIP) == 0 {
					// Found the matching real, now validate source address
					validateSourceAddressCalculation(
						t,
						clientIP,
						tunnelSrcIP,
						real,
					)
					return
				}
			}
		}
	}
}

// validateSourceAddressCalculation validates the tunnel source address calculation
// Formula: tunnel_src = client_ip & !real_mask | real_src & real_mask
// The tunnel source IP protocol is determined by the real server's IP protocol, not the client's.
func validateSourceAddressCalculation(
	t *testing.T,
	clientIP net.IP,
	tunnelSrcIP net.IP,
	real *balancerpb.Real,
) {
	t.Helper()

	if real.SrcAddr == nil || real.SrcMask == nil {
		t.Error("real server has no SrcAddr or SrcMask configured")
		return
	}

	if real.Id == nil || real.Id.Ip == nil {
		t.Error("real server has no Id or Ip configured")
		return
	}

	realSrc := real.SrcAddr.Bytes
	realMask := real.SrcMask.Bytes
	realIP := real.Id.Ip.Bytes

	// Determine real server's IP protocol from its address length
	realIsIPv6 := len(realIP) == 16
	realIsIPv4 := len(realIP) == 4

	if !realIsIPv4 && !realIsIPv6 {
		t.Errorf("unexpected real IP address length: %d", len(realIP))
		return
	}

	// Normalize client IP
	var clientIPBytes []byte
	if len(clientIP) == 4 || (len(clientIP) == 16 && clientIP.To4() != nil) {
		// Client is IPv4
		clientIPv4 := clientIP.To4()
		if clientIPv4 == nil {
			t.Error("failed to convert client IP to IPv4")
			return
		}
		clientIPBytes = []byte(clientIPv4)
	} else if len(clientIP) == 16 {
		// Client is IPv6
		clientIPBytes = []byte(clientIP)
	} else {
		t.Errorf("unexpected client IP address length: %d", len(clientIP))
		return
	}

	// Validate based on real server's IP protocol
	if realIsIPv6 {
		// Tunnel to IPv6 real: tunnel source MUST be IPv6
		if len(tunnelSrcIP) != 16 || tunnelSrcIP.To4() != nil {
			t.Errorf(
				"tunnel source IP should be IPv6 when tunneling to IPv6 real, got %s",
				tunnelSrcIP,
			)
			return
		}

		// Calculate expected source: client_ip & !real_mask | real_src & real_mask
		expectedSrc := make([]byte, 16)

		// Determine how many bytes to use from client IP
		clientLen := len(clientIPBytes)
		if clientLen > 16 {
			clientLen = 16
		}

		for i := 0; i < 16; i++ {
			var clientByte byte
			if i < clientLen {
				clientByte = clientIPBytes[i]
			} else {
				clientByte = 0
			}
			expectedSrc[i] = (clientByte & ^realMask[i]) | (realSrc[i] & realMask[i])
		}

		expectedSrcIP := net.IP(expectedSrc)
		if !tunnelSrcIP.Equal(expectedSrcIP) {
			t.Errorf(
				"tunnel source address mismatch: expected %s, got %s (client=%s, real_src=%s, real_mask=%s, real_ip=%s)",
				expectedSrcIP,
				tunnelSrcIP,
				clientIP,
				net.IP(realSrc),
				net.IP(realMask),
				net.IP(realIP),
			)
		}
	} else {
		// Tunnel to IPv4 real: tunnel source MUST be IPv4
		tunnelSrcIPv4 := tunnelSrcIP.To4()
		if tunnelSrcIPv4 == nil {
			t.Errorf(
				"tunnel source IP should be IPv4 when tunneling to IPv4 real, got %s",
				tunnelSrcIP,
			)
			return
		}

		// Calculate expected source: client_ip & !real_mask | real_src & real_mask
		// Use only first 4 bytes of client IP (whether IPv4 or IPv6)
		expectedSrc := make([]byte, 4)
		for i := 0; i < 4; i++ {
			var clientByte byte
			if i < len(clientIPBytes) {
				clientByte = clientIPBytes[i]
			} else {
				clientByte = 0
			}
			expectedSrc[i] = (clientByte & ^realMask[i]) | (realSrc[i] & realMask[i])
		}

		expectedSrcIP := net.IP(expectedSrc)
		if !tunnelSrcIPv4.Equal(expectedSrcIP) {
			t.Errorf(
				"tunnel source address mismatch: expected %s, got %s (client=%s, real_src=%s, real_mask=%s, real_ip=%s)",
				expectedSrcIP,
				tunnelSrcIPv4,
				clientIP,
				net.IP(realSrc),
				net.IP(realMask),
				net.IP(realIP),
			)
		}
	}
}
