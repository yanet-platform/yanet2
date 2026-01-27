package main

/*
#cgo CFLAGS: -I../ -I../../../../ -I../../../../lib
#cgo LDFLAGS: -L../../../../build/modules/balancer/bench -lbalancer_bench -L../../../../build/lib/utils -llib_utils -L../../../../build/mock -lyanet_mock -L../../../../build/lib/dataplane/pipeline -lpipeline -L../../../../build/lib/dataplane/worker -lworker_dp -lnuma
#cgo LDFLAGS: -L../../../../build/modules/balancer/dataplane -lbalancer_dp
#cgo LDFLAGS: -L../../../../build/modules/decap/dataplane -ldecap_dp
#cgo LDFLAGS: -L../../../../build/modules/dscp/dataplane -ldscp_dp
#cgo LDFLAGS: -L../../../../build/modules/acl/dataplane -lacl_dp
#cgo LDFLAGS: -L../../../../build/modules/fwstate/dataplane -lfwstate_dp
#cgo LDFLAGS: -L../../../../build/modules/forward/dataplane -lforward_dp
#cgo LDFLAGS: -L../../../../build/modules/route/dataplane -lroute_dp
#cgo LDFLAGS: -L../../../../build/modules/nat64/dataplane -lnat64_dp
#cgo LDFLAGS: -L../../../../build/modules/pdump/dataplane -lpdump_dp
#cgo LDFLAGS: -L../../../../build/devices/plain/dataplane -lplain_dp
#cgo LDFLAGS: -L../../../../build/devices/vlan/dataplane -lvlan_dp
#include <stdlib.h>
#include "bench.h"
#include <stdalign.h>
enum { packet_list_align = _Alignof(struct packet_list) };
void *bench_alloc_func = bench_alloc;
*/
import "C"
import (
	"fmt"
	"unsafe"

	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	dataplane "github.com/yanet-platform/yanet2/lib/utils/go"

	// Import mock to link with modules
	_ "github.com/yanet-platform/yanet2/mock/go"
)

type Bench struct {
	bench C.struct_bench
}

func NewBench(workers, totalMemory, cpMemory int) (*Bench, error) {
	b := &Bench{}
	config := C.struct_bench_config{}
	config.workers = C.size_t(workers)
	config.total_memory = C.size_t(totalMemory)
	config.cp_memory = C.size_t(cpMemory)
	ec := C.bench_init(&b.bench, &config)
	if ec != 0 {
		str := C.bench_take_error(&b.bench)
		return nil, fmt.Errorf(
			"failed to initialize bench: %s",
			C.GoString(str),
		)
	}
	return b, nil
}

func (b *Bench) Free() {
	C.bench_free(&b.bench)
}

func (b *Bench) MakePacketLists(count int) ([]dataplane.PacketList, error) {
	if count == 0 {
		return nil, nil
	}
	mem := (C.bench_alloc(
		unsafe.Pointer(&b.bench),
		C.size_t(C.packet_list_align),
		C.size_t(C.sizeof_struct_packet_list)*C.size_t(count),
	))
	p := (*dataplane.PacketList)(unsafe.Pointer(mem))
	if p == nil {
		return nil, fmt.Errorf("failed to allocate memory")
	}
	return unsafe.Slice(p, count), nil
}

func (b *Bench) InitPacketList(
	packetList *dataplane.PacketList,
	packets ...dataplane.PacketData,
) error {
	return dataplane.FillPacketListFromDataWithCustomAlloc(
		packetList,
		dataplane.NewAlloc(
			unsafe.Pointer(&b.bench),
			unsafe.Pointer(C.bench_alloc_func),
		),
		packets...,
	)
}

func (b *Bench) HandlePackets(
	worker int,
	packets []dataplane.PacketList,
) error {
	ec := C.bench_handle_packets(
		&b.bench,
		C.size_t(worker),
		(*C.struct_packet_list)(unsafe.Pointer(&packets[0])),
		C.size_t(len(packets)),
	)
	if ec != 0 {
		return fmt.Errorf("failed to run bench: %d", ec)
	}
	return nil
}

func (b *Bench) SharedMemory() *yanet.SharedMemory {
	return yanet.NewSharedMemoryFromRaw(
		unsafe.Pointer(C.bench_shared_memory(&b.bench)),
	)
}
