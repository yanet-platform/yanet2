//go:build linux && (amd64 || arm64)

package classify

import (
	"fmt"
	"slices"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// LineDomain is the size of the line attribute domain the module SDK
// compiles: every value of one byte.
const LineDomain = 0x100

// LineRange is one closed interval of a line attribute's domain; the
// compile clamps it to the domain.
type LineRange struct {
	From uint32
	To   uint32
}

// LineRule is what one rule matches on a line attribute.
type LineRule struct {
	// Ranges are the intervals the rule matches; a rule without any, or
	// whose first interval covers the domain, matches all of it.
	Ranges []LineRange
	// Absent marks a rule outside the projection the ruleset spells.
	Absent bool
}

// Line is a compiled line attribute: the region value of every point of
// its domain, and the stage a join consumes.
type Line struct {
	Values []uint32
	Stage  Stage
}

// CompileLine compiles the line attribute of a ruleset over a domain of
// size points, to the values and the stage CLASSIFY_LINE_COMPILE
// produces.
func CompileLine(rules []LineRule, size uint32) *Line {
	groups, firsts := groupLineRules(rules)

	values := make([]uint32, size)
	regions := newRefiner(size)
	for _, first := range firsts {
		rule := rules[first].Ranges
		if lineRuleIsAny(rule, size) {
			continue
		}
		regions.NewGeneration()
		for _, span := range rule {
			from, to := clampLineRange(span, size)
			for point := from; point <= to && point < size; point++ {
				values[point] = regions.Touch(values[point])
			}
		}
	}
	numbers, count := regions.Numbers()
	for point := range values {
		values[point] = numbers[values[point]]
	}

	// The registry is sized exactly: a first pass counts every group's
	// distinct values, a second one stores them.
	seen := make([]uint32, count)
	stage := Stage{RuleGroups: groups, Starts: make([]uint32, len(firsts)+1)}
	total := uint32(0)
	for group, first := range firsts {
		stage.Starts[group] = total
		forLineGroupValues(rules[first].Ranges, values, size, uint32(group), seen, func(uint32) {
			total++
		})
	}
	stage.Starts[len(firsts)] = total
	clear(seen)
	stage.Values = make([]uint32, 0, total)
	for group, first := range firsts {
		forLineGroupValues(rules[first].Ranges, values, size, uint32(group), seen, func(value uint32) {
			stage.Values = append(stage.Values, value)
			stage.MaxValue = max(stage.MaxValue, value)
		})
	}
	return &Line{Values: values, Stage: stage}
}

// forLineGroupValues calls collect with every distinct value a group
// reaches, in the order the C registry collects them: the whole domain
// for a group matching all of it, its intervals otherwise. seen stamps
// the values a group already reached with the group's number plus one.
func forLineGroupValues(rule []LineRange, values []uint32, size uint32, group uint32, seen []uint32, collect func(value uint32)) {
	reach := func(value uint32) {
		if seen[value] != group+1 {
			seen[value] = group + 1
			collect(value)
		}
	}
	if lineRuleIsAny(rule, size) {
		for _, value := range values {
			reach(value)
		}
		return
	}
	for _, span := range rule {
		from, to := clampLineRange(span, size)
		for point := from; point <= to && point < size; point++ {
			reach(values[point])
		}
	}
}

// groupLineRules returns the group of every rule, numbering the distinct
// interval lists in the order they first appear, and the first rule of
// every group. The groups are found through an open addressing table of
// group numbers, probed by the hash C uses.
func groupLineRules(rules []LineRule) ([]uint32, []uint32) {
	groups := make([]uint32, len(rules))
	firsts := make([]uint32, 0, len(rules))
	slots := uint32(1)
	for slots < uint32(len(rules))*2 {
		slots <<= 1
	}
	table := make([]uint32, slots)
	for idx := range table {
		table[idx] = InvalidGroup
	}
	for idx, rule := range rules {
		if rule.Absent {
			groups[idx] = InvalidGroup
			continue
		}
		slot := lineRuleHash(rule.Ranges) & (slots - 1)
		for {
			group := table[slot]
			if group == InvalidGroup {
				group = uint32(len(firsts))
				table[slot] = group
				firsts = append(firsts, uint32(idx))
				groups[idx] = group
				break
			}
			if slices.Equal(rules[firsts[group]].Ranges, rule.Ranges) {
				groups[idx] = group
				break
			}
			slot = (slot + 1) & (slots - 1)
		}
	}
	return groups, firsts
}

// lineRuleHash hashes the intervals of a rule as the C line compile does.
func lineRuleHash(ranges []LineRange) uint32 {
	hash := uint32(len(ranges))
	for _, span := range ranges {
		hash = hash*31 + span.From
		hash = hash*31 + span.To
	}
	return hash
}

// lineRuleIsAny reports whether a rule matches the whole domain, judged
// as C judges it: by its first interval alone.
func lineRuleIsAny(ranges []LineRange, size uint32) bool {
	return len(ranges) == 0 || (ranges[0].From == 0 && ranges[0].To >= size-1)
}

// clampLineRange clamps an interval to the domain.
func clampLineRange(span LineRange, size uint32) (uint32, uint32) {
	return span.From, min(span.To, size-1)
}

// Commit writes the line into a line attribute in shared memory as
// vline_init lays it out: a child memory context of context named for
// the attribute, holding the values.
func (m *Line) Commit(attr *layout.ClassifyAttrLine, context *layout.MemoryContext, name string) error {
	return commitVline(&attr.Line, context, leafName(name, "line"), m.Values)
}

// FreeLine releases what Commit wrote into a line attribute, as
// vline_free does; it is safe on a zeroed attribute.
func FreeLine(attr *layout.ClassifyAttrLine) {
	freeVline(&attr.Line)
}

// freeVline releases a vline's values and its memory context.
func freeVline(line *layout.Vline) {
	child := line.MemoryContext.Get()
	if child == nil {
		return
	}
	if values := line.Values.Get(); values != nil {
		shmalloc.ContextFree(child, unsafe.Pointer(values), uintptr(line.Size)*unsafe.Sizeof(uint32(0)))
		line.Values.Set(nil)
	}
	parent := child.Parent.Get()
	shmalloc.ContextFini(child)
	shmalloc.ContextFree(parent, unsafe.Pointer(child), unsafe.Sizeof(layout.MemoryContext{}))
	line.MemoryContext.Set(nil)
}

// commitVline allocates a vline under context and fills it with values.
func commitVline(line *layout.Vline, context *layout.MemoryContext, name string, values []uint32) error {
	childSize := unsafe.Sizeof(layout.MemoryContext{})
	block := shmalloc.ContextAlloc(context, childSize)
	if block == nil {
		return fmt.Errorf("allocate the memory context of %s: %w", name, errNoMemory)
	}
	child := (*layout.MemoryContext)(block)
	shmalloc.ContextInitFrom(child, context, name)
	size := uintptr(len(values)) * unsafe.Sizeof(uint32(0))
	stored := shmalloc.ContextAlloc(child, size)
	if stored == nil {
		shmalloc.ContextFini(child)
		shmalloc.ContextFree(context, block, childSize)
		return fmt.Errorf("allocate the values of %s: %w", name, errNoMemory)
	}
	copy(unsafe.Slice((*uint32)(stored), len(values)), values)
	line.MemoryContext.Set(child)
	line.Size = uint32(len(values))
	line.Values.Set((*uint32)(stored))
	return nil
}
