//go:build linux && (amd64 || arm64)

package classify

import (
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// DeviceRule is what one rule matches on a device attribute.
type DeviceRule struct {
	// IDs are the identifiers of the devices the rule matches; a rule
	// without any matches every device.
	IDs []uint32
	// Absent marks a rule outside the projection the ruleset spells.
	Absent bool
}

// deviceID is one device a rule matches, hashed as the C compile does.
type deviceID uint32

func (m deviceID) Hash() uint32 {
	return uint32(m)
}

// Device is a compiled device attribute: the region value of every
// device identifier up to the largest a rule names, and the stage a join
// consumes.
type Device struct {
	Values []uint32
	Stage  Stage
}

// CompileDevice compiles the device attribute of a ruleset to the values
// and the stage CLASSIFY_DEVICE_COMPILE produces: the domain spans the
// identifiers up to the largest a present rule names.
func CompileDevice(rules []DeviceRule) *Device {
	ids := make([][]deviceID, len(rules))
	var maxID uint32
	for idx, rule := range rules {
		if rule.Absent {
			continue
		}
		items := make([]deviceID, len(rule.IDs))
		for item, id := range rule.IDs {
			items[item] = deviceID(id)
			maxID = max(maxID, id)
		}
		ids[idx] = items
	}
	groups, firsts := groupRules(len(rules), func(idx int) ([]deviceID, bool) {
		return ids[idx], rules[idx].Absent
	})

	size := maxID + 1
	values := make([]uint32, size)
	regions := newRefiner(size)
	for _, first := range firsts {
		rule := ids[first]
		if len(rule) == 0 {
			continue
		}
		regions.NewGeneration()
		for _, id := range rule {
			values[id] = regions.Touch(values[id])
		}
	}
	numbers, count := regions.Numbers()
	for id := range values {
		values[id] = numbers[values[id]]
	}

	stage := newStage(groups, len(firsts), count, func(group uint32, builder *stageBuilder) {
		rule := ids[firsts[group]]
		if len(rule) == 0 {
			for _, value := range values {
				builder.Reach(value)
			}
			return
		}
		for _, id := range rule {
			builder.Reach(values[id])
		}
	})
	return &Device{Values: values, Stage: stage}
}

// Commit writes the device attribute into shared memory as the C compile
// commits it: a line of every identifier's region value in a child
// memory context of context named for the attribute.
func (m *Device) Commit(attr *layout.ClassifyAttrDevice, context *layout.MemoryContext, name string) error {
	return commitVline(&attr.Line, context, leafName(name, "line"), m.Values)
}

// FreeDevice releases what Commit wrote into a device attribute, as
// classify_attr_device_free does; it is safe on a zeroed attribute.
func FreeDevice(attr *layout.ClassifyAttrDevice) {
	freeVline(&attr.Line)
	*attr = layout.ClassifyAttrDevice{}
}
