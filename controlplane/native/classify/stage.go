//go:build linux && (amd64 || arm64)

package classify

import "slices"

// Stage is a compiled classification stage: the class space of an
// attribute as one range of region values per rule group, and the group
// of every rule.
//
// Only this package builds stages, so a stage always holds what its
// readers index: its accessors hand out copies.
type Stage struct {
	// ruleGroups holds the group of every rule, or InvalidGroup for an
	// absent one.
	ruleGroups []uint32
	// values holds the region values of every group, group after group,
	// each value once per group, in the order the group reached it.
	values []uint32
	// starts holds where every group's values begin, and one more entry
	// where the last group's end.
	starts []uint32
	// maxValue is the largest region value of any group.
	maxValue uint32
}

// RuleGroups returns the group of every rule, or InvalidGroup for an
// absent one.
func (m *Stage) RuleGroups() []uint32 {
	return slices.Clone(m.ruleGroups)
}

// Group returns the region values of a group, in the order the group
// reached them.
func (m *Stage) Group(group uint32) []uint32 {
	return slices.Clone(m.group(group))
}

// GroupCount returns the count of groups; a zero stage has none.
func (m *Stage) GroupCount() uint32 {
	if len(m.starts) == 0 {
		return 0
	}
	return uint32(len(m.starts) - 1)
}

// MaxValue returns the largest region value of any group.
func (m *Stage) MaxValue() uint32 {
	return m.maxValue
}

// group returns the region values of a group in place.
func (m *Stage) group(group uint32) []uint32 {
	return m.values[m.starts[group]:m.starts[group+1]]
}

// stageBuilder collects the region values of one group at a time, each
// value once.
type stageBuilder struct {
	// Values holds the values collected so far, group after group, and
	// MaxValue the largest of them.
	Values   []uint32
	MaxValue uint32
	// Seen stamps the values the current group reached with its number
	// plus one.
	Seen  []uint32
	Group uint32
}

// Reach records that the current group matches a region value.
func (m *stageBuilder) Reach(value uint32) {
	if m.Seen[value] == m.Group+1 {
		return
	}
	m.Seen[value] = m.Group + 1
	m.Values = append(m.Values, value)
	m.MaxValue = max(m.MaxValue, value)
}

// newStage builds the stage of a compile whose regions hold count
// distinct values: forGroup reaches every region value a group matches,
// repeats allowed, in the order the C registry collects them.
func newStage(ruleGroups []uint32, groupCount int, count uint32, forGroup func(group uint32, builder *stageBuilder)) Stage {
	starts := make([]uint32, groupCount+1)
	builder := &stageBuilder{Seen: make([]uint32, count)}
	for group := range uint32(groupCount) {
		starts[group] = uint32(len(builder.Values))
		builder.Group = group
		forGroup(group, builder)
	}
	starts[groupCount] = uint32(len(builder.Values))
	return Stage{ruleGroups: ruleGroups, values: builder.Values, starts: starts, maxValue: builder.MaxValue}
}
