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

func getSlicePtr[T any](slice []T, idx int, count int, pinner runtime.Pinner) *T {
	if count == 0 {
		return nil
	}
	pinner.Pin(&slice[idx])
	return &slice[idx]
}

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

// C filter attribute item types
type CDevice C.struct_filter_device
type CVlanRange C.struct_filter_vlan_range
type CNet4 C.struct_net4
type CNet6 C.struct_net6
type CProtoRange C.struct_filter_proto_range
type CPortRange C.struct_filter_port_range

// C filter attribute types
type CDevices C.struct_filter_devices
type CVlanRanges C.struct_filter_vlan_ranges
type CNet4s C.struct_filter_net4s
type CNet6s C.struct_filter_net6s
type CProtoRanges C.struct_filter_proto_ranges
type CPortRanges C.struct_filter_port_ranges

/*
The interface is used to set filter attribute inside output result set.
`set` function accepts a slice of attribute items and sets corresponding
pointer and count fields inside C-representation of filter attribute.
Item values should not be relocated until attribute processing is done.

Pinner is provided in order to pin memory address and allow GO pointers
deep inside CGO calls.
*/
type ICAttr[T any] interface {
	set([]T, *runtime.Pinner)
}

// Supported filter attribute `set` handlers
func (m *CDevices) set(values []CDevice, pinner *runtime.Pinner) {
	if len(values) > 0 {
		pinner.Pin(&values[0])
		m.items = (*C.struct_filter_device)(&values[0])
		m.count = C.uint32_t(len(values))
	} else {
		m.items = nil
		m.count = 0
	}
}

func (m *CVlanRanges) set(values []CVlanRange, pinner *runtime.Pinner) {
	if len(values) > 0 {
		pinner.Pin(&values[0])
		m.items = (*C.struct_filter_vlan_range)(&values[0])
		m.count = C.uint32_t(len(values))
	} else {
		m.items = nil
		m.count = 0
	}
}

func (m *CNet4s) set(values []CNet4, pinner *runtime.Pinner) {
	if len(values) > 0 {
		pinner.Pin(&values[0])
		m.items = (*C.struct_net4)(&values[0])
		m.count = C.uint32_t(len(values))
	} else {
		m.items = nil
		m.count = 0
	}
}

func (m *CNet6s) set(values []CNet6, pinner *runtime.Pinner) {
	if len(values) > 0 {
		pinner.Pin(&values[0])
		m.items = (*C.struct_net6)(&values[0])
		m.count = C.uint32_t(len(values))
	} else {
		m.items = nil
		m.count = 0
	}
}

func (m *CProtoRanges) set(values []CProtoRange, pinner *runtime.Pinner) {
	if len(values) > 0 {
		pinner.Pin(&values[0])
		m.items = (*C.struct_filter_proto_range)(&values[0])
		m.count = C.uint32_t(len(values))
	} else {
		m.items = nil
		m.count = 0
	}
}

func (m *CPortRanges) set(values []CPortRange, pinner *runtime.Pinner) {
	if len(values) > 0 {
		pinner.Pin(&values[0])
		m.items = (*C.struct_filter_port_range)(&values[0])
		m.count = C.uint32_t(len(values))
	} else {
		m.items = nil
		m.count = 0
	}
}

/*
Each rule contains set of attributes, an attribute is a list of enabled
values.  The interface requires a filter attribute item to  build its
C representation.
*/
type IAttrItem[T any] interface {
	build() T
}

func (m *Device) build() CDevice {
	return CDevice{
		id:   0,
		name: deviceToChar(string(m.Name)),
	}
}

func (m *VlanRange) build() CVlanRange {
	return CVlanRange{
		from: C.uint16_t(m.From),
		to:   C.uint16_t(m.To),
	}
}

func (m *IPNet4) build() CNet4 {
	return CNet4{
		addr: [4]C.uint8_t{0, 0, 0, 0},
		mask: [4]C.uint8_t{0, 0, 0, 0},
	}
}

func (m *IPNet6) build() CNet6 {
	return CNet6{
		addr: [16]C.uint8_t{0, 0, 0, 0},
		mask: [16]C.uint8_t{0, 0, 0, 0},
	}
}

func (m *ProtoRange) build() CProtoRange {
	return CProtoRange{
		from: C.uint16_t(m.From),
		to:   C.uint16_t(m.To),
	}
}

func (m *PortRange) build() CPortRange {
	return CPortRange{
		from: C.uint16_t(m.From),
		to:   C.uint16_t(m.To),
	}
}

func SetupFilterAttr[T any](
	count int,
	getItem func(ruleIdx int, itemIdx int) IAttrItem[T],
	setAttr func(ruleIdx int) ICAttr[T],
	pinner *runtime.Pinner,
) {
	itemCount := 0
	for attrIdx := 0; attrIdx < count; attrIdx++ {
		itemIdx := 0
		for getItem(attrIdx, itemIdx) != nil {
			itemIdx++
		}

		itemCount = itemCount + itemIdx
	}

	cAttrItems := make([]T, itemCount)
	cAttrItemIdx := 0

	for attrIdx := 0; attrIdx < count; attrIdx++ {
		itemIdx := 0
		item := getItem(attrIdx, itemIdx)
		for item != nil {
			cAttrItems[cAttrItemIdx+itemIdx] = item.build()
			itemIdx++
			item = getItem(attrIdx, itemIdx)
		}

		setAttr(attrIdx).set(cAttrItems[cAttrItemIdx:cAttrItemIdx+itemIdx], pinner)

		cAttrItemIdx = cAttrItemIdx + itemIdx

	}
}
