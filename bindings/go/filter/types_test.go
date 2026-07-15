package filter

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIPNet_MaskIsValid verifies that MaskIsValid accepts contiguous prefix
// masks (bi-contiguous for IPv6) and rejects masks with a hole inside
// either half.
func TestIPNet_MaskIsValid(t *testing.T) {
	tests := []struct {
		name string
		net  IPNet
		want bool
	}{
		{
			name: "ipv4 /24",
			net:  MustParseIPNet("192.0.2.0/24"),
			want: true,
		},
		{
			name: "ipv4 /0",
			net:  MustParseIPNet("0.0.0.0/0"),
			want: true,
		},
		{
			name: "ipv4 /32",
			net:  MustParseIPNet("192.0.2.1/32"),
			want: true,
		},
		{
			name: "ipv4 non-contiguous mask",
			net: IPNet{
				Addr: netip.AddrFrom4([4]byte{192, 0, 2, 0}),
				Mask: netip.AddrFrom4([4]byte{255, 0, 255, 0}),
			},
			want: false,
		},
		{
			name: "ipv6 bi-contiguous with hole at /64 boundary",
			net: IPNet{
				Addr: netip.IPv6Unspecified(),
				Mask: netip.AddrFrom16([16]byte{
					0xff, 0xff, 0xff, 0xff, 0xff, 0x00, 0x00, 0x00,
					0xff, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00, 0x00,
				}),
			},
			want: true,
		},
		{
			name: "ipv6 /0",
			net:  MustParseIPNet("::/0"),
			want: true,
		},
		{
			name: "ipv6 /128",
			net:  MustParseIPNet("2001:db8::1/128"),
			want: true,
		},
		{
			name: "ipv6 hole within hi half",
			net: IPNet{
				Addr: netip.IPv6Unspecified(),
				Mask: netip.AddrFrom16([16]byte{
					0xff, 0x00, 0xff, 0x00, 0x00, 0x00, 0x00, 0x00,
					0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				}),
			},
			want: false,
		},
		{
			name: "ipv6 hole within lo half",
			net: IPNet{
				Addr: netip.IPv6Unspecified(),
				Mask: netip.AddrFrom16([16]byte{
					0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
					0xff, 0x00, 0xff, 0x00, 0x00, 0x00, 0x00, 0x00,
				}),
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.net.MaskIsValid()
			require.Equal(t, tc.want, got)
		})
	}
}
