package cforward

//#include "modules/forward/api/controlplane.h"
import "C"

import (
	"github.com/yanet-platform/xnetip"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
)

// CounterNameMaxLen is the longest counter name the shared-memory counter
// registry accepts.
//
// The C buffer backing a counter name (COUNTER_NAME_LEN) reserves one byte
// for a trailing NUL terminator. counter_registry_register in
// lib/counters/counters.c rejects a name whose strnlen reaches the full
// buffer size, so the longest name it accepts is the buffer size minus one.
const CounterNameMaxLen = C.COUNTER_NAME_LEN - 1

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
	// Target is the forwarding target device name.
	Target string
	// Mode selects how matched packets are handled.
	Mode ForwardMode
	// Counter is the counter name for traffic accounting.
	Counter string
	// Devices is the device match set.
	Devices filter.Devices
	// VlanRanges is the VLAN range match set.
	VlanRanges filter.VlanRanges
	// Src4s is the contiguous IPv4 source match set.
	Src4s []xnetip.Contiguous[xnetip.Network4]
	// Dst4s is the contiguous IPv4 destination match set.
	Dst4s []xnetip.Contiguous[xnetip.Network4]
	// Src6s is the bi-contiguous IPv6 source match set.
	Src6s []xnetip.BiContiguous
	// Dst6s is the bi-contiguous IPv6 destination match set.
	Dst6s []xnetip.BiContiguous
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

	// A condition the rule leaves out is the rule of every value of
	// it: the wildcard is spelled here, so the compile never sees an
	// empty condition. A rule naming no network side at all is the
	// layer2 one - it matches every packet of its devices regardless
	// of the family - and keeps its empty sides.
	// A family of the rule participates when either of its sides is
	// authored; the absent partner side is the wildcard, while a family
	// left out entirely keeps its empty sides and never matches.
	src4s, dst4s := m.Src4s, m.Dst4s
	if len(src4s) > 0 || len(dst4s) > 0 {
		if len(src4s) == 0 {
			src4s = []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4}
		}
		if len(dst4s) == 0 {
			dst4s = []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4}
		}
	}
	src6s, dst6s := m.Src6s, m.Dst6s
	if len(src6s) > 0 || len(dst6s) > 0 {
		if len(src6s) == 0 {
			src6s = []xnetip.BiContiguous{filter.UnspecifiedIPv6}
		}
		if len(dst6s) == 0 {
			dst6s = []xnetip.BiContiguous{filter.UnspecifiedIPv6}
		}
	}
	vlans := m.VlanRanges
	if len(vlans) == 0 {
		vlans = filter.VlanRanges{{From: 0, To: 4095}}
	}

	filter.CBuildDevices(&cRule.devices, m.Devices, pinner)
	filter.CBuildVlanLineRanges(&cRule.vlan_ranges, vlans, pinner)
	filter.CBuildNet4s(&cRule.src_net4s, src4s, pinner)
	filter.CBuildNet4s(&cRule.dst_net4s, dst4s, pinner)
	filter.CBuildNet6s(&cRule.src_net6s, src6s, pinner)
	filter.CBuildNet6s(&cRule.dst_net6s, dst6s, pinner)

	return cRule
}
