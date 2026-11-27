//go:build linux && (amd64 || arm64)

package classify

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// LineRange is one closed interval of a line attribute's domain; the
// compile clamps it to the domain.
type LineRange struct {
	From uint32
	To   uint32
}

// foldLineRange folds an interval into a list's hash, as the line
// compile's hash does.
func foldLineRange(hash uint32, item LineRange) uint32 {
	return (hash*31+item.From)*31 + item.To
}

// LineRule is what one rule matches on a line attribute.
type LineRule struct {
	// Ranges are the intervals the rule matches; a rule without any, or
	// whose first interval covers the domain, matches all of it.
	Ranges []LineRange
	// Absent marks a rule outside the projection the ruleset spells.
	Absent bool
}

// Line is a compiled line attribute: the class of every point of its
// domain, and the stage a join consumes.
type Line struct {
	values []uint32
	stage  Stage
}

// Stage returns the stage a join consumes, a copy, so the compile keeps
// its own.
func (m *Line) Stage() *Stage {
	stage := m.stage
	return &stage
}

// CompileLine compiles the line attribute of a ruleset over a domain of
// size points, to the classes and the stage CLASSIFY_LINE_COMPILE
// produces. An empty domain is refused.
func CompileLine(rules []LineRule, size uint32) (*Line, error) {
	if size == 0 {
		return nil, fmt.Errorf("line domain of no points: %w", ErrInvalidRule)
	}
	groups, firsts := groupRules(len(rules), func(idx int) ([]LineRange, bool) {
		return rules[idx].Ranges, rules[idx].Absent
	}, foldLineRange)

	values := make([]uint32, size)
	regions := newRefiner(size)
	for _, first := range firsts {
		rule := rules[first].Ranges
		if lineRuleIsAny(rule, size) {
			continue
		}
		regions.NewGeneration()
		for _, span := range rule {
			for point := span.From; point <= min(span.To, size-1); point++ {
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
			for point := span.From; point <= min(span.To, size-1); point++ {
				builder.Reach(values[point])
			}
		}
	})
	return &Line{values: values, stage: stage}, nil
}

// lineRuleIsAny reports whether a rule matches the whole domain, judged
// as C judges it: by its first interval alone.
func lineRuleIsAny(ranges []LineRange, size uint32) bool {
	return len(ranges) == 0 || (ranges[0].From == 0 && ranges[0].To >= size-1)
}

// Commit writes the line into a line attribute in shared memory as C's
// commit leaves it: a value line in a child memory context of context
// titled for the attribute.
//
// A failure leaves the attribute zeroed.
func (m *Line) Commit(attr *layout.ClassifyAttrLine, context *layout.MemoryContext, name string) error {
	values, err := initVline(&attr.Line, context, leafName(name, "line"), uint32(len(m.values)))
	if err != nil {
		*attr = layout.ClassifyAttrLine{}
		return err
	}
	copy(values, m.values)
	return nil
}

// FreeLine releases what a commit wrote into a line attribute and zeroes
// it, as classify_attr_line_free does; it is safe on a zeroed attribute.
func FreeLine(attr *layout.ClassifyAttrLine) {
	freeVline(&attr.Line)
	*attr = layout.ClassifyAttrLine{}
}
