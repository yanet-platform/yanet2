//go:build linux && (amd64 || arm64)

package classify

import "unsafe"

// listKey identifies a list by its storage: the address of its first
// item and its length.
type listKey struct {
	Data   unsafe.Pointer
	Length int
}

// internLists numbers the lists of count rules by their storage: rules
// whose lists share an address and a length share a number, in the order
// the lists first appear, without reading what the lists hold.
//
// It returns every distinct list once and the number of every rule's
// list, none for an absent rule. Rules repeat few lists many times over -
// a production-shaped ruleset holds five times more network references
// than networks in its distinct lists - so a producer that hands one
// list to all its rules gets each one normalized and grouped once. Equal
// lists at different addresses stay apart here; grouping them by their
// content after normalization keeps the result exact either way.
func internLists[T any](count int, listOf func(idx int) ([]T, bool)) ([][]T, []uint32) {
	index := map[listKey]uint32{}
	var lists [][]T
	numbers := make([]uint32, count)
	for idx := range count {
		list, absent := listOf(idx)
		if absent {
			numbers[idx] = none
			continue
		}
		key := listKey{Data: unsafe.Pointer(unsafe.SliceData(list)), Length: len(list)}
		number, ok := index[key]
		if !ok {
			number = uint32(len(lists))
			index[key] = number
			lists = append(lists, list)
		}
		numbers[idx] = number
	}
	return lists, numbers
}

// groupLists groups the lists of the rules by their content and returns
// the group of every rule, InvalidGroup for an absent one, and the first
// list of every group; the groups come in the order of the rules, as
// groupRules numbers them over the rules themselves.
func groupLists[S ruleItem](numbers []uint32, listCount int, items func(list int) []S) ([]uint32, []uint32) {
	listGroups, firsts := groupRules(listCount, func(list int) ([]S, bool) {
		return items(list), false
	})
	groups := make([]uint32, len(numbers))
	for idx, number := range numbers {
		groups[idx] = InvalidGroup
		if number != none {
			groups[idx] = listGroups[number]
		}
	}
	return groups, firsts
}
