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

#cgo LDFLAGS: -Wl,-E

#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <errno.h>

#include "mock.h"
#include "config.h"

void
keep_refs(void **ptrs) {
	extern struct module *new_module_balancer(void);
	extern struct module *new_module_decap(void);
	extern struct module *new_module_dscp(void);
	extern struct module *new_module_acl(void);
	extern struct module *new_module_forward(void);
	extern struct module *new_module_route(void);
	extern struct module *new_module_nat64(void);
	extern struct module *new_module_pdump(void);

	extern struct device *new_device_plain(void);
	extern struct device *new_device_vlan(void);

	ptrs[0] = (void *)new_module_balancer;
	ptrs[1] = (void *)new_module_decap;
	ptrs[2] = (void *)new_module_dscp;
	ptrs[3] = (void *)new_module_acl;
	ptrs[4] = (void *)new_module_forward;
	ptrs[5] = (void *)new_module_route;
	ptrs[6] = (void *)new_module_nat64;
	ptrs[7] = (void *)new_module_pdump;
	ptrs[8] = (void *)new_device_plain;
	ptrs[9] = (void *)new_device_vlan;
}

*/
import "C"
import (
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"github.com/gopacket/gopacket"
	"github.com/yanet-platform/yanet2/common/go/dataplane"
	"github.com/yanet-platform/yanet2/common/go/xpacket"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

////////////////////////////////////////////////////////////////////////////////

// Never move this object in memory after
// construction!
type YanetMock struct {
	inner C.struct_yanet_mock
}

func NewYanetMock(config *YanetMockConfig) (*YanetMock, error) {
	cConfig := C.struct_yanet_mock_config{}
	C.memset(unsafe.Pointer(&cConfig), 0, C.size_t(unsafe.Sizeof(cConfig)))
	cConfig.cp_memory = C.size_t(config.CpMemory)
	cConfig.dp_memory = C.size_t(config.DpMemory)
	cConfig.device_count = C.size_t(len(config.Devices))
	cConfig.worker_count = C.size_t(config.Workers)
	for idx := range config.Devices {
		device := &config.Devices[idx]
		bytes := []byte(device.Name)
		C.memcpy(
			unsafe.Pointer(&cConfig.devices[idx].name[0]),
			unsafe.Pointer(&bytes[0]),
			C.size_t(len(device.Name)),
		)
	}

	// Allocate YanetMock on heap first, then initialize inner in-place
	yanetMock := &YanetMock{}
	ec, err := C.yanet_mock_init(&yanetMock.inner, &cConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to init mock: %w", err)
	}
	if ec != C.int(0) {
		return nil, fmt.Errorf("failed to init mock: ec=%d", ec)
	}
	return yanetMock, nil
}

func (mock *YanetMock) Free() {
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

	result, err := C.yanet_mock_handle_packets(
		&mock.inner,
		(*C.struct_packet_list)(unsafe.Pointer(packetList)),
		C.size_t(0), // use the first worker for now
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

func (mock *YanetMock) SetCurrentTime(time time.Time) {
	ts := C.struct_timespec{}
	ts.tv_sec = C.time_t(time.Unix())
	ts.tv_nsec = C.long(time.Nanosecond())
	C.yanet_mock_set_current_time(&mock.inner, &ts)
}

func (mock *YanetMock) AdvanceTime(duration time.Duration) time.Time {
	now := mock.CurrentTime()
	now = now.Add(duration)
	mock.SetCurrentTime(now)
	return now
}

func (mock *YanetMock) CurrentTime() time.Time {
	ts := C.yanet_mock_current_time(&mock.inner)
	return time.Unix(int64(ts.tv_sec), int64(ts.tv_nsec))
}
