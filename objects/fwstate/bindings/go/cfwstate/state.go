package cfwstate

/*
#include "map.h"
*/
import "C"

import (
	"fmt"
	"math"
	"runtime"
	"unsafe"
)

// StateLookup copies the current value and its actual table deadline.
type StateLookup struct {
	StateValue
	Deadline uint64
	Found    bool
}

// LookupState reads current state, including expired entries until reclaimed.
//
// The caller keeps the object alive and serializes access against writes and
// layer changes. The deadline comes from the actual table entry.
func (m *MapObjectConfig) LookupState(key StateKey) (StateLookup, error) {
	addressSize := 4
	if m.kind == KindV6 {
		addressSize = 16
	}
	if len(key.SrcAddr) != addressSize || len(key.DstAddr) != addressSize {
		return StateLookup{}, fmt.Errorf("key address family does not match map")
	}
	if key.Proto > math.MaxUint16 || key.SrcPort > math.MaxUint16 || key.DstPort > math.MaxUint16 {
		return StateLookup{}, fmt.Errorf("key protocol or port exceeds native width")
	}
	cKey := C.struct_cfwstate_lookup_key{
		protocol: C.uint16_t(key.Proto), source_port: C.uint16_t(key.SrcPort), destination_port: C.uint16_t(key.DstPort),
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cKey.source[0])), 16), key.SrcAddr)
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&cKey.destination[0])), 16), key.DstAddr)
	var result C.struct_cfwstate_lookup_result
	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(&cKey)
	pinner.Pin(&result)
	C.cfwstate_lookup(m.ptr, C.bool(m.kind == KindV6), &cKey, &result)
	return StateLookup{
		StateValue: stateValueFromC(&result.value), Deadline: uint64(result.deadline), Found: bool(result.found),
	}, nil
}
