//go:build linux && (amd64 || arm64)

package classify

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// maxDevices is the count of devices a line of the largest block holds.
const maxDevices = uint64(shmalloc.MaxSize) / 4

// DeviceRule is what one rule matches on a device attribute.
type DeviceRule struct {
	// IDs are the identifiers of the devices the rule matches; a rule
	// without any matches every device.
	IDs []uint32
	// Absent marks a rule outside the projection the ruleset spells.
	Absent bool
}

// Device is a compiled device attribute: the class of every device
// identifier up to the largest a rule names, and the stage a join
// consumes.
type Device struct {
	values []uint32
	stage  Stage
}

// Stage returns the stage a join consumes, a copy, so the compile keeps
// its own.
func (m *Device) Stage() *Stage {
	stage := m.stage
	return &stage
}

// CompileDevice compiles the device attribute of a ruleset to the
// classes and the stage CLASSIFY_DEVICE_COMPILE produces: the domain
// spans the identifiers up to the largest a present rule names, one its
// value line cannot hold refused.
func CompileDevice(rules []DeviceRule) (*Device, error) {
	var maxID uint32
	for idx, rule := range rules {
		if rule.Absent {
			continue
		}
		for _, id := range rule.IDs {
			// The line of the domain fits the largest shared block.
			if uint64(id)+1 > maxDevices {
				return nil, fmt.Errorf("rule %d: device %d: %w", idx, id, ErrInvalidRule)
			}
			maxID = max(maxID, id)
		}
	}
	groups, firsts := groupRules(len(rules), func(idx int) ([]uint32, bool) {
		return rules[idx].IDs, rules[idx].Absent
	})

	values := make([]uint32, maxID+1)
	regions := newRefiner(maxID + 1)
	for _, first := range firsts {
		ids := rules[first].IDs
		if len(ids) == 0 {
			continue
		}
		regions.NewGeneration()
		for _, id := range ids {
			values[id] = regions.Touch(values[id])
		}
	}
	numbers, count := regions.Numbers()
	for id := range values {
		values[id] = numbers[values[id]]
	}

	stage := newStage(groups, len(firsts), count, func(group uint32, builder *stageBuilder) {
		ids := rules[firsts[group]].IDs
		if len(ids) == 0 {
			for _, value := range values {
				builder.Reach(value)
			}
			return
		}
		for _, id := range ids {
			builder.Reach(values[id])
		}
	})
	return &Device{values: values, stage: stage}, nil
}

// Commit writes the attribute into shared memory as C's commit leaves
// it: a value line of every identifier's class in a child memory context
// of context titled for the attribute.
//
// A failure leaves the attribute zeroed.
func (m *Device) Commit(attr *layout.ClassifyAttrDevice, context *layout.MemoryContext, name string) error {
	values, err := initVline(&attr.Line, context, leafName(name, "line"), uint32(len(m.values)))
	if err != nil {
		*attr = layout.ClassifyAttrDevice{}
		return err
	}
	copy(values, m.values)
	return nil
}

// FreeDevice releases what a commit wrote into a device attribute and
// zeroes it, as classify_attr_device_free does; it is safe on a zeroed
// attribute.
func FreeDevice(attr *layout.ClassifyAttrDevice) {
	freeVline(&attr.Line)
	*attr = layout.ClassifyAttrDevice{}
}
