package common

//#cgo CFLAGS: -I../../.. -I../../../lib -I../../../common
//#cgo LDFLAGS: -L../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../build/lib/dataplane/module -ltesting_module
//#include "dataplane/packet/packet.h"
//#include "dataplane/module/testing.h"
//#include "lpm.h"
import "C"
import (
	"net/netip"
	"unsafe"
)

type PacketFrontResult struct {
	Input  [][]byte
	Output [][]byte
	Drop   [][]byte
	Bypass [][]byte
}

func ParsePackets(pf *C.struct_packet_front) {
	for p := pf.input.first; p != nil; p = p.next {
		C.parse_packet(p)
	}
}

func PacketFrontFromPayload(payload [][]byte) *C.struct_packet_front {
	testData := []C.struct_test_data{}
	for _, data := range payload {
		cBytes := C.CBytes(data)
		testData = append(testData, C.struct_test_data{
			payload: (*C.char)(cBytes),
			size:    C.uint16_t(len(data)),
		})
	}
	pf := C.testing_packet_front(
		(*C.struct_test_data)(unsafe.Pointer(&testData[0])),
		C.ulong(len(testData)),
		// FIXME: make configurable
		8096,
	)
	return pf
}

func PacketFrontToPayload(pf *C.struct_packet_front) PacketFrontResult {

	lists := []C.struct_packet_list{
		pf.input,
		pf.output,
		pf.drop,
		pf.bypass,
	}

	result := [][][]byte{}
	for _, list := range lists {
		var resultList [][]byte
		for p := list.first; p != nil; p = p.next {
			var length C.uint16_t
			data := unsafe.Pointer(C.testing_packet_data(p, &length))
			resultList = append(resultList, C.GoBytes(data, (C.int)(length)))
		}
		result = append(result, resultList)
	}
	return PacketFrontResult{
		Input:  result[0],
		Output: result[1],
		Drop:   result[2],
		Bypass: result[3],
	}
}

func BuildLPMs(prefixes []netip.Prefix) (C.struct_lpm, C.struct_lpm) {
	lpm4 := C.struct_lpm{}
	lpm6 := C.struct_lpm{}
	C.lpm_init(&lpm4)
	C.lpm_init(&lpm6)

	for _, prefix := range prefixes {
		if prefix.Addr().Is4() {
			ipv4 := prefix.Addr().As4()
			mask := ToBroadCast(prefix).As4()
			from := (*C.uint8_t)(unsafe.Pointer(&ipv4[0]))
			to := (*C.uint8_t)(unsafe.Pointer(&mask[0]))
			C.lpm_insert(&lpm4, 4, from, to, 1)
		} else {
			ipv6 := prefix.Addr().As16()
			mask := ToBroadCast(prefix).As16()
			from := (*C.uint8_t)(unsafe.Pointer(&ipv6[0]))
			to := (*C.uint8_t)(unsafe.Pointer(&mask[0]))
			C.lpm_insert(&lpm6, 16, from, to, 1)
		}
	}
	return lpm4, lpm6
}
