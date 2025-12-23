package filter

//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../
//#cgo CFLAGS: -I../../../build
//#cgo CFLAGS: -I../../../ -I../../../lib -I../../../common
//
//#include <stdlib.h>
//#include "common/strutils.h"
//#include "filter/rule.h"
import "C"

import (
	"runtime"
	"unsafe"
)

func deviceToChar(str string) [C.ACL_DEVICE_NAME_LEN]C.char {
	var result [C.ACL_DEVICE_NAME_LEN]C.char

	copyLen := len(str)
	if copyLen >= len(result) {
		copyLen = len(result) - 1
	}
	C.memcpy(
		unsafe.Pointer(&result[0]),
		unsafe.Pointer(unsafe.StringData(str)),
		C.size_t(copyLen),
	)
	result[copyLen] = 0

	return result
}

func (m *Device) Build() C.struct_filter_device {
	return C.struct_filter_device{
		id:   0,
		name: deviceToChar(string(m.Name)),
	}
}

func (m *VlanRange) Build() C.struct_filter_vlan_range {
	return C.struct_filter_vlan_range{
		from: C.uint16_t(m.From),
		to:   C.uint16_t(m.To),
	}
}

func (m *IPNet4) Build() C.struct_net4 {
	return C.struct_net4{
		addr: [4]C.uint8_t{0, 0, 0, 0},
		mask: [4]C.uint8_t{0, 0, 0, 0},
	}
}

func (m *IPNet6) Build() C.struct_net6 {
	return C.struct_net6{
		addr: [16]C.uint8_t{0, 0, 0, 0},
		mask: [16]C.uint8_t{0, 0, 0, 0},
	}
}

func (m *ProtoRange) Build() C.struct_filter_proto_range {
	return C.struct_filter_proto_range{
		from: C.uint16_t(m.From),
		to:   C.uint16_t(m.To),
	}
}

func (m *PortRange) Build() C.struct_filter_port_range {
	return C.struct_filter_port_range{
		from: C.uint16_t(m.From),
		to:   C.uint16_t(m.To),
	}
}

func (m Devices) CBuild(pinner *runtime.Pinner) *C.struct_filter_devices {
	cDevices := make([]C.struct_filter_device, len(m))
	for idx, item := range m {
		cDevices[idx] = item.Build()
	}

	pinner.Pin(&cDevices[0])
	return &C.struct_filter_devices{
		items: (*C.struct_filter_device)(&cDevices[0]),
		count: C.uint32_t(len(cDevices)),
	}
}

func (m VlanRanges) CBuild(pinner *runtime.Pinner) *C.struct_filter_vlan_ranges {
	cVlanRanges := make([]C.struct_filter_vlan_range, len(m))
	for idx, item := range m {
		cVlanRanges[idx] = item.Build()
	}

	pinner.Pin(&cVlanRanges[0])
	return &C.struct_filter_vlan_ranges{
		items: (*C.struct_filter_vlan_range)(&cVlanRanges[0]),
		count: C.uint32_t(len(cVlanRanges)),
	}
}

func (m IPNet4s) CBuild(pinner *runtime.Pinner) *C.struct_filter_net4s {
	cNet4s := make([]C.struct_net4, len(m))
	for idx, item := range m {
		cNet4s[idx] = item.Build()
	}

	pinner.Pin(&cNet4s[0])
	return &C.struct_filter_net4s{
		items: (*C.struct_net4)(&cNet4s[0]),
		count: C.uint32_t(len(cNet4s)),
	}
}

func (m IPNet6s) CBuild(pinner *runtime.Pinner) *C.struct_filter_net6s {
	cNet6s := make([]C.struct_net6, len(m))
	for idx, item := range m {
		cNet6s[idx] = item.Build()
	}

	pinner.Pin(&cNet6s[0])
	return &C.struct_filter_net6s{
		items: (*C.struct_net6)(&cNet6s[0]),
		count: C.uint32_t(len(cNet6s)),
	}
}

func (m ProtoRanges) CBuild(pinner *runtime.Pinner) *C.struct_filter_proto_ranges {
	cProtoRanges := make([]C.struct_filter_proto_range, len(m))
	for idx, item := range m {
		cProtoRanges[idx] = item.Build()
	}

	pinner.Pin(&cProtoRanges[0])
	return &C.struct_filter_proto_ranges{
		items: (*C.struct_filter_proto_range)(&cProtoRanges[0]),
		count: C.uint32_t(len(cProtoRanges)),
	}
}

func (m PortRanges) CBuild(pinner *runtime.Pinner) *C.struct_filter_port_ranges {
	cPortRanges := make([]C.struct_filter_port_range, len(m))
	for idx, item := range m {
		cPortRanges[idx] = item.Build()
	}

	pinner.Pin(&cPortRanges[0])
	return &C.struct_filter_port_ranges{
		items: (*C.struct_filter_port_range)(&cPortRanges[0]),
		count: C.uint32_t(len(cPortRanges)),
	}
}
