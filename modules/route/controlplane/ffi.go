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

const (
	addressFamilyIPv4 = 4
	addressFamilyIPv6 = 6
)

type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

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

func (m *ModuleConfig) RouteAdd(srcAddr net.HardwareAddr, dstAddr net.HardwareAddr, device string) (int, error) {
	if len(srcAddr) != 6 {
		return -1, fmt.Errorf("unsupported source MAC address: must be EUI-48")
	}
	if len(dstAddr) != 6 {
		return -1, fmt.Errorf("unsupported destination MAC address: must be EUI-48")
	}
	if device == "" {
		return -1, fmt.Errorf("device name is required")
	}

	cName := C.CString(device)
	defer C.free(unsafe.Pointer(cName))

	idx, err := C.route_module_config_add_route(
		m.asRawPtr(),
		*(*C.struct_ether_addr)(unsafe.Pointer(&dstAddr[0])),
		*(*C.struct_ether_addr)(unsafe.Pointer(&srcAddr[0])),
		cName,
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
	if len(routeIndices) == 0 {
		return -1, fmt.Errorf("failed to add route list: routeIndices must not be empty")
	}

	cRouteIndices := make([]C.uint32_t, len(routeIndices))
	for idx, v := range routeIndices {
		cRouteIndices[idx] = C.uint32_t(v)
	}

	idx, err := C.route_module_config_add_route_list(
		m.asRawPtr(),
		C.size_t(len(routeIndices)),
		&cRouteIndices[0],
	)
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

// FIBNexthop represents a single ECMP nexthop in the FIB.
type FIBNexthop struct {
	// DstMAC is the destination MAC address.
	DstMAC net.HardwareAddr
	// SrcMAC is the source MAC address.
	SrcMAC net.HardwareAddr
	// Device is the egress device name.
	Device string
}

// FIBEntry represents a single FIB prefix with its nexthops.
type FIBEntry struct {
	// AddressFamily is 4 for IPv4 or 6 for IPv6.
	AddressFamily uint8
	// PrefixFrom is the start of the prefix range.
	PrefixFrom netip.Addr
	// PrefixTo is the end of the prefix range.
	PrefixTo netip.Addr
	// Nexthops contains the ECMP nexthops for this prefix.
	Nexthops []FIBNexthop
}

// DumpFIB reads the Forwarding Information Base from shared memory using a
// zero-copy iterator.
func (m *ModuleConfig) DumpFIB() ([]FIBEntry, error) {
	it := C.fib_iter_create(m.asRawPtr())
	if it == nil {
		return nil, fmt.Errorf("failed to create FIB iterator")
	}
	defer C.fib_iter_destroy(it)

	var entries []FIBEntry

	for C.fib_iter_next(it) {
		af := uint8(C.fib_iter_address_family(it))

		from := C.fib_iter_prefix_from(it)
		to := C.fib_iter_prefix_to(it)

		var prefixFrom, prefixTo netip.Addr
		switch af {
		case addressFamilyIPv4:
			prefixFrom = netip.AddrFrom4(*(*[4]byte)(unsafe.Pointer(from)))
			prefixTo = netip.AddrFrom4(*(*[4]byte)(unsafe.Pointer(to)))
		case addressFamilyIPv6:
			prefixFrom = netip.AddrFrom16(*(*[16]byte)(unsafe.Pointer(from)))
			prefixTo = netip.AddrFrom16(*(*[16]byte)(unsafe.Pointer(to)))
		default:
			continue
		}

		nhCount := int(C.fib_iter_nexthop_count(it))
		nexthops := make([]FIBNexthop, nhCount)

		for i := range nhCount {
			idx := C.uint64_t(i)

			var dstMAC, srcMAC C.struct_ether_addr
			C.fib_iter_nexthop_dst_mac(it, idx, &dstMAC)
			C.fib_iter_nexthop_src_mac(it, idx, &srcMAC)

			dst := net.HardwareAddr(C.GoBytes(unsafe.Pointer(&dstMAC.addr[0]), 6))
			src := net.HardwareAddr(C.GoBytes(unsafe.Pointer(&srcMAC.addr[0]), 6))

			nexthops[i] = FIBNexthop{
				DstMAC: dst,
				SrcMAC: src,
				Device: C.GoString(C.fib_iter_nexthop_device_name(it, idx)),
			}
		}

		entries = append(entries, FIBEntry{
			AddressFamily: af,
			PrefixFrom:    prefixFrom,
			PrefixTo:      prefixTo,
			Nexthops:      nexthops,
		})
	}

	return entries, nil
}
