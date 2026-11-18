//go:build linux && (amd64 || arm64)

// Package classify compiles the attributes of a ruleset into the
// classifier tables the dataplane reads, as lib/classify does, without
// cgo.
//
// Only the tables a consumer embeds in its configuration go to shared
// memory. Everything a compile works through on the way - the groups of
// equal rules, the region refinement, the stage a join consumes - lives
// in flat Go slices, so a compile leaves shared memory nothing to clean
// up and allocates a handful of slices rather than one block per piece.
package classify

// InvalidGroup marks a rule outside the projection a ruleset spells
// (FILTER_GROUP_INVALID).
const InvalidGroup = ^uint32(0)

// Stage is a compiled classification stage: the class space of an
// attribute as one range of region values per rule group, and the group
// of every rule.
type Stage struct {
	// RuleGroups holds the group of every rule, or InvalidGroup for an
	// absent one.
	RuleGroups []uint32
	// Values holds the region values of every group, group after group,
	// each value once per group, in the order the group reached it.
	Values []uint32
	// Starts holds where every group's values begin in Values, and one
	// more entry where the last group's end.
	Starts []uint32
	// MaxValue is the largest region value of any group.
	MaxValue uint32
}

// Group returns the region values of a group.
func (m *Stage) Group(group uint32) []uint32 {
	return m.Values[m.Starts[group]:m.Starts[group+1]]
}

// GroupCount returns the count of groups.
func (m *Stage) GroupCount() uint32 {
	return uint32(len(m.Starts) - 1)
}
