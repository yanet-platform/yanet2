package test_balancer

//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../../
//#cgo CFLAGS: -I../../../../build
//#cgo CFLAGS: -I../../../../../ -I../../../../../../lib -I../../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/balancer/tests/utils -lbalancer_test_utils
//#cgo LDFLAGS: -L../../../../build/modules/balancer/api -lbalancer_cp
//#cgo LDFLAGS: -L../../../../build/modules/balancer/dataplane -lbalancer_dp
//#cgo LDFLAGS: -L../../../../build/filter -lfilter
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "utils/mock.h"
#include "utils/process_packets.h"
*/
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/gopacket/gopacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
	"github.com/yanet-platform/yanet2/tests/go/common"
)

type Mock struct {
	inner *C.struct_mock
}

func NewMock(memory uint64) (Mock, error) {
	mock, err := C.mock_create((C.size_t)(memory))
	if err != nil {
		return Mock{inner: nil}, fmt.Errorf("failed to create mock: %w", err)
	}
	if mock == nil {
		return Mock{inner: nil}, fmt.Errorf("failed to create mock")
	}
	return Mock{inner: mock}, nil
}

func FreeMock(mock *Mock) {
	if mock.inner != nil {
		C.mock_free(mock.inner)
	}
}

func (mock *Mock) CreateAgent(memory uint64) (ffi.Agent, error) {
	a, err := C.mock_create_agent(mock.inner, (C.size_t)(memory))
	if err != nil {
		return ffi.NewAgent(nil), fmt.Errorf("failed to create agent: %w", err)
	}
	if a == nil {
		return ffi.NewAgent(nil), fmt.Errorf("failed to create agent")
	}
	return ffi.NewAgent((unsafe.Pointer)(a)), nil
}

func HandlePackets(
	balancer *balancer.BalancerInstance,
	packets ...gopacket.Packet,
) (common.PacketFrontResult, error) {
	payload := common.PacketsToPaylod(packets)
	pf := common.PacketFrontFromPayload(payload)

	err := common.ParsePackets(pf)
	if err != nil {
		return common.PacketFrontResult{}, err
	}
	C.process_packets(
		(*C.struct_cp_module)(balancer.ModuleConfig().AsRawPtr()),
		(*C.struct_packet_front)(unsafe.Pointer(pf)),
	)
	result := common.PacketFrontToPayload(pf)
	return result, nil
}
