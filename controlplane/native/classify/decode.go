//go:build linux && (amd64 || arm64)

package classify

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// Decode writes the rule map of the stage into a value line in shared
// memory, as classify_decode does.
//
// Every value of the stage maps to the first member rule whose group
// reaches it, or to InvalidRule. The line takes a child memory context
// of context named name; a failure leaves it zeroed.
func (m *Stage) Decode(members []bool, line *layout.Vline, context *layout.MemoryContext, name string) error {
	if len(members) != len(m.ruleGroups) {
		return fmt.Errorf("%d members of %d rules: %w", len(members), len(m.ruleGroups), ErrInvalidRule)
	}
	ruleMap, err := initVline(line, context, name, m.maxValue+1)
	if err != nil {
		*line = layout.Vline{}
		return err
	}
	for idx := range ruleMap {
		ruleMap[idx] = InvalidRule
	}
	// A later member of a group finds every value of it taken.
	decoded := make([]bool, m.GroupCount())
	for rule, group := range m.ruleGroups {
		if !members[rule] || group == InvalidGroup || decoded[group] {
			continue
		}
		decoded[group] = true
		for _, value := range m.group(group) {
			if ruleMap[value] == InvalidRule {
				ruleMap[value] = uint32(rule)
			}
		}
	}
	return nil
}

// FreeDecode releases the rule map a decode wrote and zeroes it, as
// vline_free does; it is safe on a zeroed line.
func FreeDecode(line *layout.Vline) {
	freeVline(line)
	*line = layout.Vline{}
}
