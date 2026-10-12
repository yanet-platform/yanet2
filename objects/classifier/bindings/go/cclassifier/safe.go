package cclassifier

//#cgo CFLAGS: -I../../../../../
//
//#include "api/agent.h"
//#include "objects/classifier/api/classifier_object.h"
import "C"

import (
	"runtime"

	"github.com/yanet-platform/xnetip"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Rule describes one rule of the classifier object's ruleset: the
// filter conditions alone, no decision - the modules linking the
// object resolve the classification results into their own decisions.
type Rule struct {
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

func (r *Rule) cBuild(pinner *runtime.Pinner) C.struct_classifier_object_rule {
	cRule := C.struct_classifier_object_rule{}

	filter.CBuildDevices(&cRule.devices, r.Devices, pinner)
	filter.CBuildVlanLineRanges(&cRule.vlan_ranges, r.VlanRanges, pinner)
	filter.CBuildNet4s(&cRule.src_net4s, r.Src4s, pinner)
	filter.CBuildNet4s(&cRule.dst_net4s, r.Dst4s, pinner)
	filter.CBuildNet6s(&cRule.src_net6s, r.Src6s, pinner)
	filter.CBuildNet6s(&cRule.dst_net6s, r.Dst6s, pinner)

	return cRule
}

// NewL3 creates the l3-subtype classifier object: the device and vlan
// attributes with both network families, the bundle the mirror and
// forward modules compiled in-house before the object existed.
func NewL3(agent *ffi.Agent, name string, rules []Rule) (*ObjectConfig, error) {
	return newObject(agent, name, rules,
		func(agent *C.struct_agent, cName *C.char, rules *C.struct_classifier_object_rule, count C.uint32_t, cErr **C.yanet_error) *C.struct_cp_object {
			return C.classifier_object_config_new_l3(agent, cName, rules, count, cErr)
		})
}

// NewL2 creates the l2-subtype classifier object: the device and vlan
// attributes alone. A rule carrying network conditions is refused.
func NewL2(agent *ffi.Agent, name string, rules []Rule) (*ObjectConfig, error) {
	return newObject(agent, name, rules,
		func(agent *C.struct_agent, cName *C.char, rules *C.struct_classifier_object_rule, count C.uint32_t, cErr **C.yanet_error) *C.struct_cp_object {
			return C.classifier_object_config_new_l2(agent, cName, rules, count, cErr)
		})
}
