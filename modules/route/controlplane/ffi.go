package route

//#cgo CFLAGS: -I../../../ -I../../../lib
//#cgo LDFLAGS: -L../../../build/modules/route/api -lroute_cp
//
//#include "api/agent.h"
//#include "modules/route/api/controlplane.h"
import "C"

import (
	"fmt"
	"net"
	"net/netip"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.route_module_config_init((*C.struct_agent)(agent.AsRawPtr()), cName)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize module config: %w", err)
	}
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize module config: module %q not found", name)
	}

	return &ModuleConfig{
		ptr: ffi.NewModuleConfig(unsafe.Pointer(ptr)),
	}, nil
}

func (m *ModuleConfig) asRawPtr() *C.struct_module_data {
	return (*C.struct_module_data)(m.ptr.AsRawPtr())
}

func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

func (m *ModuleConfig) RouteAdd(srcAddr net.HardwareAddr, dstAddr net.HardwareAddr) (int, error) {
	if len(srcAddr) != 6 {
		return -1, fmt.Errorf("unsupported source MAC address: must be EUI-48")
	}
	if len(dstAddr) != 6 {
		return -1, fmt.Errorf("unsupported destination MAC address: must be EUI-48")
	}

	idx, err := C.route_module_config_add_route(
		m.asRawPtr(),
		*(*C.struct_ether_addr)(unsafe.Pointer(&dstAddr[0])),
		*(*C.struct_ether_addr)(unsafe.Pointer(&srcAddr[0])),
	)
	if err != nil {
		return -1, fmt.Errorf("failed to add route: %w", err)
	}
	if idx < 0 {
		return -1, fmt.Errorf("failed to add route: unknown error")
	}

	return int(idx), nil
}

func (m *ModuleConfig) RouteListAdd(routeIndices []uint32) (int, error) {
	cRouteIndices := make([]C.uint32_t, len(routeIndices))
	for idx, v := range routeIndices {
		cRouteIndices[idx] = C.uint32_t(v)
	}

	idx, err := C.route_module_config_add_route_list((*C.struct_module_data)(m.ptr.AsRawPtr()), C.size_t(len(routeIndices)), &cRouteIndices[0])
	if err != nil {
		return -1, fmt.Errorf("failed to add route list: %w", err)
	}
	if idx < 0 {
		return -1, fmt.Errorf("failed to add route list: unknown error")
	}

	return int(idx), nil
}

func (m *ModuleConfig) PrefixAdd(prefix netip.Prefix, routeListIdx uint32) error {
	addrStart := prefix.Addr()
	addrEnd := xnetip.LastAddr(prefix)

	if addrStart.Is4() {
		return m.prefixAdd4(addrStart.As4(), addrEnd.As4(), routeListIdx)
	}
	if addrStart.Is6() {
		return m.prefixAdd6(addrStart.As16(), addrEnd.As16(), routeListIdx)
	}
	return fmt.Errorf("unsupported prefix: must be either IPv4 or IPv6")
}

func (m *ModuleConfig) prefixAdd4(addrStart [4]byte, addrEnd [4]byte, routeListIdx uint32) error {
	if rc := C.route_module_config_add_prefix_v4(
		m.asRawPtr(),
		(*C.uint8_t)(&addrStart[0]),
		(*C.uint8_t)(&addrEnd[0]),
		C.uint32_t(routeListIdx),
	); rc != 0 {
		return fmt.Errorf("failed to add v4 prefix: unknown error code=%d", rc)
	}
	return nil
}

func (m *ModuleConfig) prefixAdd6(addrStart [16]byte, addrEnd [16]byte, routeListIdx uint32) error {
	if rc := C.route_module_config_add_prefix_v6(
		m.asRawPtr(),
		(*C.uint8_t)(&addrStart[0]),
		(*C.uint8_t)(&addrEnd[0]),
		C.uint32_t(routeListIdx),
	); rc != 0 {
		return fmt.Errorf("failed to add v6 prefix: unknown error code=%d", rc)
	}
	return nil
}
