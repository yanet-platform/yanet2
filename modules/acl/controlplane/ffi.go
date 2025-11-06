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
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
)

type Rule struct {
	inner C.acl_rule_t
}

type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

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

func fillNet(len int, fromAddr *[]byte, prefixLen uint32, toAddr []C.uint8_t, toMask []C.uint8_t) {
	for b := range len {
		toAddr[b] = C.uint8_t((*fromAddr)[b])
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
		fillNet(4, &netPb.Ip, netPb.PrefixLen, net.addr[:], net.mask[:])
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
		fillNet(16, &netPb.Ip, netPb.PrefixLen, net.addr[:], net.mask[:])
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
	}
	// todo

	return (C.enum_acl_action)(action), flags
}

////////////////////////////////////////////////////////////////////////////////

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
	rules := make([]Rule, ruleCount)
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
	}

	tr, err := C.acl_module_config_create((*C.struct_agent)(agent.AsRawPtr()), cName)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize acl module config: %w", err)
	}
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize acl module config: module %q not found", name)
	}

	return &ModuleConfig{
		ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}
