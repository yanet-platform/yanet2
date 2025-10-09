package common

//#cgo CFLAGS: -I../../.. -I../../../lib -I../../../common
//#cgo LDFLAGS: -L../../../build/lib/dataplane/packet -lpacket
//#cgo LDFLAGS: -L../../../build/lib/dataplane/module -ltesting_module
//#include "dataplane/packet/packet.h"
//#include "dataplane/module/testing.h"
import "C"
import (
	"fmt"
	"runtime"
	"unsafe"
)

// FIXME: make configurable
var mbufSize uint64 = 8096

type CPacketFront C.struct_packet_front

type PacketFrontResult struct {
	Input  [][]uint8
	Output [][]uint8
	Drop   [][]uint8
}

func ParsePackets(pf *CPacketFront) error {
	for p := pf.input.first; p != nil; p = p.next {
		rc := C.parse_packet(p)
		if rc != 0 {
			return fmt.Errorf("failed to call C.parse_packet rc=%d", rc)
		}
	}
	return nil
}

func PacketFrontFromPayload(payload [][]byte) *CPacketFront {
	testData := []C.struct_test_data{}
	var payloadPinner runtime.Pinner
	for _, data := range payload {
		payloadPinner.Pin(&data[0])
		testData = append(testData, C.struct_test_data{
			payload: (*C.uint8_t)(&data[0]),
			size:    C.uint16_t(len(data)),
		})
	}
	arenaSize := uint64(unsafe.Sizeof(C.struct_packet_front{})) + mbufSize*uint64(len(testData))
	arena := make([]byte, arenaSize)
	pf := C.testing_packet_front(
		(*C.struct_test_data)(unsafe.Pointer(&testData[0])), // payload
		(*C.uint8_t)(&arena[0]),                             // arena
		C.uint64_t(arenaSize),                               // arena_size
		C.uint64_t(len(testData)),                           // mbuf_count
		C.uint16_t(mbufSize),
	)
	payloadPinner.Unpin()

	return (*CPacketFront)(pf)
}

func PacketFrontToPayload(pf *CPacketFront) PacketFrontResult {

	lists := []C.struct_packet_list{
		pf.input,
		pf.output,
		pf.drop,
	}

	result := [][][]byte{}
	for _, list := range lists {
		var resultList [][]byte
		for p := list.first; p != nil; p = p.next {
			var length C.uint16_t
			data := unsafe.Slice((*uint8)(C.testing_packet_data(p, &length)), length)
			resultList = append(resultList, data)
		}
		result = append(result, resultList)
	}
	return PacketFrontResult{
		Input:  result[0],
		Output: result[1],
		Drop:   result[2],
	}
}
