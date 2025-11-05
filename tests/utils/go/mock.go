package test_utils

//#cgo CFLAGS: -I../
//#cgo LDFLAGS: -L../ -lyanet_test_utils
/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "yanet_mock.h"
*/
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/gopacket/gopacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
	"github.com/yanet-platform/yanet2/tests/go/common"
)

type YanetMock struct {
	inner  C.struct_yanet_mock
	memory unsafe.Pointer
}

func (mock *YanetMock) Free() {
	C.yanet_mock_free(&mock.inner)
	C.free(mock.memory)
}

func NewYanetMock(dpMemory uint64, cpMemory uint64, moduleTypes []string) (*YanetMock, error) {
	mock := YanetMock{}
	memory := C.aligned_alloc(64, C.size_t((1<<20)+dpMemory+cpMemory))
	cModuleTypes := make([]unsafe.Pointer, 0)
	for _, moduleType := range moduleTypes {
		val := C.CString(moduleType)
		cModuleTypes = append(cModuleTypes, unsafe.Pointer(val))
	}
	defer func() {
		for _, ptr := range cModuleTypes {
			C.free(ptr)
		}
	}()
	rc, err := C.yanet_mock_init(
		&mock.inner,
		memory,
		C.size_t(dpMemory),
		C.size_t(cpMemory),
		(**C.char)(unsafe.Pointer(&cModuleTypes[0])),
		C.size_t(len(moduleTypes)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to init yanet mock: %w", err)
	}
	if rc != 0 {
		return nil, fmt.Errorf("failed to init yanet mock: ec=%d", rc)
	}
	return &mock, nil
}

func (mock *YanetMock) AttachAgent(name string, memoryLimit uint64) (*ffi.Agent, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	agent, err := C.yanet_mock_agent_attach(&mock.inner, cName, C.size_t(memoryLimit))
	if err != nil {
		return nil, fmt.Errorf("failed to attach agent: %w", err)
	}
	if agent == nil {
		return nil, fmt.Errorf("failed to attach agent")
	}
	result := ffi.NewAgent(unsafe.Pointer(agent))
	return &result, nil
}

func (mock *YanetMock) PrepareForCpUpdate() error {
	if _, err := C.yanet_mock_cp_update_prepare(&mock.inner); err != nil {
		return fmt.Errorf("failed to prepare for controlplane update: %w", err)
	}
	return nil
}

type HandlePacketsResult struct {
	Input  []*framework.PacketInfo
	Output []*framework.PacketInfo
	Drop   []*framework.PacketInfo
}

func (mock *YanetMock) HandlePackets(
	cpModule unsafe.Pointer,
	handler unsafe.Pointer,
	packets ...gopacket.Packet,
) (HandlePacketsResult, error) {
	payload := common.PacketsToPaylod(packets)
	pf := common.PacketFrontFromPayload(payload)

	err := common.ParsePackets(pf)
	if err != nil {
		return HandlePacketsResult{}, err
	}
	C.yanet_mock_handle_packets(
		&mock.inner,
		(*C.struct_cp_module)(cpModule),
		(*C.struct_packet_front)(unsafe.Pointer(pf)),
		(C.packets_handler)(handler),
	)
	bytes := common.PacketFrontToPayload(pf)
	f := func(payload [][]uint8) ([]*framework.PacketInfo, error) {
		r := make([]*framework.PacketInfo, 0)
		for idx := range payload {
			packetInfo, err := framework.NewPacketParser().ParsePacket(payload[idx])
			if err != nil {
				return nil, fmt.Errorf("failed to parse packet no. %d: %w", idx+1, err)
			}
			r = append(r, packetInfo)
		}
		return r, nil
	}
	input, err := f(bytes.Input)
	if err != nil {
		return HandlePacketsResult{}, fmt.Errorf("failed to parse input: %w", err)
	}
	drop, err := f(bytes.Drop)
	if err != nil {
		return HandlePacketsResult{}, fmt.Errorf("failed to parse input: %w", err)
	}
	output, err := f(bytes.Output)
	if err != nil {
		return HandlePacketsResult{}, fmt.Errorf("failed to parse output: %w", err)
	}
	return HandlePacketsResult{
		Input:  input,
		Output: output,
		Drop:   drop,
	}, nil
}
