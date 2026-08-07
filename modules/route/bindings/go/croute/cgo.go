package croute

//#cgo CFLAGS: -I../../../../../ -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/route/api -lroute_cp
//
//#include "api/agent.h"
//#include "counters/counters.h"
//#include "modules/route/api/controlplane.h"
import "C"

import (
	"fmt"
	"net"
	"net/netip"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// CounterNameMaxLen is the longest counter name the shared-memory counter
// registry accepts.
const CounterNameMaxLen = C.COUNTER_NAME_LEN - 1

// ModuleConfig is an opaque handle to the route module configuration in shared
// memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig allocates a new route module configuration via the C API.
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.route_module_config_new((*C.struct_agent)(agent.AsRawPtr()), cName, &cErr)
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
		C.route_module_config_free(ptr)
		m.ptr = ffi.ModuleConfig{}
	}
}

// addRoute maps 1:1 to route_module_config_add_route.
//
// An empty counter reaches C as a nil pointer: route_module_config_add_route
// treats a NULL counter_name the same as an empty one, and passing nil here
// avoids allocating a zero-length CString for the common uncounted case.
func (m *ModuleConfig) addRoute(dstMAC [6]byte, srcMAC [6]byte, device string, counter string) (int, error) {
	cName := C.CString(device)
	defer C.free(unsafe.Pointer(cName))

	var cCounter *C.char
	if counter != "" {
		cCounter = C.CString(counter)
		defer C.free(unsafe.Pointer(cCounter))
	}

	var cErr *C.yanet_error
	idx := C.route_module_config_add_route(
		m.asRawPtr(),
		*(*C.struct_ether_addr)(unsafe.Pointer(&dstMAC)),
		*(*C.struct_ether_addr)(unsafe.Pointer(&srcMAC)),
		cName,
		cCounter,
		&cErr,
	)
	if idx < 0 {
		return -1, fmt.Errorf("failed to add route: %w", cerrors.FromC(unsafe.Pointer(cErr)))
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

// RouteCount returns the number of distinct hardware nexthops held by the
// config.
//
// Despite the name, which mirrors the C symbol, this is not a route count
// in the routing sense: it counts the resolved forwarding targets, each a
// distinct (dst MAC, src MAC, device) triple, that prefixes point at.
func (m *ModuleConfig) RouteCount() uint64 {
	return uint64(C.route_module_config_route_count(m.asRawPtr()))
}

// FIBRangeCountV4 returns the number of IPv4 FIB ranges.
//
// The count is computed inside the C API without materializing any range,
// which makes it cheap enough for a metrics scrape.
func (m *ModuleConfig) FIBRangeCountV4() uint64 {
	return uint64(C.route_module_config_fib_range_count_v4(m.asRawPtr()))
}

// FIBRangeCountV6 returns the number of IPv6 FIB ranges.
//
// The count is computed inside the C API without materializing any range,
// which makes it cheap enough for a metrics scrape.
func (m *ModuleConfig) FIBRangeCountV6() uint64 {
	return uint64(C.route_module_config_fib_range_count_v6(m.asRawPtr()))
}

// fibIter wraps the C fib_iter handle.
type fibIter struct {
	ptr *C.struct_fib_iter
}

// newFIBIter maps 1:1 to fib_iter_new.
func newFIBIter(config *ModuleConfig) (*fibIter, error) {
	ptr := C.fib_iter_new(config.asRawPtr())
	if ptr == nil {
		return nil, fmt.Errorf("fib_iter_new: allocation failure")
	}
	return &fibIter{ptr: ptr}, nil
}

// destroy maps 1:1 to fib_iter_free.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (m *fibIter) destroy() {
	if m.ptr != nil {
		C.fib_iter_free(m.ptr)
		m.ptr = nil
	}
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

// nexthopCounterName maps 1:1 to fib_iter_nexthop_counter_name.
func (m *fibIter) nexthopCounterName(idx uint64) string {
	return C.GoString(C.fib_iter_nexthop_counter_name(m.ptr, C.uint64_t(idx)))
}

// ActiveCounterNames walks the remainder of the FIB, collecting every
// non-empty per-nexthop counter name, then destroys the iterator.
func (m *fibIter) ActiveCounterNames() map[string]struct{} {
	defer m.destroy()

	names := map[string]struct{}{}
	for m.next() {
		for idx := range m.nexthopCount() {
			if name := m.nexthopCounterName(idx); name != "" {
				names[name] = struct{}{}
			}
		}
	}
	return names
}

// Entries walks the remainder of the FIB into Go-owned entries, then
// destroys the iterator.
//
// The prefix bounds returned during a step are borrowed from the iterator's
// own cursor and get overwritten on the next step. Keeping the walk beside
// the accessors confines that lifetime rule here, so the safe layer only
// ever sees Go-owned values.
func (m *fibIter) Entries() []FIBEntry {
	defer m.destroy()

	var entries []FIBEntry

	for m.next() {
		af := m.addressFamily()

		from := m.prefixFrom()
		to := m.prefixTo()

		var prefixFrom, prefixTo netip.Addr
		switch af {
		case AddressFamilyIPv4:
			prefixFrom = netip.AddrFrom4(*(*[4]byte)(from))
			prefixTo = netip.AddrFrom4(*(*[4]byte)(to))
		case AddressFamilyIPv6:
			prefixFrom = netip.AddrFrom16(*(*[16]byte)(from))
			prefixTo = netip.AddrFrom16(*(*[16]byte)(to))
		default:
			continue
		}

		nhCount := m.nexthopCount()
		nexthops := make([]FIBNexthop, nhCount)

		for idx := range nhCount {
			dstMAC := m.nexthopDstMAC(idx)
			srcMAC := m.nexthopSrcMAC(idx)

			nexthops[idx] = FIBNexthop{
				DstMAC:  net.HardwareAddr(dstMAC[:]),
				SrcMAC:  net.HardwareAddr(srcMAC[:]),
				Device:  m.nexthopDeviceName(idx),
				Counter: m.nexthopCounterName(idx),
			}
		}

		entries = append(entries, FIBEntry{
			AddressFamily: af,
			PrefixFrom:    prefixFrom,
			PrefixTo:      prefixTo,
			Nexthops:      nexthops,
		})
	}

	return entries
}
