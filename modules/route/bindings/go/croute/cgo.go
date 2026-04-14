package croute

//#cgo CFLAGS: -I../../../../../ -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/route/api -lroute_cp
//
//#include "api/agent.h"
//#include "modules/route/api/controlplane.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig is an opaque handle to the route module configuration in shared
// memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig allocates a new route module configuration via the C API.
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.route_module_config_create((*C.struct_agent)(agent.AsRawPtr()), cName)
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

func (m *ModuleConfig) asRawPtr() *C.struct_cp_module {
	return (*C.struct_cp_module)(m.ptr.AsRawPtr())
}

// AsFFIModule returns the underlying common module config handle.
func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

// Free releases the underlying C memory.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (m *ModuleConfig) Free() {
	if ptr := m.asRawPtr(); ptr != nil {
		C.route_module_config_free(ptr)
		m.ptr = ffi.ModuleConfig{}
	}
}

// addRoute maps 1:1 to route_module_config_add_route.
func (m *ModuleConfig) addRoute(dstMAC [6]byte, srcMAC [6]byte, device string) (int, error) {
	cName := C.CString(device)
	defer C.free(unsafe.Pointer(cName))

	idx, err := C.route_module_config_add_route(
		m.asRawPtr(),
		*(*C.struct_ether_addr)(unsafe.Pointer(&dstMAC)),
		*(*C.struct_ether_addr)(unsafe.Pointer(&srcMAC)),
		cName,
	)
	if err != nil {
		return -1, fmt.Errorf("route_module_config_add_route: %w", err)
	}
	if idx < 0 {
		return -1, fmt.Errorf("route_module_config_add_route: unknown error")
	}

	return int(idx), nil
}

// addRouteList maps 1:1 to route_module_config_add_route_list.
func (m *ModuleConfig) addRouteList(indices []uint32) (int, error) {
	cIndices := make([]C.uint32_t, len(indices))
	for i, v := range indices {
		cIndices[i] = C.uint32_t(v)
	}

	idx, err := C.route_module_config_add_route_list(
		m.asRawPtr(),
		C.size_t(len(indices)),
		&cIndices[0],
	)
	if err != nil {
		return -1, fmt.Errorf("route_module_config_add_route_list: %w", err)
	}
	if idx < 0 {
		return -1, fmt.Errorf("route_module_config_add_route_list: unknown error")
	}

	return int(idx), nil
}

// addPrefixV4 maps 1:1 to route_module_config_add_prefix_v4.
func (m *ModuleConfig) addPrefixV4(from [4]byte, to [4]byte, routeListIndex uint32) error {
	if rc := C.route_module_config_add_prefix_v4(
		m.asRawPtr(),
		(*C.uint8_t)(&from[0]),
		(*C.uint8_t)(&to[0]),
		C.uint32_t(routeListIndex),
	); rc != 0 {
		return fmt.Errorf("route_module_config_add_prefix_v4: error code=%d", rc)
	}
	return nil
}

// addPrefixV6 maps 1:1 to route_module_config_add_prefix_v6.
func (m *ModuleConfig) addPrefixV6(from [16]byte, to [16]byte, routeListIndex uint32) error {
	if rc := C.route_module_config_add_prefix_v6(
		m.asRawPtr(),
		(*C.uint8_t)(&from[0]),
		(*C.uint8_t)(&to[0]),
		C.uint32_t(routeListIndex),
	); rc != 0 {
		return fmt.Errorf("route_module_config_add_prefix_v6: error code=%d", rc)
	}
	return nil
}

// fibIter wraps the C fib_iter handle.
type fibIter struct {
	ptr *C.struct_fib_iter
}

// newFIBIter maps 1:1 to fib_iter_create.
func newFIBIter(config *ModuleConfig) (*fibIter, error) {
	ptr := C.fib_iter_create(config.asRawPtr())
	if ptr == nil {
		return nil, fmt.Errorf("fib_iter_create: allocation failure")
	}
	return &fibIter{ptr: ptr}, nil
}

// destroy maps 1:1 to fib_iter_destroy.
func (m *fibIter) destroy() {
	C.fib_iter_destroy(m.ptr)
}

// next maps 1:1 to fib_iter_next.
func (m *fibIter) next() bool {
	return bool(C.fib_iter_next(m.ptr))
}

// addressFamily maps 1:1 to fib_iter_address_family.
func (m *fibIter) addressFamily() uint8 {
	return uint8(C.fib_iter_address_family(m.ptr))
}

// prefixFrom maps 1:1 to fib_iter_prefix_from.
// Returns a pointer to 4 (IPv4) or 16 (IPv6) bytes.
func (m *fibIter) prefixFrom() unsafe.Pointer {
	return unsafe.Pointer(C.fib_iter_prefix_from(m.ptr))
}

// prefixTo maps 1:1 to fib_iter_prefix_to.
// Returns a pointer to 4 (IPv4) or 16 (IPv6) bytes.
func (m *fibIter) prefixTo() unsafe.Pointer {
	return unsafe.Pointer(C.fib_iter_prefix_to(m.ptr))
}

// nexthopCount maps 1:1 to fib_iter_nexthop_count.
func (m *fibIter) nexthopCount() uint64 {
	return uint64(C.fib_iter_nexthop_count(m.ptr))
}

// nexthopDstMAC maps 1:1 to fib_iter_nexthop_dst_mac.
func (m *fibIter) nexthopDstMAC(idx uint64) [6]byte {
	var mac C.struct_ether_addr
	C.fib_iter_nexthop_dst_mac(m.ptr, C.uint64_t(idx), &mac)
	return *(*[6]byte)(unsafe.Pointer(&mac.addr[0]))
}

// nexthopSrcMAC maps 1:1 to fib_iter_nexthop_src_mac.
func (m *fibIter) nexthopSrcMAC(idx uint64) [6]byte {
	var mac C.struct_ether_addr
	C.fib_iter_nexthop_src_mac(m.ptr, C.uint64_t(idx), &mac)
	return *(*[6]byte)(unsafe.Pointer(&mac.addr[0]))
}

// nexthopDeviceName maps 1:1 to fib_iter_nexthop_device_name.
func (m *fibIter) nexthopDeviceName(idx uint64) string {
	return C.GoString(C.fib_iter_nexthop_device_name(m.ptr, C.uint64_t(idx)))
}
