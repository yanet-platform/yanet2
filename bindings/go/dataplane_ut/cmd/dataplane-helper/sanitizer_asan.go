//go:build linux && (amd64 || arm64) && asan

package main

/*
#include <stdlib.h>

#include <sanitizer/asan_interface.h>

// Reads memory marked unaddressable, as a read of a free block would.
static void
read_poisoned(void) {
	volatile char *bytes = malloc(8);
	__asan_poison_memory_region((void *)bytes, 8);
	(void)bytes[0];
	__asan_unpoison_memory_region((void *)bytes, 8);
	free((void *)bytes);
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/nativetest/helperproto"
)

// read keeps a byte read only for its report.
var read byte

// readPoisonedFromC makes C read memory marked unaddressable, which the
// sanitizer reports.
func readPoisonedFromC() error {
	C.read_poisoned()
	return nil
}

// readPast reads the byte of the storage at the offset, which the
// sanitizer reports when the client marked it unaddressable.
func readPast(storage []byte, offset uint64) error {
	if offset >= uint64(len(storage)) {
		return fmt.Errorf("offset %#x lies past the storage", offset)
	}
	read = storage[offset]
	return nil
}

// poison makes the control-plane zone, from the offset on, addressable but
// for the runs the client's sanitizer holds unaddressable: its free blocks
// and red zones. A run past the storage panics.
func poison(storage []byte, from uint64, runs []helperproto.Range) {
	zone := storage[from:]
	C.__asan_unpoison_memory_region(unsafe.Pointer(&zone[0]), C.size_t(len(zone)))
	for _, run := range runs {
		region := storage[run.Offset : run.Offset+run.Size]
		C.__asan_poison_memory_region(unsafe.Pointer(unsafe.SliceData(region)), C.size_t(len(region)))
	}
}
