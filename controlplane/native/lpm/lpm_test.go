//go:build linux && (amd64 || arm64)

package lpm_test

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/lpm"
)

// keyRange is an insert of the keys from the start through the end, both
// included, mapped to the value.
type keyRange struct {
	From  []byte
	To    []byte
	Value uint32
}

// prefixRange returns the insert of every address of a prefix.
func prefixRange(prefix string, value uint32) keyRange {
	parsed := netip.MustParsePrefix(prefix)
	from := parsed.Masked().Addr().AsSlice()
	to := parsed.Masked().Addr().AsSlice()
	for bit := parsed.Bits(); bit < len(to)*8; bit++ {
		to[bit/8] |= 0x80 >> (bit % 8)
	}
	return keyRange{From: from, To: to, Value: value}
}

// chunkedRanges returns count /25 ranges over distinct second octets,
// each splitting two pages off the tree, so 40 of them outgrow five page
// chunks and grow the chunk directory three times.
func chunkedRanges(count int) []keyRange {
	ranges := make([]keyRange, 0, count)
	for idx := range count {
		ranges = append(ranges, prefixRange(fmt.Sprintf("10.%d.%d.128/25", idx, idx%7), uint32(idx)))
	}
	return ranges
}

// Test_Insert_RefusesKeySizes verifies that keys of no or differing sizes,
// or longer than C can size, are refused before the tree is touched.
//
// The tree has no pages, so any read of one would fault.
func Test_Insert_RefusesKeySizes(t *testing.T) {
	cases := []struct {
		name string
		from []byte
		to   []byte
	}{
		{name: "empty keys", from: []byte{}, to: []byte{}},
		{name: "keys of differing sizes", from: []byte{10, 0, 0, 0}, to: []byte{10, 0}},
		{name: "keys longer than 255 bytes", from: make([]byte, 256), to: make([]byte, 256)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, lpm.Insert(&layout.Lpm{}, tc.from, tc.to, 1))
		})
	}
}
