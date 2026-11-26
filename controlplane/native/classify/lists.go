//go:build linux && (amd64 || arm64)

package classify

import (
	"hash/maphash"
	"slices"
)

// groupRules returns the group of every rule, numbering the distinct item
// lists in the order they first appear, as C does, and the first rule of
// every group; an absent rule has no group.
func groupRules[S comparable](count int, rule func(idx int) ([]S, bool)) ([]uint32, []uint32) {
	seed := maphash.MakeSeed()
	// byHash holds the groups whose item lists hash alike.
	byHash := map[uint64][]uint32{}
	groups := make([]uint32, count)
	var firsts []uint32
	for idx := range count {
		items, absent := rule(idx)
		if absent {
			groups[idx] = InvalidGroup
			continue
		}
		var hash maphash.Hash
		hash.SetSeed(seed)
		for _, item := range items {
			maphash.WriteComparable(&hash, item)
		}
		sum := hash.Sum64()
		group := uint32(len(firsts))
		for _, candidate := range byHash[sum] {
			if first, _ := rule(int(firsts[candidate])); slices.Equal(first, items) {
				group = candidate
				break
			}
		}
		if group == uint32(len(firsts)) {
			firsts = append(firsts, uint32(idx))
			byHash[sum] = append(byHash[sum], group)
		}
		groups[idx] = group
	}
	return groups, firsts
}
