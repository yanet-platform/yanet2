// Package cl3bobject is the Go binding for the l3b virtual service object.
package cl3bobject

//#cgo CFLAGS: -I../../../../../
//#cgo CFLAGS: -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/objects/l3b/api -ll3b_objects
//#cgo LDFLAGS: -L../../../../../build/lib/filter -lfilter_compiler
//#cgo LDFLAGS: -L../../../../../build/lib/statemap -lstatemap
//#cgo LDFLAGS: -L../../../../../build/lib/counters -lcounters
//
//#include "api/agent.h"
//#include "objects/l3b/api/l3b_session_table_object.h"
//#include "objects/l3b/api/l3b_virtual_service_object.h"
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

// VirtualServiceConfig is the control-plane descriptor used to create a
// virtual service.
type VirtualServiceConfig struct {
	SourceFilterRules []SourceFilterRule
	RealServers       []RealServer
	HashMask          uint32
	IndexMask         uint32
	RingCapacity      uint32
	// Hash index size of the service's session table; zero selects the
	// default.
	SessionIndexSize uint32
}

// VirtualServiceObjectType is the registered shared-memory object type of a
// virtual service.
const VirtualServiceObjectType = C.L3B_VIRTUAL_SERVICE_OBJECT_TYPE

// SessionTableObjectType is the registered shared-memory object type of a
// virtual service's session table.
const SessionTableObjectType = C.L3B_SESSION_TABLE_OBJECT_TYPE

// VirtualServiceObject is an opaque handle to a named virtual service and
// its session table, published as standalone cp_objects in shared memory.
type VirtualServiceObject struct {
	name string
	ptr  *C.struct_cp_object
	// The session table object born with the service; published and
	// destroyed together with it.
	sessionTable *C.struct_cp_object
}

func (m *VirtualServiceObject) asRawPtr() *C.struct_cp_object {
	return m.ptr
}

// Name returns the service name the object is registered under.
func (m *VirtualServiceObject) Name() string {
	return m.name
}

// CreateVirtualService allocates a named virtual service object together with
// its session table object in the agent's shared memory, from its descriptor.
// workerCount must cover every worker that will pin sessions.
//
// The returned object is not yet visible to the dataplane; call Publish to
// install it into a configuration generation.
func CreateVirtualService(
	agent *ffi.Agent,
	name string,
	workerCount uint16,
	config VirtualServiceConfig,
) (*VirtualServiceObject, error) {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	cConfig := config.cBuild(pinner)

	var cErr *C.yanet_error
	var sessionTable *C.struct_cp_object
	ptr := C.l3b_virtual_service_create(
		(*C.struct_agent)(agent.AsRawPtr()),
		cName,
		C.uint16_t(workerCount),
		&cConfig,
		&sessionTable,
		&cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to create virtual service: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return &VirtualServiceObject{name: name, ptr: ptr, sessionTable: sessionTable}, nil
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
	if m.sessionTable != nil {
		objects = append(objects, m.sessionTable)
	}
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

// DeleteVirtualService removes the named service object and its session table
// from the agent's registry; a replacement generation published afterwards
// drops them from the dataplane. The caller remains the objects' owner and
// must still free its handle separately.
func DeleteVirtualService(agent *ffi.Agent, name string) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	for _, objectType := range []string{VirtualServiceObjectType, SessionTableObjectType} {
		cType := C.CString(objectType)

		var cErr *C.yanet_error
		rc := C.agent_delete_object(
			(*C.struct_agent)(agent.AsRawPtr()),
			cType,
			cName,
			&cErr,
		)
		C.free(unsafe.Pointer(cType))
		if rc != 0 {
			return fmt.Errorf(
				"failed to delete %s object: %w",
				objectType,
				cerrors.FromC(unsafe.Pointer(cErr)),
			)
		}
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

func (m *SourceFilterRule) cBuild(
	pinner *runtime.Pinner,
) C.struct_l3b_source_filter_rule {
	c := C.struct_l3b_source_filter_rule{}
	filter.CBuildNet6s(&c.net6s, m.Net6s, pinner)
	filter.CBuildNet4s(&c.net4s, m.Net4s, pinner)
	filter.CBuildPortRanges(&c.port_ranges, m.PortRanges, pinner)
	return c
}

func (m *RealServer) cBuild() C.struct_l3b_real_server {
	c := C.struct_l3b_real_server{}

	// The 'type' field is a Go keyword; write it through its offset.
	*(*uint32)(unsafe.Pointer(&c)) = uint32(m.Type)

	sourceNetAddr := m.SourceNet.Addr()
	sourceNetMask := m.SourceNet.Mask()
	if sourceNetAddr.Is4() {
		sourceNet := (*C.struct_net4)(unsafe.Pointer(&c.source_net))
		addr := sourceNetAddr.As4()
		mask := sourceNetMask.As4()
		for idx := range 4 {
			sourceNet.addr[idx] = C.uint8_t(addr[idx])
			sourceNet.mask[idx] = C.uint8_t(mask[idx])
		}

		destinationAddr := (*C.struct_net4_addr)(unsafe.Pointer(&c.destination_addr))
		dst := m.DestinationAddress.As4()
		for idx := range 4 {
			destinationAddr.bytes[idx] = C.uint8_t(dst[idx])
		}
	} else {
		sourceNet := (*C.struct_net6)(unsafe.Pointer(&c.source_net))
		addr := sourceNetAddr.As16()
		mask := sourceNetMask.As16()
		for idx := range 16 {
			sourceNet.addr[idx] = C.uint8_t(addr[idx])
			sourceNet.mask[idx] = C.uint8_t(mask[idx])
		}

		destinationAddr := (*C.struct_net6_addr)(unsafe.Pointer(&c.destination_addr))
		dst := m.DestinationAddress.As16()
		for idx := range 16 {
			destinationAddr.bytes[idx] = C.uint8_t(dst[idx])
		}
	}

	return c
}

func (m *VirtualServiceConfig) cBuild(
	pinner *runtime.Pinner,
) C.struct_l3b_virtual_service {
	c := C.struct_l3b_virtual_service{
		hash_mask:          C.uint32_t(m.HashMask),
		index_mask:         C.uint32_t(m.IndexMask),
		ring_capacity:      C.uint32_t(m.RingCapacity),
		session_index_size: C.uint32_t(m.SessionIndexSize),
	}

	if len(m.SourceFilterRules) > 0 {
		cRules := make([]C.struct_l3b_source_filter_rule, len(m.SourceFilterRules))
		for idx := range m.SourceFilterRules {
			cRules[idx] = m.SourceFilterRules[idx].cBuild(pinner)
		}
		pinner.Pin(&cRules[0])
		c.source_filter_rules = &cRules[0]
		c.source_filter_rule_count = C.uint32_t(len(cRules))
	}

	if len(m.RealServers) > 0 {
		cServers := make([]C.struct_l3b_real_server, len(m.RealServers))
		for idx := range m.RealServers {
			cServers[idx] = m.RealServers[idx].cBuild()
		}
		pinner.Pin(&cServers[0])
		c.real_servers = &cServers[0]
		c.real_server_count = C.uint32_t(len(cServers))
	}

	return c
}
