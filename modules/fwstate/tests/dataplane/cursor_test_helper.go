package fwstate

/*
#include "modules/fwstate/api/fwstate_cp.h"
#include "modules/fwstate/dataplane/config.h"
#include "lib/fwstate/config.h"
#include "lib/fwstate/fwmap.h"
#include "lib/fwstate/fwstate_cursor.h"
#include "lib/fwstate/types.h"
#include "common/memory.h"
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

// insertFw4Entry inserts a single IPv4 fwstate entry directly into the
// active layer's fwmap using fwmap_put.
func insertFw4Entry(
	cpModule *C.struct_cp_module,
	proto uint16, srcPort uint16, dstPort uint16,
	srcAddr uint32, dstAddr uint32,
	srcFlags uint8, dstFlags uint8,
	createdAt uint64, updatedAt uint64,
) error {
	// Resolve the active IPv4 map (layer 0)
	fwmap := C.fwstate_config_resolve_map(cpModule, C.bool(false), C.uint32_t(0))
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

	ttl := C.uint64_t(50000) // large TTL for the bucket deadline
	ret := C.fwmap_put(fwmap, C.uint16_t(0), C.uint64_t(updatedAt), ttl,
		unsafe.Pointer(&key), unsafe.Pointer(&val), nil)
	if ret < 0 {
		return fmt.Errorf("fwmap_put failed: %d", ret)
	}
	return nil
}

// readCursorForward reads entries in the forward direction using the cursor API.
func readCursorForward(
	cpModule *C.struct_cp_module,
	isIPv6 bool, layerIndex uint32,
	index uint32, includeExpired bool,
	now uint64, count uint32,
) ([]CursorResult, uint32, error) {
	var cursor C.fwstate_cursor_t
	rc := C.fwstate_config_cursor_create(
		cpModule, &cursor,
		C.bool(isIPv6), C.uint32_t(layerIndex),
		C.uint32_t(index), C.bool(includeExpired),
	)
	if rc != 0 {
		return nil, 0, fmt.Errorf("fwstate_config_cursor_create failed: %d", rc)
	}

	fwmap := C.fwstate_config_resolve_map(cpModule, C.bool(isIPv6), C.uint32_t(layerIndex))
	if fwmap == nil {
		return nil, 0, fmt.Errorf("fwstate_config_resolve_map returned nil")
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

	return results, uint32(cursor.key_pos), nil
}

// readCursorBackward reads entries in the backward direction using the cursor API.
func readCursorBackward(
	cpModule *C.struct_cp_module,
	isIPv6 bool, layerIndex uint32,
	index uint32, includeExpired bool,
	now uint64, count uint32,
) ([]CursorResult, uint32, error) {
	var cursor C.fwstate_cursor_t
	rc := C.fwstate_config_cursor_create(
		cpModule, &cursor,
		C.bool(isIPv6), C.uint32_t(layerIndex),
		C.uint32_t(index), C.bool(includeExpired),
	)
	if rc != 0 {
		return nil, 0, fmt.Errorf("fwstate_config_cursor_create failed: %d", rc)
	}

	fwmap := C.fwstate_config_resolve_map(cpModule, C.bool(isIPv6), C.uint32_t(layerIndex))
	if fwmap == nil {
		return nil, 0, fmt.Errorf("fwstate_config_resolve_map returned nil")
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

	return results, uint32(cursor.key_pos), nil
}
