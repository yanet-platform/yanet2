//go:build linux && amd64 && !asan

package shmalloc

import "unsafe"

// RedZone is the guard an AddressSanitizer build puts on both sides of
// every block (ASAN_RED_ZONE); other builds put none.
const RedZone = 0

// poison marks memory unaddressable in AddressSanitizer builds only.
func poison(unsafe.Pointer, uintptr) {}

// unpoison marks memory addressable in AddressSanitizer builds only.
func unpoison(unsafe.Pointer, uintptr) {}
