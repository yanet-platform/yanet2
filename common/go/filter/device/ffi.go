package device

//#cgo CFLAGS: -I../../../..
//#include <stdlib.h>
//#include <string.h>
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

func (m Devices) CBuild(pinner *runtime.Pinner) *C.struct_filter_devices {
	if len(m) == 0 {
		return &C.struct_filter_devices{
			items: nil,
			count: 0,
		}
	}

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

func CBuilds[T any](dst *T, m Devices, pinner *runtime.Pinner) {
	*dst = *(*T)(unsafe.Pointer(m.CBuild(pinner)))
}
