// Package cl3bobject is the Go binding for the l3b virtual service object.
package cl3bobject

//#cgo CFLAGS: -I../../../../../
//#cgo CFLAGS: -I../../../../../lib
//#cgo LDFLAGS: -L../../../../../build/objects/l3b/api -ll3b_objects
//#cgo LDFLAGS: -L../../../../../build/lib/filter -lfilter_compiler
//#cgo LDFLAGS: -L../../../../../build/lib/statemap -lstatemap
//#cgo LDFLAGS: -L../../../../../build/lib/l3state -ll3state
//#cgo LDFLAGS: -L../../../../../build/lib/counters -lcounters
//
//#include "api/agent.h"
//#include "lib/l3state/l3state.h"
//#include "objects/l3b/api/l3b_session_table_object.h"
//#include "objects/l3b/api/l3b_virtual_service_object.h"
//
//// cl3bobject_real_info is one live real-server record as read from the
//// published object.
//struct cl3bobject_real_info {
//	uint8_t family;
//	uint8_t enabled;
//	uint8_t destination[16];
//	uint8_t source_addr[16];
//	uint8_t source_mask[16];
//};
//
//// cl3bobject_service_inspect reads the scheduler masks and the real server
//// records of the published service object.
//static inline int
//cl3bobject_service_inspect(
//	struct cp_object *service,
//	uint32_t *hash_mask,
//	uint32_t *index_mask,
//	struct cl3bobject_real_info *out,
//	uint32_t capacity
//) {
//	struct virtual_service *vs = &((struct l3b_virtual_service_object *)
//		service)->virtual_service;
//	*hash_mask = vs->scheduler_hash_mask;
//	*index_mask = vs->scheduler_index_mask;
//
//	struct real_server *real_servers = ADDR_OF(&vs->real_servers);
//	uint32_t count = vs->real_server_count;
//	if (count > capacity) {
//		count = capacity;
//	}
//	for (uint32_t idx = 0; idx < count; ++idx) {
//		const struct real_server *real = &real_servers[idx];
//		struct cl3bobject_real_info *info = &out[idx];
//		memset(info, 0, sizeof(*info));
//		info->family = real->type == ip_family_ip4 ? 4 : 6;
//		info->enabled = real->state == real_state_enabled;
//		if (real->type == ip_family_ip4) {
//			memcpy(info->destination, real->destination_addr.v4.bytes, 4);
//			memcpy(info->source_addr, real->source_net.v4.addr, 4);
//			memcpy(info->source_mask, real->source_net.v4.mask, 4);
//		} else {
//			memcpy(info->destination, real->destination_addr.v6.bytes, 16);
//			memcpy(info->source_addr, real->source_net.v6.addr, 16);
//			memcpy(info->source_mask, real->source_net.v6.mask, 16);
//		}
//	}
//	return (int)vs->real_server_count;
//}
//
//// cl3bobject_sessions_read resolves the service's session table and pages
//// its records through the l3state cursor.
//static inline int
//cl3bobject_sessions_read(
//	struct cp_object *service,
//	uint64_t now,
//	uint64_t *cursor,
//	struct l3s_session *sessions,
//	uint32_t capacity
//) {
//	struct l3b_session_table_object *table =
//		l3b_virtual_service_session_table(service);
//	if (table == NULL) {
//		return -1;
//	}
//	l3s_cursor_t l3s_cursor = {.token = *cursor};
//	uint32_t count = l3s_table_read(
//		&table->table, now, &l3s_cursor, sessions, capacity
//	);
//	*cursor = l3s_cursor.token;
//	return (int)count;
//}
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
	// Scheduling mode bits; SchedulerCounter selects one-packet
	// scheduling from the module link's per-worker packet counter.
	SchedulerFlags uint32
	// Service behavior flags; FixMSS clamps the TCP MSS option of SYN
	// packets and PureL3 keeps the client's ports out of the session
	// identity.
	Flags uint32
	// Outer-header DSCP marking, (dscp << 2) | mode; zero inherits the
	// inner DSCP.
	DSCPFlags uint32
	// Session lifetime policy in seconds; zero fields select the
	// defaults.
	SessionTimeouts SessionTimeouts
}

// SessionTimeouts is the session lifetime policy of a service. A record's
// deadline follows the packet that touched it last: TCP by its flags, UDP
// its own value, everything else the catch-all.
type SessionTimeouts struct {
	TCP       uint32
	TCPSyn    uint32
	TCPSynAck uint32
	TCPFin    uint32
	UDP       uint32
	Other     uint32
}

// VirtualServiceObjectType is the registered shared-memory object type of a
// virtual service.
const VirtualServiceObjectType = C.L3B_VIRTUAL_SERVICE_OBJECT_TYPE

// SessionTableObjectType is the registered shared-memory object type of a
// virtual service's session table.
const SessionTableObjectType = C.L3B_SESSION_TABLE_OBJECT_TYPE

// FixMSS is the service flag clamping the TCP MSS option of SYN packets.
const FixMSS = uint32(C.L3B_FLAG_FIX_MSS)

// PureL3 is the service flag keeping the client's ports out of the session
// identity: every flow of one source address shares a single pin.
const PureL3 = uint32(C.L3B_FLAG_PURE_L3)

// DSCP marking modes combined with a 6-bit DSCP value into DSCPFlags.
const (
	// DSCPMarkNever inherits the inner DSCP into the outer header.
	DSCPMarkNever = uint32(C.L3B_DSCP_MARK_NEVER)
	// DSCPMark marks the outer header only when the DSCP is zero.
	DSCPMark = uint32(C.L3B_DSCP_MARK)
	// DSCPMarkAlways marks the outer header unconditionally.
	DSCPMarkAlways = uint32(C.L3B_DSCP_MARK_ALWAYS)
)

// DSCPFlagsOf packs a 6-bit DSCP value with a marking mode.
func DSCPFlagsOf(mode uint32, dscp uint8) uint32 {
	return mode | (uint32(dscp) << 2 & 0xFC)
}

// SchedulerCounter is the scheduling mode selecting one-packet scheduling:
// the ring slot derives from the module link's per-worker packet counter
// instead of the packet hash.
const SchedulerCounter = uint32(C.L3B_SCHEDULER_COUNTER)

// VirtualServiceObject is an opaque handle to a named virtual service and
// its session table, published as standalone cp_objects in shared memory.
type VirtualServiceObject struct {
	name string
	ptr  *C.struct_cp_object
	// The session table object born with the service; published and
	// destroyed together with it.
	sessionTable *C.struct_cp_object
	// Number of real servers the service was configured with; indexes
	// crossing into C arrays are validated against it on the Go side.
	realServerCount uint32
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
// workerCount must cover every worker that will pin sessions. A non-nil
// previous makes the new service adopt that handle's session table, so every
// pinned flow keeps its real server across the update; the previous handle
// must then be retired through RetireService, not freed.
//
// The returned object is not yet visible to the dataplane; call Publish to
// install it into a configuration generation.
func CreateVirtualService(
	agent *ffi.Agent,
	name string,
	workerCount uint16,
	config VirtualServiceConfig,
	previous *VirtualServiceObject,
) (*VirtualServiceObject, error) {
	pinner := &runtime.Pinner{}
	defer pinner.Unpin()

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	cConfig := config.cBuild(pinner)
	// The creation config embeds a pointer to the descriptor, so the
	// descriptor itself must live in pinned memory for the call.
	pinner.Pin(&cConfig)

	var adopt *C.struct_cp_object
	if previous != nil {
		adopt = previous.sessionTable
	}

	cCreate := C.struct_l3b_virtual_service_create_config{
		agent:               (*C.struct_agent)(agent.AsRawPtr()),
		name:                cName,
		worker_count:        C.uint16_t(workerCount),
		adopt_session_table: adopt,
		virtual_service:     &cConfig,
	}
	pinner.Pin(&cCreate)

	var cErr *C.yanet_error
	var sessionTable *C.struct_cp_object
	ptr := C.l3b_virtual_service_create(&cCreate, &sessionTable, &cErr)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to create virtual service: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}

	// Adoption moves the table's ownership: the previous handle stops
	// referencing it so a later Free on it cannot destroy the table the
	// new handle now owns.
	if previous != nil {
		previous.sessionTable = nil
	}

	return &VirtualServiceObject{
		name:            name,
		ptr:             ptr,
		sessionTable:    sessionTable,
		realServerCount: uint32(len(config.RealServers)),
	}, nil
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
		C.size_t(len(objects)),
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

// RetireService destroys only the service object, once it is dangling —
// referenced by no live configuration generation — and reports nil. The
// session table is deliberately left alive: it survives service updates, and
// an updating caller passes its handle to CreateVirtualService as previous.
// While a live generation still references the service the free is refused
// with ffi.ErrStillReferenced. Safe to call multiple times: subsequent calls
// are no-ops reporting nil.
func (m *VirtualServiceObject) RetireService() error {
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

// Free destroys the service object and its session table once both are
// dangling — referenced by no live configuration generation — and reports
// nil. While a live generation still references either object the free stops
// at the refusal with ffi.ErrStillReferenced and the caller must retry: the
// already-destroyed part stays destroyed. Safe to call multiple times:
// subsequent calls are no-ops reporting nil.
func (m *VirtualServiceObject) Free() error {
	if err := m.RetireService(); err != nil {
		return err
	}

	if m.sessionTable == nil {
		return nil
	}
	var cErr *C.yanet_error
	rc, errno := C.l3b_session_table_object_free(m.sessionTable, &cErr)
	if rc == 0 {
		m.sessionTable = nil
		return nil
	}
	if errors.Is(errno, syscall.EAGAIN) {
		C.yanet_error_free(cErr)
		return ffi.ErrStillReferenced
	}
	return fmt.Errorf(
		"failed to free session table object: %w",
		cerrors.FromC(unsafe.Pointer(cErr)),
	)
}

// ReadServiceCounter reads one packets/bytes counter pair of the named
// service object from the per-worker storage of a published generation's
// execution context, exposed by the harness or a dataplane as an opaque
// pointer. Values are per worker; callers aggregate across workers.
func ReadServiceCounter(ectx uintptr, service string, counter string) (uint64, uint64, error) {
	cService := C.CString(service)
	defer C.free(unsafe.Pointer(cService))
	cCounter := C.CString(counter)
	defer C.free(unsafe.Pointer(cCounter))

	var values [2]C.uint64_t
	rc := C.l3b_virtual_service_counter_read(C.uint64_t(ectx), cService, cCounter, &values[0])
	if rc != 0 {
		return 0, 0, fmt.Errorf("counter %q of virtual service %q not found", counter, service)
	}
	return uint64(values[0]), uint64(values[1]), nil
}

// Session is one decoded session record of a service's table.
type Session struct {
	// SourceAddress is the client flow's source address.
	SourceAddress netip.Addr
	// SourcePort is the client flow's source port.
	SourcePort uint16
	// RealAddress is the destination address of the pinned real server.
	RealAddress netip.Addr
	// ExpiresAt is the record's absolute deadline in nanoseconds since
	// the epoch.
	ExpiresAt uint64
}

// RealServerInfo is the live status of one real server as read from the
// published service object.
type RealServerInfo struct {
	// Family is the tunnel address family.
	Family IPFamily
	// DestinationAddress is the real server tunnel destination.
	DestinationAddress netip.Addr
	// SourceNet is the network the outer source address is derived from.
	SourceNet xnetip.Network
	// Enabled is whether the real server currently takes traffic.
	Enabled bool
}

// ServiceInfo is the live status of a published virtual service object.
type ServiceInfo struct {
	HashMask    uint32
	IndexMask   uint32
	RealServers []RealServerInfo
}

// Inspect reads the scheduler masks and the real server records from the
// published service object. Weights are control-plane state and are not
// part of the object; the backend layers them on.
func (m *VirtualServiceObject) Inspect() (ServiceInfo, error) {
	if m.ptr == nil {
		return ServiceInfo{}, fmt.Errorf("virtual service object is nil")
	}

	var hashMask, indexMask C.uint32_t
	infos := make([]C.struct_cl3bobject_real_info, 64)
	count, errno := C.cl3bobject_service_inspect(m.ptr, &hashMask, &indexMask, &infos[0], C.uint32_t(len(infos)))
	_ = errno
	if count < 0 {
		return ServiceInfo{}, fmt.Errorf("failed to inspect service")
	}
	if uint32(count) > uint32(len(infos)) {
		// More real servers than the initial guess: reread with the
		// exact capacity.
		infos = make([]C.struct_cl3bobject_real_info, uint32(count))
		count, _ = C.cl3bobject_service_inspect(m.ptr, &hashMask, &indexMask, &infos[0], C.uint32_t(len(infos)))
	}

	realServers := make([]RealServerInfo, 0, count)
	for idx := uint32(0); idx < uint32(count); idx++ {
		info := infos[idx]

		var destination netip.Addr
		var sourceAddrBytes, sourceMaskBytes [16]byte
		for byteIdx := range sourceAddrBytes {
			sourceAddrBytes[byteIdx] = byte(info.source_addr[byteIdx])
			sourceMaskBytes[byteIdx] = byte(info.source_mask[byteIdx])
		}
		var destinationBytes [16]byte
		for byteIdx := range destinationBytes {
			destinationBytes[byteIdx] = byte(info.destination[byteIdx])
		}

		family := IPv6
		if info.family == 4 {
			family = IPv4
			var destination4 [4]byte
			copy(destination4[:], destinationBytes[0:4])
			destination = netip.AddrFrom4(destination4)
		} else {
			destination = netip.AddrFrom16(destinationBytes)
		}

		sourceAddr := netip.AddrFrom16(sourceAddrBytes)
		sourceMask := netip.AddrFrom16(sourceMaskBytes)
		if info.family == 4 {
			var sourceAddr4 [4]byte
			var sourceMask4 [4]byte
			copy(sourceAddr4[:], sourceAddrBytes[0:4])
			copy(sourceMask4[:], sourceMaskBytes[0:4])
			sourceAddr = netip.AddrFrom4(sourceAddr4)
			sourceMask = netip.AddrFrom4(sourceMask4)
		}
		sourceNet, err := xnetip.NetworkFrom(sourceAddr, sourceMask)
		if err != nil {
			return ServiceInfo{}, fmt.Errorf("invalid real server source network: %w", err)
		}

		realServers = append(realServers, RealServerInfo{
			Family:             family,
			DestinationAddress: destination,
			SourceNet:          sourceNet,
			Enabled:            info.enabled != 0,
		})
	}

	return ServiceInfo{
		HashMask:    uint32(hashMask),
		IndexMask:   uint32(indexMask),
		RealServers: realServers,
	}, nil
}

// ReadSessions pages through the session records of the service's table.
//
// cursor continues a previous listing (0 starts from the beginning); limit
// bounds the page size. Returns the page and the continuation token for the
// next one — 0 when the listing is complete. The returned nowNs is the
// dataplane's current time, the reference point of the records' deadlines.
func (m *VirtualServiceObject) ReadSessions(
	agent *ffi.Agent,
	cursor uint64,
	limit uint32,
) ([]Session, uint64, uint64, error) {
	if m.ptr == nil {
		return nil, 0, 0, fmt.Errorf("virtual service object is nil")
	}
	if limit == 0 {
		limit = 1
	}

	nowNs, ok := agent.DPConfig().CurrentTime()
	if !ok {
		return nil, 0, 0, fmt.Errorf("failed to read dataplane time")
	}

	sessions := make([]C.struct_l3s_session, limit)
	cCursor := C.uint64_t(cursor)
	count, errno := C.cl3bobject_sessions_read(
		m.ptr,
		C.uint64_t(nowNs),
		&cCursor,
		&sessions[0],
		C.uint32_t(limit),
	)
	_ = errno
	if count < 0 {
		return nil, 0, 0, fmt.Errorf("failed to read sessions: the service carries no session table")
	}

	out := make([]Session, 0, count)
	for idx := uint32(0); idx < uint32(count); idx++ {
		key := sessions[idx].key
		value := sessions[idx].value

		var sourceAddress netip.Addr
		var realAddress netip.Addr
		var sourceBytes [16]byte
		var realBytes [16]byte
		for idx := range sourceBytes {
			sourceBytes[idx] = byte(key.src_addr[idx])
			realBytes[idx] = byte(value.destination[idx])
		}
		if key.family == 4 {
			sourceAddress = netip.AddrFrom4(
				[4]byte(sourceBytes[0:4]),
			)
		} else {
			sourceAddress = netip.AddrFrom16(sourceBytes)
		}
		if value.family == 4 {
			realAddress = netip.AddrFrom4(
				[4]byte(realBytes[0:4]),
			)
		} else {
			realAddress = netip.AddrFrom16(realBytes)
		}

		out = append(out, Session{
			SourceAddress: sourceAddress,
			SourcePort:    uint16(key.src_port),
			RealAddress:   realAddress,
			ExpiresAt:     uint64(value.expires_at),
		})
	}

	next := uint64(cCursor)
	if next == C.L3S_CURSOR_DONE {
		next = 0
	}
	return out, next, nowNs, nil
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
// The index is validated against the configured real server count before
// crossing into C.
func (m *VirtualServiceObject) SetRealServerState(index uint32, enabled bool) error {
	if m.ptr == nil {
		return fmt.Errorf("virtual service object is nil")
	}
	if index >= m.realServerCount {
		return fmt.Errorf("real server index %d is out of range: the service has %d real servers", index, m.realServerCount)
	}

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
		scheduler_flags:    C.uint32_t(m.SchedulerFlags),
		dscp_flags:         C.uint32_t(m.DSCPFlags),
		flags:              C.uint32_t(m.Flags),
		session_timeouts: C.struct_l3b_session_timeouts{
			tcp:         C.uint32_t(m.SessionTimeouts.TCP),
			tcp_syn:     C.uint32_t(m.SessionTimeouts.TCPSyn),
			tcp_syn_ack: C.uint32_t(m.SessionTimeouts.TCPSynAck),
			tcp_fin:     C.uint32_t(m.SessionTimeouts.TCPFin),
			udp:         C.uint32_t(m.SessionTimeouts.UDP),
			other:       C.uint32_t(m.SessionTimeouts.Other),
		},
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
