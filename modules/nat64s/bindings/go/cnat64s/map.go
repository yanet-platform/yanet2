package cnat64s

/*
#cgo CFLAGS: -I../../../../../

#include "api/agent.h"
#include "common/container_of.h"
#include "lib/errors/errors.h"
#include "objects/fwstate/api/fwstate_map_nat64_object.h"

// cp_object is the first field of the nat64state map object, so the cast
// preserves the address.
static inline struct fwstate_map_nat64_object *
cnat64s_from_cp_object(struct cp_object *cp_object) {
	return (struct fwstate_map_nat64_object *)cp_object;
}

// One session entry of a cursor batch: the slot's key and value in the
// table's layer store, valid until the control plane's next reclamation.
struct cnat64s_session_slot {
	const void *key;
	const struct nat64state_value *value;
	uint32_t idx;
	_Bool expired;
};

// The lifetime of a session's last refresh, in nanoseconds. The field is
// a bit-field, which cgo cannot name.
static inline uint64_t
cnat64s_session_last_ttl(const struct nat64state_value *value) {
	return value->last_ttl;
}

// Walk the active layer of the object's lan table from the slot after
// pos, collecting up to count live sessions into out. A slot whose value
// was never written, or whose session expired before now, is skipped.
// Returns the number of sessions written and leaves *next at the slot to
// resume from.
//
// The walk holds no table lock: it is a diagnostic read of a table the
// dataplane mutates concurrently, so a session may change between this
// read and the caller's use of it, and a value read here can be
// overwritten by a refresh right after. The caller copies each value out
// before returning, and holds the registry's lock across the whole
// call: that lock, not anything in the table, is what keeps the layer
// allocated while it is being walked.
static inline uint32_t
cnat64s_read_lan_sessions(
	struct fwstate_map_nat64_object *object,
	int64_t pos,
	uint64_t now,
	uint32_t count,
	struct cnat64s_session_slot *out,
	int64_t *next
) {
	fwmap_t *layer = ADDR_OF(&object->lan.head);
	if (layer == NULL || count == 0) {
		*next = pos;
		return 0;
	}
	uint32_t limit =
		__atomic_load_n(&layer->key_cursor, __ATOMIC_ACQUIRE);
	uint32_t collected = 0;
	while (collected < count && pos >= 0 && pos < (int64_t)limit) {
		const struct nat64state_value *value =
			(const struct nat64state_value *)fwmap_get_value(
				layer, (uint32_t)pos
			);
		pos += 1;
		if (value == NULL || value->updated_at == 0) {
			continue;
		}
		_Bool expired = now >= value->updated_at + value->last_ttl;
		if (expired) {
			continue;
		}
		const void *key = fwmap_get_key(layer, (uint32_t)(pos - 1));
		if (key == NULL) {
			continue;
		}
		out[collected].key = key;
		out[collected].value = value;
		out[collected].idx = (uint32_t)(pos - 1);
		out[collected].expired = expired;
		collected += 1;
	}
	*next = pos;
	return collected;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// MapObjectType is the object type of the nat64state session tables, as
// registered in the shared-memory object namespace.
const MapObjectType = C.FWSTATE_MAP_NAT64_OBJECT_TYPE

// MapConfig carries the sizing of the session tables' layers and their
// shared stash, mirroring struct fwstate_map_create_config.
//
// Zero values select their C defaults, the worker count excepted: it
// must be set.
type MapConfig struct {
	IndexSize        uint32
	ExtraBucketCount uint32
	WorkerCount      uint16
	StashSize        uint64
}

func (m MapConfig) toC() C.struct_fwstate_map_create_config {
	return C.struct_fwstate_map_create_config{
		index_size:         C.uint32_t(m.IndexSize),
		extra_bucket_count: C.uint32_t(m.ExtraBucketCount),
		worker_count:       C.uint16_t(m.WorkerCount),
		stash_size:         C.uint64_t(m.StashSize),
	}
}

// MapObjectConfig is a live nat64state map object in shared memory: the
// lan and wan session tables of one stateful NAT64 instance, registered
// under (MapObjectType, name) and published via Publish.
type MapObjectConfig struct {
	ptr *C.struct_cp_object
}

// NewMapObjectConfig allocates an unpublished map object.
//
// The caller owns it and releases it with Free while its agent is alive:
// the object lives in shared memory, which nothing releases on the
// owner's behalf once the handle is dropped.
func NewMapObjectConfig(agent *ffi.Agent, name string) (*MapObjectConfig, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent cannot be nil")
	}

	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.fwstate_map_nat64_object_config_new(
		(*C.struct_agent)(agent.AsRawPtr()), cName, &cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to create nat64state map object: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}

	object := &MapObjectConfig{ptr: ptr}
	return object, nil
}

func (m *MapObjectConfig) asRawPtr() *C.struct_cp_object { return m.ptr }

// CreateMap installs the first layer of both tables and their per-worker
// stash.
func (m *MapObjectConfig) CreateMap(config MapConfig) error {
	if m.ptr == nil {
		return fmt.Errorf("nat64state map object is nil")
	}
	cConfig := config.toC()
	rc, errno := C.fwstate_map_nat64_object_create(
		C.cnat64s_from_cp_object(m.ptr), &cConfig,
	)
	if rc != 0 {
		return tablesFailure("create the nat64state tables", errno)
	}
	return nil
}

// InsertLayer prepends a fresh layer to both tables' chains.
func (m *MapObjectConfig) InsertLayer(config MapConfig) error {
	if m.ptr == nil {
		return fmt.Errorf("nat64state map object is nil")
	}
	cConfig := config.toC()
	rc, errno := C.fwstate_map_nat64_object_insert_layer(
		C.cnat64s_from_cp_object(m.ptr),
		cConfig.index_size, cConfig.extra_bucket_count,
		cConfig.worker_count,
	)
	if rc != 0 {
		return tablesFailure("insert the nat64state layers", errno)
	}
	return nil
}

// tablesFailure reports a refused creation of tables by its cause, the
// libc errno: a size the agent cannot allocate, or a count the tables
// refuse.
//
// A zone with no room is the resource the config draws from running out,
// the kind the publish reports as such; the cause stays in the chain.
func tablesFailure(what string, errno error) error {
	if errors.Is(errno, syscall.ENOMEM) {
		return fmt.Errorf(
			"failed to %s: %w: %w",
			what,
			ffi.ErrResourceExhausted,
			errno,
		)
	}
	return fmt.Errorf("failed to %s: %w", what, errno)
}

// UnlinkStaleLayers parks expired layers of both tables in their stale
// chains, where FreeStaleLayers releases them after a generation barrier.
func (m *MapObjectConfig) UnlinkStaleLayers(now uint64) error {
	if m.ptr == nil {
		return fmt.Errorf("nat64state map object is nil")
	}
	rc := C.fwstate_map_nat64_object_unlink_stale_layers(
		C.cnat64s_from_cp_object(m.ptr), C.uint64_t(now),
	)
	if rc != 0 {
		return fmt.Errorf("failed to unlink stale nat64state layers: error code=%d", rc)
	}
	return nil
}

// HasReclaimable reports whether a reclamation round would do anything on
// either table, so a caller can skip the generation barriers it otherwise
// costs.
func (m *MapObjectConfig) HasReclaimable(now uint64) bool {
	if m.ptr == nil {
		return false
	}
	return bool(C.fwstate_map_nat64_object_has_reclaimable(
		C.cnat64s_from_cp_object(m.ptr), C.uint64_t(now),
	))
}

// FreeStaleLayers releases the layers parked by UnlinkStaleLayers.
//
// Safe only after a generation barrier that every worker advanced past
// since the unlink.
func (m *MapObjectConfig) FreeStaleLayers() {
	if m.ptr == nil {
		return
	}
	C.fwstate_map_nat64_object_free_stale_layers(
		C.cnat64s_from_cp_object(m.ptr),
	)
}

// Publish upserts the object into a new config generation and blocks until
// every dataplane worker has advanced to it, acting as the RCU barrier the
// layer reclamation needs.
func (m *MapObjectConfig) Publish(agent *ffi.Agent) error {
	if m.ptr == nil {
		return fmt.Errorf("nat64state map object is nil")
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
			"failed to update objects: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return nil
}

// Free destroys the object when it is dangling and reports nil. While a
// live generation still references it the free is refused with
// ffi.ErrStillReferenced and the handle stays usable. Safe to call
// multiple times.
func (m *MapObjectConfig) Free() error {
	ptr := m.ptr
	if ptr == nil {
		return nil
	}

	var cErr *C.yanet_error
	rc, errno := C.fwstate_map_nat64_object_config_free(ptr, &cErr)
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
		"failed to free nat64state map object: %w",
		cerrors.FromC(unsafe.Pointer(cErr)),
	)
}

// DeleteMapObject removes a named nat64state map object from the
// dataplane.
func DeleteMapObject(agent *ffi.Agent, name string) error {
	objectType := C.CString(MapObjectType)
	defer C.free(unsafe.Pointer(objectType))
	objectName := C.CString(name)
	defer C.free(unsafe.Pointer(objectName))

	var cErr *C.yanet_error
	rc := C.agent_delete_object(
		(*C.struct_agent)(agent.AsRawPtr()),
		objectType,
		objectName,
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf(
			"failed to delete object type %q name %q: %w",
			MapObjectType,
			name,
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return nil
}

// ReadSessions returns the live sessions of the object's lan table after
// the cursor, at most count of them, and the cursor to resume from.
//
// The cursor is a slot index into the table's active layer. It stays valid
// across calls as long as the map keeps that layer: the registry
// recreating the map gives it a new first layer, so a cursor from before
// names an unrelated slot after. A page read after a recreation can
// therefore repeat or miss sessions; it cannot read freed memory, because
// the registry's lock covers each call.
//
// count of zero returns the cursor unchanged and no sessions.
func (m *MapObjectConfig) ReadSessions(
	cursor int64,
	now uint64,
	count uint32,
) ([]Session, int64, error) {
	if m.ptr == nil {
		return nil, cursor, fmt.Errorf("nat64state map object is nil")
	}
	if count == 0 {
		return nil, cursor, nil
	}
	if count > maxSessionBatch {
		count = maxSessionBatch
	}

	object := C.cnat64s_from_cp_object(m.ptr)
	slots := make([]C.struct_cnat64s_session_slot, count)
	next := C.int64_t(cursor)
	n := C.cnat64s_read_lan_sessions(
		object,
		C.int64_t(cursor),
		C.uint64_t(now),
		C.uint32_t(count),
		&slots[0],
		&next,
	)

	sessions := make([]Session, 0, int(n))
	for idx := range int(n) {
		slot := slots[idx]
		key := (*C.struct_nat64state_lan_key)(unsafe.Pointer(slot.key))
		value := (*C.struct_nat64state_value)(unsafe.Pointer(slot.value))

		// Copy every field out here: the slot is dataplane memory that
		// the next refresh overwrites, and the value pointer does not
		// outlive this call.
		session := Session{
			Proto:        uint16(key.hdr.proto),
			ClientPort:   uint16(key.hdr.src_port),
			RemotePort:   uint16(key.hdr.dst_port),
			ExternalPort: uint16(value.port),
			Established:  bool(value.tcp_established),
			ClientClosed: bool(value.lan_closed),
			RemoteClosed: bool(value.wan_closed),
			CreatedAt:    uint64(value.created_at),
			UpdatedAt:    uint64(value.updated_at),
			LastTTLNs:    uint64(C.cnat64s_session_last_ttl(value)),
		}
		copy(session.ClientAddr[:], C.GoBytes(unsafe.Pointer(&key.src_addr[0]), 16))
		copy(session.RemoteAddr[:], C.GoBytes(unsafe.Pointer(&key.dst_addr[0]), 16))
		copy(session.ExternalAddr[:], C.GoBytes(unsafe.Pointer(&value.ip4), 4))
		session.ExpiresAt = session.UpdatedAt + session.LastTTLNs
		sessions = append(sessions, session)
	}
	return sessions, int64(next), nil
}
