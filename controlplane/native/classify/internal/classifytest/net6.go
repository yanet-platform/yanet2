//go:build linux && (amd64 || arm64)

package classifytest

import (
	"encoding/binary"
	"slices"

	"github.com/yanet-platform/yanet2/controlplane/native/classify"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// Net6Key is an IPv6 address by its halves.
type Net6Key struct {
	Hi uint64
	Lo uint64
}

// Net6Matches reports whether a rule matches an IPv6 address.
func Net6Matches(rule classify.Net6Rule, key Net6Key) bool {
	if rule.Absent {
		return false
	}
	return len(rule.Networks) == 0 || slices.ContainsFunc(rule.Networks, func(network classify.Net6Network) bool {
		hi, hiMask := halves(network, 0)
		lo, loMask := halves(network, 8)
		return key.Hi&hiMask == hi&hiMask && key.Lo&loMask == lo&loMask
	})
}

// halves returns the half of a network's address and of its mask at an
// offset.
func halves(network classify.Net6Network, offset int) (uint64, uint64) {
	return binary.BigEndian.Uint64(network.Addr[offset:]), binary.BigEndian.Uint64(network.Mask[offset:])
}

// Net6Keys returns every cross of a high and a low half key, the keys of
// each half being both ends of every elementary interval the networks of
// the rules cut it into.
func Net6Keys(rules []classify.Net6Rule) []Net6Key {
	hiKeys, loKeys := Net6HalfKeys(rules)
	keys := make([]Net6Key, 0, len(hiKeys)*len(loKeys))
	for _, hi := range hiKeys {
		for _, lo := range loKeys {
			keys = append(keys, Net6Key{Hi: hi, Lo: lo})
		}
	}
	return keys
}

// Net6HalfKeys returns the keys of each half, ascending: both ends of
// every elementary interval the networks of the rules cut it into.
func Net6HalfKeys(rules []classify.Net6Rule) ([]uint64, []uint64) {
	hiKeys := []uint64{0, ^uint64(0)}
	loKeys := []uint64{0, ^uint64(0)}
	for _, rule := range rules {
		for _, network := range rule.Networks {
			hiKeys = appendEnds(hiKeys, network, 0)
			loKeys = appendEnds(loKeys, network, 8)
		}
	}
	slices.Sort(hiKeys)
	slices.Sort(loKeys)
	return slices.Compact(hiKeys), slices.Compact(loKeys)
}

// appendEnds appends the first and last value of a network's half at an
// offset and the values on both sides of them.
func appendEnds(keys []uint64, network classify.Net6Network, offset int) []uint64 {
	addr, mask := halves(network, offset)
	from := addr & mask
	to := from | ^mask
	return append(keys, from, from-1, to, to+1)
}

// Net6Classes is a committed IPv6 network attribute read through walks
// of the prefix trees of its halves and its table.
type Net6Classes struct {
	hi    Stretches[uint64]
	lo    Stretches[uint64]
	table [][]uint32
}

// ReadNet6 reads a committed IPv6 network attribute.
func ReadNet6(attr *layout.ClassifyAttrNet6) Net6Classes {
	return Net6Classes{hi: readTree[uint64](&attr.Hi), lo: readTree[uint64](&attr.Lo), table: Table(&attr.Comb)}
}

// Class returns the class of an address, as classify_net6_lookup finds
// it: the high half's class, or the cell of the high half's row at the
// low half's column.
func (m Net6Classes) Class(key Net6Key) uint32 {
	value := m.hi.Class(key.Hi)
	if value&classify.Net6RowMark == 0 {
		return value
	}
	return m.table[value&^classify.Net6RowMark][m.lo.Class(key.Lo)]
}
