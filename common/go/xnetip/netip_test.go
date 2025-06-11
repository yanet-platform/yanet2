package xnetip

import (
	"net/netip"
	"testing"
)

func TestLastAddr(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		expected string
	}{
		// IPv4 tests
		{
			name:     "IPv4 /0 (entire IPv4 space)",
			prefix:   "0.0.0.0/0",
			expected: "255.255.255.255",
		},
		{
			name:     "IPv4 /8 (Class A)",
			prefix:   "10.0.0.0/8",
			expected: "10.255.255.255",
		},
		{
			name:     "IPv4 /16 (Class B)",
			prefix:   "192.168.0.0/16",
			expected: "192.168.255.255",
		},
		{
			name:     "IPv4 /24 (Class C)",
			prefix:   "192.168.1.0/24",
			expected: "192.168.1.255",
		},
		{
			name:     "IPv4 /25 (subnet)",
			prefix:   "192.168.1.0/25",
			expected: "192.168.1.127",
		},
		{
			name:     "IPv4 /30 (point-to-point)",
			prefix:   "192.168.1.0/30",
			expected: "192.168.1.3",
		},
		{
			name:     "IPv4 /31 (RFC 3021)",
			prefix:   "192.168.1.0/31",
			expected: "192.168.1.1",
		},
		{
			name:     "IPv4 /32 (host)",
			prefix:   "192.168.1.1/32",
			expected: "192.168.1.1",
		},
		{
			name:     "IPv4 /1 (half of IPv4 space)",
			prefix:   "0.0.0.0/1",
			expected: "127.255.255.255",
		},
		{
			name:     "IPv4 /12 (large subnet)",
			prefix:   "172.16.0.0/12",
			expected: "172.31.255.255",
		},
		{
			name:     "IPv4 /28 (small subnet)",
			prefix:   "192.168.1.32/28",
			expected: "192.168.1.47",
		},

		// IPv6 tests
		{
			name:     "IPv6 /0 (entire IPv6 space)",
			prefix:   "::/0",
			expected: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 /8",
			prefix:   "2000::/8",
			expected: "20ff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 /16",
			prefix:   "2001::/16",
			expected: "2001:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 /32 (common allocation)",
			prefix:   "2001:db8::/32",
			expected: "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 /48 (site prefix)",
			prefix:   "2001:db8:1234::/48",
			expected: "2001:db8:1234:ffff:ffff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 /56",
			prefix:   "2001:db8:1234:ab00::/56",
			expected: "2001:db8:1234:abff:ffff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 /64 (standard subnet)",
			prefix:   "2001:db8:1234:5678::/64",
			expected: "2001:db8:1234:5678:ffff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 /96",
			prefix:   "2001:db8:1234:5678:9abc:def0::/96",
			expected: "2001:db8:1234:5678:9abc:def0:ffff:ffff",
		},
		{
			name:     "IPv6 /112",
			prefix:   "2001:db8:1234:5678:9abc:def0:1234::/112",
			expected: "2001:db8:1234:5678:9abc:def0:1234:ffff",
		},
		{
			name:     "IPv6 /128 (host)",
			prefix:   "2001:db8:1234:5678:9abc:def0:1234:5678/128",
			expected: "2001:db8:1234:5678:9abc:def0:1234:5678",
		},

		// Edge cases around 64-bit boundary
		{
			name:     "IPv6 /63 (just before 64-bit boundary)",
			prefix:   "2001:db8:1234:5678::/63",
			expected: "2001:db8:1234:5679:ffff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 /65 (just after 64-bit boundary)",
			prefix:   "2001:db8:1234:5678:8000::/65",
			expected: "2001:db8:1234:5678:ffff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 /63 (just before 64-bit boundary - zeros)",
			prefix:   "2001:db8:1234:0::/63",
			expected: "2001:db8:1234:1:ffff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 0/65 (just after 64-bit boundary - zeros)",
			prefix:   "2001:db8:1234:5678:0::/65",
			expected: "2001:db8:1234:5678:7fff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 /80",
			prefix:   "2001:db8:1234:5678:9abc::/80",
			expected: "2001:db8:1234:5678:9abc:ffff:ffff:ffff",
		},

		// Additional edge cases
		{
			name:     "IPv4 with high bits set",
			prefix:   "255.255.255.0/24",
			expected: "255.255.255.255",
		},
		{
			name:     "IPv6 with high bits set",
			prefix:   "ffff:ffff:ffff:ffff::/64",
			expected: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 /1 (half of IPv6 space)",
			prefix:   "8000::/1",
			expected: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		},
		{
			name:     "IPv6 /127",
			prefix:   "2001:db8::1:0/127",
			expected: "2001:db8::1:1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, err := netip.ParsePrefix(tt.prefix)
			if err != nil {
				t.Fatalf("Failed to parse prefix %s: %v", tt.prefix, err)
			}

			result := LastAddr(prefix)
			expected, err := netip.ParseAddr(tt.expected)
			if err != nil {
				t.Fatalf("Failed to parse expected address %s: %v", tt.expected, err)
			}

			if result != expected {
				t.Errorf("LastAddr(%s) = %s, want %s", tt.prefix, result, expected)
			}
		})
	}
}

// TestLastAddrProperties tests mathematical properties of the LastAddr function
func TestLastAddrProperties(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
	}{
		{"IPv4 /24", "192.168.1.0/24"},
		{"IPv4 /16", "10.0.0.0/16"},
		{"IPv6 /64", "2001:db8::/64"},
		{"IPv6 /48", "2001:db8:1234::/48"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prefix, err := netip.ParsePrefix(tt.prefix)
			if err != nil {
				t.Fatalf("Failed to parse prefix %s: %v", tt.prefix, err)
			}

			lastAddr := LastAddr(prefix)

			// The last address should be within the prefix
			if !prefix.Contains(lastAddr) {
				t.Errorf("LastAddr(%s) = %s is not contained in the prefix", tt.prefix, lastAddr)
			}

			// For non-host prefixes, the last address should be different from the network address
			if prefix.Bits() < 32 && prefix.Addr().Is4() || prefix.Bits() < 128 && prefix.Addr().Is6() {
				if lastAddr == prefix.Addr() {
					t.Errorf("LastAddr(%s) = %s should not equal the network address for non-host prefix", tt.prefix, lastAddr)
				}
			}
		})
	}
}

// TestLastAddrHostPrefixes tests that host prefixes return the same address
func TestLastAddrHostPrefixes(t *testing.T) {
	tests := []string{
		"192.168.1.1/32",
		"10.0.0.1/32",
		"2001:db8::1/128",
		"::1/128",
	}

	for _, prefixStr := range tests {
		t.Run(prefixStr, func(t *testing.T) {
			prefix, err := netip.ParsePrefix(prefixStr)
			if err != nil {
				t.Fatalf("Failed to parse prefix %s: %v", prefixStr, err)
			}

			result := LastAddr(prefix)
			if result != prefix.Addr() {
				t.Errorf("LastAddr(%s) = %s, want %s (should be same for host prefix)",
					prefixStr, result, prefix.Addr())
			}
		})
	}
}

// BenchmarkLastAddr benchmarks the LastAddr function
func BenchmarkLastAddr(b *testing.B) {
	prefixes := []netip.Prefix{
		netip.MustParsePrefix("192.168.1.0/24"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("2001:db8::/32"),
		netip.MustParsePrefix("2001:db8:1234:5678::/64"),
	}

	for b.Loop() {
		for _, p := range prefixes {
			LastAddr(p)
		}
	}
}
