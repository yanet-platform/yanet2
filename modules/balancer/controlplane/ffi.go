package balancer

//#cgo CFLAGS: -I../../../ -I../../../lib
//#cgo LDFLAGS: -L../../../build/modules/balancer/ -lbalancer_cp -llogging
//#cgo LDFLAGS: -L../../../build/lib/logging/ -llogging
//
//#include "api/agent.h"
//#include "modules/balancer/controlplane.h"
//#include "modules/balancer/defines.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig wraps C module configuration
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig creates a new balancer module configuration
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.balancer_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize balancer module config: %w", err)
	}
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize balancer module config: module %q not found", name)
	}

	return &ModuleConfig{
		ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

// AsFFIModule returns the module configuration as an FFI module
func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

func sliceToPtr(s []byte) *C.uint8_t {
	return (*C.uint8_t)(&s[0])
}

func (m *ModuleConfig) AddService(service Service) error {
	typ := C.uint64_t(0)
	if service.Encap {
		typ = typ | C.VS_OPT_ENCAP
	}
	if service.Addr.Is4() {
		typ = typ | C.VS_TYPE_V4
	} else {
		typ = typ | C.VS_TYPE_V6
	}

	ptr, err := C.balancer_service_config_create(
		typ,
		sliceToPtr(service.Addr.AsSlice()),
		C.uint64_t(len(service.Reals)),
		C.uint64_t(len(service.Prefixes)),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize balancer service config: %w", err)
	}
	if ptr == nil {
		return fmt.Errorf("failed to initialize module config")
	}
	defer C.balancer_service_config_free(ptr)

	for i, p := range service.Prefixes {
		addrStart := p.Addr()
		addrEnd := xnetip.LastAddr(p)

		C.balancer_service_config_set_src_prefix(
			ptr,
			C.uint64_t(i),
			sliceToPtr(addrStart.AsSlice()),
			sliceToPtr(addrEnd.AsSlice()),
		)
	}

	for i, r := range service.Reals {
		typ := C.uint64_t(0)
		if r.DstAddr.Is4() {
			typ = typ | C.RS_TYPE_V4
		} else {
			typ = typ | C.RS_TYPE_V6
		}
		C.balancer_service_config_set_real(
			ptr,
			C.uint64_t(i),
			typ,
			C.uint16_t(r.Weight),
			sliceToPtr(r.DstAddr.AsSlice()),
			sliceToPtr(r.SrcAddr.AsSlice()),
			sliceToPtr(r.SrcMask.AsSlice()),
		)
	}

	ret := C.balancer_module_config_add_service(m.asRawPtr(), ptr)
	if ret != 0 {
		return fmt.Errorf("failed to add service: unknown error code=%d", ret)
	}

	return nil
}

func (m *ModuleConfig) SetStateConfig(stateConfig StateConfig) {
	C.balancer_module_config_set_state_config(
		m.asRawPtr(),
		C.uint32_t(stateConfig.TcpSynAckTtl),
		C.uint32_t(stateConfig.TcpSynTtl),
		C.uint32_t(stateConfig.TcpFinTtl),
		C.uint32_t(stateConfig.TcpTtl),
		C.uint32_t(stateConfig.UdpTtl),
		C.uint32_t(stateConfig.DefaultTtl),
	)
}

func (m *ModuleConfig) UpdateRealWeight(
	serviceIdx int,
	realIdx int,
	weight uint16,
) error {
	ret := C.balancer_module_config_update_real_weight(
		m.asRawPtr(),
		C.uint64_t(serviceIdx),
		C.uint64_t(realIdx),
		C.uint16_t(weight),
	)
	if ret != 0 {
		return fmt.Errorf("failed to update real weight: unknown error code=%d", ret)
	}
	return nil
}
