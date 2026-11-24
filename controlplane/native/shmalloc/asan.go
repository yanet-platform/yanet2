//go:build linux && (amd64 || arm64) && asan

package shmalloc

// #include <sanitizer/asan_interface.h>
import "C"

import "unsafe"

// RedZone is the guard an AddressSanitizer build puts on both sides of
// every block (ASAN_RED_ZONE, MEMORY_BLOCK_MAX_ALIGN).
const RedZone = 64

// poison marks memory unaddressable to AddressSanitizer, as the C
// allocator marks free blocks and the link words it is not touching.
func poison(address unsafe.Pointer, size uintptr) {
	C.__asan_poison_memory_region(address, C.size_t(size))
}

// unpoison marks memory addressable to AddressSanitizer again.
func unpoison(address unsafe.Pointer, size uintptr) {
	C.__asan_unpoison_memory_region(address, C.size_t(size))
}
