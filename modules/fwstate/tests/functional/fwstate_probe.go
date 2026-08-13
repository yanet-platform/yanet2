package fwstate_test

//#cgo CFLAGS: -I../../../../ -I../../../../lib
//#cgo LDFLAGS: -L../../../../build/lib/counters -lcounters
//
//#include <string.h>
//
//#include "lib/fwstate/fwmap.h"
//#include "lib/fwstate/types.h"
//
// // yanet_test_fwstate_insert_ipv6 inserts one synthetic TCP state entry
// // directly into a map object's live IPv6 layer, using the same insert
// // path the dataplane's own sync-frame handler uses, but without going
// // through a worker round: this test package never drives packet
// // processing, so a second publish after one has run would otherwise
// // block forever waiting for a round nothing here drives.
// static int
// yanet_test_fwstate_insert_ipv6(
// 	fwmap_t *map,
// 	uint64_t now,
// 	const uint8_t *src_addr,
// 	const uint8_t *dst_addr,
// 	uint16_t src_port,
// 	uint16_t dst_port
// ) {
// 	if (map == NULL) {
// 		return -1;
// 	}
//
// 	struct fw6_state_key key;
// 	memset(&key, 0, sizeof(key));
// 	key.hdr.proto = 6; // IPPROTO_TCP
// 	key.hdr.src_port = src_port;
// 	key.hdr.dst_port = dst_port;
// 	memcpy(key.src_addr, src_addr, 16);
// 	memcpy(key.dst_addr, dst_addr, 16);
//
// 	struct fw_state_value val;
// 	memset(&val, 0, sizeof(val));
// 	val.flags.tcp.src = FWSTATE_ACK;
// 	val.flags.tcp.dst = FWSTATE_ACK;
// 	val.created_at = now;
// 	val.updated_at = now;
// 	val.packets_forward = 1;
//
// 	return fwmap_put_safe(map, 0, now, (uint64_t)120e9, &key, &val);
// }
import "C"

import (
	"errors"
	"unsafe"
)

// errInsertFailed reports that the underlying insert call could not
// resolve the target map: the map object has no IPv6 layer yet.
var errInsertFailed = errors.New("fwstate: failed to resolve ipv6 map for insert")

// insertFWStateIPv6Entry inserts one synthetic TCP state entry directly
// into the given map object's active IPv6 layer.
//
// The pointer must be the fwmap handle a *objfwstate.MapObjectConfig
// exposes via ResolveMap(0). The addresses must each be 16 bytes.
func insertFWStateIPv6Entry(
	fwmapPtr unsafe.Pointer,
	now uint64,
	srcAddr, dstAddr [16]byte,
	srcPort, dstPort uint16,
) error {
	rc := C.yanet_test_fwstate_insert_ipv6(
		(*C.fwmap_t)(fwmapPtr),
		C.uint64_t(now),
		(*C.uint8_t)(&srcAddr[0]),
		(*C.uint8_t)(&dstAddr[0]),
		C.uint16_t(srcPort),
		C.uint16_t(dstPort),
	)
	if rc < 0 {
		return errInsertFailed
	}
	return nil
}
