package relptr

import "unsafe"

// Deref resolves a relative pointer field to *T.
//
// A relative pointer P stores the offset from its own address to the target:
//
//	target = &P + P  (when P != 0)
//	target = nil     (when P == 0)
//
// This mirrors the C macro ADDR_OF from common/memory_address.h.
// The field parameter must be a pointer to a pointer field (e.g. &vs.Reals).
func Deref[T any](field **T) *T {
	raw := (*uintptr)(unsafe.Pointer(field))
	offset := *raw
	if offset == 0 {
		return nil
	}
	return (*T)(unsafe.Pointer(offset + uintptr(unsafe.Pointer(field))))
}

// Slice resolves a relative pointer field to a []T slice over contiguous
// memory. The returned slice shares the underlying memory (zero-copy).
func Slice[T any](field **T, count uint32) []T {
	if count == 0 {
		return nil
	}
	ptr := Deref(field)
	if ptr == nil {
		return nil
	}
	return unsafe.Slice(ptr, count)
}

// Set makes a relative pointer field point to ptr.
//
// This mirrors the C macro SET_OFFSET_OF from common/memory_address.h.
func Set[T any](field **T, ptr *T) {
	raw := (*uintptr)(unsafe.Pointer(field))
	if ptr == nil {
		*raw = 0
		return
	}
	*raw = uintptr(unsafe.Pointer(ptr)) - uintptr(unsafe.Pointer(field))
}

func Equate[T any](dst **T, src **T) {
	Set(dst, Deref(src))
}

func SetSlice[T any](field **T, ptr []T) {
	if len(ptr) == 0 {
		Set(field, nil)
		return
	}
	Set(field, &ptr[0])
}

// DerefOpaque resolves a relative pointer stored in a uintptr field.
// Use this for opaque pointer fields (e.g. filter, selector) where
// the concrete type is not known to Go.
func DerefOpaque(field *uintptr) unsafe.Pointer {
	offset := *field
	if offset == 0 {
		return nil
	}
	return unsafe.Pointer(offset + uintptr(unsafe.Pointer(field)))
}

// SetOpaque makes a uintptr relative pointer field point to addr.
func SetOpaque(field *uintptr, addr unsafe.Pointer) {
	if addr == nil {
		*field = 0
		return
	}
	*field = uintptr(addr) - uintptr(unsafe.Pointer(field))
}
