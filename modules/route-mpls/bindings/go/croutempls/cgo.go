package croutempls

//#cgo CFLAGS: -I../../../../../ -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/route-mpls/api -lroute_mpls_cp
//#cgo LDFLAGS: -L../../../../../build/lib/filter -lfilter_compiler
//
//#include <errno.h>
//
//#include "api/agent.h"
//#include "modules/route-mpls/api/controlplane.h"
//
//static inline void
//set_ip4_tunnel(struct route_mpls_nexthop *nexthop, const uint8_t *src, const uint8_t *dst) {
//	memcpy(&nexthop->ip4_tunnel.src, src, 4);
//	memcpy(&nexthop->ip4_tunnel.dst, dst, 4);
//}
//
//static inline void
//set_ip6_tunnel(struct route_mpls_nexthop *nexthop, const uint8_t *src, const uint8_t *dst) {
//	memcpy(&nexthop->ip6_tunnel.src, src, 16);
//	memcpy(&nexthop->ip6_tunnel.dst, dst, 16);
//}
import "C"

import (
	"fmt"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig is an opaque handle to the route-mpls module configuration in
// shared memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig allocates a new route-mpls module configuration via the C API.
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.route_mpls_module_config_new((*C.struct_agent)(agent.AsRawPtr()), cName, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf("failed to initialize module config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
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
		C.route_mpls_module_config_free(ptr)
		m.ptr = ffi.ModuleConfig{}
	}
}

// Update marshals rules into C structs and calls route_mpls_module_config_update.
func (m *ModuleConfig) Update(rules []Rule) error {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cRules := make([]C.struct_route_mpls_rule, len(rules))

	for idx, rule := range rules {
		cNextHops := make([]C.struct_route_mpls_nexthop, len(rule.Nexthops))

		for jdx, nexthop := range rule.Nexthops {
			cNextHops[jdx].weight = C.uint64_t(nexthop.Weight)
			cCounter := C.CString(nexthop.Counter)
			C.strncpy(&cNextHops[jdx].counter[0], cCounter, C.COUNTER_NAME_LEN)
			C.free(unsafe.Pointer(cCounter))

			if nexthop.Kind == KindNone {
				cNextHops[jdx].kind = C.ROUTE_MPLS_TYPE_NONE
				continue
			}

			if nexthop.Destination.BitLen() == 32 {
				cNextHops[jdx].kind = C.ROUTE_MPLS_TYPE_V4
				C.set_ip4_tunnel(&cNextHops[jdx], (*C.uint8_t)(&nexthop.Source.AsSlice()[0]), (*C.uint8_t)(&nexthop.Destination.AsSlice()[0]))
			} else {
				cNextHops[jdx].kind = C.ROUTE_MPLS_TYPE_V6
				C.set_ip6_tunnel(&cNextHops[jdx], (*C.uint8_t)(&nexthop.Source.AsSlice()[0]), (*C.uint8_t)(&nexthop.Destination.AsSlice()[0]))
			}

			cNextHops[jdx].mpls_label = C.uint32_t(nexthop.MPLSLabel)
		}

		cRule := &cRules[idx]
		filter.CBuildNet4s(&cRule.net4s, rule.Dst4s, pinner)
		filter.CBuildNet6s(&cRule.net6s, rule.Dst6s, pinner)

		pinner.Pin(&cNextHops[0])
		cRule.nexthops = &cNextHops[0]
		cRule.nexthop_count = C.uint64_t(len(cNextHops))
	}

	var cErr *C.yanet_error
	rc := C.route_mpls_module_config_update(
		m.asRawPtr(),
		&cRules[0],
		C.uint64_t(len(cRules)),
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf("failed to update config: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return nil
}
