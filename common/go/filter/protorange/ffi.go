package protorange

//#cgo CFLAGS: -I../../../..
//#include <stdlib.h>
//#include "filter/rule.h"
import "C"

import (
	"runtime"
	"unsafe"
)

func (m *ProtoRange) Build() C.struct_filter_proto_range {
	return C.struct_filter_proto_range{
		from: C.uint16_t(m.From),
		to:   C.uint16_t(m.To),
	}
}

func (m ProtoRanges) CBuild(pinner *runtime.Pinner) *C.struct_filter_proto_ranges {
	if len(m) == 0 {
		return &C.struct_filter_proto_ranges{
			items: nil,
			count: 0,
		}
	}

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

func CBuilds[T any](dst *T, m ProtoRanges, pinner *runtime.Pinner) {
	*dst = *(*T)(unsafe.Pointer(m.CBuild(pinner)))
}
