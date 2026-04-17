package utils

import (
	"bytes"
	"fmt"
	"math"
	"net"
	"net/netip"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// ValidatePacket validates that a packet has been properly processed by the balancer.
func ValidatePacket(
	config *balancerpb.BalancerConfig,
	originalGoPacket gopacket.Packet,
	resultPacket *framework.PacketInfo,
) (PacketInfo, error) {
	parser := framework.NewPacketParser()
	originalPacket, err := parser.ParsePacket(originalGoPacket.Data())
	if err != nil {
		return PacketInfo{}, fmt.Errorf("failed to parse packet: %w", err)
	}

	if err := validateTunnelStructure(originalPacket, resultPacket, originalGoPacket); err != nil {
		return PacketInfo{}, fmt.Errorf("tunnel structure validation failed: %w", err)
	}

	if err := validateTosPreservation(originalPacket, originalGoPacket, resultPacket); err != nil {
		return PacketInfo{}, fmt.Errorf("ToS preservation validation failed: %w", err)
	}

	packetProto, err := validateProtocol(originalPacket, resultPacket)
	if err != nil {
		return PacketInfo{}, fmt.Errorf("protocol validation failed: %w", err)
	}

	vs, rl, err := validateServiceAndReal(config, originalPacket, resultPacket, packetProto)
	if err != nil {
		return PacketInfo{}, fmt.Errorf("service/real validation failed: %w", err)
	}

	if err := validateTunnelSourceAddress(config, originalPacket, resultPacket); err != nil {
		return PacketInfo{}, fmt.Errorf("tunnel source address validation failed: %w", err)
	}

	clientIP, _ := netip.AddrFromSlice(originalPacket.SrcIP)
	clientPort := originalPacket.SrcPort

	return PacketInfo{
		VsID:       VsIDFromPb(vs.Id),
		ClientAddr: clientIP,
		ClientPort: clientPort,
		RealID:     RealIDFromPb(rl.Id),
		Packet:     resultPacket,
	}, nil
}

func validateTunnelStructure(
	originalPacket *framework.PacketInfo,
	resultPacket *framework.PacketInfo,
	originalGoPacket gopacket.Packet,
) error {
	if !resultPacket.IsTunneled {
		return fmt.Errorf("result packet is not tunneled")
	}

	resultInner := resultPacket.InnerPacket
	if resultInner == nil {
		return fmt.Errorf("no inner packet in result")
	}

	if !originalPacket.DstIP.Equal(resultInner.DstIP) {
		return fmt.Errorf("encapsulated packet dst ip mismatch")
	}

	if !originalPacket.SrcIP.Equal(resultInner.SrcIP) {
		return fmt.Errorf("encapsulated packet src ip mismatch")
	}

	if !bytes.Equal(originalGoPacket.ApplicationLayer().Payload(), resultPacket.Payload) {
		return fmt.Errorf("payload mismatch")
	}

	return nil
}

func validateTosPreservation(
	originalPacket *framework.PacketInfo,
	originalGoPacket gopacket.Packet,
	resultPacket *framework.PacketInfo,
) error {
	originalToS, err := getOriginalTos(originalPacket, originalGoPacket)
	if err != nil {
		return fmt.Errorf("failed to get original ToS: %w", err)
	}
	if originalToS == nil {
		return nil
	}

	tunneled := gopacket.NewPacket(
		resultPacket.RawData,
		layers.LayerTypeEthernet,
		gopacket.Default,
	)
	if tunneled.ErrorLayer() != nil {
		return fmt.Errorf("failed to parse tunneled packet: %v", tunneled.ErrorLayer().Error())
	}

	outerToS, err := getOuterTos(resultPacket, tunneled)
	if err != nil {
		return fmt.Errorf("failed to get outer ToS: %w", err)
	}
	if outerToS == nil {
		return nil
	}

	innerToS, err := getInnerTos(tunneled)
	if err != nil {
		return fmt.Errorf("failed to get inner ToS: %w", err)
	}
	if innerToS == nil {
		return nil
	}

	if *originalToS != *outerToS {
		return fmt.Errorf(
			"outer packet ToS/TrafficClass mismatch with original: expected %d, got %d",
			*originalToS, *outerToS,
		)
	}
	if *originalToS != *innerToS {
		return fmt.Errorf(
			"inner packet ToS/TrafficClass mismatch with original: expected %d, got %d",
			*originalToS, *innerToS,
		)
	}

	return nil
}

func getOriginalTos(
	originalPacket *framework.PacketInfo,
	originalGoPacket gopacket.Packet,
) (*uint8, error) {
	var tos uint8
	if originalPacket.IsIPv4 {
		if ipv4 := originalGoPacket.Layer(layers.LayerTypeIPv4); ipv4 != nil {
			tos = ipv4.(*layers.IPv4).TOS
		} else {
			return nil, fmt.Errorf("no IPv4 layer in original packet")
		}
	} else if originalPacket.IsIPv6 {
		if ipv6 := originalGoPacket.Layer(layers.LayerTypeIPv6); ipv6 != nil {
			tos = ipv6.(*layers.IPv6).TrafficClass
		} else {
			return nil, fmt.Errorf("no IPv6 layer in original packet")
		}
	}
	return &tos, nil
}

func getOuterTos(
	resultPacket *framework.PacketInfo,
	tunneled gopacket.Packet,
) (*uint8, error) {
	var tos uint8

	switch {
	case resultPacket.IsIPv4:
		ipv4 := tunneled.Layer(layers.LayerTypeIPv4)
		if ipv4 == nil {
			return nil, fmt.Errorf("no outer IPv4 layer")
		}
		tos = ipv4.(*layers.IPv4).TOS

	case resultPacket.IsIPv6:
		ipv6 := tunneled.Layer(layers.LayerTypeIPv6)
		if ipv6 == nil {
			return nil, fmt.Errorf("no outer IPv6 layer")
		}
		tos = ipv6.(*layers.IPv6).TrafficClass

	default:
		return nil, fmt.Errorf("unknown outer IP version")
	}

	return &tos, nil
}

func getInnerTos(tunneled gopacket.Packet) (*uint8, error) {
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
		return nil, fmt.Errorf("failed to locate inner IP header")
	}
	return &innerToS, nil
}

func validateProtocol(
	originalPacket *framework.PacketInfo,
	resultPacket *framework.PacketInfo,
) (balancerpb.TransportProto, error) {
	resultInner := resultPacket.InnerPacket
	var originPacketProto layers.IPProtocol

	if originalPacket.IsIPv4 {
		if originalPacket.Protocol != resultInner.Protocol {
			return 0, fmt.Errorf(
				"encapsulated packet protocol mismatch: original %v, result %v",
				originalPacket.Protocol, resultInner.Protocol,
			)
		}
		originPacketProto = originalPacket.Protocol
	} else {
		if originalPacket.NextHeader != resultInner.NextHeader {
			return 0, fmt.Errorf(
				"encapsulated packet protocol mismatch: original %v, result %v",
				originalPacket.NextHeader, resultInner.NextHeader,
			)
		}
		originPacketProto = originalPacket.NextHeader
	}

	if originPacketProto.LayerType() == layers.LayerTypeTCP {
		return balancerpb.TransportProto_TCP, nil
	}
	if originPacketProto.LayerType() == layers.LayerTypeUDP {
		return balancerpb.TransportProto_UDP, nil
	}
	return 0, fmt.Errorf("invalid packet protocol: %s", originPacketProto.String())
}

func validateServiceAndReal(
	config *balancerpb.BalancerConfig,
	originalPacket *framework.PacketInfo,
	resultPacket *framework.PacketInfo,
	packetProto balancerpb.TransportProto,
) (*balancerpb.VirtualService, *balancerpb.Real, error) {
	if config.PacketHandler == nil {
		return nil, nil, fmt.Errorf("packet handler config is nil")
	}

	originalDstIP := netip.MustParseAddr(originalPacket.DstIP.String())

	for _, service := range config.PacketHandler.Vs {
		vsAddr, _ := netip.AddrFromSlice(service.Id.Addr)

		if vsAddr.Compare(originalDstIP) == 0 &&
			(service.Id.Port == uint32(originalPacket.DstPort) || service.Flags.PureL3) &&
			service.Id.Proto == packetProto {

			if err := validateTunnelType(service, vsAddr, resultPacket); err != nil {
				return nil, nil, err
			}

			if rl := findMatchingReal(service, resultPacket); rl != nil {
				return service, rl, nil
			}

			return nil, nil, fmt.Errorf(
				"no real found that matches packet destination (original: %v, result: %v)",
				originalPacket, resultPacket,
			)
		}
	}

	return nil, nil, fmt.Errorf(
		"no service found that matches packet (original: %v, result: %v)",
		originalPacket, resultPacket,
	)
}

func validateTunnelType(
	service *balancerpb.VirtualService,
	vsAddr netip.Addr,
	resultPacket *framework.PacketInfo,
) error {
	if service.Flags.Gre {
		expectedTunnelType := "gre-ip4"
		if vsAddr.Is6() {
			expectedTunnelType = "gre-ip6"
		}
		if resultPacket.TunnelType != expectedTunnelType {
			return fmt.Errorf(
				"packet tunnel type must be %s, got %s",
				expectedTunnelType, resultPacket.TunnelType,
			)
		}
	}
	return nil
}

func findMatchingReal(
	service *balancerpb.VirtualService,
	resultPacket *framework.PacketInfo,
) *balancerpb.Real {
	resultDstIP := netip.MustParseAddr(resultPacket.DstIP.String())

	for _, real := range service.Reals {
		realAddr, _ := netip.AddrFromSlice(real.Id.Ip)
		if realAddr.Compare(resultDstIP) == 0 {
			return real
		}
	}
	return nil
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
func CountPacketsPerReal(packets []PacketInfo) (map[netip.Addr]int, error) {
	counts := make(map[netip.Addr]int)
	for _, packet := range packets {
		realIP, err := ExtractDestinationReal(packet.Packet)
		if err != nil {
			return nil, err
		}
		counts[realIP]++
	}
	return counts, nil
}

// ValidateWeightDistribution checks if packet distribution matches expected weights.
func ValidateWeightDistribution(
	counts map[netip.Addr]int,
	expectedWeights map[netip.Addr]uint32,
	tolerance float64,
) error {
	totalPackets := 0
	for _, count := range counts {
		totalPackets += count
	}

	totalWeight := uint32(0)
	for _, weight := range expectedWeights {
		totalWeight += weight
	}

	if totalPackets == 0 {
		return fmt.Errorf("no packets to validate")
	}
	if totalWeight == 0 {
		return fmt.Errorf("total weight is zero")
	}

	for realIP, expectedWeight := range expectedWeights {
		actualCount := counts[realIP]
		expectedRatio := float64(expectedWeight) / float64(totalWeight)
		actualRatio := float64(actualCount) / float64(totalPackets)

		diff := math.Abs(actualRatio - expectedRatio)
		if diff > tolerance {
			return fmt.Errorf(
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

	return nil
}

// AllSessionsToSameReal checks if all packets went to the same real server.
func AllSessionsToSameReal(sessions []PacketInfo) (netip.Addr, bool) {
	if len(sessions) == 0 {
		return netip.Addr{}, true
	}

	var firstReal netip.Addr
	firstSet := false

	for _, packet := range sessions {
		realIP, ok := netip.AddrFromSlice(packet.RealID.addr[:])
		if !ok {
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

func validateTunnelSourceAddress(
	config *balancerpb.BalancerConfig,
	originalPacket *framework.PacketInfo,
	resultPacket *framework.PacketInfo,
) error {
	if !resultPacket.IsTunneled {
		return nil
	}

	clientIP := originalPacket.SrcIP
	if clientIP == nil {
		return fmt.Errorf("original packet has no source IP")
	}

	tunnelSrcIP := resultPacket.SrcIP
	if tunnelSrcIP == nil {
		return fmt.Errorf("result packet has no source IP")
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
		return fmt.Errorf("packet handler config is nil")
	}

	for _, service := range config.PacketHandler.Vs {
		vsAddr, _ := netip.AddrFromSlice(service.Id.Addr)

		if vsAddr.Compare(originalDstIP) == 0 &&
			(service.Id.Port == uint32(originalPacket.DstPort) || service.Flags.PureL3) &&
			service.Id.Proto == packetProto {

			for _, real := range service.Reals {
				realAddr, _ := netip.AddrFromSlice(real.Id.Ip)
				if realAddr.Compare(resultDstIP) == 0 {
					return validateSourceAddressCalculation(clientIP, tunnelSrcIP, real)
				}
			}
		}
	}

	return nil
}

func validateSourceAddressCalculation(
	clientIP net.IP,
	tunnelSrcIP net.IP,
	r *balancerpb.Real,
) error {
	if r.Src == nil {
		return fmt.Errorf("real server has no Src configured")
	}

	realSrc := r.Src.Addr
	realMask := r.Src.Mask
	realIP := r.Id.Ip

	realIsIPv6 := len(realIP) == 16
	realIsIPv4 := len(realIP) == 4

	if !realIsIPv4 && !realIsIPv6 {
		return fmt.Errorf("unexpected real IP address length: %d", len(realIP))
	}

	var clientIPBytes []byte
	switch {
	case len(clientIP) == 4:
		clientIPv4 := clientIP.To4()
		if clientIPv4 == nil {
			return fmt.Errorf("failed to convert client IP to IPv4")
		}
		clientIPBytes = []byte(clientIPv4)

	case len(clientIP) == 16:
		clientIPBytes = []byte(clientIP)

	default:
		return fmt.Errorf("unexpected client IP address length: %d", len(clientIP))
	}

	if realIsIPv6 {
		if len(tunnelSrcIP) != 16 {
			return fmt.Errorf("tunnel source IP should be IPv6 for IPv6 real, got %s", tunnelSrcIP)
		}

		expectedSrc := make([]byte, 16)
		clientLen := min(len(clientIPBytes), 16)
		for i := range 16 {
			var clientByte byte
			if i < clientLen {
				clientByte = clientIPBytes[i]
			}
			expectedSrc[i] = (clientByte & ^realMask[i]) | (realSrc[i] & realMask[i])
		}

		if !tunnelSrcIP.Equal(net.IP(expectedSrc)) {
			return fmt.Errorf(
				"tunnel source address mismatch: expected %s, got %s (client=%s, src=%s, mask=%s)",
				net.IP(expectedSrc),
				tunnelSrcIP,
				clientIP,
				net.IP(realSrc),
				net.IP(realMask),
			)
		}
	} else {
		tunnelSrcIPv4 := tunnelSrcIP.To4()
		if tunnelSrcIPv4 == nil {
			return fmt.Errorf("tunnel source IP should be IPv4 for IPv4 real, got %s", tunnelSrcIP)
		}

		expectedSrc := make([]byte, 4)
		for i := range 4 {
			var clientByte byte
			if i < len(clientIPBytes) {
				clientByte = clientIPBytes[i]
			}
			expectedSrc[i] = (clientByte & ^realMask[i]) | (realSrc[i] & realMask[i])
		}

		if !tunnelSrcIPv4.Equal(net.IP(expectedSrc)) {
			return fmt.Errorf(
				"tunnel source address mismatch: expected %s, got %s (client=%s, src=%s, mask=%s)",
				net.IP(expectedSrc), tunnelSrcIPv4, clientIP, net.IP(realSrc), net.IP(realMask),
			)
		}
	}

	return nil
}
