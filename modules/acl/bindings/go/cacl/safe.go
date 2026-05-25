package cacl

//#include "modules/acl/api/controlplane.h"
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
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

// AclAction is a single action applied to a matched packet.
type AclAction struct {
	Kind uint32
}

// AclRule describes a single ACL rule composed of match criteria and actions.
type AclRule struct {
	Actions       []AclAction
	Counter       string
	Devices       filter.Devices
	VlanRanges    filter.VlanRanges
	Src4s         filter.IPNets
	Dst4s         filter.IPNets
	Src6s         filter.IPNets
	Dst6s         filter.IPNets
	ProtoRanges   filter.ProtoRanges
	SrcPortRanges filter.PortRanges
	DstPortRanges filter.PortRanges
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

// UpdateRules compiles the given rules into C structures and pushes them into
// shared memory.
func (m *ModuleConfig) UpdateRules(rules []AclRule) error {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cRules := make([]C.struct_acl_rule, len(rules))
	for idx, rule := range rules {
		cRules[idx] = rule.cBuild(pinner)
	}

	var cRulesPtr *C.struct_acl_rule
	if len(cRules) > 0 {
		cRulesPtr = &cRules[0]
	}

	var cErr *C.yanet_error
	rc := C.acl_module_config_update(
		m.asRawPtr(),
		cRulesPtr,
		C.uint32_t(len(cRules)),
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf("failed to update ACL config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return nil
}

// SetFwStateConfig links the given fwstate module config to this ACL config.
func (m *ModuleConfig) SetFwStateConfig(fw ffi.ModuleConfig) {
	C.acl_module_config_set_fwstate_config(m.asRawPtr(), (*C.struct_cp_module)(fw.AsRawPtr()))
}

// TransferFwStateConfig copies the fwstate pointer from old into this config.
func (m *ModuleConfig) TransferFwStateConfig(old ffi.ModuleConfig) {
	C.acl_module_config_transfer_fwstate_config(m.asRawPtr(), (*C.struct_cp_module)(old.AsRawPtr()))
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

	return cRule
}
