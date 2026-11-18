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

	stage := newStage(groups, len(firsts), count, func(group uint32, builder *stageBuilder) {
		rule := rules[firsts[group]].Ranges
		if lineRuleIsAny(rule, size) {
			for _, value := range values {
				builder.Reach(value)
			}
			return
		}
		for _, span := range rule {
			from, to := clampLineRange(span, size)
			for point := from; point <= to && point < size; point++ {
				builder.Reach(values[point])
			}
		}
	})
	return &Line{Values: values, Stage: stage}
}

// groupLineRules returns the group of every rule and the first rule of
// every group.
func groupLineRules(rules []LineRule) ([]uint32, []uint32) {
	return groupRules(len(rules), func(idx int) ([]LineRange, bool) {
		return rules[idx].Ranges, rules[idx].Absent
	})
}

// ruleItem is one item of what a rule matches on an attribute - an
// interval or a network - with the hash the C compiles give it.
type ruleItem interface {
	comparable
	Hash() uint32
}

func (m LineRange) Hash() uint32 {
	return m.From*31 + m.To
}

// groupRules returns the group of every rule, numbering the distinct
// item lists in the order they first appear, and the first rule of
// every group; an absent rule has no group.
//
// The groups are found through an open addressing table of group numbers
// plus one, zero marking a free slot, probed by the hash the C compiles
// use. It grows with the groups, kept at most half full, so a ruleset of
// many rules outside the projection or many equal ones costs a table of
// its groups, not of its rules.
func groupRules[S ruleItem](count int, rule func(idx int) ([]S, bool)) ([]uint32, []uint32) {
	groups := make([]uint32, count)
	var firsts, hashes []uint32
	table := make([]uint32, 16)
	for idx := range count {
		spans, absent := rule(idx)
		if absent {
			groups[idx] = InvalidGroup
			continue
		}
		hash := itemsHash(spans)
		mask := uint32(len(table) - 1)
		slot := hash & mask
		for ; table[slot] != 0; slot = (slot + 1) & mask {
			group := table[slot] - 1
			if hashes[group] != hash {
				continue
			}
			if first, _ := rule(int(firsts[group])); slices.Equal(first, spans) {
				break
			}
		}
		if table[slot] != 0 {
			groups[idx] = table[slot] - 1
			continue
		}
		groups[idx] = uint32(len(firsts))
		firsts = append(firsts, uint32(idx))
		hashes = append(hashes, hash)
		table[slot] = uint32(len(firsts))
		if 2*len(firsts) > len(table) {
			table = groupTable(hashes, 2*len(table))
		}
	}
	return groups, firsts
}

// groupTable places every group, by its hash, in a fresh table of size
// slots.
func groupTable(hashes []uint32, size int) []uint32 {
	table := make([]uint32, size)
	mask := uint32(size - 1)
	for group, hash := range hashes {
		slot := hash & mask
		for table[slot] != 0 {
			slot = (slot + 1) & mask
		}
		table[slot] = uint32(group) + 1
	}
	return table
}

// itemsHash hashes an item list as the C compiles do.
func itemsHash[S ruleItem](items []S) uint32 {
	hash := uint32(len(items))
	for _, item := range items {
		hash = hash*31*31 + item.Hash()
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
	if err := allocVline(line, context, name, uint32(len(values))); err != nil {
		return fmt.Errorf("allocate the line %s: %w", name, err)
	}
	copy(unsafe.Slice(line.Values.Get(), len(values)), values)
	return nil
}
