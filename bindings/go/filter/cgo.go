package filter

//#cgo CFLAGS: -I../../..
//#include <stdlib.h>
//#include <string.h>
//#include "filter/rule.h"
import "C"

import (
	"runtime"
)

func (m *Device) build() C.struct_filter_device {
	var name [C.ACL_DEVICE_NAME_LEN]C.char

	for idx := 0; idx < len(m.Name) && idx < len(name)-1; idx++ {
		name[idx] = C.char(m.Name[idx])
	}

	return C.struct_filter_device{
		id:   0,
		name: name,
	}
}

func (m Devices) cBuild(pinner *runtime.Pinner) *C.struct_filter_devices {
	if len(m) == 0 {
		return &C.struct_filter_devices{
			items: nil,
			count: 0,
		}
	}

	cDevices := make([]C.struct_filter_device, len(m))
	for idx, item := range m {
		cDevices[idx] = item.build()
	}

	pinner.Pin(&cDevices[0])
	return &C.struct_filter_devices{
		items: (*C.struct_filter_device)(&cDevices[0]),
		count: C.uint32_t(len(cDevices)),
	}
}

func (m *IPNet) buildNet4() C.struct_net4 {
	res := C.struct_net4{
		addr: [4]C.uint8_t{},
		mask: [4]C.uint8_t{},
	}
	addr := m.Addr.As4()
	mask := m.Mask.As4()
	for idx := 0; idx < 4; idx++ {
		res.addr[idx] = C.uint8_t(addr[idx])
		res.mask[idx] = C.uint8_t(mask[idx])
	}

	return res
}

func (m IPNets) cBuildNet4s(pinner *runtime.Pinner) *C.struct_filter_net4s {
	if len(m) == 0 {
		return &C.struct_filter_net4s{
			items: nil,
			count: 0,
		}
	}

	cNet4s := make([]C.struct_net4, len(m))
	for idx, item := range m {
		cNet4s[idx] = item.buildNet4()
	}

	pinner.Pin(&cNet4s[0])
	return &C.struct_filter_net4s{
		items: (*C.struct_net4)(&cNet4s[0]),
		count: C.uint32_t(len(cNet4s)),
	}
}

func (m *IPNet) buildNet6() C.struct_net6 {
	res := C.struct_net6{
		addr: [16]C.uint8_t{0},
		mask: [16]C.uint8_t{0},
	}
	addr := m.Addr.As16()
	mask := m.Mask.As16()
	for idx := 0; idx < 16; idx++ {
		res.addr[idx] = C.uint8_t(addr[idx])
		res.mask[idx] = C.uint8_t(mask[idx])
	}

	return res
}

func (m IPNets) cBuildNet6s(pinner *runtime.Pinner) *C.struct_filter_net6s {
	if len(m) == 0 {
		return &C.struct_filter_net6s{
			items: nil,
			count: 0,
		}
	}

	cNet6s := make([]C.struct_net6, len(m))
	for idx, item := range m {
		cNet6s[idx] = item.buildNet6()
	}

	pinner.Pin(&cNet6s[0])
	return &C.struct_filter_net6s{
		items: (*C.struct_net6)(&cNet6s[0]),
		count: C.uint32_t(len(cNet6s)),
	}
}

func (m *PortRange) build() C.struct_filter_port_range {
	return C.struct_filter_port_range{
		from: C.uint16_t(m.From),
		to:   C.uint16_t(m.To),
	}
}

func (m PortRanges) cBuild(pinner *runtime.Pinner) *C.struct_filter_port_ranges {
	if len(m) == 0 {
		return &C.struct_filter_port_ranges{
			items: nil,
			count: 0,
		}
	}

	cPortRanges := make([]C.struct_filter_port_range, len(m))
	for idx, item := range m {
		cPortRanges[idx] = item.build()
	}

	pinner.Pin(&cPortRanges[0])
	return &C.struct_filter_port_ranges{
		items: (*C.struct_filter_port_range)(&cPortRanges[0]),
		count: C.uint32_t(len(cPortRanges)),
	}
}

func (m *ProtoRange) build() C.struct_filter_proto_range {
	return C.struct_filter_proto_range{
		from: C.uint16_t(m.From),
		to:   C.uint16_t(m.To),
	}
}

func (m ProtoRanges) cBuild(pinner *runtime.Pinner) *C.struct_filter_proto_ranges {
	if len(m) == 0 {
		return &C.struct_filter_proto_ranges{
			items: nil,
			count: 0,
		}
	}

	cProtoRanges := make([]C.struct_filter_proto_range, len(m))
	for idx, item := range m {
		cProtoRanges[idx] = item.build()
	}

	pinner.Pin(&cProtoRanges[0])
	return &C.struct_filter_proto_ranges{
		items: (*C.struct_filter_proto_range)(&cProtoRanges[0]),
		count: C.uint32_t(len(cProtoRanges)),
	}
}

func (m *VlanRange) build() C.struct_filter_vlan_range {
	return C.struct_filter_vlan_range{
		from: C.uint16_t(m.From),
		to:   C.uint16_t(m.To),
	}
}

func (m VlanRanges) cBuild(pinner *runtime.Pinner) *C.struct_filter_vlan_ranges {
	if len(m) == 0 {
		return &C.struct_filter_vlan_ranges{
			items: nil,
			count: 0,
		}
	}

	cVlanRanges := make([]C.struct_filter_vlan_range, len(m))
	for idx, item := range m {
		cVlanRanges[idx] = item.build()
	}

	pinner.Pin(&cVlanRanges[0])
	return &C.struct_filter_vlan_ranges{
		items: (*C.struct_filter_vlan_range)(&cVlanRanges[0]),
		count: C.uint32_t(len(cVlanRanges)),
	}
}
