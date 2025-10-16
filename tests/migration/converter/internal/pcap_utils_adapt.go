package internal

// AdaptAddressesToYanet2 adapts packet addresses to yanet2 test infrastructure
// Only adapts yanet's own addresses and gateway addresses, leaves VIPs/RS unchanged
// isExpect indicates if this is an expected (response) packet where MAC addresses are swapped
func (p *PcapAnalyzer) AdaptAddressesToYanet2(info *PacketInfo, isExpect bool) {
	// MAC addresses: use framework addresses, swapped for expected packets
	if isExpect {
		// For expected/response packets: YANET sends back with swapped MACs
		info.SrcMAC = "52:54:00:6b:ff:a5" // framework.DstMAC (YANET's MAC)
		info.DstMAC = "52:54:00:6b:ff:a1" // framework.SrcMAC (client's MAC)
	} else {
		// For send packets: client sends to YANET
		info.SrcMAC = "52:54:00:6b:ff:a1" // framework.SrcMAC (client's MAC)
		info.DstMAC = "52:54:00:6b:ff:a5" // framework.DstMAC (YANET's MAC)
	}

	// IP addresses: only adapt yanet's own and gateway addresses
	// Same logic for both send and expect packets - just adapt router addresses
	if info.IsIPv4 {
		info.SrcIP = p.adaptIPv4Address(info.SrcIP)
		info.DstIP = p.adaptIPv4Address(info.DstIP)
	} else if info.IsIPv6 {
		info.SrcIP = p.adaptIPv6Address(info.SrcIP)
		info.DstIP = p.adaptIPv6Address(info.DstIP)
	}
}

// adaptIPv4Address adapts IPv4 address to yanet2 infrastructure
// Only replaces yanet1 router address (200.0.0.1) to yanet2 router (203.0.113.1)
// Leaves all other addresses unchanged (VIPs, RS, client IPs, etc)
func (p *PcapAnalyzer) adaptIPv4Address(originalIP string) string {
	// Only adapt yanet1 router address to yanet2 router address
	if originalIP == "200.0.0.1" {
		return "203.0.113.1"
	}

	// Keep all other addresses unchanged
	return originalIP
}

// adaptIPv6Address adapts IPv6 address to yanet2 infrastructure
// For now, keeps all IPv6 addresses unchanged
// Can be extended if specific IPv6 router addresses need adaptation
func (p *PcapAnalyzer) adaptIPv6Address(originalIP string) string {
	// Keep all IPv6 addresses unchanged for now
	// If specific yanet1 IPv6 router addresses are found, add mapping here
	return originalIP
}
