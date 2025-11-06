package acl

//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../
//#cgo CFLAGS: -I../../../build
//#cgo CFLAGS: -I../../../ -I../../../lib -I../../../common
//#cgo LDFLAGS: -L../../../build/modules/acl/api -lacl_cp
//#cgo LDFLAGS: -L../../../build/filter -lfilter
//#cgo LDFLAGS: -L../../../build/lib/logging -llogging
//
//#include "filter/rule.h"
//#include "api/agent.h"
//#include "modules/acl/api/module.h"
//#include "modules/acl/api/rule.h"
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
)

////////////////////////////////////////////////////////////////////////////////

type pool[T any] struct {
	pool [][]T
}

func newPool[T any]() pool[T] {
	return pool[T]{
		pool: make([][]T, 0),
	}
}

func (p *pool[T]) new(count int) []T {
	// pin array to heap
	array := make([]T, count)
	p.pool = append(p.pool, array)
	return p.pool[len(p.pool)-1]
}

////////////////////////////////////////////////////////////////////////////////

type memoryPool struct {
	protoRange pool[C.struct_filter_proto_range]
	portRange  pool[C.struct_filter_port_range]
	net4       pool[C.struct_net4]
	net6       pool[C.struct_net6]
	devices    pool[*C.char]
}

////////////////////////////////////////////////////////////////////////////////

func makeRuleTransport(rulePb *aclpb.Rule, pool *memoryPool) C.struct_filter_transport {
	// make protos
	protoCount := len(rulePb.Filter.ProtoRanges)
	protos := pool.protoRange.new(protoCount)
	for idx := range protos {
		protos[idx] = C.struct_filter_proto_range{
			from: C.uint16_t(rulePb.Filter.ProtoRanges[idx].From),
			to:   C.uint16_t(rulePb.Filter.ProtoRanges[idx].To),
		}
	}

	// make src ports
	srcPortRangeCount := len(rulePb.Filter.SrcPortRanges)
	srcPortRanges := pool.portRange.new(srcPortRangeCount)
	for idx := range srcPortRanges {
		srcPortRanges[idx] = C.struct_filter_port_range{
			from: C.uint16_t(rulePb.Filter.SrcPortRanges[idx].From),
			to:   C.uint16_t(rulePb.Filter.SrcPortRanges[idx].To),
		}
	}

	// make dst ports
	dstPortRangeCount := len(rulePb.Filter.DstPortRanges)
	dstPortRanges := pool.portRange.new(dstPortRangeCount)
	for idx := range srcPortRanges {
		dstPortRanges[idx] = C.struct_filter_port_range{
			from: C.uint16_t(rulePb.Filter.DstPortRanges[idx].From),
			to:   C.uint16_t(rulePb.Filter.DstPortRanges[idx].To),
		}
	}

	// made transport
	return C.struct_filter_transport{
		proto_count: C.uint16_t(protoCount),
		protos:      (*C.struct_filter_proto_range)(&protos[0]),
		src_count:   C.uint16_t(srcPortRangeCount),
		srcs:        (*C.struct_filter_port_range)(&srcPortRanges[0]),
		dst_count:   C.uint16_t(dstPortRangeCount),
		dsts:        (*C.struct_filter_port_range)(&dstPortRanges[0]),
	}
}

////////////////////////////////////////////////////////////////////////////////

func fillNet(len int, fromAddr []byte, prefixLen uint32, toAddr []C.uint8_t, toMask []C.uint8_t) {
	for b := range len {
		toAddr[b] = C.uint8_t(fromAddr[b])
	}
	for b := range len {
		toMask[b] = C.uint8_t(0)
		for bit := range 8 {
			if uint32(b*8+bit) < prefixLen {
				toMask[b] |= 1 << bit
			}
		}
	}
}

////////////////////////////////////////////////////////////////////////////////

func makeNets4(netsPb []*aclpb.IPNet, pool *memoryPool) (*C.struct_net4, C.uint32_t) {
	count := len(netsPb)
	if count == 0 {
		return nil, 0
	}
	nets := pool.net4.new(count)
	for idx := range netsPb {
		netPb := netsPb[idx]
		net := &nets[idx]
		fillNet(4, netPb.Ip, netPb.PrefixLen, net.addr[:], net.mask[:])
	}
	return &nets[0], C.uint32_t(count)
}

func makeNets6(netsPb []*aclpb.IPNet, pool *memoryPool) (*C.struct_net6, C.uint32_t) {
	count := len(netsPb)
	if count == 0 {
		return nil, 0
	}
	nets := pool.net6.new(count)
	for idx := range netsPb {
		netPb := netsPb[idx]
		net := &nets[idx]
		fillNet(16, netPb.Ip, netPb.PrefixLen, net.addr[:], net.mask[:])
	}
	return &nets[0], C.uint32_t(count)
}

////////////////////////////////////////////////////////////////////////////////

func makeRuleNet4(rulePb *aclpb.Rule, pool *memoryPool) C.struct_filter_net4 {
	srcs, srcCount := makeNets4(rulePb.Filter.Src4S, pool)
	dsts, dstCount := makeNets4(rulePb.Filter.Dst4S, pool)

	return C.struct_filter_net4{
		src_count: srcCount,
		srcs:      srcs,
		dst_count: dstCount,
		dsts:      dsts,
	}
}

func makeRuleNet6(rulePb *aclpb.Rule, pool *memoryPool) C.struct_filter_net6 {
	srcs, srcCount := makeNets6(rulePb.Filter.Src6S, pool)
	dsts, dstCount := makeNets6(rulePb.Filter.Dst6S, pool)

	return C.struct_filter_net6{
		src_count: srcCount,
		srcs:      srcs,
		dst_count: dstCount,
		dsts:      dsts,
	}
}

////////////////////////////////////////////////////////////////////////////////

func makeRuleAction(rulePb *aclpb.Rule) (C.enum_acl_action, C.uint8_t) {
	flags := C.uint8_t(0)

	if rulePb.Log {
		flags |= C.ACL_RULE_LOG_FLAG
	}

	if rulePb.Filter.KeepState {
		flags |= C.ACL_RULE_KEEP_STATE_FLAG
	}

	// todo: more flags?

	action := 0
	switch rulePb.Action {
	case aclpb.ActionKind_ACTION_KIND_PASS:
		{
			action = C.acl_action_pass
		}
	case aclpb.ActionKind_ACTION_KIND_DENY:
		{
			action = C.acl_action_deny
		}
	case aclpb.ActionKind_ACTION_KIND_COUNT:
		{
			action = C.acl_action_action_count
			flags |= C.ACL_RULE_NON_TERMINATE_FLAG
		}
	case aclpb.ActionKind_ACTION_KIND_CHECK_STATE:
		{
			action = C.acl_action_check_state
			flags |= C.ACL_RULE_NON_TERMINATE_FLAG
		}
	}

	// todo: handle all cases

	return (C.enum_acl_action)(action), flags
}

////////////////////////////////////////////////////////////////////////////////
// Module Config public API
////////////////////////////////////////////////////////////////////////////////

// Config of the ACL module
type ModuleConfig struct {
	inner *C.struct_cp_module
}

func NewModuleConfig(agent *ffi.Agent, name string, rulesPb []*aclpb.Rule) (*ModuleConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	memoryPool := memoryPool{
		protoRange: newPool[C.struct_filter_proto_range](),
		portRange:  newPool[C.struct_filter_port_range](),
		net4:       newPool[C.struct_net4](),
		net6:       newPool[C.struct_net6](),
		devices:    newPool[*C.char](),
	}

	cDeviceNames := make([]*C.char, 0)
	deviceNames := make([]string, 0)
	defer func() {
		for _, d := range cDeviceNames {
			C.free(unsafe.Pointer(d))
		}
	}()

	ruleCount := len(rulesPb)
	rules := make([]C.acl_rule_t, ruleCount)
	for idx, rulePb := range rulesPb {
		transport := makeRuleTransport(rulePb, &memoryPool)
		net4 := makeRuleNet4(rulePb, &memoryPool)
		net6 := makeRuleNet6(rulePb, &memoryPool)

		// make devices
		deviceCount := len(rulePb.Filter.Devices)
		devices := memoryPool.devices.new(deviceCount)
		for idx, devicePb := range rulePb.Filter.Devices {
			deviceIdx := len(cDeviceNames)
			for i, dName := range deviceNames {
				if dName == devicePb {
					deviceIdx = i
					break
				}
			}
			if deviceIdx == len(cDeviceNames) {
				deviceNames = append(deviceNames, devicePb)
				cDeviceNames = append(cDeviceNames, C.CString(devicePb))
			}
			devices[idx] = cDeviceNames[deviceIdx]
		}

		// get action and flags
		action, flags := makeRuleAction(rulePb)

		// fill rule
		_, err := C.acl_rule_fill(&rules[idx], net4, net6, transport, C.size_t(deviceCount), &devices[0], action, flags)
		if err != nil {
			return nil, fmt.Errorf("failed to fill rule no. %d: %w", idx+1, err)
		}
	}

	// create module config

	config, err := C.acl_module_config_create((*C.struct_agent)(agent.AsRawPtr()), cName, C.size_t(ruleCount), &rules[0])
	if err != nil {
		return nil, fmt.Errorf("failed to create acl module config: %w", err)
	}
	if config == nil {
		return nil, fmt.Errorf("failed to create acl module config")
	}

	// memory pool must be alive until module config created
	runtime.KeepAlive(memoryPool)

	return &ModuleConfig{
		inner: config,
	}, nil
}

func (config *ModuleConfig) Free() {
	C.acl_module_config_free(config.inner)
}

func (config *ModuleConfig) LinkIntoDataplane(agent *ffi.Agent) error {
	_, err := C.agent_update_modules((*C.struct_agent)(agent.AsRawPtr()), C.size_t(1), &config.inner)
	if err != nil {
		return fmt.Errorf("failed to update modules: %w", err)
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////

// Useful in tests
func (config *ModuleConfig) AsRawPtr() unsafe.Pointer {
	return unsafe.Pointer(config.inner)
}
