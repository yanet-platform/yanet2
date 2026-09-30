// Package cring provides Go bindings for the standalone ring object.
//
// A ring object is a named record buffer with one ring per worker. When a
// ring is full, the writer drops the oldest records. No dataplane module
// owns the object.
package cring

//#cgo CFLAGS: -I../../../../../
//#cgo LDFLAGS: -L../../../../../build/objects/ring/api -lring_objects
//
//#include "api/agent.h"
//#include "objects/ring/api/ring_object.h"
import "C"

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// ObjectType is the registered shared-memory object type for a ring.
const ObjectType = C.RING_OBJECT_TYPE

// MaxNameLen is the size of the C object-name buffer, including the
// terminating NUL.
//
// So the longest accepted name is one byte shorter than this value.
const MaxNameLen = C.CP_OBJECT_NAME_LEN

// DefaultPublishBatch is the publish batch a ring gets by default.
//
// The publish batch is the number of records the writer commits before it
// makes them visible to readers on its own. A creator may ask for another
// value.
const DefaultPublishBatch = uint32(C.RING_PUBLISH_BATCH_DEFAULT)

// MaxPublishBatch is the largest publish batch a ring accepts.
const MaxPublishBatch = uint32(C.RING_PUBLISH_BATCH_MAX)

// Object is an opaque handle to a named ring object in shared memory.
//
// The control plane owns the object until it frees it.
type Object struct {
	ptr   ffi.ObjectConfig
	agent *ffi.Agent
}

// NewObject creates a ring with the given per-worker capacity and publish
// batch. Both are fixed for the life of the ring.
//
// The dataplane does not see the new object until the caller calls Publish.
// The C layer rejects a capacity that is not a power of two, is smaller
// than the record frame, or is larger than the allocator's biggest block.
// It also rejects a publish batch outside 1 to MaxPublishBatch. Both cases
// return an error that matches cerrors.InvalidArgument with errors.Is.
// NewObject does not look for an already published ring with the same
// name. A caller that must reject duplicate names checks that itself.
func NewObject(agent *ffi.Agent, name string, capacity uint32, publishBatch uint32) (*Object, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	ptr := C.ring_object_config_new(
		(*C.struct_agent)(agent.AsRawPtr()), cName, C.uint32_t(capacity), C.uint32_t(publishBatch), &cErr,
	)
	if ptr == nil {
		return nil, fmt.Errorf("failed to create ring object: %w", cerrors.FromC(unsafe.Pointer(cErr)))
	}

	return &Object{
		ptr:   ffi.NewObjectConfig(unsafe.Pointer(ptr)),
		agent: agent,
	}, nil
}

// AsRawPtr returns the pointer to the underlying C object.
//
// Another CGo package uses it when it needs to reach the object directly.
func (m *Object) AsRawPtr() unsafe.Pointer {
	return m.ptr.AsRawPtr()
}

func (m *Object) asRawPtr() *C.struct_cp_object {
	return (*C.struct_cp_object)(m.ptr.AsRawPtr())
}

// errFreed is returned by a method called after Free destroyed the object.
var errFreed = errors.New("ring object already freed")

// Publish adds or replaces the object in a new configuration generation.
//
// From then on, a module that links the ring by name uses this object.
func (m *Object) Publish() error {
	return m.agent.UpdateObjects([]ffi.ObjectConfig{m.ptr})
}

// Free destroys the object.
//
// While a live generation still holds the object, Free returns
// ffi.ErrStillReferenced. The handle then stays usable, so the caller can
// retry. Free is safe to call more than once. After a successful free, the
// size accessors return 0, and opening a source or a reader and publishing
// are refused.
func (m *Object) Free() error {
	return m.ptr.Free(func(ptr unsafe.Pointer) (int, unsafe.Pointer, error) {
		var cErr *C.yanet_error
		rc, errno := C.ring_object_config_free((*C.struct_cp_object)(ptr), &cErr)
		return int(rc), unsafe.Pointer(cErr), errno
	})
}

// Exists reports whether a ring of the given name is in the agent's
// currently published configuration generation.
func Exists(agent *ffi.Agent, name string) bool {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	return bool(C.ring_object_exists((*C.struct_agent)(agent.AsRawPtr()), cName))
}

// DeleteObject removes the named ring from the dataplane.
//
// It fails while a module config still links the ring. The error then
// matches ffi.ErrBusy with errors.Is.
func DeleteObject(agent *ffi.Agent, name string) error {
	return agent.DeleteObject(ObjectType, name)
}

// Capacity returns the size of each worker's data area in bytes.
//
// The size is fixed when the ring is created.
func (m *Object) Capacity() uint32 {
	ptr := m.asRawPtr()
	if ptr == nil {
		return 0
	}
	return uint32(C.ring_object_capacity(ptr))
}

// PublishBatch returns how many records the writer commits before it
// publishes them on its own.
//
// The value is fixed when the ring is created.
func (m *Object) PublishBatch() uint32 {
	ptr := m.asRawPtr()
	if ptr == nil {
		return 0
	}
	return uint32(C.ring_object_publish_batch(ptr))
}

// Sources returns the RecordSource of each worker's ring, indexed by worker.
//
// Most callers use OpenReaders instead. Sources is for a caller that wraps
// a real source, for example to run the read protocol against writer state
// that the caller controls.
func (m *Object) Sources() ([]RecordSource, error) {
	return SourcesFromRaw(m.AsRawPtr())
}

// SourcesFromRaw is Sources for a raw ring object pointer.
//
// Another CGo package uses it with a pointer it found in a published
// generation.
func SourcesFromRaw(objPtr unsafe.Pointer) ([]RecordSource, error) {
	if objPtr == nil {
		return nil, errFreed
	}
	ptr := (*C.struct_cp_object)(objPtr)

	// The C accessor returns no view after the last worker, so the loop
	// needs no worker count.
	//
	// The count fits in 16 bits, so the loop ends before the index can
	// wrap. Every address comes from the view, which the C archive fills.
	// Go never reads a field of the metadata struct itself: the offset of
	// the published positions depends on the cache line size that the
	// archive was built with, and this package may be built with another.
	var sources []RecordSource
	for idx := uint16(0); ; idx++ {
		var view C.struct_ring_worker_view
		if !C.ring_object_worker_view(ptr, C.uint64_t(idx), &view) {
			break
		}
		if view.data == nil {
			return nil, fmt.Errorf("worker %d has no data area", idx)
		}

		sources = append(sources, &shmSource{
			writeIdx:    (*uint64)(unsafe.Pointer(view.write_idx)),
			readableIdx: (*uint64)(unsafe.Pointer(view.readable_idx)),
			data:        unsafe.Slice((*byte)(unsafe.Pointer(view.data)), uint32(view.size)),
			mask:        uint64(view.mask),
		})
	}
	if len(sources) == 0 {
		return nil, errors.New("ring object has no worker rings")
	}
	return sources, nil
}

// OpenReaders opens one reader for each worker's ring, indexed by worker.
//
// Each reader starts at the oldest readable record of its ring. Every record
// a reader returns carries its worker index. A second call opens another
// set of readers. Each reader keeps its own read position and does not
// affect the others.
func (m *Object) OpenReaders() ([]*Reader, error) {
	sources, err := m.Sources()
	if err != nil {
		return nil, err
	}

	capacity := m.Capacity()
	readers := make([]*Reader, 0, len(sources))
	for idx, src := range sources {
		reader, err := NewReader(uint16(idx), capacity, src)
		if err != nil {
			return nil, err
		}
		readers = append(readers, reader)
	}
	return readers, nil
}
