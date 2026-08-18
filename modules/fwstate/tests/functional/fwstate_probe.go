package fwstate_test

//#cgo CFLAGS: -I../../../../
//#cgo LDFLAGS: -L../../../../build/modules/fwstate/api -lfwstate_cp
//#cgo LDFLAGS: -L../../../../build/lib/counters -lcounters
//
//#include <string.h>
//
//#include "lib/fwstate/fwmap.h"
//#include "lib/fwstate/types.h"
//#include "modules/fwstate/api/fwstate_cp.h"
//
// // yanet_test_fwstate_insert_ipv6 inserts one synthetic TCP state entry
// // directly into the module's live IPv6 map, using the same insert path
// // the dataplane's own sync-frame handler uses, but without going through
// // a worker round: this test package never drives packet processing, so a
// // second publish would otherwise block forever waiting for a round
// // nothing here drives.
// static int
// yanet_test_fwstate_insert_ipv6(
// 	struct cp_module *cp_module,
// 	uint64_t now,
// 	const uint8_t *src_addr,
// 	const uint8_t *dst_addr,
// 	uint16_t src_port,
// 	uint16_t dst_port
// ) {
// 	fwmap_t *map = fwstate_config_resolve_map(cp_module, true, 0);
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
// resolve the target map: the config has no IPv6 maps yet.
var errInsertFailed = errors.New("fwstate: failed to resolve ipv6 map for insert")

// insertFWStateIPv6Entry inserts one synthetic TCP state entry directly
// into the given module's live IPv6 map.
//
// The pointer must be the unsafe.Pointer a *cfwstate.ModuleConfig exposes
// via AsFFIModule().AsRawPtr(). The addresses must each be 16 bytes.
func insertFWStateIPv6Entry(
	modPtr unsafe.Pointer,
	now uint64,
	srcAddr, dstAddr [16]byte,
	srcPort, dstPort uint16,
) error {
	rc := C.yanet_test_fwstate_insert_ipv6(
		(*C.struct_cp_module)(modPtr),
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
