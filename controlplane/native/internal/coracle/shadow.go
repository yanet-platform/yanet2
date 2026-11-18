//go:build linux && amd64

package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include <stdint.h>
#include "common/asan.h"

// coracle_poisoned_runs stores up to capacity [start, end) offsets of the
// runs AddressSanitizer marks unaddressable in [base, base + size) and
// returns how many runs there are. A granule whose first byte is
// unaddressable is unaddressable whole, so runs are walked by granule
// where they can be.
static size_t
coracle_poisoned_runs(
	uintptr_t base, size_t size, uintptr_t *runs, size_t capacity
) {
	size_t count = 0;
#ifdef HAVE_ASAN
	uintptr_t pos = base;
	uintptr_t end = base + size;
	while (pos < end) {
		void *first = __asan_region_is_poisoned((void *)pos, end - pos);
		if (first == NULL) {
			break;
		}
		uintptr_t stop = (uintptr_t)first;
		while (stop < end && __asan_address_is_poisoned((void *)stop)) {
			stop = stop % 8 == 0 && stop + 8 <= end ? stop + 8 : stop + 1;
		}
		if (count < capacity) {
			runs[2 * count] = (uintptr_t)first - base;
			runs[2 * count + 1] = stop - base;
		}
		++count;
		pos = stop;
	}
#else
	(void)base;
	(void)size;
	(void)runs;
	(void)capacity;
#endif
	return count;
}

static void
coracle_poison(void *address, size_t size) {
	asan_poison_memory_region(address, size);
}

static void
coracle_unpoison(void *address, size_t size) {
	asan_unpoison_memory_region(address, size);
}
*/
import "C"

import (
	"bytes"
	"unsafe"
)

// Run is a range of memory AddressSanitizer marks unaddressable, as
// offsets from the start of the memory it was read from.
type Run struct {
	Start uintptr
	End   uintptr
}

// PoisonedRuns returns the runs of memory AddressSanitizer marks
// unaddressable; none outside AddressSanitizer builds.
func PoisonedRuns(memory []byte) []Run {
	base := C.uintptr_t(uintptr(unsafe.Pointer(&memory[0])))
	count := C.coracle_poisoned_runs(base, C.size_t(len(memory)), nil, 0)
	if count == 0 {
		return nil
	}
	offsets := make([]C.uintptr_t, 2*count)
	C.coracle_poisoned_runs(base, C.size_t(len(memory)), &offsets[0], count)
	runs := make([]Run, count)
	for idx := range runs {
		runs[idx] = Run{Start: uintptr(offsets[2*idx]), End: uintptr(offsets[2*idx+1])}
	}
	return runs
}

// Repoison makes exactly the runs of memory unaddressable.
func Repoison(memory []byte, runs []Run) {
	base := unsafe.Pointer(&memory[0])
	C.coracle_unpoison(base, C.size_t(len(memory)))
	for _, run := range runs {
		C.coracle_poison(unsafe.Add(base, run.Start), C.size_t(run.End-run.Start))
	}
}

// Snapshot returns a copy of memory and the runs of it AddressSanitizer
// marks unaddressable, reading through them without a report.
func Snapshot(memory []byte) ([]byte, []Run) {
	runs := PoisonedRuns(memory)
	C.coracle_unpoison(unsafe.Pointer(&memory[0]), C.size_t(len(memory)))
	image := bytes.Clone(memory)
	Repoison(memory, runs)
	return image, runs
}

// Restore puts a snapshot back: the bytes and what AddressSanitizer marks
// unaddressable.
func Restore(memory, image []byte, runs []Run) {
	C.coracle_unpoison(unsafe.Pointer(&memory[0]), C.size_t(len(memory)))
	copy(memory, image)
	Repoison(memory, runs)
}

// Unpoison makes all of memory addressable, as memory about to be
// unmapped must be left.
func Unpoison(memory []byte) {
	C.coracle_unpoison(unsafe.Pointer(&memory[0]), C.size_t(len(memory)))
}
