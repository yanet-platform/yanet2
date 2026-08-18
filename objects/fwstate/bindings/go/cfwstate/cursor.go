package cfwstate

//#cgo CFLAGS: -I../../../../../
//#include <stdlib.h>
//#include "lib/fwstate/fwstate_cursor.h"
import "C"

import "unsafe"

// maxCursorBatch caps the allocation made by readEntries regardless of the
// caller-supplied count, providing a defence-in-depth limit at the binding layer.
const maxCursorBatch uint32 = 10000

// StateKey stores a cursor key with address bytes as plain Go data.
type StateKey struct {
	Proto   uint32
	SrcPort uint32
	DstPort uint32
	SrcAddr []byte
	DstAddr []byte
}

// StateValue stores cursor value details for a state entry.
type StateValue struct {
	External        bool
	Flags           uint32
	CreatedAt       uint64
	UpdatedAt       uint64
	PacketsBackward uint64
	PacketsForward  uint64
}

// CursorEntry represents a single entry read from the cursor.
type CursorEntry struct {
	Key     StateKey
	Value   StateValue
	Idx     uint32
	Expired bool
}

func convertCKey(ptr unsafe.Pointer, isIPv6 bool) StateKey {
	var srcAddr []byte
	var dstAddr []byte
	if isIPv6 {
		k := (*C.struct_fw6_state_key)(ptr)
		srcAddr = C.GoBytes(unsafe.Pointer(&k.src_addr[0]), 16)
		dstAddr = C.GoBytes(unsafe.Pointer(&k.dst_addr[0]), 16)
	} else {
		k := (*C.struct_fw4_state_key)(ptr)
		srcAddr = make([]byte, 4)
		dstAddr = make([]byte, 4)
		*(*uint32)(unsafe.Pointer(&srcAddr[0])) = uint32(k.src_addr)
		*(*uint32)(unsafe.Pointer(&dstAddr[0])) = uint32(k.dst_addr)
	}

	hdr := (*C.struct_fw_state_key_hdr)(ptr)
	return StateKey{
		Proto:   uint32(hdr.proto),
		SrcPort: uint32(hdr.src_port),
		DstPort: uint32(hdr.dst_port),
		SrcAddr: srcAddr,
		DstAddr: dstAddr,
	}
}

func stateValueFromC(value *C.struct_fw_state_value) StateValue {
	return StateValue{
		External:        bool(value.external),
		Flags:           uint32(value.flags[0]),
		CreatedAt:       uint64(value.created_at),
		UpdatedAt:       uint64(value.updated_at),
		PacketsBackward: uint64(value.packets_backward),
		PacketsForward:  uint64(value.packets_forward),
	}
}
