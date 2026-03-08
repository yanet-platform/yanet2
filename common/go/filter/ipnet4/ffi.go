package ipnet4

//#cgo CFLAGS: -I../../../..
//#include <stdlib.h>
//#include "filter/rule.h"
import "C"

import (
	"runtime"
	"unsafe"
)

func (m *IPNet) Build() C.struct_net4 {
	res := C.struct_net4{
		addr: [4]C.uint8_t{},
		mask: [4]C.uint8_t{},
	}
	addr := m.Addr.AsSlice()
	mask := m.Mask.AsSlice()
	for idx := 0; idx < 4; idx++ {
		res.addr[idx] = C.uint8_t(addr[idx])
		res.mask[idx] = C.uint8_t(mask[idx])
	}

	return res
}

func (m IPNets) CBuild(pinner *runtime.Pinner) *C.struct_filter_net4s {
	if len(m) == 0 {
		return &C.struct_filter_net4s{
			items: nil,
			count: 0,
		}
	}

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

func CBuilds[T any](dst *T, m IPNets, pinner *runtime.Pinner) {
	*dst = *(*T)(unsafe.Pointer(m.CBuild(pinner)))
}
