//go:build linux && (amd64 || arm64)

package classify

import (
	"slices"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// PortDomain is the size of the 16-bit domain of a port attribute.
const PortDomain = 1 << 16

// PortRange is one closed interval of the 16-bit domain; one whose From
// is above its To covers nothing.
type PortRange struct {
	From uint16
	To   uint16
}

func (m PortRange) Bounds() (uint32, uint32) {
	return uint32(m.From), uint32(m.To)
}

// PortRule is what one rule matches on a port attribute.
type PortRule struct {
	// Ranges are the intervals the rule matches; a rule without any, or
	// whose first interval is the whole domain, matches all of it.
	Ranges []PortRange
	// Absent marks a rule outside the projection the ruleset spells.
	Absent bool
}

// Port is a compiled port attribute: the domain cut into regions, the
// region value of every region, and the stage a join consumes.
type Port struct {
	// Starts holds the first value of every region, ascending.
	Starts []uint32
	// Values holds the region value of every region.
	Values []uint32
	Stage  Stage
}

// CompilePorts compiles the port attribute of a ruleset to the values
// and the stage CLASSIFY_U16_RANGES_COMPILE produces.
//
// C cuts every interval into aligned blocks for a radix collector and
// enumerates the regions those leave; the values a port ends up with
// depend only on which groups cover it, so the domain is cut here only
// where an interval starts or ends, a region per stretch of equal
// coverage.
func CompilePorts(rules []PortRule) *Port {
	groups, firsts := groupRules(len(rules), func(idx int) ([]PortRange, bool) {
		return rules[idx].Ranges, rules[idx].Absent
	})

	starts := []uint32{0}
	for _, first := range firsts {
		for _, span := range rules[first].Ranges {
			if span.From > span.To {
				continue
			}
			starts = append(starts, uint32(span.From))
			if span.To < PortDomain-1 {
				starts = append(starts, uint32(span.To)+1)
			}
		}
	}
	slices.Sort(starts)
	starts = slices.Compact(starts)
	// regionsOf returns the regions an interval covers.
	regionsOf := func(span PortRange) (int, int) {
		first, _ := slices.BinarySearch(starts, uint32(span.From))
		end := len(starts)
		if span.To < PortDomain-1 {
			end, _ = slices.BinarySearch(starts, uint32(span.To)+1)
		}
		return first, end
	}

	values := make([]uint32, len(starts))
	regions := newRefiner(uint32(len(starts)))
	for _, first := range firsts {
		rule := rules[first].Ranges
		if portRuleIsAny(rule) {
			continue
		}
		regions.NewGeneration()
		for _, span := range rule {
			if span.From > span.To {
				continue
			}
			from, end := regionsOf(span)
			for region := from; region < end; region++ {
				values[region] = regions.Touch(values[region])
			}
		}
	}
	numbers, count := regions.Numbers()
	for region := range values {
		values[region] = numbers[values[region]]
	}

	stage := newStage(groups, len(firsts), count, func(group uint32, builder *stageBuilder) {
		rule := rules[firsts[group]].Ranges
		if portRuleIsAny(rule) {
			for _, value := range values {
				builder.Reach(value)
			}
			return
		}
		for _, span := range rule {
			if span.From > span.To {
				continue
			}
			from, end := regionsOf(span)
			for region := from; region < end; region++ {
				builder.Reach(values[region])
			}
		}
	})
	return &Port{Starts: starts, Values: values, Stage: stage}
}

// portRuleIsAny reports whether a rule matches the whole domain, judged
// as C judges it: by its first interval alone.
func portRuleIsAny(ranges []PortRange) bool {
	return len(ranges) == 0 || (ranges[0].From == 0 && ranges[0].To == PortDomain-1)
}

// Value returns the region value of a port.
func (m *Port) Value(port uint16) uint32 {
	region, found := slices.BinarySearch(m.Starts, uint32(port))
	if !found {
		region--
	}
	return m.Values[region]
}

// Commit writes the port attribute into shared memory as the C compile
// commits it: a line of every port's region value in a child memory
// context of context named for the attribute.
func (m *Port) Commit(attr *layout.ClassifyAttrPort, context *layout.MemoryContext, name string) error {
	line := &attr.Line
	if err := allocVline(line, context, leafName(name, "line"), PortDomain); err != nil {
		return err
	}
	values := unsafe.Slice(line.Values.Get(), PortDomain)
	for region, start := range m.Starts {
		end := uint32(PortDomain)
		if region+1 < len(m.Starts) {
			end = m.Starts[region+1]
		}
		fill := values[start:end]
		for idx := range fill {
			fill[idx] = m.Values[region]
		}
	}
	return nil
}

// FreePort releases what Commit wrote into a port attribute; it is safe
// on a zeroed attribute.
func FreePort(attr *layout.ClassifyAttrPort) {
	freeVline(&attr.Line)
}

// allocVline allocates a vline of size values under context.
func allocVline(line *layout.Vline, context *layout.MemoryContext, name string, size uint32) error {
	childSize := unsafe.Sizeof(layout.MemoryContext{})
	block := shmalloc.ContextAlloc(context, childSize)
	if block == nil {
		return errNoMemory
	}
	child := (*layout.MemoryContext)(block)
	shmalloc.ContextInitFrom(child, context, name)
	stored := shmalloc.ContextAlloc(child, uintptr(size)*unsafe.Sizeof(uint32(0)))
	if stored == nil {
		shmalloc.ContextFini(child)
		shmalloc.ContextFree(context, block, childSize)
		return errNoMemory
	}
	line.MemoryContext.Set(child)
	line.Size = size
	line.Values.Set((*uint32)(stored))
	return nil
}
