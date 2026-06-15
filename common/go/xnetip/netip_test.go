package xnetip

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
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

func TestRangeToCIDR(t *testing.T) {
	tests := []struct {
		name   string
		from   string
		to     string
		prefix string
		ok     bool
	}{
		// Valid IPv4 ranges.
		{
			name:   "IPv4 /32 host",
			from:   "10.0.0.1",
			to:     "10.0.0.1",
			prefix: "10.0.0.1/32",
			ok:     true,
		},
		{
			name:   "IPv4 /24",
			from:   "192.168.1.0",
			to:     "192.168.1.255",
			prefix: "192.168.1.0/24",
			ok:     true,
		},
		{
			name:   "IPv4 /16",
			from:   "172.16.0.0",
			to:     "172.16.255.255",
			prefix: "172.16.0.0/16",
			ok:     true,
		},
		{
			name:   "IPv4 /0",
			from:   "0.0.0.0",
			to:     "255.255.255.255",
			prefix: "0.0.0.0/0",
			ok:     true,
		},

		// Valid IPv6 ranges.
		{
			name:   "IPv6 /128 host",
			from:   "2001:db8::1",
			to:     "2001:db8::1",
			prefix: "2001:db8::1/128",
			ok:     true,
		},
		{
			name:   "IPv6 /64",
			from:   "2001:db8:1234:5678::",
			to:     "2001:db8:1234:5678:ffff:ffff:ffff:ffff",
			prefix: "2001:db8:1234:5678::/64",
			ok:     true,
		},
		{
			name:   "IPv6 /48",
			from:   "2001:db8:abcd::",
			to:     "2001:db8:abcd:ffff:ffff:ffff:ffff:ffff",
			prefix: "2001:db8:abcd::/48",
			ok:     true,
		},
		{
			name:   "IPv6 /0",
			from:   "::",
			to:     "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
			prefix: "::/0",
			ok:     true,
		},

		// Non-octet-aligned prefixes.
		{
			name:   "IPv4 /25",
			from:   "192.168.1.0",
			to:     "192.168.1.127",
			prefix: "192.168.1.0/25",
			ok:     true,
		},
		{
			name:   "IPv4 /21",
			from:   "10.0.8.0",
			to:     "10.0.15.255",
			prefix: "10.0.8.0/21",
			ok:     true,
		},
		{
			name:   "IPv4 /30",
			from:   "192.168.1.4",
			to:     "192.168.1.7",
			prefix: "192.168.1.4/30",
			ok:     true,
		},
		{
			name:   "IPv6 /45",
			from:   "2001:db8:abc0::",
			to:     "2001:db8:abc7:ffff:ffff:ffff:ffff:ffff",
			prefix: "2001:db8:abc0::/45",
			ok:     true,
		},
		{
			name:   "IPv6 /53",
			from:   "2001:db8:abcd:0::",
			to:     "2001:db8:abcd:7ff:ffff:ffff:ffff:ffff",
			prefix: "2001:db8:abcd:0::/53",
			ok:     true,
		},
		{
			name:   "IPv6 /79",
			from:   "2001:db8:1234:5678:ab00::",
			to:     "2001:db8:1234:5678:ab01:ffff:ffff:ffff",
			prefix: "2001:db8:1234:5678:ab00::/79",
			ok:     true,
		},
		{
			name:   "IPv6 /121",
			from:   "2001:db8::1:0",
			to:     "2001:db8::1:7f",
			prefix: "2001:db8::1:0/121",
			ok:     true,
		},

		// Edge cases around the 64-bit boundary.
		{
			name:   "IPv6 /63 crosses 64-bit boundary",
			from:   "2001:db8:1234:5678::",
			to:     "2001:db8:1234:5679:ffff:ffff:ffff:ffff",
			prefix: "2001:db8:1234:5678::/63",
			ok:     true,
		},
		{
			name:   "IPv6 /65 just past 64-bit boundary",
			from:   "2001:db8:1234:5678:8000::",
			to:     "2001:db8:1234:5678:ffff:ffff:ffff:ffff",
			prefix: "2001:db8:1234:5678:8000::/65",
			ok:     true,
		},

		// Invalid ranges.
		{
			name: "IPv4 not aligned to single prefix",
			from: "10.0.0.1",
			to:   "10.0.0.3",
			ok:   false,
		},
		{
			name: "IPv4 from > to",
			from: "10.0.1.0",
			to:   "10.0.0.255",
			ok:   false,
		},
		{
			name: "IPv6 not aligned to single prefix",
			from: "2001:db8::1",
			to:   "2001:db8::3",
			ok:   false,
		},
		{
			name: "IPv4 partial range",
			from: "10.0.0.0",
			to:   "10.0.0.200",
			ok:   false,
		},
		{
			name: "mixed address families",
			from: "10.0.0.0",
			to:   "2001:db8::ffff",
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from := netip.MustParseAddr(tt.from)
			to := netip.MustParseAddr(tt.to)

			prefix, ok := RangeToCIDR(from, to)
			require.Equal(t, tt.ok, ok)
			if !tt.ok {
				return
			}

			expected := netip.MustParsePrefix(tt.prefix)
			assert.Equal(t, expected, prefix)
		})
	}
}

func TestRangeToCIDRs(t *testing.T) {
	tests := []struct {
		name     string
		from     string
		to       string
		expected []string
		ok       bool
	}{
		{
			name:     "single aligned /24",
			from:     "192.168.1.0",
			to:       "192.168.1.255",
			expected: []string{"192.168.1.0/24"},
			ok:       true,
		},
		{
			name:     "host /32",
			from:     "10.0.0.1",
			to:       "10.0.0.1",
			expected: []string{"10.0.0.1/32"},
			ok:       true,
		},
		{
			name:     "full IPv4 space",
			from:     "0.0.0.0",
			to:       "255.255.255.255",
			expected: []string{"0.0.0.0/0"},
			ok:       true,
		},
		{
			name:     "non-aligned range 10.0.0.0..10.0.0.10",
			from:     "10.0.0.0",
			to:       "10.0.0.10",
			expected: []string{"10.0.0.0/29", "10.0.0.8/31", "10.0.0.10/32"},
			ok:       true,
		},
		{
			name:     "real IPv6 dump range",
			from:     "2a02:6b8:c0e:600::",
			to:       "2a02:6b8:c0e:8ff:ffff:ffff:ffff:ffff",
			expected: []string{"2a02:6b8:c0e:600::/55", "2a02:6b8:c0e:800::/56"},
			ok:       true,
		},
		{
			name:     "full IPv6 space",
			from:     "::",
			to:       "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
			expected: []string{"::/0"},
			ok:       true,
		},
		{
			name: "mixed address families",
			from: "10.0.0.0",
			to:   "2001:db8::ffff",
			ok:   false,
		},
		{
			name: "from > to",
			from: "10.0.0.10",
			to:   "10.0.0.0",
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from := netip.MustParseAddr(tt.from)
			to := netip.MustParseAddr(tt.to)

			prefixes, ok := RangeToCIDRs(from, to)
			require.Equal(t, tt.ok, ok)
			if !tt.ok {
				require.Nil(t, prefixes)
				return
			}

			// Each returned prefix must be properly masked.
			for _, p := range prefixes {
				require.Equal(t, p.Masked(), p, "prefix %s is not properly masked", p)
			}

			// Build expected prefixes.
			expected := make([]netip.Prefix, len(tt.expected))
			for idx, s := range tt.expected {
				expected[idx] = netip.MustParsePrefix(s)
			}
			require.Equal(t, expected, prefixes)

			// Verify contiguous coverage: re-merge and confirm [from..to].
			require.Equal(t, from, prefixes[0].Addr(), "first prefix must start at from")
			require.Equal(t, to, LastAddr(prefixes[len(prefixes)-1]), "last prefix must end at to")
			for idx := 1; idx < len(prefixes); idx++ {
				prev := LastAddr(prefixes[idx-1])
				next := prefixes[idx].Addr()
				require.Equal(t, prev.Next(), next, "gap between prefixes[%d] and prefixes[%d]", idx-1, idx)
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

func BenchmarkRangeToCIDR(b *testing.B) {
	ranges := []struct {
		name string
		from netip.Addr
		to   netip.Addr
	}{
		{"IPv4/24", netip.MustParseAddr("192.168.1.0"), netip.MustParseAddr("192.168.1.255")},
		{"IPv4/25", netip.MustParseAddr("192.168.1.0"), netip.MustParseAddr("192.168.1.127")},
		{"IPv4/30", netip.MustParseAddr("192.168.1.4"), netip.MustParseAddr("192.168.1.7")},
		{"IPv4/32", netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.1")},
		{"IPv6/45", netip.MustParseAddr("2001:db8:abc0::"), netip.MustParseAddr("2001:db8:abc7:ffff:ffff:ffff:ffff:ffff")},
		{"IPv6/53", netip.MustParseAddr("2001:db8:abcd::"), netip.MustParseAddr("2001:db8:abcd:7ff:ffff:ffff:ffff:ffff")},
		{"IPv6/64", netip.MustParseAddr("2001:db8:1234:5678::"), netip.MustParseAddr("2001:db8:1234:5678:ffff:ffff:ffff:ffff")},
		{"IPv6/79", netip.MustParseAddr("2001:db8:1234:5678:ab00::"), netip.MustParseAddr("2001:db8:1234:5678:ab01:ffff:ffff:ffff")},
		{"IPv6/121", netip.MustParseAddr("2001:db8::1:0"), netip.MustParseAddr("2001:db8::1:7f")},
		{"IPv6/128", netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("2001:db8::1")},
	}

	for _, r := range ranges {
		b.Run(r.name, func(b *testing.B) {
			for b.Loop() {
				RangeToCIDR(r.from, r.to)
			}
		})
	}
}

// genV4Addr generates pure IPv4 addresses via AddrFrom4.
//
// IPv4-in-IPv6 (Is4In6) addresses are deliberately excluded — the impl
// branches on Is4() and does not special-case the mapped form.
var genV4Addr = rapid.Custom(func(t *rapid.T) netip.Addr {
	raw := rapid.SliceOfN(rapid.Byte(), 4, 4).Draw(t, "v4bytes")
	return netip.AddrFrom4([4]byte(raw))
})

// genV6Addr generates pure IPv6 addresses via AddrFrom16.
//
// IPv4-in-IPv6 (Is4In6) addresses are deliberately excluded — the impl
// branches on Is4() and does not special-case the mapped form.
var genV6Addr = rapid.Custom(func(t *rapid.T) netip.Addr {
	raw := rapid.SliceOfN(rapid.Byte(), 16, 16).Draw(t, "v6bytes")
	return netip.AddrFrom16([16]byte(raw))
})

// genBitsV4 generates IPv4 prefix lengths, concentrating on boundary values.
var genBitsV4 = rapid.OneOf(
	rapid.SampledFrom([]int{0, 1, 31, 32}),
	rapid.IntRange(0, 32),
)

// genBitsV6 generates IPv6 prefix lengths, concentrating on boundary values.
var genBitsV6 = rapid.OneOf(
	rapid.SampledFrom([]int{0, 1, 63, 64, 65, 127, 128}),
	rapid.IntRange(0, 128),
)

// genPrefix draws an unmasked prefix from either address family.
//
// Unmasked prefixes exercise LastAddr's host-bit OR path.
var genPrefix = rapid.Custom(func(t *rapid.T) netip.Prefix {
	if rapid.Bool().Draw(t, "isV4") {
		addr := genV4Addr.Draw(t, "addr")
		b := genBitsV4.Draw(t, "bits")
		return netip.PrefixFrom(addr, b)
	}
	addr := genV6Addr.Draw(t, "addr")
	b := genBitsV6.Draw(t, "bits")
	return netip.PrefixFrom(addr, b)
})

// genRangeSameFamily draws two addresses of the same family, ordered so that
// from <= to (by Compare).
func genRangeSameFamily(t *rapid.T) (from, to netip.Addr) {
	if rapid.Bool().Draw(t, "isV4") {
		a := genV4Addr.Draw(t, "addrA")
		b := genV4Addr.Draw(t, "addrB")
		if a.Compare(b) <= 0 {
			return a, b
		}
		return b, a
	}
	a := genV6Addr.Draw(t, "addrA")
	b := genV6Addr.Draw(t, "addrB")
	if a.Compare(b) <= 0 {
		return a, b
	}
	return b, a
}

// TestRangeToCIDRRoundTripPBT verifies that every masked prefix round-trips
// through RangeToCIDR: RangeToCIDR(p.Addr(), LastAddr(p)) == (p, true).
func TestRangeToCIDRRoundTripPBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		raw := genPrefix.Draw(t, "prefix")
		p := raw.Masked()

		got, ok := RangeToCIDR(p.Addr(), LastAddr(p))
		require.True(t, ok, "RangeToCIDR(%s, %s) returned false", p.Addr(), LastAddr(p))
		require.Equal(t, p, got)
	})
}

// TestRangeToCIDRsStructuralPBT checks structural invariants of RangeToCIDRs
// output: every prefix is masked, has the correct family, and adjacent
// prefixes are contiguous, with the first starting at from and the last
// ending at to.
func TestRangeToCIDRsStructuralPBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		from, to := genRangeSameFamily(t)
		prefixes, ok := RangeToCIDRs(from, to)
		require.True(t, ok)
		require.NotEmpty(t, prefixes)

		for idx, p := range prefixes {
			assert.Equal(t, p.Masked(), p, "prefix[%d] %s is not masked", idx, p)
			assert.Equal(t, from.Is4(), p.Addr().Is4(), "prefix[%d] family mismatch", idx)
		}

		assert.Equal(t, from, prefixes[0].Addr(), "first prefix must start at from")
		assert.Equal(t, to, LastAddr(prefixes[len(prefixes)-1]), "last prefix must end at to")

		for idx := 1; idx < len(prefixes); idx++ {
			prev := LastAddr(prefixes[idx-1])
			next := prefixes[idx].Addr()
			assert.Equal(t, prev.Next(), next,
				"gap between prefixes[%d] and prefixes[%d]", idx-1, idx)
		}
	})
}
