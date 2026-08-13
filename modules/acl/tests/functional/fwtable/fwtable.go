// Package fwtable provides test helpers for seeding firewall state
// directly into a fwstate-map object's fwtable, standing in for the
// sync-receive path that populates tables in production.
package fwtable

//#cgo CFLAGS: -I../../../../../ -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/objects/fwstate/api -lfwstate_objects
//#cgo LDFLAGS: -L../../../../../build/lib/counters -lcounters
//
//#include <stdint.h>
//#include <string.h>
//
//#include "lib/fwstate/fwtable.h"
//#include "lib/fwstate/types.h"
//#include "objects/fwstate/api/fwstate_map_v4_object.h"
//
//static inline int64_t
//test_fwtable_insert_state_v4(
//	struct cp_object *cp_object,
//	uint16_t worker_idx,
//	uint64_t now,
//	uint64_t ttl,
//	uint16_t proto,
//	uint16_t src_port,
//	uint16_t dst_port,
//	uint32_t src_addr_le,
//	uint32_t dst_addr_le
//) {
//	fwtable_t *table = fwstate_map_v4_object_table(cp_object);
//
//	struct fw4_state_key key;
//	memset(&key, 0, sizeof(key));
//	key.hdr.proto = proto;
//	key.hdr.src_port = src_port;
//	key.hdr.dst_port = dst_port;
//	key.src_addr = src_addr_le;
//	key.dst_addr = dst_addr_le;
//
//	struct fw_state_value value;
//	memset(&value, 0, sizeof(value));
//	value.external = true;
//	value.created_at = now;
//	value.updated_at = now;
//	value.packets_forward = 1;
//	fwstate_value_set_last_ttl(&value, ttl);
//
//	return fwtable_insert(
//		table, worker_idx, now, ttl, &key, &value, NULL
//	);
//}
import "C"

import (
	"encoding/binary"
	"net"

	objfwstate "github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
)

// InsertV4State inserts a live state entry for the direction
// (srcIP:srcPort -> dstIP:dstPort) into the v4 map object's fwtable. A
// CHECK_STATE lookup for the opposite direction finds it. Returns false
// when the table rejected the entry.
//
// Addresses are loaded little-endian so the key's in-memory bytes match
// the packet-header field image the lookup path copies.
func InsertV4State(
	obj *objfwstate.MapObjectConfig,
	workerIdx uint16,
	now, ttl uint64,
	proto uint16,
	srcIP net.IP,
	srcPort uint16,
	dstIP net.IP,
	dstPort uint16,
) bool {
	rc := C.test_fwtable_insert_state_v4(
		(*C.struct_cp_object)(obj.CPObjectPtr()),
		C.uint16_t(workerIdx),
		C.uint64_t(now),
		C.uint64_t(ttl),
		C.uint16_t(proto),
		C.uint16_t(srcPort),
		C.uint16_t(dstPort),
		C.uint32_t(binary.LittleEndian.Uint32(srcIP.To4())),
		C.uint32_t(binary.LittleEndian.Uint32(dstIP.To4())),
	)
	return int64(rc) >= 0
}
