//go:build linux && (amd64 || arm64)

package classify

import "slices"

// groupRules returns the group of every rule, numbering the distinct item
// lists in the order they first appear, as C does, and the first rule of
// every group; an absent rule has no group.
//
// A list hashes as the C compiles hash it: its length, then every item
// folded in by the attribute's own function.
func groupRules[S comparable](count int, rule func(idx int) ([]S, bool), foldItem func(hash uint32, item S) uint32) ([]uint32, []uint32) {
	index := newHashIndex()
	groups := make([]uint32, count)
	var firsts []uint32
	for idx := range count {
		items, absent := rule(idx)
		if absent {
			groups[idx] = InvalidGroup
			continue
		}
		hash := uint32(len(items))
		for _, item := range items {
			hash = foldItem(hash, item)
		}
		group := index.Find(hash, func(group uint32) bool {
			first, _ := rule(int(firsts[group]))
			return slices.Equal(first, items)
		})
		if group == none {
			group = index.Add(hash)
			firsts = append(firsts, uint32(idx))
		}
		groups[idx] = group
	}
	return groups, firsts
}
