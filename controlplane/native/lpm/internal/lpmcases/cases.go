// Package lpmcases holds the prefix trees the Go tests and the C test of
// common/lpm.h build, as the inserts that build them.
package lpmcases

import (
	"fmt"
	"net/netip"
)

// Range is an insert of the keys from the start through the end, both
// included, mapped to the value.
type Range struct {
	From  []byte
	To    []byte
	Value uint32
}

// prefixRange returns the insert of every address of a prefix.
func prefixRange(prefix string, value uint32) Range {
	parsed := netip.MustParsePrefix(prefix)
	from := parsed.Masked().Addr().AsSlice()
	to := parsed.Masked().Addr().AsSlice()
	for bit := parsed.Bits(); bit < len(to)*8; bit++ {
		to[bit/8] |= 0x80 >> (bit % 8)
	}
	return Range{From: from, To: to, Value: value}
}

// ChunkedRanges returns count /25 ranges over distinct second octets,
// each splitting two pages off the tree, so 40 of them outgrow five page
// chunks and grow the chunk directory three times.
func ChunkedRanges(count int) []Range {
	ranges := make([]Range, 0, count)
	for idx := range count {
		ranges = append(ranges, prefixRange(fmt.Sprintf("10.%d.%d.128/25", idx, idx%7), uint32(idx)))
	}
	return ranges
}

// Tree is a tree built from its ranges in order.
type Tree struct {
	Name   string
	Ranges []Range
}

// KeySize returns the size of the tree's keys in bytes; a tree without
// ranges has 4-byte keys.
func (m Tree) KeySize() int {
	if len(m.Ranges) == 0 {
		return 4
	}
	return len(m.Ranges[0].From)
}

// Trees are the trees both the Go tests and the C test check.
var Trees = []Tree{
	{Name: "no ranges"},
	{Name: "one ipv4 range", Ranges: []Range{prefixRange("10.0.0.0/8", 1)}},
	{Name: "value with its top bit set", Ranges: []Range{prefixRange("10.0.0.0/8", 0x80000001)}},
	{
		Name: "overlapping ipv4 ranges, the later winning",
		Ranges: []Range{
			prefixRange("10.0.0.0/8", 1),
			prefixRange("10.1.2.128/25", 2),
			prefixRange("0.0.0.0/0", 3),
			prefixRange("10.1.0.0/16", 4),
		},
	},
	{
		Name:   "range off prefix boundaries",
		Ranges: []Range{{From: []byte{10, 0, 0, 5}, To: []byte{10, 0, 3, 250}, Value: 7}},
	},
	{
		Name:   "start past the end",
		Ranges: []Range{{From: []byte{10, 0, 0, 9}, To: []byte{10, 0, 0, 1}, Value: 7}},
	},
	{
		Name: "ipv6 ranges",
		Ranges: []Range{
			prefixRange("2001:db8::/32", 1),
			prefixRange("2001:db8:1::/48", 2),
			prefixRange("::1/128", 3),
			prefixRange("fe80::/10", 4),
		},
	},
	{Name: "chunk directory grown", Ranges: ChunkedRanges(40)},
}
