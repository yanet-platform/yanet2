package probe

/*
#cgo CFLAGS: -I../../../../../ -I../../../../../lib -D_GNU_SOURCE
#cgo amd64 CFLAGS: -march=haswell
#include "../fwmap_probe.h"
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const (
	Fresh = iota
	MatchLive
	MatchEqualDeadline
	MatchExpired
	ReuseEqualDeadline
	ReuseExpired
	ZeroTTL
	OverflowExhausted
	KeyExhaustedAfterLink
	MatchAfterExpired
	ZeroDeadlineTerminates
)

type Snapshot struct {
	Bytes                          []byte
	Keys, Values                   []byte
	KeyCursor, ExtraFree, RootNext uint32
	Total, Chain, Deadline         [2]uint64
	RootDeadline, ExtraDeadline    [4]uint64
	RootIndex, ExtraIndex          [4]uint32
	RootBytes                      []byte
}

type Result struct {
	Before, Discovered, Committed                           Snapshot
	Index                                                   uint32
	LookupIndex                                             int64
	LookupPackets                                           uint64
	MatchingSlots                                           uint32
	HasKey, HasValue, Empty, LockIsRoot                     bool
	LockBefore, LockDiscovered, LockCommitted, LockReleased int
}

func snapshot(value *C.struct_fwmap_probe_snapshot, root uint32) Snapshot {
	result := Snapshot{
		Bytes:     C.GoBytes(unsafe.Pointer(value), C.int(C.sizeof_struct_fwmap_probe_snapshot)),
		Keys:      C.GoBytes(unsafe.Pointer(&value.keys), C.int(unsafe.Sizeof(value.keys))),
		Values:    C.GoBytes(unsafe.Pointer(&value.values), C.int(unsafe.Sizeof(value.values))),
		RootBytes: C.GoBytes(unsafe.Pointer(&value.roots[root]), C.int(unsafe.Sizeof(value.roots[0]))),
		KeyCursor: uint32(value.key_cursor), ExtraFree: uint32(value.extra_free_idx), RootNext: uint32(value.root_next),
	}
	for idx := range 2 {
		result.Total[idx] = uint64(value.total_elements[idx])
		result.Chain[idx] = uint64(value.max_chain[idx])
		result.Deadline[idx] = uint64(value.max_deadline[idx])
	}
	for idx := range 4 {
		result.RootDeadline[idx] = uint64(value.roots[root].deadline[idx])
		result.RootIndex[idx] = uint32(value.roots[root].idx[idx])
		result.ExtraDeadline[idx] = uint64(value.extra[1].deadline[idx])
		result.ExtraIndex[idx] = uint32(value.extra[1].idx[idx])
	}
	return result
}

// Entry observes a single discovery and commit on an unused map.
func Entry(mapPointer unsafe.Pointer, scenario int, locked bool) (Result, error) {
	var raw C.struct_fwmap_probe_result
	useLock := C.int(0)
	if locked {
		useLock = 1
	}
	status := C.fwmap_probe_entry(mapPointer, C.enum_fwmap_probe_case(scenario), useLock, &raw)
	if status != 0 {
		return Result{}, fmt.Errorf("fwmap probe: status %d", int(status))
	}
	root := uint32(raw.root_idx)
	return Result{
		Before: snapshot(&raw.before, root), Discovered: snapshot(&raw.discovered, root), Committed: snapshot(&raw.committed, root),
		Index: uint32(raw.result_idx), HasKey: raw.result_has_key != 0, HasValue: raw.result_has_value != 0, Empty: raw.result_empty != 0,
		LookupIndex: int64(raw.lookup_idx), LookupPackets: uint64(raw.lookup_packets), MatchingSlots: uint32(raw.matching_slots),
		LockIsRoot: raw.lock_is_root != 0, LockBefore: int(raw.lock_before), LockDiscovered: int(raw.lock_discovered), LockCommitted: int(raw.lock_committed), LockReleased: int(raw.lock_released),
	}, nil
}
