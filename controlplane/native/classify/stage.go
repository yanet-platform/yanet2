//go:build linux && (amd64 || arm64)

package classify

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

// stageBuilder collects the region values of one group at a time, each
// value once.
type stageBuilder struct {
	Stage *Stage
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
	m.Stage.Values = append(m.Stage.Values, value)
	m.Stage.MaxValue = max(m.Stage.MaxValue, value)
}

// newStage builds the stage of a compile whose regions hold count
// distinct values: forGroup reaches every region value a group matches,
// repeats allowed, in the order the C registry collects them.
func newStage(ruleGroups []uint32, groupCount int, count uint32, forGroup func(group uint32, builder *stageBuilder)) Stage {
	stage := Stage{RuleGroups: ruleGroups, Starts: make([]uint32, groupCount+1)}
	builder := &stageBuilder{Stage: &stage, Seen: make([]uint32, count)}
	for group := range uint32(groupCount) {
		stage.Starts[group] = uint32(len(stage.Values))
		builder.Group = group
		forGroup(group, builder)
	}
	stage.Starts[groupCount] = uint32(len(stage.Values))
	return stage
}
