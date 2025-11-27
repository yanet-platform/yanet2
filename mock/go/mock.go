package mock

/*
#cgo CFLAGS: -I../
#cgo CFLAGS: -I../../
#cgo CFLAGS: -I../../lib
#cgo CFLAGS: -I../../build/subprojects/dpdk/lib
#cgo CFLAGS: -I../../build/mock
#cgo LDFLAGS: -L../../build/modules/balancer/dataplane -lbalancer_dp
#cgo LDFLAGS: -L../../build/modules/decap/dataplane -ldecap_dp
#cgo LDFLAGS: -L../../build/modules/dscp/dataplane -ldscp_dp
#cgo LDFLAGS: -L../../build/modules/acl/dataplane -lacl_dp
#cgo LDFLAGS: -L../../build/modules/forward/dataplane -lforward_dp
#cgo LDFLAGS: -L../../build/modules/route/dataplane -lroute_dp
#cgo LDFLAGS: -L../../build/modules/nat64/dataplane -lnat64_dp
#cgo LDFLAGS: -L../../build/modules/pdump/dataplane -lpdump_dp
#cgo LDFLAGS: -L../../build/devices/plain/dataplane -lplain_dp
#cgo LDFLAGS: -L../../build/devices/vlan/dataplane -lvlan_dp
#cgo LDFLAGS: -L../../build/mock -lyanet_mock
#cgo LDFLAGS: -L../../build/lib/dataplane/pipeline -lpipeline
#cgo LDFLAGS: -L../../build/lib/logging  -llogging
#cgo LDFLAGS: -L../../build/lib/controlplane/agent  -lagent
#cgo LDFLAGS: -L../../build/lib/counters  -lcounters
#cgo LDFLAGS: -L../../build/filter -lfilter
#cgo LDFLAGS: -lnuma
#cgo LDFLAGS: -ldl

#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <errno.h>

#include "mock.h"
#include "config.h"
*/
import "C"
import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/gopacket/gopacket"
	"github.com/yanet-platform/yanet2/common/go/dataplane"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

////////////////////////////////////////////////////////////////////////////////

func openAggregator() (unsafe.Pointer, error) {
	path := C.CString("../../build/mock/libdataplane_aggregator.so")
	defer C.free(unsafe.Pointer(path))

	h, err := C.dlopen(path, C.RTLD_NOW|C.RTLD_GLOBAL)
	if h == nil {
		return nil, fmt.Errorf("dlopen failed: %w", err)
	}

	return h, nil
}

////////////////////////////////////////////////////////////////////////////////

type YanetMock struct {
	aggregator unsafe.Pointer
	inner      C.struct_yanet_mock
}

func NewYanetMock(config *YanetMockConfig) (*YanetMock, error) {
	h, err := openAggregator()
	if err != nil {
		return nil, fmt.Errorf("failed to open aggregator: %v", err)
	}

	cConfig := C.struct_yanet_mock_config{}
	C.memset(unsafe.Pointer(&cConfig), 0, C.size_t(unsafe.Sizeof(cConfig)))
	cConfig.cp_memory = C.size_t(config.CpMemory)
	cConfig.dp_memory = C.size_t(config.DpMemory)
	cConfig.device_count = C.size_t(len(config.Devices))
	cConfig.worker_count = C.size_t(config.Workers)
	for idx := range config.Devices {
		device := &config.Devices[idx]
		bytes := []byte(device.name)
		C.memcpy(
			unsafe.Pointer(&cConfig.devices[idx].name[0]),
			unsafe.Pointer(&bytes[0]),
			C.size_t(len(device.name)),
		)
	}

	mock := C.struct_yanet_mock{}
	ec, err := C.yanet_mock_init(&mock, &cConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to init mock: %w", err)
	}
	if ec != C.int(0) {
		return nil, fmt.Errorf("failed to init mock: ec=%d", ec)
	}
	return &YanetMock{inner: mock, aggregator: h}, nil
}

func (mock *YanetMock) Free() {
	C.dlclose(mock.aggregator)
	C.yanet_mock_free(&mock.inner)
}

func (mock *YanetMock) SharedMemory() *ffi.SharedMemory {
	shm := C.yanet_mock_shm(&mock.inner)
	return ffi.NewSharedMemoryFromRaw(unsafe.Pointer(shm))
}

type HandlePacketsResult struct {
	Output []*framework.PacketInfo
	Drop   []*framework.PacketInfo
}

func (mock *YanetMock) HandlePackets(packets ...gopacket.Packet) (*HandlePacketsResult, error) {
	pinner := runtime.Pinner{}
	defer pinner.Unpin()

	payloads := xpacket.PacketsGoPayload(packets...)
	txDeviceId := 0
	rxDeviceId := 0
	data := make([]dataplane.PacketData, 0, len(payloads))
	for idx := range payloads {
		data = append(data, dataplane.PacketData{
			Payload:    payloads[idx],
			TxDeviceId: uint16(txDeviceId),
			RxDeviceId: uint16(rxDeviceId),
		})
	}
	packetList, err := dataplane.NewPacketListFromData(&pinner, data...)
	if err != nil {
		return nil, err
	}

	pinner.Pin(mock)
	result, err := C.yanet_worker_mock_handle_packets(
		&mock.inner.workers[0],
		(*C.struct_packet_list)(unsafe.Pointer(packetList)),
	)
	if err != nil {
		return nil, err
	}

	pinner.Pin(&result)

	output := (*dataplane.PacketList)(unsafe.Pointer(&result.output_packets))
	drop := (*dataplane.PacketList)(unsafe.Pointer(&result.drop_packets))

	return &HandlePacketsResult{
		Output: output.Info(),
		Drop:   drop.Info(),
	}, nil
}
