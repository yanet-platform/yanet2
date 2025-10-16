package balancer_test

//#cgo LDFLAGS: -ldl -lrt -lpcap -lm -lpthread
//#cgo CFLAGS: -I../../../../build
//#cgo CFLAGS: -I../../../../../ -I../../../../../../lib -I../../../../../common
//#cgo CFLAGS: -I../../../../
//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../../lib
//#cgo CFLAGS: -I../../
//#cgo LDFLAGS: -L../../../../build/filter -lfilter
//#cgo LDFLAGS: -L../../../../build/modules/balancer -lbalancer_dp
//#cgo LDFLAGS: -L../../../../build/modules/balancer -lbalancer_cp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../../build/lib/controlplane/config -lconfig_cp
//#cgo LDFLAGS: -L../../../../build/lib/dataplane/config -lconfig_dp
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
//#cgo LDFLAGS: -L../../../../build/lib/counters -lcounters
//#cgo LDFLAGS: -L../../../../build/modules/balancer/tests/utils -lbalancer_test_utils
/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "utils/balancer.h"

#include "dataplane/module/module.h"
#include "dataplane/packet/packet.h"
#include "logging/log.h"

#include <common/memory.h>
#include <common/memory_block.h>

void
balancer_handle_packets(
	struct dp_config *dp_config,
	uint64_t worker_idx,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front
);
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/gopacket/gopacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	cp "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	"github.com/yanet-platform/yanet2/tests/go/common"
)

////////////////////////////////////////////////////////////////////////////////

type Balancer struct {
	alloc           *C.struct_block_allocator
	ModuleConfig    cp.ModuleConfig
	PersistentState cp.PersistentStatePtr
}

////////////////////////////////////////////////////////////////////////////////

func AllocateBalancerArena(arenaSize uint64) (unsafe.Pointer, error) {
	memory := C.malloc(C.uint64_t(arenaSize))
	if memory != nil {
		return unsafe.Pointer(memory), nil
	} else {
		return nil, fmt.Errorf("failed to allocate balancer arena")
	}
}

////////////////////////////////////////////////////////////////////////////////

func MakeBalancer(arena unsafe.Pointer, arenaSize uint64, workers uint64, sessionsToReserve uint64, timeouts cp.Timeouts) (*Balancer, error) {
	alloc := new(C.struct_block_allocator)
	res := C.block_allocator_init(alloc)
	if res != 0 {
		return nil, fmt.Errorf("failed to init block allocator")
	}
	C.block_allocator_put_arena(alloc, arena, C.size_t(arenaSize))
	mctx := C.struct_memory_context{}
	res = C.memory_context_init(&mctx, C.CString("test"), alloc)
	if res != 0 {
		return nil, fmt.Errorf("failed to init memory context")
	}
	state := C.make_balancer_state(&mctx, C.size_t(workers), C.size_t(sessionsToReserve))
	if state == nil {
		return nil, fmt.Errorf("failed to make persistent state")
	}
	stateWrapper := cp.MakePersistentStatePtr(unsafe.Pointer(state))
	balancerCpModule := C.make_balancer(&mctx, nil, state)
	ffiBalancerModuleConfig := ffi.NewModuleConfig((unsafe.Pointer)(balancerCpModule))
	balancerModuleConfig := cp.ModuleConfig{
		Ptr: ffiBalancerModuleConfig,
	}
	balancerModuleConfig.SetTimeouts(timeouts)
	return &Balancer{
		alloc:           alloc,
		ModuleConfig:    balancerModuleConfig,
		PersistentState: stateWrapper,
	}, nil
}

////////////////////////////////////////////////////////////////////////////////

func (balancer *Balancer) HandlePackets(workerIdx uint64, packets ...gopacket.Packet) (common.PacketFrontResult, error) {
	payload := common.PacketsToPaylod(packets)
	pf := common.PacketFrontFromPayload(payload)

	err := common.ParsePackets(pf)
	if err != nil {
		return common.PacketFrontResult{}, err
	}
	C.balancer_handle_packets(nil, C.uint64_t(workerIdx), (*C.struct_cp_module)(balancer.ModuleConfig.Ptr.AsRawPtr()), nil, (*C.struct_packet_front)(unsafe.Pointer(pf)))
	result := common.PacketFrontToPayload(pf)
	return result, nil
}

////////////////////////////////////////////////////////////////////////////////

func (balancer *Balancer) AddService(service cp.Service) error {
	return balancer.ModuleConfig.AddService(service)
}
