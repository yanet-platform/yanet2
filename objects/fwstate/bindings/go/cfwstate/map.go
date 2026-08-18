package cfwstate

//#cgo CFLAGS: -I../../../../../
//#cgo LDFLAGS: -L../../../../../build/objects/fwstate/api -lfwstate_objects
//#cgo LDFLAGS: -L../../../../../build/lib/counters -lcounters
//
//#include "api/agent.h"
//#include "common/container_of.h"
//#include "common/numutils.h"
//#include "lib/errors/errors.h"
//#include "lib/fwstate/config.h"
//#include "lib/fwstate/fwmap.h"
//#include "lib/fwstate/fwstate_cursor.h"
//#include "lib/fwstate/fwtable.h"
//#include "objects/fwstate/api/fwstate_map_v4_object.h"
//#include "objects/fwstate/api/fwstate_map_v6_object.h"
//
//// Per-family upcasts: cp_object is the first field of both
//// fwstate_map_v4_object and fwstate_map_v6_object, so the cast
//// preserves the address.
//static inline struct fwstate_map_v4_object *
//fwstate_map_v4_from_cp_object(struct cp_object *cp_object) {
//	return (struct fwstate_map_v4_object *)cp_object;
//}
//static inline struct fwstate_map_v6_object *
//fwstate_map_v6_from_cp_object(struct cp_object *cp_object) {
//	return (struct fwstate_map_v6_object *)cp_object;
//}
//
//// cfwstate_map_resolve_map_v4/v6 walk the object's fwtable layer chain
//// to the requested layer index (0 = active head). The chain uses offset
//// indirection (fwmap_t::next), so ADDR_OF dereferences each link.
//static inline fwmap_t *
//cfwstate_map_resolve_map_v4(
//	struct cp_object *cp_object, uint32_t layer_index
//) {
//	fwtable_t *table = fwstate_map_v4_object_table(cp_object);
//	fwmap_t *layer = ADDR_OF(&table->head);
//	for (uint32_t i = 0; layer != NULL && i < layer_index; i++) {
//		layer = (fwmap_t *)ADDR_OF(&layer->next);
//	}
//	return layer;
//}
//static inline fwmap_t *
//cfwstate_map_resolve_map_v6(
//	struct cp_object *cp_object, uint32_t layer_index
//) {
//	fwtable_t *table = fwstate_map_v6_object_table(cp_object);
//	fwmap_t *layer = ADDR_OF(&table->head);
//	for (uint32_t i = 0; layer != NULL && i < layer_index; i++) {
//		layer = (fwmap_t *)ADDR_OF(&layer->next);
//	}
//	return layer;
//}
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Object type strings matching the C FWSTATE_MAP_V4_OBJECT_TYPE and
// FWSTATE_MAP_V6_OBJECT_TYPE macros.
const (
	// MapV4ObjectType is the registered shared-memory object type for an
	// IPv4 fwstate-map.
	MapV4ObjectType = C.FWSTATE_MAP_V4_OBJECT_TYPE
	// MapV6ObjectType is the registered shared-memory object type for an
	// IPv6 fwstate-map.
	MapV6ObjectType = C.FWSTATE_MAP_V6_OBJECT_TYPE
)

// Kind identifies the address family of a standalone fwstate-map object.
type Kind uint32

// Kind constants select the address family passed to the per-family C
// object constructors.
const (
	// KindV4 selects an IPv4 firewall-state table.
	KindV4 Kind = 0
	// KindV6 selects an IPv6 firewall-state table.
	KindV6 Kind = 1
)

// String returns a human-readable representation of the kind.
func (m Kind) String() string {
	switch m {
	case KindV4:
		return "v4"
	case KindV6:
		return "v6"
	default:
		return "unknown"
	}
}

// ObjectType returns the shared-memory object type string for this kind
// ("fwstate_map_v4" or "fwstate_map_v6").
func (m Kind) ObjectType() string {
	if m == KindV6 {
		return MapV6ObjectType
	}
	return MapV4ObjectType
}

// MapObjectConfig is an opaque handle to a standalone named fwstate-map
// cp_object in shared memory.
//
// It wraps the cp_object pointer returned by the per-family constructor
// (fwstate_map_v4_object_config_new or fwstate_map_v6_object_config_new).
// The underlying struct (fwstate_map_v4_object or fwstate_map_v6_object)
// owns a single fwtable_t for one address family. The object is
// registered under (ObjectType(), Name()) and published via Publish.
type MapObjectConfig struct {
	name       string
	kind       Kind
	generation uint64
	ptr        *C.struct_cp_object
}

// Generation returns the generation counter, incremented on layer insert
// or trim.
func (m *MapObjectConfig) Generation() uint64 {
	return m.generation
}

// NewMapObjectConfig creates a new standalone fwstate-map object for the
// given address family.
//
// The returned handle is not yet published to the dataplane; call Publish
// to publish it. The object starts with an empty fwtable; CreateMap
// installs the first layer.
func NewMapObjectConfig(agent *ffi.Agent, name string, kind Kind) (*MapObjectConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	var ptr *C.struct_cp_object
	if kind == KindV6 {
		ptr = C.fwstate_map_v6_object_config_new(
			(*C.struct_agent)(agent.AsRawPtr()), cName, &cErr,
		)
	} else {
		ptr = C.fwstate_map_v4_object_config_new(
			(*C.struct_agent)(agent.AsRawPtr()), cName, &cErr,
		)
	}
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to initialize fwstate-map object: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}

	return &MapObjectConfig{
		name: name,
		kind: kind,
		ptr:  ptr,
	}, nil
}

// Name returns the map name.
func (m *MapObjectConfig) Name() string {
	return m.name
}

// Kind returns the address family of this map's fwtable.
func (m *MapObjectConfig) Kind() Kind {
	return m.kind
}

func (m *MapObjectConfig) asRawPtr() *C.struct_cp_object {
	return m.ptr
}

// insertLayer appends one fwmap layer to the object's fwtable chain,
// dispatching to the per-family C insert helper. fwtable_insert_layer_cp
// works on an empty table to install the very first layer, so this single
// entry point serves both CreateMap and InsertLayer.
func (m *MapObjectConfig) insertLayer(
	indexSize uint32,
	extraBucketCount uint32,
	workerCount uint16,
) error {
	var rc C.int
	if m.kind == KindV6 {
		rc = C.fwstate_map_v6_object_insert_layer(
			C.fwstate_map_v6_from_cp_object(m.asRawPtr()),
			C.uint32_t(indexSize),
			C.uint32_t(extraBucketCount),
			C.uint16_t(workerCount),
		)
	} else {
		rc = C.fwstate_map_v4_object_insert_layer(
			C.fwstate_map_v4_from_cp_object(m.asRawPtr()),
			C.uint32_t(indexSize),
			C.uint32_t(extraBucketCount),
			C.uint16_t(workerCount),
		)
	}
	if rc != 0 {
		return fmt.Errorf(
			"failed to insert fwstate-map layer: error code=%d", rc,
		)
	}
	m.generation++
	return nil
}

// CreateMap installs the first fwtable layer for this map's address family.
func (m *MapObjectConfig) CreateMap(
	indexSize uint32,
	extraBucketCount uint32,
	workerCount uint16,
) error {
	return m.insertLayer(indexSize, extraBucketCount, workerCount)
}

// InsertLayer inserts a new layer into the fwtable chain of this map.
func (m *MapObjectConfig) InsertLayer(
	indexSize uint32,
	extraBucketCount uint32,
	workerCount uint16,
) error {
	return m.insertLayer(indexSize, extraBucketCount, workerCount)
}

// resolveMap walks the fwtable chain to the requested layer index
// (0 = active head) for this map's address family.
func (m *MapObjectConfig) resolveMap(layerIndex uint32) *C.fwmap_t {
	if m.kind == KindV6 {
		return C.cfwstate_map_resolve_map_v6(m.asRawPtr(), C.uint32_t(layerIndex))
	}
	return C.cfwstate_map_resolve_map_v4(m.asRawPtr(), C.uint32_t(layerIndex))
}

// GetStats retrieves statistics for this map's fwtable.
func (m *MapObjectConfig) GetStats() MapStats {
	return mapStatsFromC(fwmapStatsOrZero(m.resolveMap(0)))
}

// ResolveMap resolves a specific layer's fwmap pointer.
//
// Returns nil if the table has no layers or layerIndex is out of range.
func (m *MapObjectConfig) ResolveMap(layerIndex uint32) unsafe.Pointer {
	ptr := m.resolveMap(layerIndex)
	if ptr == nil {
		return nil
	}
	return unsafe.Pointer(ptr)
}

// UnlinkStaleLayers parks expired layers in the fwtable stale chain.
//
// The parked layers stay allocated until FreeStaleLayers releases
// them after the caller's generation barrier has elapsed; a failed
// barrier leaves them parked for a later round.
func (m *MapObjectConfig) UnlinkStaleLayers(now uint64) error {
	var rc C.int
	if m.kind == KindV6 {
		rc = C.fwstate_map_v6_object_unlink_stale_layers(
			C.fwstate_map_v6_from_cp_object(m.asRawPtr()),
			C.uint64_t(now),
		)
	} else {
		rc = C.fwstate_map_v4_object_unlink_stale_layers(
			C.fwstate_map_v4_from_cp_object(m.asRawPtr()),
			C.uint64_t(now),
		)
	}
	if rc != 0 {
		return fmt.Errorf("failed to unlink stale layers: error code=%d", rc)
	}
	m.generation++
	return nil
}

// FreeStaleLayers releases the layers parked by UnlinkStaleLayers.
//
// Safe only after a generation barrier that every worker advanced past
// since the unlink: the barrier is what proves no reader is still
// walking the parked chain.
func (m *MapObjectConfig) FreeStaleLayers() error {
	if m.kind == KindV6 {
		C.fwstate_map_v6_object_free_stale_layers(
			C.fwstate_map_v6_from_cp_object(m.asRawPtr()),
		)
	} else {
		C.fwstate_map_v4_object_free_stale_layers(
			C.fwstate_map_v4_from_cp_object(m.asRawPtr()),
		)
	}
	return nil
}

// Free releases the underlying C memory via the fwstate-map free handler.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (m *MapObjectConfig) Free() {
	if ptr := m.asRawPtr(); ptr != nil {
		if m.kind == KindV6 {
			C.fwstate_map_v6_object_config_free(ptr)
		} else {
			C.fwstate_map_v4_object_config_free(ptr)
		}
		m.ptr = nil
	}
}

// Publish upserts this object into a new dataplane configuration generation
// via agent_update_objects and blocks until every worker has advanced to it.
//
// This is the generation barrier used after mutating the layer chain: the
// upsert drives cp_config_gen_install, which performs SET_OFFSET_OF on the
// new generation followed by dp_config_wait_for_gen. Re-upserting the same
// object pointer is safe — the registry uses reference counting, so the
// ref/unref pair nets to zero and the object survives intact while only the
// generation advances.
func (m *MapObjectConfig) Publish(agent *ffi.Agent) error {
	if m.ptr == nil {
		return fmt.Errorf("fwstate-map object config is nil")
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

// DeleteMapObject removes a named object from the dataplane by type and
// name (e.g. ("fwstate_map_v4", "default")) via agent_delete_object.
func DeleteMapObject(agent *ffi.Agent, objectType, objectName string) error {
	cObjectType := C.CString(objectType)
	defer C.free(unsafe.Pointer(cObjectType))
	cObjectName := C.CString(objectName)
	defer C.free(unsafe.Pointer(cObjectName))

	var cErr *C.yanet_error
	rc := C.agent_delete_object(
		(*C.struct_agent)(agent.AsRawPtr()),
		cObjectType,
		cObjectName,
		&cErr,
	)
	if rc != 0 {
		return fmt.Errorf(
			"failed to delete object type %q name %q: %w",
			objectType,
			objectName,
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}
	return nil
}

// ReadForward reads up to count entries in the forward direction.
func (m *MapObjectConfig) ReadForward(
	layerIndex uint32,
	index int64,
	includeExpired bool,
	now uint64,
	count uint32,
) ([]CursorEntry, int64, bool, error) {
	return m.readEntries(layerIndex, index, includeExpired, now, count, false)
}

// ReadBackward reads up to count entries in the backward direction.
func (m *MapObjectConfig) ReadBackward(
	layerIndex uint32,
	index int64,
	includeExpired bool,
	now uint64,
	count uint32,
) ([]CursorEntry, int64, bool, error) {
	return m.readEntries(layerIndex, index, includeExpired, now, count, true)
}

func (m *MapObjectConfig) readEntries(
	layerIndex uint32,
	index int64,
	includeExpired bool,
	now uint64,
	count uint32,
	backward bool,
) ([]CursorEntry, int64, bool, error) {
	fwmap := m.resolveMap(layerIndex)
	if fwmap == nil {
		return nil, 0, false, fmt.Errorf("failed to resolve map")
	}

	cursor := C.fwstate_cursor_t{
		key_pos:         C.int64_t(index),
		include_expired: C.bool(includeExpired),
	}

	if count == 0 {
		return nil, int64(cursor.key_pos), false, nil
	}
	if count > maxCursorBatch {
		count = maxCursorBatch
	}

	buf := make([]C.fwstate_cursor_entry_t, count)
	var cEntries *C.fwstate_cursor_entry_t
	if len(buf) > 0 {
		cEntries = &buf[0]
	}

	var n C.uint32_t
	if backward {
		n = C.fwstate_cursor_read_backward(fwmap, &cursor, C.uint64_t(now), cEntries, C.uint32_t(count))
	} else {
		n = C.fwstate_cursor_read_forward(fwmap, &cursor, C.uint64_t(now), cEntries, C.uint32_t(count))
	}

	isIPv6 := m.kind == KindV6
	entries := make([]CursorEntry, 0, n)
	for idx := range n {
		entry := buf[idx]
		val := (*C.struct_fw_state_value)(entry.value)

		entries = append(entries, CursorEntry{
			Key:     convertCKey(entry.key, isIPv6),
			Value:   stateValueFromC(val),
			Idx:     uint32(entry.idx),
			Expired: bool(entry.expired),
		})
	}

	newIndex := int64(cursor.key_pos)
	keyLimit := fwmap.key_cursor
	hasMore := false
	if backward {
		hasMore = newIndex > -1
	} else {
		hasMore = newIndex < int64(keyLimit)
	}

	return entries, newIndex, hasMore, nil
}
