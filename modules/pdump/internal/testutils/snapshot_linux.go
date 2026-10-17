package testutils

/*
#cgo CFLAGS: -I../../../../
#cgo LDFLAGS: -Wl,--wrap=rte_zmalloc
#cgo LDFLAGS: -L../../../../build/modules/pdump/dataplane -lpdump_dp
#cgo LDFLAGS: -L../../../../build/objects/ring/api -lring_objects
#cgo LDFLAGS: -L../../../../build/subprojects/dpdk/lib -l:librte_bpf.a
#cgo LDFLAGS: -Wl,-E
#include <stdlib.h>
#include "snapshot.h"
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// PrepareWorker enables real execution-context publication without packet I/O.
func PrepareWorker(memory *ffi.SharedMemory) error {
	if C.pdump_test_prepare_worker((*C.struct_yanet_shm)(memory.AsRawPtr())) != 0 {
		return fmt.Errorf("failed to allocate idle pdump worker")
	}
	return nil
}

// FreeWorker releases the idle worker after backend and ring teardown.
func FreeWorker(memory *ffi.SharedMemory) {
	C.pdump_test_free_worker((*C.struct_yanet_shm)(memory.AsRawPtr()))
}

// SetWorkerTime publishes a deterministic timestamp for the idle worker.
func SetWorkerTime(agent *ffi.Agent, timestamp uint64) {
	C.pdump_test_set_worker_time((*C.struct_agent)(agent.AsRawPtr()), C.uint64_t(timestamp))
}

// RateSnapshot contains copied limiter parameters and activation state.
type RateSnapshot struct {
	Cost      uint64
	Cap       uint64
	EDT       uint64
	HasBucket bool
}

// ReadRate snapshots the named published configuration without exposing pointers.
func ReadRate(agent *ffi.Agent, name string) (RateSnapshot, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var snapshot C.struct_pdump_test_rate_snapshot
	if C.pdump_test_read_rate((*C.struct_agent)(agent.AsRawPtr()), cName, &snapshot) != 0 {
		return RateSnapshot{}, fmt.Errorf("pdump configuration %q is not published", name)
	}
	return RateSnapshot{
		Cost:      uint64(snapshot.cost),
		Cap:       uint64(snapshot.cap),
		EDT:       uint64(snapshot.edt),
		HasBucket: bool(snapshot.has_bucket),
	}, nil
}
