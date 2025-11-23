package lib

import (
	"strings"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// addressMappings is a map of yanet1 to yanet2 IP address conversions.
// This is the single source of truth for address mappings.
// Uses framework constants for yanet2 infrastructure addresses.
var addressMappings = map[string]string{
	// IPv4 gateway addresses adaptation
	"200.0.0.1": framework.VMIPv4Gateway, // 203.0.113.1
	"200.0.0.2": framework.VMIPv4Host,     // 203.0.113.14

	// IPv6 gateway addresses adaptation
	"fe80::1": framework.VMIPv6Gateway, // fe80::1 (same)
	"fe80::2": framework.VMIPv6Host,     // fe80::5054:ff:fe6b:ffa5

	// Common test addresses from yanet1 that should be adapted
	// These are frequently used in yanet1 tests
	"aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:1": framework.VMIPv6Gateway,
	"aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:aaaa:2": framework.VMIPv6Host,
}

// AdaptIPAddress adapts a single IP address from yanet1 to yanet2 infrastructure.
// This function is the single source of truth for IP address mapping logic.
// It checks the addressMappings table and returns the mapped address if found,
// otherwise returns the original address unchanged.
//
// Parameters:
//   - ipAddr: IP address string from yanet1 test (may contain whitespace)
//
// Returns:
//   - string: Mapped IP address for yanet2 framework, or original if no mapping exists
func AdaptIPAddress(ipAddr string) string {
	ipAddr = strings.TrimSpace(ipAddr)

	if mapped, ok := addressMappings[ipAddr]; ok {
		return mapped
	}

	return ipAddr
}

