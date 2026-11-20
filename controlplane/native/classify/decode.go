//go:build linux && (amd64 || arm64)

package classify

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// Decode writes the rule map of a stage into a line in shared memory, as
// classify_decode does: every value of the stage's space maps to the
// first member rule whose group reaches it, or InvalidRule. The line
// lives in a child memory context of context named name itself.
func Decode(stage *Stage, members []bool, line *layout.Vline, context *layout.MemoryContext, name string) error {
	size := stage.MaxValue + 1
	if err := allocVline(line, context, name, size); err != nil {
		return fmt.Errorf("allocate the rule map %s: %w", name, err)
	}
	ruleMap := unsafe.Slice(line.Values.Get(), size)
	for idx := range ruleMap {
		ruleMap[idx] = InvalidRule
	}
	for rule, group := range stage.RuleGroups {
		if !members[rule] || group == InvalidGroup {
			continue
		}
		for _, value := range stage.Group(group) {
			if ruleMap[value] == InvalidRule {
				ruleMap[value] = uint32(rule)
			}
		}
	}
	return nil
}

// FreeDecode releases what Decode wrote into a line, as vline_free does;
// it is safe on a zeroed line.
func FreeDecode(line *layout.Vline) {
	freeVline(line)
}
