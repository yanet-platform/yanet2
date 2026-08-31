// Package cl3b is Go binding for the l3b module
package cl3b

//#cgo CFLAGS: -I../../../../../
//#cgo CFLAGS: -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/l3b/api -ll3b_cp
//#cgo LDFLAGS: -L../../../../../build/lib/filter -lfilter_compiler
//
//#include "api/agent.h"
//#include "modules/l3b/api/controlplane.h"
import "C"

import (
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/yanet-platform/xnetip"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ModuleConfig is an opaque handle to the 'l3b' module configuration in
// shared memory.
type ModuleConfig struct {
	ptr ffi.ModuleConfig
}

// NewModuleConfig allocates a new L3b module configuration via the C API.
func NewModuleConfig(agent *ffi.Agent, name string) (*ModuleConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.l3b_module_config_new((*C.struct_agent)(agent.AsRawPtr()), cName, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to initialize module config: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
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

// Free destroys the module config when it is dangling — referenced by no
// live configuration generation — and reports nil. While a live generation
// still references it the free is refused with ffi.ErrStillReferenced
// and the handle stays usable: the caller must remember it and free it
// again once the generations holding it drain. Safe to call multiple
// times: subsequent calls are no-ops reporting nil.
func (m *ModuleConfig) Free() error {
	ptr := m.asRawPtr()
	if ptr == nil {
		return nil
	}
	var cErr *C.yanet_error
	rc, errno := C.l3b_module_config_free(ptr, &cErr)
	if rc == 0 {
		m.ptr = ffi.ModuleConfig{}
		return nil
	}
	if errors.Is(errno, syscall.EAGAIN) {
		// The refused attempt allocated an error chain; release it
		// rather than leaking one per attempt. The object is intact.
		C.yanet_error_free(cErr)
		return ffi.ErrStillReferenced
	}
	return fmt.Errorf(
		"failed to free module config: %w",
		cerrors.FromC(unsafe.Pointer(cErr)),
	)
}

// Update installs the destination filter rules and virtual service handles
// into the module configuration.
func (m *ModuleConfig) Update(
	rules []DestinationFilterRule,
	handles []*VirtualServiceHandle,
) error {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	var cRulesPtr *C.struct_l3b_destination_filter_rule
	if len(rules) > 0 {
		cRules := make([]C.struct_l3b_destination_filter_rule, len(rules))
		for idx := range rules {
			cRules[idx] = rules[idx].cBuild(pinner)
		}
		cRulesPtr = &cRules[0]
	}

	var cHandlesPtr **C.struct_virtual_service_handle
	if len(handles) > 0 {
		cHandles := make([]*C.struct_virtual_service_handle, len(handles))
		for idx, handle := range handles {
			cHandles[idx] = handle.ptr
		}
		pinner.Pin(&cHandles[0])
		cHandlesPtr = &cHandles[0]
	}

	var cErr *C.yanet_error
	rc := C.l3b_module_config_update(
		m.asRawPtr(),
		cRulesPtr,
		C.uint32_t(len(rules)),
		cHandlesPtr,
		C.uint32_t(len(handles)),
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf(
			"failed to update module config: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return nil
}

// IPFamily selects the address family of a real server tunnel.
type IPFamily uint32

const (
	// IPv4 is an IPv4-in-IPv4 tunnel.
	IPv4 IPFamily = C.ip_family_ip4
	// IPv6 is an IPv6-in-IPv6 tunnel.
	IPv6 IPFamily = C.ip_family_ip6
)

// RealServer describes a single backend reachable through an IP-in-IP tunnel.
type RealServer struct {
	Type               IPFamily
	DestinationAddress netip.Addr
	SourceNet          xnetip.Network
}

// SourceFilterRule describes the source-side match criteria of a service.
type SourceFilterRule struct {
	Net6s      []xnetip.BiContiguous
	Net4s      []xnetip.Contiguous[xnetip.Network4]
	PortRanges filter.PortRanges
}

// DestinationFilterRule describes a destination-side classification rule.
type DestinationFilterRule struct {
	Net6s               []xnetip.BiContiguous
	Net4s               []xnetip.Contiguous[xnetip.Network4]
	ProtoRanges         filter.ProtoRanges
	VirtualServiceIndex uint32
}

// VirtualServiceConfig is the control-plane descriptor used to create a
// virtual service.
type VirtualServiceConfig struct {
	SourceFilterRules []SourceFilterRule
	RealServers       []RealServer
	HashMask          uint32
	IndexMask         uint32
	RingCapacity      uint32
}

// VirtualService is an opaque handle to a created virtual service in shared
// memory.
type VirtualService struct {
	ptr *C.struct_virtual_service
}

// VirtualServiceHandle is an opaque handle wrapping a virtual service pointer;
// it is the indirection installed into a module configuration.
type VirtualServiceHandle struct {
	ptr *C.struct_virtual_service_handle
}

// CreateVirtualService allocates a virtual service in the agent's shared
// memory from its descriptor.
func CreateVirtualService(
	agent *ffi.Agent,
	config VirtualServiceConfig,
) (*VirtualService, error) {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cConfig := config.cBuild(pinner)

	var cErr *C.yanet_error
	ptr := C.l3b_virtual_service_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		&cConfig,
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to create virtual service: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return &VirtualService{ptr: ptr}, nil
}

// CreateVirtualServiceHandle allocates a handle wrapping a virtual service.
func CreateVirtualServiceHandle(
	agent *ffi.Agent,
	virtualService *VirtualService,
) (*VirtualServiceHandle, error) {
	ptr := C.l3b_virtual_service_handle_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		virtualService.ptr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to create virtual service handle")
	}
	return &VirtualServiceHandle{ptr: ptr}, nil
}

// Update repoints the handle at a different virtual service.
func (h *VirtualServiceHandle) Update(virtualService *VirtualService) {
	C.l3b_virtual_service_handle_update(h.ptr, virtualService.ptr)
}

// UpdateRing populates the real server ring of a virtual service.
func (vs *VirtualService) UpdateRing(serverIndexes []uint32) error {
	var cIndexesPtr *C.uint32_t
	if len(serverIndexes) > 0 {
		cIndexes := make([]C.uint32_t, len(serverIndexes))
		for idx, value := range serverIndexes {
			cIndexes[idx] = C.uint32_t(value)
		}
		cIndexesPtr = &cIndexes[0]
	}

	var cErr *C.yanet_error
	rc := C.l3b_virtual_service_update_ring(
		vs.ptr,
		cIndexesPtr,
		C.uint32_t(len(serverIndexes)),
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf(
			"failed to update real server ring: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return nil
}

// SetRealServerState enables or disables a single real server by its index.
func (vs *VirtualService) SetRealServerState(index uint32, enabled bool) error {
	var cErr *C.yanet_error
	rc := C.l3b_virtual_service_set_real_server_state(
		vs.ptr,
		C.uint32_t(index),
		C._Bool(enabled),
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf(
			"failed to set real server state: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return nil
}

func (r *SourceFilterRule) cBuild(
	pinner *runtime.Pinner,
) C.struct_l3b_source_filter_rule {
	c := C.struct_l3b_source_filter_rule{}
	filter.CBuildNet6s(&c.net6s, r.Net6s, pinner)
	filter.CBuildNet4s(&c.net4s, r.Net4s, pinner)
	filter.CBuildPortRanges(&c.port_ranges, r.PortRanges, pinner)
	return c
}

func (r *DestinationFilterRule) cBuild(
	pinner *runtime.Pinner,
) C.struct_l3b_destination_filter_rule {
	c := C.struct_l3b_destination_filter_rule{}
	filter.CBuildNet6s(&c.net6s, r.Net6s, pinner)
	filter.CBuildNet4s(&c.net4s, r.Net4s, pinner)
	filter.CBuildProtoRanges(&c.proto_ranges, r.ProtoRanges, pinner)
	c.virtual_service_index = C.uint32_t(r.VirtualServiceIndex)
	return c
}

func (r *RealServer) cBuild() C.struct_l3b_real_server {
	c := C.struct_l3b_real_server{}

	// The 'type' field is a Go keyword; write it through its offset.
	*(*uint32)(unsafe.Pointer(&c)) = uint32(r.Type)

	sourceNetAddr := r.SourceNet.Addr()
	sourceNetMask := r.SourceNet.Mask()
	if sourceNetAddr.Is4() {
		sourceNet := (*C.struct_net4)(unsafe.Pointer(&c.source_net))
		addr := sourceNetAddr.As4()
		mask := sourceNetMask.As4()
		for idx := 0; idx < 4; idx++ {
			sourceNet.addr[idx] = C.uint8_t(addr[idx])
			sourceNet.mask[idx] = C.uint8_t(mask[idx])
		}

		destinationAddr := (*C.struct_net4_addr)(unsafe.Pointer(&c.destination_addr))
		dst := r.DestinationAddress.As4()
		for idx := 0; idx < 4; idx++ {
			destinationAddr.bytes[idx] = C.uint8_t(dst[idx])
		}
	} else {
		sourceNet := (*C.struct_net6)(unsafe.Pointer(&c.source_net))
		addr := sourceNetAddr.As16()
		mask := sourceNetMask.As16()
		for idx := 0; idx < 16; idx++ {
			sourceNet.addr[idx] = C.uint8_t(addr[idx])
			sourceNet.mask[idx] = C.uint8_t(mask[idx])
		}

		destinationAddr := (*C.struct_net6_addr)(unsafe.Pointer(&c.destination_addr))
		dst := r.DestinationAddress.As16()
		for idx := 0; idx < 16; idx++ {
			destinationAddr.bytes[idx] = C.uint8_t(dst[idx])
		}
	}

	return c
}

func (config *VirtualServiceConfig) cBuild(
	pinner *runtime.Pinner,
) C.struct_l3b_virtual_service {
	c := C.struct_l3b_virtual_service{
		hash_mask:     C.uint32_t(config.HashMask),
		index_mask:    C.uint32_t(config.IndexMask),
		ring_capacity: C.uint32_t(config.RingCapacity),
	}

	if len(config.SourceFilterRules) > 0 {
		cRules := make([]C.struct_l3b_source_filter_rule, len(config.SourceFilterRules))
		for idx := range config.SourceFilterRules {
			cRules[idx] = config.SourceFilterRules[idx].cBuild(pinner)
		}
		pinner.Pin(&cRules[0])
		c.source_filter_rules = &cRules[0]
		c.source_filter_rule_count = C.uint32_t(len(cRules))
	}

	if len(config.RealServers) > 0 {
		cServers := make([]C.struct_l3b_real_server, len(config.RealServers))
		for idx := range config.RealServers {
			cServers[idx] = config.RealServers[idx].cBuild()
		}
		pinner.Pin(&cServers[0])
		c.real_servers = &cServers[0]
		c.real_server_count = C.uint32_t(len(cServers))
	}

	return c
}
