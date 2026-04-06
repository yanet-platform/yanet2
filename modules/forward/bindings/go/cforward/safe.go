package cforward

//#include "modules/forward/api/controlplane.h"
import "C"

import (
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
)

// ForwardMode defines the forwarding direction.
type ForwardMode int

const (
	ModeNone ForwardMode = 0
	ModeIn   ForwardMode = 1
	ModeOut  ForwardMode = 2
)

func (m ForwardMode) toC() C.uint8_t {
	switch m {
	case ModeIn:
		return C.FORWARD_MODE_IN
	case ModeOut:
		return C.FORWARD_MODE_OUT
	default:
		return C.FORWARD_MODE_NONE
	}
}

// ForwardRule describes a single forwarding rule in Go types.
type ForwardRule struct {
	Target     string
	Mode       ForwardMode
	Counter    string
	Devices    filter.Devices
	VlanRanges filter.VlanRanges
	Src4s      filter.IPNets
	Dst4s      filter.IPNets
	Src6s      filter.IPNets
	Dst6s      filter.IPNets
}

// Update compiles the given rules into C structures and pushes them into
// shared memory.
func (m *ModuleConfig) Update(rules []ForwardRule) error {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cRules := make([]C.struct_forward_rule, len(rules))
	for idx := range rules {
		cRules[idx] = rules[idx].cBuild(pinner)
	}

	var cRulesPtr *C.struct_forward_rule
	if len(cRules) > 0 {
		cRulesPtr = &cRules[0]
	}

	return m.update(cRulesPtr, C.uint32_t(len(cRules)))
}

func (m *ForwardRule) cBuild(pinner *runtime.Pinner) C.struct_forward_rule {
	cRule := C.struct_forward_rule{}

	target := unsafe.Slice((*byte)(unsafe.Pointer(&cRule.target[0])), C.CP_DEVICE_NAME_LEN)
	copy(target, m.Target)

	counter := unsafe.Slice((*byte)(unsafe.Pointer(&cRule.counter[0])), C.COUNTER_NAME_LEN)
	copy(counter, m.Counter)

	cRule.mode = m.Mode.toC()

	filter.CBuildDevices(&cRule.devices, m.Devices, pinner)
	filter.CBuildVlanRanges(&cRule.vlan_ranges, m.VlanRanges, pinner)
	filter.CBuildNet4s(&cRule.src_net4s, m.Src4s, pinner)
	filter.CBuildNet4s(&cRule.dst_net4s, m.Dst4s, pinner)
	filter.CBuildNet6s(&cRule.src_net6s, m.Src6s, pinner)
	filter.CBuildNet6s(&cRule.dst_net6s, m.Dst6s, pinner)

	return cRule
}
