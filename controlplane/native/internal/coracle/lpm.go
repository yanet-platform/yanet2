//go:build linux && (amd64 || arm64)

package coracle

/*
#cgo CFLAGS: -I${SRCDIR}/../../../..
#include "common/lpm.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"unsafe"
)

// LpmInit runs lpm_init, preparing an empty prefix tree whose memory
// context is a child of the parent context.
func LpmInit(tree, parent unsafe.Pointer, name string) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	if C.lpm_init((*C.struct_lpm)(tree), (*C.struct_memory_context)(parent), cName) != 0 {
		return errors.New("lpm_init failed")
	}
	return nil
}

// LpmInsert runs lpm_insert, mapping the keys from the start through the
// end to the value; both keys have the size of the start.
func LpmInsert(tree unsafe.Pointer, from, to []byte, value uint32) error {
	rc := C.lpm_insert(
		(*C.struct_lpm)(tree),
		C.uint8_t(len(from)),
		(*C.uint8_t)(unsafe.Pointer(&from[0])),
		(*C.uint8_t)(unsafe.Pointer(&to[0])),
		C.uint32_t(value),
	)
	if rc != 0 {
		return errors.New("lpm_insert failed")
	}
	return nil
}

// LpmFree runs lpm_free, returning a prefix tree's pages and unlinking
// its memory context.
func LpmFree(tree unsafe.Pointer) {
	C.lpm_free((*C.struct_lpm)(tree))
}
