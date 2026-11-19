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
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// MapObjectType is the object type of the nat64state session tables, as
// registered in the shared-memory object namespace.
const MapObjectType = C.FWSTATE_MAP_NAT64_OBJECT_TYPE

// MapConfig carries the sizing of the session tables' layers and their
// shared stash, mirroring struct fwstate_map_create_config. Zero values
// select their C defaults.
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
	runtime.SetFinalizer(object, func(o *MapObjectConfig) { _ = o.Free() })
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
	rc := C.fwstate_map_nat64_object_create(
		C.cnat64s_from_cp_object(m.ptr), &cConfig,
	)
	if rc != 0 {
		return fmt.Errorf("failed to create the nat64state tables: error code=%d", rc)
	}
	return nil
}

// InsertLayer prepends a fresh layer to both tables' chains.
func (m *MapObjectConfig) InsertLayer(config MapConfig) error {
	if m.ptr == nil {
		return fmt.Errorf("nat64state map object is nil")
	}
	cConfig := config.toC()
	rc := C.fwstate_map_nat64_object_insert_layer(
		C.cnat64s_from_cp_object(m.ptr),
		cConfig.index_size, cConfig.extra_bucket_count,
		cConfig.worker_count,
	)
	if rc != 0 {
		return fmt.Errorf("failed to insert the nat64state layers: error code=%d", rc)
	}
	return nil
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
