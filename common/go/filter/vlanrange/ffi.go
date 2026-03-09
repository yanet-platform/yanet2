package vlanrange

//#cgo CFLAGS: -I../../../..
//#include <stdlib.h>
//#include "filter/rule.h"
import "C"

import (
	"runtime"
	"unsafe"
)

func (m *VlanRange) Build() C.struct_filter_vlan_range {
	return C.struct_filter_vlan_range{
		from: C.uint16_t(m.From),
		to:   C.uint16_t(m.To),
	}
}

func (m VlanRanges) CBuild(pinner *runtime.Pinner) *C.struct_filter_vlan_ranges {
	if len(m) == 0 {
		return &C.struct_filter_vlan_ranges{
			items: nil,
			count: 0,
		}
	}

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

func CBuilds[T any](dst *T, m VlanRanges, pinner *runtime.Pinner) {
	*dst = *(*T)(unsafe.Pointer(m.CBuild(pinner)))
}
