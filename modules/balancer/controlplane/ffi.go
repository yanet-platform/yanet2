package balancer

//#cgo CFLAGS: -I../../../ -I../../../lib -I../../../build
//#cgo LDFLAGS: -L../../../build/modules/balancer/ -lbalancer_cp
//#cgo LDFLAGS: -L../../../build/lib/logging/ -llogging
//#cgo LDFLAGS: -L../../../build/filter -lfilter
//
//#include "api/agent.h"
//#include "modules/balancer/controlplane.h"
//#include <netinet/ip.h>
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

type PersistentStatePtr struct {
	inner *C.struct_balancer_state
}

// ModuleConfig wraps C module configuration
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

func NewPersistentState(agent *ffi.Agent, sessionsToReserve uint64) (*PersistentStatePtr, error) {
	state := &PersistentStatePtr{}
	res, err := C.balancer_state_init((*C.struct_agent)(agent.AsRawPtr()), C.uint64_t(sessionsToReserve))
	if err != nil {
		return nil, fmt.Errorf("failed to initialize balancer persistent state: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("failed to initialize balancer persistent state, null pointer returned")
	}
	state.inner = (*C.struct_balancer_state)(res)
	return state, nil
}

// NewModuleConfig creates a new balancer module configuration
func NewModuleConfig(agent *ffi.Agent, persistentState *PersistentStatePtr, name string) (*ModuleConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.balancer_module_config_init((*C.struct_agent)(agent.AsRawPtr()), (*C.struct_balancer_state)(persistentState.inner), cName)
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

func (proto *ServiceProto) asInt() C.uint8_t {
	if *proto == ServiceProtoUdp {
		return C.IPPROTO_UDP
	} else {
		return C.IPPROTO_TCP
	}
}

func (m *ModuleConfig) AddService(service Service) error {
	flags := C.balancer_vs_flags_t(0)
	if service.GRE {
		flags |= C.BALANCER_VS_GRE_FLAG
	}
	if service.FixMss {
		flags |= C.BALANCER_VS_FIX_MSS_FLAG
	}
	if service.OnePacketScheduler {
		flags |= C.BALANCER_VS_OPS_FLAG
	}

	ptr, err := C.balancer_service_config_create(
		flags,
		sliceToPtr(service.Addr.AsSlice()),
		C.uint16_t(service.Port),
		service.Proto.asInt(),
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
		flags := C.balancer_rs_flags_t(0)
		if r.DstAddr.Is6() {
			flags = flags | C.BALANCER_RS_IPV6_FLAG
		}
		C.balancer_service_config_set_real(
			ptr,
			C.uint64_t(i),
			flags,
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

func (m *ModuleConfig) SetStateConfig(timeouts Timeouts) {
	C.balancer_module_config_set_timeouts(
		m.asRawPtr(),
		C.uint32_t(timeouts.TcpSynAckTtl),
		C.uint32_t(timeouts.TcpSynTtl),
		C.uint32_t(timeouts.TcpFinTtl),
		C.uint32_t(timeouts.TcpTtl),
		C.uint32_t(timeouts.UdpTtl),
		C.uint32_t(timeouts.DefaultTtl),
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
