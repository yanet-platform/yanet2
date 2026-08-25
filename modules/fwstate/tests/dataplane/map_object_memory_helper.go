package fwstate

/*
#include <harness.h>
*/
import "C"

import (
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/testutils"
)

// MemCounters is a snapshot of one memory context's accounting counters,
// for tests that assert where allocations and frees were charged.
type MemCounters struct {
	BallocCount uint64
	BfreeCount  uint64
	BallocSize  uint64
	BfreeSize   uint64
}

// NewHarnessAgent allocates a stand-alone harness agent in the given
// memory context, without any module config or linked map objects.
func NewHarnessAgent(memCtx testutils.MemoryContext) *C.struct_agent {
	cStubAgent := C.CString("stub agent")
	defer C.free(unsafe.Pointer(cStubAgent))
	return C.fwstate_test_agent_new(
		(*C.struct_memory_context)(memCtx.AsRawPtr()),
		cStubAgent,
	)
}

// NewMapV4Object creates an IPv4 map object with its first table layer
// already installed, leaving it unregistered (dangling).
func NewMapV4Object(agent *C.struct_agent, name string) *C.struct_fwstate_map_v4_object {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	obj := C.fwstate_test_map_object_new(agent, false, cName)
	if obj == nil {
		return nil
	}
	// The common-object header is the first field of the map object.
	return (*C.struct_fwstate_map_v4_object)(unsafe.Pointer(obj))
}

// AgentMemCounters snapshots the stand-in agent's context counters.
func AgentMemCounters(agent *C.struct_agent) MemCounters {
	var counters C.struct_fwstate_test_mem_counters
	C.fwstate_test_agent_mem_counters(agent, &counters)
	return MemCounters{
		BallocCount: uint64(counters.balloc_count),
		BfreeCount:  uint64(counters.bfree_count),
		BallocSize:  uint64(counters.balloc_size),
		BfreeSize:   uint64(counters.bfree_size),
	}
}

// ObjectMemCounters snapshots a map object's own context counters.
func ObjectMemCounters(object *C.struct_fwstate_map_v4_object) MemCounters {
	var counters C.struct_fwstate_test_mem_counters
	C.fwstate_test_object_mem_counters(
		(*C.struct_cp_object)(unsafe.Pointer(object)), &counters,
	)
	return MemCounters{
		BallocCount: uint64(counters.balloc_count),
		BfreeCount:  uint64(counters.bfree_count),
		BallocSize:  uint64(counters.balloc_size),
		BfreeSize:   uint64(counters.bfree_size),
	}
}

// InsertMapV4Layer appends one layer to the object's table chain and
// reports the C return code.
func InsertMapV4Layer(
	object *C.struct_fwstate_map_v4_object,
	indexSize uint32,
	extraBucketCount uint32,
	workerCount uint16,
) int {
	return int(C.fwstate_map_v4_object_insert_layer(
		object,
		C.uint32_t(indexSize),
		C.uint32_t(extraBucketCount),
		C.uint16_t(workerCount),
	))
}

// UnlinkStaleMapV4Layers parks expired tail layers in the stale chain and
// reports the C return code.
func UnlinkStaleMapV4Layers(object *C.struct_fwstate_map_v4_object, now uint64) int {
	return int(C.fwstate_map_v4_object_unlink_stale_layers(
		object, C.uint64_t(now),
	))
}

// FreeStaleMapV4Layers releases the layers parked by
// UnlinkStaleMapV4Layers.
func FreeStaleMapV4Layers(object *C.struct_fwstate_map_v4_object) {
	C.fwstate_map_v4_object_free_stale_layers(object)
}
