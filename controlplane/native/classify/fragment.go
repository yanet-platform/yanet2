//go:build linux && (amd64 || arm64)

package classify

import (
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
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

// FragmentRule is what one rule matches on the fragment attribute.
type FragmentRule struct {
	Fragment Fragment
	// Absent marks a rule outside the projection the ruleset spells.
	Absent bool
}

// FragmentAttr is a compiled fragment attribute: the value of both
// regions, and the stage a join consumes.
type FragmentAttr struct {
	Values [fragmentRegions]uint32
	Stage  Stage
}

// Regions returns the regions a fragment constraint covers.
func (m Fragment) Regions() []uint32 {
	switch m {
	case FragmentNone:
		return []uint32{0}
	case FragmentFrag:
		return []uint32{1}
	default:
		return []uint32{0, 1}
	}
}

// CompileFragment compiles the fragment attribute of a ruleset to the
// values and the stage CLASSIFY_IPFRAG_COMPILE produces: rules group by
// their constraint, numbered by first appearance.
func CompileFragment(rules []FragmentRule) *FragmentAttr {
	groups := make([]uint32, len(rules))
	groupOf := [3]uint32{InvalidGroup, InvalidGroup, InvalidGroup}
	var firsts []Fragment
	for idx, rule := range rules {
		if rule.Absent {
			groups[idx] = InvalidGroup
			continue
		}
		if groupOf[rule.Fragment] == InvalidGroup {
			groupOf[rule.Fragment] = uint32(len(firsts))
			firsts = append(firsts, rule.Fragment)
		}
		groups[idx] = groupOf[rule.Fragment]
	}

	attr := &FragmentAttr{}
	regions := newRefiner(fragmentRegions)
	for _, fragment := range firsts {
		if fragment == FragmentAny {
			continue
		}
		regions.NewGeneration()
		for _, region := range fragment.Regions() {
			attr.Values[region] = regions.Touch(attr.Values[region])
		}
	}
	numbers, count := regions.Numbers()
	for region := range attr.Values {
		attr.Values[region] = numbers[attr.Values[region]]
	}

	attr.Stage = newStage(groups, len(firsts), count, func(group uint32, builder *stageBuilder) {
		for _, region := range firsts[group].Regions() {
			builder.Reach(attr.Values[region])
		}
	})
	return attr
}

// Commit writes the fragment attribute into shared memory as the C
// compile commits it: a table of one row of both regions' values in a
// child memory context of context named for the attribute.
func (m *FragmentAttr) Commit(attr *layout.ClassifyAttrIpfrag, context *layout.MemoryContext, name string) error {
	table := &attr.ValueTable
	if err := allocTable(table, context, leafName(name, "table"), 1, fragmentRegions); err != nil {
		return err
	}
	rowSize := uintptr(fragmentRegions) * unsafe.Sizeof(uint32(0))
	row := shmalloc.ContextAlloc(table.MemoryContext.Get(), rowSize)
	if row == nil {
		FreeJoin(table)
		return errNoMemory
	}
	copy(unsafe.Slice((*uint32)(row), fragmentRegions), m.Values[:])
	unsafe.Slice(table.Values.Get(), 1)[0].Set((*uint32)(row))
	return nil
}

// FreeFragment releases what Commit wrote into a fragment attribute, as
// classify_attr_ipfrag_free does; it is safe on a zeroed attribute.
func FreeFragment(attr *layout.ClassifyAttrIpfrag) {
	FreeJoin(&attr.ValueTable)
	*attr = layout.ClassifyAttrIpfrag{}
}
