package cfwstate

/*
#include "map.h"
*/
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"
)

// StashLayout describes the actual per-worker allocation without writable data.
type StashLayout struct {
	SlotAddress    uintptr
	RecordsAddress uintptr
	StashSize      uint64
}

// StashLayout reads one worker's allocation, or zeroes before map creation.
//
// The caller keeps the object alive and the worker idle during inspection.
func (m *MapObjectConfig) StashLayout(worker uint16) (StashLayout, error) {
	stash := C.cfwstate_map_stash(m.ptr, C.bool(m.kind == KindV6))
	layout := StashLayout{StashSize: uint64(stash.size)}
	if stash.worker_count == 0 {
		return layout, nil
	}
	if worker >= uint16(stash.worker_count) {
		return StashLayout{}, fmt.Errorf("worker index %d exceeds stash worker count %d", worker, stash.worker_count)
	}
	var slot *C.struct_fwstate_stash_slot
	if m.kind == KindV6 {
		slot = C.fwstate_map_v6_object_stash(m.ptr, C.uint16_t(worker))
	} else {
		slot = C.fwstate_map_v4_object_stash(m.ptr, C.uint16_t(worker))
	}
	layout.SlotAddress = uintptr(unsafe.Pointer(slot))
	layout.RecordsAddress = uintptr(unsafe.Pointer(C.fwstate_stash_slot_records(slot)))
	return layout, nil
}

// AppendLocalEvents appends pending records for an idle worker's iteration.
//
// The caller keeps the object alive and supplies the worker's actual iteration.
// A new iteration discards unread prior records. Invalid batches leave the
// stash unchanged, including its prior iteration.
func (m *MapObjectConfig) AppendLocalEvents(worker uint16, iteration uint64, frames ...[]byte) error {
	stash := C.cfwstate_map_stash(m.ptr, C.bool(m.kind == KindV6))
	if worker >= uint16(stash.worker_count) {
		return fmt.Errorf("worker index %d exceeds stash worker count %d", worker, stash.worker_count)
	}
	if len(frames) > int(MaxStashSize)/StashRecordSize {
		return fmt.Errorf("event batch exceeds maximum stash capacity")
	}
	data := make([]byte, 0, len(frames)*SyncFrameSize)
	for idx, frame := range frames {
		if len(frame) != SyncFrameSize {
			return fmt.Errorf("frame %d has invalid size %d", idx, len(frame))
		}
		data = append(data, frame...)
	}
	var raw *C.struct_fw_state_sync_frame
	var pinner runtime.Pinner
	defer pinner.Unpin()
	if len(data) > 0 {
		pinner.Pin(&data[0])
		raw = (*C.struct_fw_state_sync_frame)(unsafe.Pointer(&data[0]))
	}
	status := C.cfwstate_stash_append(stash, C.uint16_t(worker), C.uint64_t(iteration), raw, C.size_t(len(frames)), C.bool(m.kind == KindV6))
	if status != 0 {
		return fmt.Errorf("append local events: %d", status)
	}
	return nil
}
