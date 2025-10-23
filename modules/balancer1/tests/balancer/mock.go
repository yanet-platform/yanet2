package test_balancer

//#cgo CFLAGS: -I../
//#cgo CFLAGS: -I../../../../
//#cgo CFLAGS: -I../../../../build
//#cgo CFLAGS: -I../../../../../ -I../../../../../../lib -I../../../../../common
//#cgo LDFLAGS: -L../../../../build/modules/balancer1/tests/utils -lbalancer_test_utils
//#cgo LDFLAGS: -L../../../../build/filter -lfilter
//#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#include "utils/mock.h"
*/
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

type Mock struct {
	inner *C.struct_mock
}

func NewMock(memory uint64) (Mock, error) {
	mock, err := C.mock_create((C.size_t)(memory))
	if err != nil {
		return Mock{inner: nil}, fmt.Errorf("failed to create mock: %w", err)
	}
	if mock == nil {
		return Mock{inner: nil}, fmt.Errorf("failed to create mock")
	}
	return Mock{inner: mock}, nil
}

func FreeMock(mock *Mock) {
	if mock.inner != nil {
		C.mock_free(mock.inner)
	}
}

func (mock *Mock) CreateAgent(memory uint64) (ffi.Agent, error) {
	a, err := C.mock_create_agent(mock.inner, (C.size_t)(memory))
	if err != nil {
		return ffi.NewAgent(nil), fmt.Errorf("failed to create agent: %w", err)
	}
	if a == nil {
		return ffi.NewAgent(nil), fmt.Errorf("failed to create agent")
	}
	return ffi.NewAgent((unsafe.Pointer)(a)), nil
}
