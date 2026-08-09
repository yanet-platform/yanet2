package fwstate

/*
#include "common/container_of.h"
#include "common/memory.h"
#include "modules/fwstate/api/fwstate_cp.h"
#include "modules/fwstate/dataplane/config.h"
#include "modules/fwstate/objects/fwstate_map_object.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/fwstate_cursor.h"
#include "lib/fwstate/types.h"

// fwstate_test_resolve_map_object resolves a layer's fwmap from a
// standalone fwstate-map object's table.
static inline fwmap_t *
fwstate_test_resolve_map_object(
	struct cp_object *cp_object, uint32_t layer_index
) {
	fwtable_t *table = fwstate_map_object_table(cp_object);
	fwmap_t *head = ADDR_OF(&table->head);
	return fwstate_resolve_map(head, layer_index);
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// CursorResult holds data from a single cursor entry, copied into Go memory.
type CursorResult struct {
	Idx      uint32
	Proto    uint16
	SrcPort  uint16
	DstPort  uint16
	SrcAddr  uint32
	DstAddr  uint32
	Flags    uint8
	External bool

	CreatedAt   uint64
	UpdatedAt   uint64
	PktForward  uint64
	PktBackward uint64
}

// insertFw4Entry inserts a single IPv4 fwstate entry into the active layer.
func insertFw4Entry(
	mapObject *C.struct_cp_object,
	proto uint16, srcPort uint16, dstPort uint16,
	srcAddr uint32, dstAddr uint32,
	srcFlags uint8, dstFlags uint8,
	createdAt uint64, updatedAt uint64,
	ttlNs uint64,
) error {
	fwmap := C.fwstate_test_resolve_map_object(mapObject, C.uint32_t(0))
	if fwmap == nil {
		return fmt.Errorf("failed to resolve IPv4 map")
	}

	var key C.struct_fw4_state_key
	key.hdr.proto = C.uint16_t(proto)
	key.hdr.src_port = C.uint16_t(srcPort)
	key.hdr.dst_port = C.uint16_t(dstPort)
	key.src_addr = C.uint32_t(srcAddr)
	key.dst_addr = C.uint32_t(dstAddr)

	var val C.struct_fw_state_value
	val.flags[0] = byte(srcFlags | (dstFlags << 4))
	val.external = C.bool(false)
	val.created_at = C.uint64_t(createdAt)
	val.updated_at = C.uint64_t(updatedAt)
	val.packets_forward = C.uint64_t(1)
	val.packets_backward = C.uint64_t(0)
	C.fwstate_value_set_last_ttl(&val, C.uint64_t(ttlNs))

	ret := C.fwmap_put(fwmap, C.uint16_t(0), C.uint64_t(updatedAt), C.uint64_t(ttlNs),
		unsafe.Pointer(&key), unsafe.Pointer(&val), nil)
	if ret < 0 {
		return fmt.Errorf("fwmap_put failed: %d", ret)
	}
	return nil
}

// readCursorForward reads entries in the forward direction using the cursor API.
func readCursorForward(
	mapObject *C.struct_cp_object,
	isIPv6 bool, layerIndex uint32,
	index int64, includeExpired bool,
	now uint64, count uint32,
) ([]CursorResult, int64, error) {
	fwmap := C.fwstate_test_resolve_map_object(mapObject, C.uint32_t(layerIndex))
	if fwmap == nil {
		return nil, 0, fmt.Errorf("fwstate_test_resolve_map_object returned nil")
	}

	var cursor C.fwstate_cursor_t
	rc := C.fwstate_cursor_init(
		fwmap, &cursor,
		C.int64_t(index), C.bool(includeExpired),
	)
	if rc != 0 {
		return nil, 0, fmt.Errorf("fwstate_cursor_init failed: %d", rc)
	}

	buf := make([]C.fwstate_cursor_entry_t, count)
	n := C.fwstate_cursor_read_forward(
		fwmap, &cursor, C.uint64_t(now), &buf[0], C.uint32_t(count),
	)

	results := make([]CursorResult, 0, n)
	for i := range n {
		entry := buf[i]
		k := (*C.struct_fw4_state_key)(entry.key)
		v := (*C.struct_fw_state_value)(entry.value)

		results = append(results, CursorResult{
			Idx:         uint32(entry.idx),
			Proto:       uint16(k.hdr.proto),
			SrcPort:     uint16(k.hdr.src_port),
			DstPort:     uint16(k.hdr.dst_port),
			SrcAddr:     uint32(k.src_addr),
			DstAddr:     uint32(k.dst_addr),
			Flags:       uint8(v.flags[0]),
			External:    bool(v.external),
			CreatedAt:   uint64(v.created_at),
			UpdatedAt:   uint64(v.updated_at),
			PktForward:  uint64(v.packets_forward),
			PktBackward: uint64(v.packets_backward),
		})
	}

	return results, int64(cursor.key_pos), nil
}

// readCursorBackward reads entries in the backward direction using the cursor API.
func readCursorBackward(
	mapObject *C.struct_cp_object,
	isIPv6 bool, layerIndex uint32,
	index int64, includeExpired bool,
	now uint64, count uint32,
) ([]CursorResult, int64, error) {
	fwmap := C.fwstate_test_resolve_map_object(mapObject, C.uint32_t(layerIndex))
	if fwmap == nil {
		return nil, 0, fmt.Errorf("fwstate_test_resolve_map_object returned nil")
	}

	var cursor C.fwstate_cursor_t
	rc := C.fwstate_cursor_init(
		fwmap, &cursor,
		C.int64_t(index), C.bool(includeExpired),
	)
	if rc != 0 {
		return nil, 0, fmt.Errorf("fwstate_cursor_init failed: %d", rc)
	}

	buf := make([]C.fwstate_cursor_entry_t, count)
	n := C.fwstate_cursor_read_backward(
		fwmap, &cursor, C.uint64_t(now), &buf[0], C.uint32_t(count),
	)

	results := make([]CursorResult, 0, n)
	for i := range n {
		entry := buf[i]
		k := (*C.struct_fw4_state_key)(entry.key)
		v := (*C.struct_fw_state_value)(entry.value)

		results = append(results, CursorResult{
			Idx:         uint32(entry.idx),
			Proto:       uint16(k.hdr.proto),
			SrcPort:     uint16(k.hdr.src_port),
			DstPort:     uint16(k.hdr.dst_port),
			SrcAddr:     uint32(k.src_addr),
			DstAddr:     uint32(k.dst_addr),
			Flags:       uint8(v.flags[0]),
			External:    bool(v.external),
			CreatedAt:   uint64(v.created_at),
			UpdatedAt:   uint64(v.updated_at),
			PktForward:  uint64(v.packets_forward),
			PktBackward: uint64(v.packets_backward),
		})
	}

	return results, int64(cursor.key_pos), nil
}
