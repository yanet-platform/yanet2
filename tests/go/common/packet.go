package common

//#cgo CFLAGS: -I../../.. -I../../../lib -I../../../common
//#cgo LDFLAGS: -L../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../build/lib/dataplane/module -ltesting_module
//#include "dataplane/packet/packet.h"
//#include "dataplane/module/testing.h"
import "C"
import (
	"runtime"
	"unsafe"
)

// FIXME: make configurable
var mbufSize uint64 = 8096

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

func PacketFrontFromPayload(payload [][]byte) (runtime.Pinner, *C.struct_packet_front) {
	testData := []C.struct_test_data{}
	var payloadPinner runtime.Pinner
	for _, data := range payload {
		payloadPinner.Pin(&data[0])
		testData = append(testData, C.struct_test_data{
			payload: (*C.char)(unsafe.Pointer(&data[0])),
			size:    C.uint16_t(len(data)),
		})
	}
	arenaSize := uint64(unsafe.Sizeof(C.struct_packet_front{})) + mbufSize*uint64(len(testData))
	arena := make([]byte, arenaSize)
	var pinner runtime.Pinner
	pinner.Pin(&arena[0])
	pf := C.testing_packet_front(
		(*C.struct_test_data)(unsafe.Pointer(&testData[0])), // payload
		(*C.uint8_t)(unsafe.Pointer(&arena[0])),             // arena
		C.uint64_t(arenaSize),                               // arena_size
		C.uint64_t(len(testData)),                           // mbuf_count
		C.uint16_t(mbufSize),
	)
	payloadPinner.Unpin()

	return pinner, pf
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
			dataPtr := unsafe.Pointer(C.testing_packet_data(p, &length))
			data := unsafe.Slice((*byte)(dataPtr), length)
			resultList = append(resultList, data)
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

