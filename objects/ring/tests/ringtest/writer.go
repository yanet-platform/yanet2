package ringtest

//#cgo CFLAGS: -I../../../../
//#cgo LDFLAGS: -L../../../../build/objects/ring/tests -lringtest_writer
//#cgo LDFLAGS: -L../../../../build/objects/ring/api -lring_objects
//#cgo LDFLAGS: -L../../../../build/lib/controlplane/config -lconfig_cp
//
//#include <stdlib.h>
//
//#include "objects/ring/tests/ringtest_writer.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
)

// Writer runs the C writer functions on one worker's ring.
//
// It finds the ring through a raw object pointer.
type Writer struct {
	object    unsafe.Pointer
	workerIdx uint16
	worker    *C.struct_ring_worker
	data      *C.uint8_t
}

// NewWriter returns a Writer for one worker of a ring object, given as a raw
// pointer.
func NewWriter(objPtr unsafe.Pointer, workerIdx uint16) (*Writer, error) {
	cpObject := (*C.struct_cp_object)(objPtr)

	worker := C.ring_object_worker(cpObject, C.uint64_t(workerIdx))
	if worker == nil {
		return nil, fmt.Errorf("worker index %d has no ring", workerIdx)
	}
	data := C.ring_object_worker_data(cpObject, C.uint64_t(workerIdx))
	if data == nil {
		return nil, fmt.Errorf("worker index %d has no data area", workerIdx)
	}

	return &Writer{object: objPtr, workerIdx: workerIdx, worker: worker, data: data}, nil
}

// NewPublishedWriter returns a Writer for one worker of the ring published
// under the given name.
//
// It finds the ring the way the service that owns it publishes it.
func NewPublishedWriter(agent *ffi.Agent, name string, workerIdx uint16) (*Writer, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	object := C.ringtest_lookup_ring((*C.struct_agent)(agent.AsRawPtr()), cName)
	if object == nil {
		return nil, fmt.Errorf("ring %q is not published", name)
	}
	return NewWriter(unsafe.Pointer(object), workerIdx)
}

// Object returns the raw ring object pointer this writer uses.
func (m *Writer) Object() unsafe.Pointer {
	return m.object
}

// Source returns the production record source of this writer's worker.
//
// So a test reads records the same way a real reader does.
func (m *Writer) Source() (cring.RecordSource, error) {
	sources, err := cring.SourcesFromRaw(m.object)
	if err != nil {
		return nil, err
	}
	if int(m.workerIdx) >= len(sources) {
		return nil, fmt.Errorf("worker index %d has no ring source", m.workerIdx)
	}
	return sources[m.workerIdx], nil
}

// SetIndices sets the write and readable positions.
//
// It sets both the writer's own copies and the published ones. A test uses
// it to start near the physical end or with a backlog, without writing
// records to get there.
func (m *Writer) SetIndices(write, readable uint64) {
	C.ringtest_set_positions(m.worker, C.uint64_t(write), C.uint64_t(readable))
}

// CorruptTotalLen overwrites the length in the frame at a logical offset.
//
// A test uses it to get a corrupt frame without a real race with the
// writer.
func (m *Writer) CorruptTotalLen(logicalOffset uint64, totalLen uint32) {
	C.ringtest_corrupt_total_len(m.worker, m.data, C.uint64_t(logicalOffset), C.uint32_t(totalLen))
}

// WriteRecord commits one record and publishes it.
//
// It returns the sequence number stamped on the record. When the record
// does not fit the ring, it returns an error and writes nothing, like the
// C writer.
func (m *Writer) WriteRecord(payload []byte) (uint32, error) {
	seqno, err := m.CommitRecord(payload)
	if err != nil {
		return 0, err
	}
	m.Publish()
	return seqno, nil
}

// CommitRecord adds one record to the unpublished batch and returns its
// sequence number.
//
// Readers see the record after the next Publish. The ring may also publish
// the batch on its own: when this record fills the publish batch, or when the
// batch would grow too large. When the record does not fit the
// ring, CommitRecord returns an error and writes nothing, like the C
// writer.
func (m *Writer) CommitRecord(payload []byte) (uint32, error) {
	var cPayload *C.uint8_t
	if len(payload) > 0 {
		cPayload = (*C.uint8_t)(unsafe.Pointer(&payload[0]))
	}

	seqno, errno := C.ringtest_commit_record(m.worker, m.data, cPayload, C.uint32_t(len(payload)))
	if seqno < 0 {
		return 0, fmt.Errorf("ring_worker_prepare: %w", errno)
	}
	return uint32(seqno), nil
}

// Publish makes every record committed since the last publication visible
// to readers.
func (m *Writer) Publish() {
	C.ringtest_publish(m.worker)
}
