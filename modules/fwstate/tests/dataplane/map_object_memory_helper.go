package fwstate

/*
#include <harness.h>
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
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
	obj := C.fwstate_test_map_object_new(agent, false, cName, 0)
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

// NewBareMapV4Object creates an IPv4 map object that is not created yet:
// no stash and no table layer, unregistered (dangling).
func NewBareMapV4Object(agent *C.struct_agent, name string) *C.struct_fwstate_map_v4_object {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var cErr *C.yanet_error
	obj := C.fwstate_map_v4_object_config_new(agent, cName, &cErr)
	if obj == nil {
		C.yanet_error_free(cErr)
		return nil
	}
	return (*C.struct_fwstate_map_v4_object)(unsafe.Pointer(obj))
}

// CreateMapV4 creates the map with a 1024-slot first layer and a stash of
// stashSize bytes per worker, zero selecting the default, and reports the
// C return code with errno.
func CreateMapV4(object *C.struct_fwstate_map_v4_object, workerCount uint16, indexSize uint32, stashSize uint64) (int, error) {
	config := C.struct_fwstate_map_create_config{
		index_size:         C.uint32_t(indexSize),
		extra_bucket_count: 64,
		worker_count:       C.uint16_t(workerCount),
		stash_size:         C.uint64_t(stashSize),
	}
	rc, errno := C.fwstate_map_v4_object_create(object, &config)
	return int(rc), errno
}

// MapV4StashSize reports the object's per-worker stash size in bytes.
func MapV4StashSize(object *C.struct_fwstate_map_v4_object) uint64 {
	return uint64(C.fwstate_map_v4_object_stash_size(
		(*C.struct_cp_object)(unsafe.Pointer(object)),
	))
}

// HarnessWorkerCount is the dataplane worker count of the harness agent.
const HarnessWorkerCount = int(C.FWSTATE_TEST_WORKER_COUNT)

// DefaultStashSize is the per-worker stash size a zero request selects.
const DefaultStashSize = uint64(C.FWSTATE_STASH_DEFAULT_SIZE)

// StashRecordSize is the stash bytes one record takes.
const StashRecordSize = uint64(C.sizeof_struct_fwstate_sync_record)

// DestroyMapV4Object tears the object down and returns its storage to the
// agent, as the typed destructor does once the object dangles.
func DestroyMapV4Object(agent *C.struct_agent, object *C.struct_fwstate_map_v4_object) {
	C.fwstate_map_v4_object_fini(object)
	C.fwstate_map_v4_object_free(object, agent)
}

// AgentFreeBytes sums the free bytes of the allocator behind the agent.
func AgentFreeBytes(agent *C.struct_agent) uint64 {
	return uint64(C.fwstate_test_free_bytes(agent))
}

// CreateAndFreeModuleConfig builds an fwstate module config without map
// links on the agent and destroys it again, reporting how many per-worker
// read positions the config held and the first error.
func CreateAndFreeModuleConfig(agent *C.struct_agent) (uint64, error) {
	cName := C.CString("cycle")
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	config := C.fwstate_module_config_new(agent, cName, nil, nil, nil, &cErr)
	if config == nil {
		return 0, fmt.Errorf("failed to create module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	fwstateConfig := (*C.struct_fwstate_module_config)(unsafe.Pointer(config))
	positions := uint64(0)
	if C.addr_of((*unsafe.Pointer)(unsafe.Pointer(&fwstateConfig.positions))) != nil {
		positions = uint64(fwstateConfig.worker_count)
	}
	if rc := C.fwstate_module_config_free(config, &cErr); rc != 0 {
		return 0, fmt.Errorf("failed to free module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return positions, nil
}
