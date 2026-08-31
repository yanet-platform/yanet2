// Package cl3b is Go binding for the l3b module
package cl3b

//#cgo CFLAGS: -I../../../../../
//#cgo CFLAGS: -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/modules/l3b/api -ll3b_cp
//#cgo LDFLAGS: -L../../../../../build/lib/filter -lfilter_compiler
//
//#include "api/agent.h"
//#include "modules/l3b/api/controlplane.h"
//#include "modules/l3b/dataplane/config.h"
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

// Update installs the destination filter rules and links the named virtual
// service objects into the module configuration. The services must already be
// published through agent_update_objects; the update links them in array
// order.
func (m *ModuleConfig) Update(
	rules []DestinationFilterRule,
	serviceNames []string,
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

	var cNamesPtr **C.char
	if len(serviceNames) > 0 {
		cNames := make([]*C.char, len(serviceNames))
		for idx, name := range serviceNames {
			cName := C.CString(name)
			pinner.Pin(cName)
			cNames[idx] = cName
		}
		pinner.Pin(&cNames[0])
		cNamesPtr = &cNames[0]
	}

	var cErr *C.yanet_error
	rc := C.l3b_module_config_update(
		m.asRawPtr(),
		cRulesPtr,
		C.uint32_t(len(rules)),
		cNamesPtr,
		C.uint32_t(len(serviceNames)),
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

// VirtualServiceObjectType is the registered shared-memory object type of a
// virtual service.
const VirtualServiceObjectType = C.L3B_VIRTUAL_SERVICE_OBJECT_TYPE

// VirtualServiceObject is an opaque handle to a named virtual service
// published as a standalone cp_object in shared memory.
type VirtualServiceObject struct {
	name string
	ptr  *C.struct_cp_object
}

func (m *VirtualServiceObject) asRawPtr() *C.struct_cp_object {
	return m.ptr
}

// Name returns the service name the object is registered under.
func (m *VirtualServiceObject) Name() string {
	return m.name
}

// CreateVirtualService allocates a named virtual service object in the agent's
// shared memory from its descriptor.
//
// The returned object is not yet visible to the dataplane; call Publish to
// install it into a configuration generation.
func CreateVirtualService(
	agent *ffi.Agent,
	name string,
	config VirtualServiceConfig,
) (*VirtualServiceObject, error) {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	cConfig := config.cBuild(pinner)

	var cErr *C.yanet_error
	ptr := C.l3b_virtual_service_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		&cConfig,
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to create virtual service: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return &VirtualServiceObject{name: name, ptr: ptr}, nil
}

// Publish upserts the object into a new dataplane configuration generation
// through agent_update_objects and blocks until every worker has advanced to
// it. Re-upserting a replacement object under the same name swaps the service
// atomically.
func (m *VirtualServiceObject) Publish(agent *ffi.Agent) error {
	if m.ptr == nil {
		return fmt.Errorf("virtual service object is nil")
	}

	objects := []*C.struct_cp_object{m.ptr}
	var cErr *C.yanet_error
	rc := C.agent_update_objects(
		(*C.struct_agent)(agent.AsRawPtr()),
		C.size_t(1),
		&objects[0],
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf(
			"failed to publish virtual service object: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return nil
}

// Free destroys the virtual service object when it is dangling — referenced
// by no live configuration generation — and reports nil. While a live
// generation still references it the free is refused with
// ffi.ErrStillReferenced and the handle stays usable: the caller must
// remember it and free it again once the generations holding it drain.
// Safe to call multiple times: subsequent calls are no-ops reporting nil.
func (m *VirtualServiceObject) Free() error {
	ptr := m.asRawPtr()
	if ptr == nil {
		return nil
	}
	var cErr *C.yanet_error
	rc, errno := C.l3b_virtual_service_free(ptr, &cErr)
	if rc == 0 {
		m.ptr = nil
		return nil
	}
	if errors.Is(errno, syscall.EAGAIN) {
		// The refused attempt allocated an error chain; release it
		// rather than leaking one per attempt. The object is intact.
		C.yanet_error_free(cErr)
		return ffi.ErrStillReferenced
	}
	return fmt.Errorf(
		"failed to free virtual service object: %w",
		cerrors.FromC(unsafe.Pointer(cErr)),
	)
}

// DeleteVirtualService removes the named service object from the agent's
// registry; a replacement generation published afterwards drops it from the
// dataplane. The caller remains the object's owner and must still free its
// handle separately.
func DeleteVirtualService(agent *ffi.Agent, name string) error {
	cType := C.CString(VirtualServiceObjectType)
	defer C.free(unsafe.Pointer(cType))
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	rc := C.agent_delete_object(
		(*C.struct_agent)(agent.AsRawPtr()),
		cType,
		cName,
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf(
			"failed to delete virtual service object: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return nil
}

// UpdateRing populates the real server ring of the virtual service.
func (m *VirtualServiceObject) UpdateRing(serverIndexes []uint32) error {
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
		m.asRawPtr(),
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
func (m *VirtualServiceObject) SetRealServerState(index uint32, enabled bool) error {
	var cErr *C.yanet_error
	rc := C.l3b_virtual_service_set_real_server_state(
		m.asRawPtr(),
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
