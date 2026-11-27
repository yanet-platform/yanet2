//go:build linux && (amd64 || arm64)

package classify

import (
	"slices"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// PortDomain is the size of the 16-bit domain of a port attribute.
const PortDomain = 1 << 16

// PortRange is one closed interval of the 16-bit domain; one starting
// after it ends covers nothing.
type PortRange struct {
	From uint16
	To   uint16
}

// foldPortRange folds an interval into a list's hash, as the port
// compile's hash does.
func foldPortRange(hash uint32, item PortRange) uint32 {
	return (hash*31+uint32(item.From))*31 + uint32(item.To)
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
// class of every region, and the stage a join consumes.
type Port struct {
	// starts holds the first value of every region, ascending.
	starts []uint32
	values []uint32
	stage  Stage
}

// Stage returns the stage a join consumes, a copy, so the compile keeps
// its own.
func (m *Port) Stage() *Stage {
	stage := m.stage
	return &stage
}

// CompilePort compiles the port attribute of a ruleset to the classes
// and the stage CLASSIFY_U16_RANGES_COMPILE produces.
//
// C cuts every interval into aligned blocks and refines the regions
// those leave; a port's class depends only on the groups covering it, so
// the domain is cut here only where an interval starts or ends.
func CompilePort(rules []PortRule) *Port {
	groups, firsts := groupRules(len(rules), func(idx int) ([]PortRange, bool) {
		return rules[idx].Ranges, rules[idx].Absent
	}, foldPortRange)

	starts := []uint32{0}
	for _, first := range firsts {
		for _, span := range rules[first].Ranges {
			if span.From > span.To {
				continue
			}
			starts = append(starts, uint32(span.From), uint32(span.To)+1)
		}
	}
	slices.Sort(starts)
	starts = slices.Compact(starts)
	// The end of an interval reaching the domain's end starts no region.
	if starts[len(starts)-1] == PortDomain {
		starts = starts[:len(starts)-1]
	}
	// regionsOf returns the regions an interval covers, the last one
	// excluded.
	regionsOf := func(span PortRange) (int, int) {
		first, _ := slices.BinarySearch(starts, uint32(span.From))
		end, _ := slices.BinarySearch(starts, uint32(span.To)+1)
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
			from, end := regionsOf(span)
			for region := from; region < end; region++ {
				builder.Reach(values[region])
			}
		}
	})
	return &Port{starts: starts, values: values, stage: stage}
}

// portRuleIsAny reports whether a rule matches the whole domain, judged
// as C judges it: by its first interval alone.
func portRuleIsAny(ranges []PortRange) bool {
	return len(ranges) == 0 || (ranges[0].From == 0 && ranges[0].To == PortDomain-1)
}

// Commit writes the attribute into shared memory as C's commit leaves
// it: a value line of every port's class in a child memory context of
// context titled for the attribute.
//
// A failure leaves the attribute zeroed.
func (m *Port) Commit(attr *layout.ClassifyAttrPort, context *layout.MemoryContext, name string) error {
	values, err := initVline(&attr.Line, context, leafName(name, "line"), PortDomain)
	if err != nil {
		*attr = layout.ClassifyAttrPort{}
		return err
	}
	for region, start := range m.starts {
		end := uint32(PortDomain)
		if region+1 < len(m.starts) {
			end = m.starts[region+1]
		}
		fill := values[start:end]
		for idx := range fill {
			fill[idx] = m.values[region]
		}
	}
	return nil
}

// FreePort releases what a commit wrote into a port attribute and zeroes
// it, as classify_attr_port_free does; it is safe on a zeroed attribute.
func FreePort(attr *layout.ClassifyAttrPort) {
	freeVline(&attr.Line)
	*attr = layout.ClassifyAttrPort{}
}
