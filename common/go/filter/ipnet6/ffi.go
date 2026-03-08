package ipnet6

//#cgo CFLAGS: -I../../../..
//#include <stdlib.h>
//#include "filter/rule.h"
import "C"

import (
	"runtime"
	"unsafe"
)

func (m *IPNet) Build() C.struct_net6 {
	res := C.struct_net6{
		addr: [16]C.uint8_t{0},
		mask: [16]C.uint8_t{0},
	}
	addr := m.Addr.AsSlice()
	mask := m.Mask.AsSlice()
	for idx := 0; idx < 16; idx++ {
		res.addr[idx] = C.uint8_t(addr[idx])
		res.mask[idx] = C.uint8_t(mask[idx])
	}

	return res
}

func (m IPNets) CBuild(pinner *runtime.Pinner) *C.struct_filter_net6s {
	if len(m) == 0 {
		return &C.struct_filter_net6s{
			items: nil,
			count: 0,
		}
	}

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

func CBuilds[T any](dst *T, m IPNets, pinner *runtime.Pinner) {
	*dst = *(*T)(unsafe.Pointer(m.CBuild(pinner)))
}
