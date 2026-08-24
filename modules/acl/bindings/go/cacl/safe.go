package cacl

//#include "modules/acl/api/controlplane.h"
//#include "lib/fwstate/config.h"
import "C"

import (
	"github.com/yanet-platform/xnetip"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
)

// Action kind constants mirror the C ACL_RULE_ACTION_KIND_* enum values.
const (
	ActionAllow       = C.ACL_RULE_ACTION_KIND_ALLOW
	ActionDeny        = C.ACL_RULE_ACTION_KIND_DENY
	ActionCount       = C.ACL_RULE_ACTION_KIND_COUNT
	ActionCheckState  = C.ACL_RULE_ACTION_KIND_CHECK_STATE
	ActionCreateState = C.ACL_RULE_ACTION_KIND_CREATE_STATE
	ActionLog         = C.ACL_RULE_ACTION_KIND_LOG
)

const (
	FragmentNone = C.FILTER_IP_FRAG_NONE
	FragmentFrag = C.FILTER_IP_FRAG_FRAG
	FragmentAny  = C.FILTER_IP_FRAG_ANY
)

// AclAction is a single action applied to a matched packet.
type AclAction struct {
	Kind uint32
}

// AclRule describes a single ACL rule composed of match criteria and actions.
type AclRule struct {
	// Actions is the ordered action list, the last one terminal.
	Actions []AclAction
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
	// ProtoRanges is the protocol and subtype range match set.
	ProtoRanges filter.ProtoRanges
	// SrcPortRanges is the source port range match set.
	SrcPortRanges filter.PortRanges
	// DstPortRanges is the destination port range match set.
	DstPortRanges filter.PortRanges
	// Fragment is the fragmentation attribute to match.
	Fragment filter.Fragment
}

// AclConfigInfo holds metadata about a compiled ACL configuration.
type AclConfigInfo struct {
	CompilationTimeNs      uint64
	FilterRuleCountIp4     uint64
	FilterRuleCountIp4Port uint64
	FilterRuleCountIp6     uint64
	FilterRuleCountIp6Port uint64
	FilterRuleCountVlan    uint64
}

// GetInfo returns compiled configuration metadata for this ACL module.
func (m *ModuleConfig) GetInfo() *AclConfigInfo {
	var cInfo C.struct_acl_config_info
	C.acl_module_config_get_info(m.asRawPtr(), &cInfo)
	return &AclConfigInfo{
		CompilationTimeNs:      uint64(cInfo.compilation_time_ns),
		FilterRuleCountIp4:     uint64(cInfo.filter_rule_count_ip4),
		FilterRuleCountIp4Port: uint64(cInfo.filter_rule_count_ip4_port),
		FilterRuleCountIp6:     uint64(cInfo.filter_rule_count_ip6),
		FilterRuleCountIp6Port: uint64(cInfo.filter_rule_count_ip6_port),
		FilterRuleCountVlan:    uint64(cInfo.filter_rule_count_vlan),
	}
}

// cBuildActions writes the C representation of AclActions into dst.
func cBuildActions(dst *C.struct_acl_rule, actions []AclAction, pinner *runtime.Pinner) {
	if len(actions) == 0 {
		return
	}

	cActions := make([]C.struct_acl_action, len(actions))

	for idx, a := range actions {
		cActions[idx].kind = C.enum_acl_rule_action_kind(a.Kind)
	}

	pinner.Pin(&cActions[0])
	dst.actions = &cActions[0]
	dst.action_count = C.uint64_t(len(cActions))
}

func (m *AclRule) cBuild(pinner *runtime.Pinner) C.struct_acl_rule {
	cRule := C.struct_acl_rule{}

	cBuildActions(&cRule, m.Actions, pinner)

	counter := unsafe.Slice((*byte)(unsafe.Pointer(&cRule.counter[0])), C.COUNTER_NAME_LEN)
	copy(counter, m.Counter)

	filter.CBuildDevices(&cRule.devices, m.Devices, pinner)
	filter.CBuildVlanRanges(&cRule.vlan_ranges, m.VlanRanges, pinner)
	filter.CBuildNet4s(&cRule.src_net4s, m.Src4s, pinner)
	filter.CBuildNet4s(&cRule.dst_net4s, m.Dst4s, pinner)
	filter.CBuildNet6s(&cRule.src_net6s, m.Src6s, pinner)
	filter.CBuildNet6s(&cRule.dst_net6s, m.Dst6s, pinner)
	filter.CBuildProtoRanges(&cRule.proto_ranges, m.ProtoRanges, pinner)
	filter.CBuildPortRanges(&cRule.src_port_ranges, m.SrcPortRanges, pinner)
	filter.CBuildPortRanges(&cRule.dst_port_ranges, m.DstPortRanges, pinner)

	switch m.Fragment {
	case filter.FragmentNone:
		cRule.fragment = FragmentNone
	case filter.FragmentFrag:
		cRule.fragment = FragmentFrag
	case filter.FragmentAny:
		cRule.fragment = FragmentAny
	}

	return cRule
}
