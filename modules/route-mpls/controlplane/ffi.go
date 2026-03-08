package route_mpls

//#cgo CFLAGS: -I../../../ -I../../../lib
//#cgo LDFLAGS: -L../../../build/modules/route-mpls/api -lroute_mpls_cp
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
	"net/netip"
	"runtime"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/filter/ipnet4"
	"github.com/yanet-platform/yanet2/common/go/filter/ipnet6"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ptr, err := C.route_mpls_module_config_create((*C.struct_agent)(agent.AsRawPtr()), cName)
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

func (m *ModuleConfig) AsFFIModule() ffi.ModuleConfig {
	return m.ptr
}

// Free frees the route module configuration
func (m *ModuleConfig) Free() {
	C.route_mpls_module_config_free(m.asRawPtr())
}

type routeMPLSKind uint16

const (
	routeMPLSKindNone routeMPLSKind = iota
	routeMPLSKindTun
)

type routeMPLSNextHop struct {
	Kind        routeMPLSKind
	Source      netip.Addr
	Destination netip.Addr
	MPLSLabel   uint32
	Weight      uint64
	Counter     string
}

type routeMPLSRule struct {
	Dst4s    ipnet4.IPNets
	Dst6s    ipnet6.IPNets
	NextHops []routeMPLSNextHop
}

func (m *routeMPLSRule) CBuild(pinner *runtime.Pinner) C.struct_route_mpls_rule {
	cRule := C.struct_route_mpls_rule{}

	ipnet4.CBuilds(&cRule.net4s, m.Dst4s, pinner)
	ipnet6.CBuilds(&cRule.net6s, m.Dst6s, pinner)

	return cRule
}

func (m *ModuleConfig) Update(rules []routeMPLSRule) error {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cRules := make([]C.struct_route_mpls_rule, len(rules))

	for idx, rule := range rules {
		cNextHops := make([]C.struct_route_mpls_nexthop, len(rule.NextHops))

		for idx, nextHop := range rule.NextHops {
			cNextHops[idx].weight = C.uint64_t(nextHop.Weight)
			cCounter := C.CString(nextHop.Counter)
			C.strncpy(&cNextHops[idx].counter[0], cCounter, C.COUNTER_NAME_LEN)
			C.free(unsafe.Pointer(cCounter))

			if nextHop.Kind == routeMPLSKindNone {
				cNextHops[idx].kind = C.ROUTE_MPLS_TYPE_NONE
				continue
			}

			if nextHop.Destination.BitLen() == 32 {
				cNextHops[idx].kind = C.ROUTE_MPLS_TYPE_V4
				C.set_ip4_tunnel(&cNextHops[idx], (*C.uint8_t)(&nextHop.Source.AsSlice()[0]), (*C.uint8_t)(&nextHop.Destination.AsSlice()[0]))
			} else {
				cNextHops[idx].kind = C.ROUTE_MPLS_TYPE_V6
				C.set_ip6_tunnel(&cNextHops[idx], (*C.uint8_t)(&nextHop.Source.AsSlice()[0]), (*C.uint8_t)(&nextHop.Destination.AsSlice()[0]))
			}

			cNextHops[idx].mpls_label = C.uint32_t(nextHop.MPLSLabel)
		}

		cRule := &cRules[idx]
		ipnet4.CBuilds(&cRule.net4s, rule.Dst4s, pinner)
		ipnet6.CBuilds(&cRule.net6s, rule.Dst6s, pinner)

		pinner.Pin(&cNextHops[0])
		cRule.nexthops = &cNextHops[0]
		cRule.nexthop_count = C.uint64_t(len(cNextHops))
	}

	rc, err := C.route_mpls_module_config_update(
		m.asRawPtr(),
		&cRules[0],
		C.uint64_t(len(cRules)),
	)

	if err != nil {
		return fmt.Errorf("failed to update config %w", err)
	}
	if rc != 0 {
		return fmt.Errorf("failed to update config with return code=%d", rc)
	}
	return nil
}
