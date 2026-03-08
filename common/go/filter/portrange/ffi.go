package portrange

//#cgo CFLAGS: -I../../../..
//#include <stdlib.h>
//#include "filter/rule.h"
import "C"

import (
	"runtime"
	"unsafe"
)

func (m *PortRange) Build() C.struct_filter_port_range {
	return C.struct_filter_port_range{
		from: C.uint16_t(m.From),
		to:   C.uint16_t(m.To),
	}
}

func (m PortRanges) CBuild(pinner *runtime.Pinner) *C.struct_filter_port_ranges {
	if len(m) == 0 {
		return &C.struct_filter_port_ranges{
			items: nil,
			count: 0,
		}
	}

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

func CBuilds[T any](dst *T, m PortRanges, pinner *runtime.Pinner) {
	*dst = *(*T)(unsafe.Pointer(m.CBuild(pinner)))
}
