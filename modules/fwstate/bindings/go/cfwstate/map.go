package cfwstate

//#cgo CFLAGS: -I../../../../../
//#cgo CFLAGS: -I../../../../../lib
//
//#include "api/agent.h"
//#include "common/container_of.h"
//#include "common/numutils.h"
//#include "lib/errors/errors.h"
//#include "lib/fwstate/config.h"
//#include "lib/fwstate/fwmap.h"
//#include "lib/fwstate/fwstate_cursor.h"
//#include "lib/fwstate/fwtable.h"
//#include "modules/fwstate/objects/fwstate_map_object.h"
//
//// fwstate_map_from_cp_object upcasts a cp_object pointer to its
//// enclosing fwstate_map_object. cp_object is the first field, so the
//// cast preserves the address.
//static inline struct fwstate_map_object *
//fwstate_map_from_cp_object(struct cp_object *cp_object) {
//	return (struct fwstate_map_object *)cp_object;
//}
//
//// cfwstate_map_resolve_map resolves a layer's fwmap from a map object.
//// Go-side glue: extracts the head fwmap of the embedded fwtable via
//// fwstate_map_object_table + ADDR_OF, then walks the chain via
//// fwstate_resolve_map.
//static inline fwmap_t *
//cfwstate_map_resolve_map(
//	struct cp_object *cp_object, uint32_t layer_index
//) {
//	fwtable_t *table = fwstate_map_object_table(cp_object);
//	fwmap_t *head = ADDR_OF(&table->head);
//	return fwstate_resolve_map(head, layer_index);
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

// Kind constants mirror the C enum fwtable_kind values.
const (
	// KindV4 selects an IPv4 firewall-state table.
	KindV4 Kind = Kind(C.FWTABLE_KIND_V4)
	// KindV6 selects an IPv6 firewall-state table.
	KindV6 Kind = Kind(C.FWTABLE_KIND_V6)
)

// toC converts the Go Kind to the C enum_fwtable_kind used by
// fwstate_map_object_config_new.
func (m Kind) toC() C.enum_fwtable_kind {
	return C.enum_fwtable_kind(m)
}

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
// It wraps the cp_object pointer returned by fwstate_map_object_config_new.
// The underlying struct is fwstate_map_object, which owns a single fwtable_t
// for one address family (v4 or v6). The object is registered under
// (ObjectType(), Name()) and published via agent.UpdateObjects.
type MapObjectConfig struct {
	name       string
	kind       Kind
	generation uint64
	ptr        ffi.ObjectConfig
}

// Generation returns the generation counter, incremented on layer insert
// or trim.
func (m *MapObjectConfig) Generation() uint64 {
	return m.generation
}

// NewMapObjectConfig creates a new standalone fwstate-map object for the
// given address family.
//
// The returned handle is not yet published to the dataplane; call
// agent.UpdateObjects to publish it.
func NewMapObjectConfig(agent *ffi.Agent, name string, kind Kind) (*MapObjectConfig, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	cType := C.CString(kind.ObjectType())
	defer C.free(unsafe.Pointer(cType))

	var cErr *C.yanet_error
	ptr := C.fwstate_map_object_config_new(
		(*C.struct_agent)(agent.AsRawPtr()), cType, cName, kind.toC(), &cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf(
			"failed to initialize fwstate-map object: %w",
			cerrors.FromC(unsafe.Pointer(cErr)),
		)
	}

	return &MapObjectConfig{
		name: name,
		kind: kind,
		ptr:  ffi.NewObjectConfig(unsafe.Pointer(ptr)),
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
	return (*C.struct_cp_object)(m.ptr.AsRawPtr())
}

func (m *MapObjectConfig) objectPtr() *C.struct_fwstate_map_object {
	return C.fwstate_map_from_cp_object(m.asRawPtr())
}

// AsFFIObject returns the underlying common object config handle for
// passing to agent.UpdateObjects.
func (m *MapObjectConfig) AsFFIObject() ffi.ObjectConfig {
	return m.ptr
}

// CreateMap creates the initial fwtable layer for this map's address family.
func (m *MapObjectConfig) CreateMap(
	indexSize uint32,
	extraBucketCount uint32,
	workerCount uint16,
) error {
	if rc := C.fwstate_map_object_create_map(
		m.objectPtr(),
		C.uint32_t(indexSize),
		C.uint32_t(extraBucketCount),
		C.uint16_t(workerCount),
	); rc != 0 {
		return fmt.Errorf(
			"failed to create fwstate-map table: error code=%d", rc,
		)
	}
	m.generation++
	return nil
}

// InsertLayer inserts a new layer into the fwtable chain of this map.
func (m *MapObjectConfig) InsertLayer(
	indexSize uint32,
	extraBucketCount uint32,
	workerCount uint16,
) error {
	if rc := C.fwstate_map_object_insert_layer(
		m.objectPtr(),
		C.uint32_t(indexSize),
		C.uint32_t(extraBucketCount),
		C.uint16_t(workerCount),
	); rc != 0 {
		return fmt.Errorf(
			"failed to insert fwstate-map layer: error code=%d", rc,
		)
	}
	m.generation++
	return nil
}

// GetStats retrieves statistics for this map's fwtable.
func (m *MapObjectConfig) GetStats() MapStats {
	return mapStatsFromC(fwmapStatsOrZero(C.cfwstate_map_resolve_map(m.asRawPtr(), 0)))
}

// ResolveMap resolves a specific layer's fwmap pointer.
//
// Returns nil if the table has no layers or layerIndex is out of range.
func (m *MapObjectConfig) ResolveMap(layerIndex uint32) unsafe.Pointer {
	ptr := C.cfwstate_map_resolve_map(
		m.asRawPtr(),
		C.uint32_t(layerIndex),
	)
	if ptr == nil {
		return nil
	}
	return unsafe.Pointer(ptr)
}

// TrimStaleLayers trims stale layers from the fwtable chain.
//
// Trimmed layers are tracked in the fwtable stale chain and freed on the
// next trim call (giving the dataplane one trim cycle to quiesce), so the
// caller has nothing to free.
func (m *MapObjectConfig) TrimStaleLayers(now uint64) error {
	if rc := C.fwstate_map_object_trim_stale_layers(
		m.objectPtr(),
		C.uint64_t(now),
	); rc != 0 {
		return fmt.Errorf("failed to trim stale layers: error code=%d", rc)
	}
	m.generation++
	return nil
}

// Free releases the underlying C memory via the fwstate-map free handler.
//
// Safe to call multiple times: subsequent calls are no-ops.
func (m *MapObjectConfig) Free() {
	if ptr := m.asRawPtr(); ptr != nil {
		C.fwstate_map_object_config_free(ptr)
		m.ptr = ffi.ObjectConfig{}
	}
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
	fwmap := C.cfwstate_map_resolve_map(m.asRawPtr(), C.uint32_t(layerIndex))
	if fwmap == nil {
		return nil, 0, false, fmt.Errorf("failed to resolve map")
	}

	var cursor C.fwstate_cursor_t
	rc := C.fwstate_cursor_init(
		fwmap, &cursor,
		C.int64_t(index), C.bool(includeExpired),
	)
	if rc != 0 {
		return nil, 0, false, fmt.Errorf("failed to create cursor")
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
