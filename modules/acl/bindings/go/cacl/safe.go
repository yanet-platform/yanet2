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

// ACLAction is a single action applied to a matched packet.
type ACLAction struct {
	Kind uint32
}

// ACLRule describes a single ACL rule composed of match criteria and actions.
type ACLRule struct {
	// Actions is the ordered action list, the last one terminal.
	Actions []ACLAction
	// Counter is the counter name for traffic accounting.
	Counter string
	// Devices is the device match set.
	Devices filter.Devices
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

// ACLConfigInfo holds metadata about a compiled ACL configuration.
type ACLConfigInfo struct {
	CompilationTimeNs      uint64
	FilterRuleCountL2      uint64
	FilterRuleCountIp4     uint64
	FilterRuleCountIp4Tcp  uint64
	FilterRuleCountIp4Udp  uint64
	FilterRuleCountIp4Icmp uint64
	FilterRuleCountIp6     uint64
	FilterRuleCountIp6Tcp  uint64
	FilterRuleCountIp6Udp  uint64
	FilterRuleCountIp6Icmp uint64
}

// GetInfo returns compiled configuration metadata for this ACL module.
func (m *ModuleConfig) GetInfo() *ACLConfigInfo {
	var cInfo C.struct_acl_config_info
	C.acl_module_config_get_info(m.asRawPtr(), &cInfo)
	return &ACLConfigInfo{
		CompilationTimeNs:      uint64(cInfo.compilation_time_ns),
		FilterRuleCountL2:      uint64(cInfo.filter_rule_count_l2),
		FilterRuleCountIp4:     uint64(cInfo.filter_rule_count_ip4),
		FilterRuleCountIp4Tcp:  uint64(cInfo.filter_rule_count_ip4_tcp),
		FilterRuleCountIp4Udp:  uint64(cInfo.filter_rule_count_ip4_udp),
		FilterRuleCountIp4Icmp: uint64(cInfo.filter_rule_count_ip4_icmp),
		FilterRuleCountIp6:     uint64(cInfo.filter_rule_count_ip6),
		FilterRuleCountIp6Tcp:  uint64(cInfo.filter_rule_count_ip6_tcp),
		FilterRuleCountIp6Udp:  uint64(cInfo.filter_rule_count_ip6_udp),
		FilterRuleCountIp6Icmp: uint64(cInfo.filter_rule_count_ip6_icmp),
	}
}

// cBuildLineRanges writes the derived line intervals into the C rule
// view: the pinned slice stays alive through the compile call.
func cBuildLineRanges(
	dst *C.struct_classify_line_ranges,
	intervals []LineRange,
	pinner *runtime.Pinner,
) {
	if len(intervals) == 0 {
		return
	}

	cIntervals := make([]C.struct_classify_line_range, len(intervals))
	for idx, interval := range intervals {
		cIntervals[idx].from = C.uint32_t(interval.From)
		cIntervals[idx].to = C.uint32_t(interval.To)
	}

	pinner.Pin(&cIntervals[0])
	dst.items = &cIntervals[0]
	dst.count = C.uint32_t(len(cIntervals))
}

// cBuildActions writes packet actions into the C rule representation.
func cBuildActions(dst *C.struct_acl_rule, actions []ACLAction, pinner *runtime.Pinner) {
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

func (m *ACLRule) cBuild(pinner *runtime.Pinner) C.struct_acl_rule {
	cRule := C.struct_acl_rule{}

	cBuildActions(&cRule, m.Actions, pinner)

	counter := unsafe.Slice((*byte)(unsafe.Pointer(&cRule.counter[0])), C.COUNTER_NAME_LEN)
	copy(counter, m.Counter)

	filter.CBuildDevices(&cRule.devices, m.Devices, pinner)
	filter.CBuildNet4s(&cRule.src_net4s, m.Src4s, pinner)
	filter.CBuildNet4s(&cRule.dst_net4s, m.Dst4s, pinner)
	filter.CBuildNet6s(&cRule.src_net6s, m.Src6s, pinner)
	filter.CBuildNet6s(&cRule.dst_net6s, m.Dst6s, pinner)
	split := SplitProtoRanges(m.ProtoRanges)
	cBuildLineRanges(&cRule.ipproto_ranges, split.IpProto, pinner)
	cBuildLineRanges(&cRule.tcp_flags_ranges, split.TCPFlags, pinner)
	cBuildLineRanges(&cRule.icmp_type_ranges, split.ICMPTypes, pinner)

	flags := RulePathMembership(
		m.ProtoRanges, m.SrcPortRanges, m.DstPortRanges, m.Fragment,
	)
	cRule.path_plain = C.bool(flags.Plain)
	cRule.path_tcp = C.bool(flags.TCP)
	cRule.path_udp = C.bool(flags.UDP)
	cRule.path_icmp = C.bool(flags.ICMP)
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
