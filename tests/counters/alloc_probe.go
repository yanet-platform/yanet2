package counters_test

// The probe owns only wrapped allocations in explicitly measured C reads.
//
// Linker wrapping excludes shared-library internals and query compilation before
// the reader. Ownership survives thread changes until actual free. Measured
// readers and one-shot controls must not run in parallel.

/*
#cgo CFLAGS: -I${SRCDIR}/../.. -fno-builtin-malloc -fno-builtin-calloc -fno-builtin-strdup -fno-builtin-free
#cgo LDFLAGS: -Wl,--wrap=malloc -Wl,--wrap=calloc -Wl,--wrap=strdup -Wl,--wrap=free -Wl,--wrap=yanet_get_counters_by_tags_per_worker -pthread
#include "alloc_probe.h"
#include "../../api/counter.h"
*/
import "C"

type outstandingAllocs struct {
	count uint64
	bytes uint64
}

// probeRootBytes derives the requested root size from the public C layout.
func probeRootBytes(workerCount uint64) uint64 {
	return uint64(C.sizeof_struct_counter_worker_set_list) +
		workerCount*uint64(C.sizeof_struct_counter_worker_set)
}

type allocationSnapshot struct {
	allocations    uint64
	outstanding    outstandingAllocs
	noiseCompleted uint64
	suppressed     uint64
	incomplete     bool
	hookError      int
}

// probeSnapshot observes all accounting fields at one instant.
func probeSnapshot() allocationSnapshot {
	snapshot := C.probe_snapshot_load()
	return allocationSnapshot{
		allocations:    uint64(snapshot.allocations),
		outstanding:    outstandingAllocs{uint64(snapshot.live_count), uint64(snapshot.live_bytes)},
		noiseCompleted: uint64(snapshot.noise_completed),
		suppressed:     uint64(snapshot.suppressed),
		incomplete:     snapshot.incomplete != 0,
		hookError:      int(snapshot.hook_error),
	}
}

func probeArmRead()           { C.probe_arm_read() }
func probeArmNoise()          { C.probe_arm_noise() }
func probeForeignNoise()      { C.probe_foreign_noise() }
func probeArmRetention()      { C.probe_arm_retention() }
func probeArmOverflow()       { C.probe_arm_overflow() }
func probeResetControls() int { return int(C.probe_reset_controls()) }
func probeReleaseRetained(otherThread bool) int {
	var threaded C.int
	if otherThread {
		threaded = 1
	}
	return int(C.probe_release_retained(threaded))
}
