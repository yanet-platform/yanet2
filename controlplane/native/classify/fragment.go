//go:build linux && (amd64 || arm64)

package classify

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// Fragment is what a rule matches on the IP fragment attribute, numbered
// as enum filter_ip_fragment.
type Fragment uint8

const (
	// FragmentAny matches fragments and whole packets.
	FragmentAny Fragment = 0
	// FragmentNone matches whole packets only.
	FragmentNone Fragment = 1
	// FragmentFrag matches fragments only.
	FragmentFrag Fragment = 2
)

// fragmentRegions is the size of the fragment attribute's domain: region
// 0 holds whole packets, region 1 fragments.
const fragmentRegions = 2

// fragmentRegionsOf returns the regions a fragment constraint covers.
func fragmentRegionsOf(fragment Fragment) []uint32 {
	switch fragment {
	case FragmentNone:
		return []uint32{0}
	case FragmentFrag:
		return []uint32{1}
	default:
		return []uint32{0, 1}
	}
}

// FragmentRule is what one rule matches on the fragment attribute.
type FragmentRule struct {
	Fragment Fragment
	// Absent marks a rule outside the projection the ruleset spells.
	Absent bool
}

// FragmentAttr is a compiled fragment attribute: the class of both
// regions, and the stage a join consumes.
type FragmentAttr struct {
	values [fragmentRegions]uint32
	stage  Stage
}

// Stage returns the stage a join consumes, a copy, so the compile keeps
// its own.
func (m *FragmentAttr) Stage() *Stage {
	stage := m.stage
	return &stage
}

// CompileFragment compiles the fragment attribute of a ruleset to the
// classes and the stage CLASSIFY_IPFRAG_COMPILE produces: rules group by
// their constraint, numbered by first appearance. A constraint outside
// the enumeration is refused.
func CompileFragment(rules []FragmentRule) (*FragmentAttr, error) {
	groups := make([]uint32, len(rules))
	groupOf := [...]uint32{InvalidGroup, InvalidGroup, InvalidGroup}
	var firsts []Fragment
	for idx, rule := range rules {
		if rule.Absent {
			groups[idx] = InvalidGroup
			continue
		}
		if int(rule.Fragment) >= len(groupOf) {
			return nil, fmt.Errorf("rule %d: fragment constraint %d: %w", idx, rule.Fragment, ErrInvalidRule)
		}
		if groupOf[rule.Fragment] == InvalidGroup {
			groupOf[rule.Fragment] = uint32(len(firsts))
			firsts = append(firsts, rule.Fragment)
		}
		groups[idx] = groupOf[rule.Fragment]
	}

	var values [fragmentRegions]uint32
	regions := newRefiner(fragmentRegions)
	for _, fragment := range firsts {
		if fragment == FragmentAny {
			continue
		}
		regions.NewGeneration()
		for _, region := range fragmentRegionsOf(fragment) {
			values[region] = regions.Touch(values[region])
		}
	}
	numbers, count := regions.Numbers()
	for region := range values {
		values[region] = numbers[values[region]]
	}

	stage := newStage(groups, len(firsts), count, func(group uint32, builder *stageBuilder) {
		for _, region := range fragmentRegionsOf(firsts[group]) {
			builder.Reach(values[region])
		}
	})
	return &FragmentAttr{values: values, stage: stage}, nil
}

// Commit writes the attribute into shared memory as C's commit leaves
// it: a value table of one row of both regions' classes in a child
// memory context of context titled for the attribute.
//
// A failure leaves the attribute zeroed.
func (m *FragmentAttr) Commit(attr *layout.ClassifyAttrIpfrag, context *layout.MemoryContext, name string) error {
	rows, err := initValueTable(&attr.ValueTable, context, leafName(name, "table"), 1, fragmentRegions)
	if err != nil {
		*attr = layout.ClassifyAttrIpfrag{}
		return err
	}
	copy(rows[0], m.values[:])
	return nil
}

// FreeFragment releases what a commit wrote into a fragment attribute
// and zeroes it, as classify_attr_ipfrag_free does; it is safe on a
// zeroed attribute.
func FreeFragment(attr *layout.ClassifyAttrIpfrag) {
	freeValueTable(&attr.ValueTable)
	*attr = layout.ClassifyAttrIpfrag{}
}
