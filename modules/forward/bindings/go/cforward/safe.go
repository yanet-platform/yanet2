package cforward

//#cgo CFLAGS: -I../../../../../
//
//#include "modules/forward/api/controlplane.h"
import "C"

import (
	"unsafe"

	"github.com/yanet-platform/xnetip"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/objects/classifier/bindings/go/cclassifier"
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

// ForwardRule describes a single authored forwarding rule in Go types:
// the filter conditions classify, the action forwards.
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

// Decision is the forwarding decision one rule of the linked
// classifier object's ruleset resolves to.
type Decision struct {
	// Target is the forwarding target device name.
	Target string
	// Mode selects how matched packets are handled.
	Mode ForwardMode
	// Counter is the counter name for traffic accounting.
	Counter string
}

func (d *Decision) cBuild() C.struct_forward_decision {
	cDecision := C.struct_forward_decision{}

	target := unsafe.Slice((*byte)(unsafe.Pointer(&cDecision.target[0])), C.CP_DEVICE_NAME_LEN)
	copy(target, d.Target)

	counter := unsafe.Slice((*byte)(unsafe.Pointer(&cDecision.counter[0])), C.COUNTER_NAME_LEN)
	copy(counter, d.Counter)

	cDecision.mode = d.Mode.toC()

	return cDecision
}

// Split divides the authored rules into the halves the classifier
// binding consumes: the ruleset of the classifier object - the filter
// conditions, one per rule, in order - and the decisions the module
// resolves the classification results through, indexed by the same
// rule order.
func Split(rules []ForwardRule) (classifierRules []cclassifier.Rule, decisions []Decision) {
	classifierRules = make([]cclassifier.Rule, len(rules))
	decisions = make([]Decision, len(rules))

	for idx := range rules {
		rule := &rules[idx]

		classifierRules[idx] = cclassifier.Rule{
			Devices:    rule.Devices,
			VlanRanges: rule.VlanRanges,
			Src4s:      rule.Src4s,
			Dst4s:      rule.Dst4s,
			Src6s:      rule.Src6s,
			Dst6s:      rule.Dst6s,
		}

		decisions[idx] = Decision{
			Target:  rule.Target,
			Mode:    rule.Mode,
			Counter: rule.Counter,
		}
	}

	return classifierRules, decisions
}
