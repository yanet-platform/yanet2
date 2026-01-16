package utils

import (
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
