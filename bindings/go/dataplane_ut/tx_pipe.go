package dataplaneut

/*
#include "lib/dataplane_ut/tx_pipe.h"
*/
import "C"

import (
	"fmt"
)

// TxPipeFixture is a handle to a lib/dataplane_ut/tx_pipe.h fixture.
//
// One worker_tx_pipe is paired with a mock mempool, exercised directly
// without spinning up a full Harness.
type TxPipeFixture struct {
	ptr *C.struct_dataplane_ut_tx_pipe
}

// NewTxPipeFixture constructs a fixture.
// Free must be called when the test is done.
func NewTxPipeFixture() (*TxPipeFixture, error) {
	ptr := C.dataplane_ut_tx_pipe_new()
	if ptr == nil {
		return nil, fmt.Errorf("failed to create tx pipe fixture: dataplane_ut_tx_pipe_new returned NULL")
	}

	return &TxPipeFixture{ptr: ptr}, nil
}

// Free tears down the fixture. Nil-safe.
func (m *TxPipeFixture) Free() {
	C.dataplane_ut_tx_pipe_free(m.ptr)
	m.ptr = nil
}

// AllocMbuf allocates a chained mbuf of segCount segments from the
// fixture's mock pool.
//
// segCount must be at least 1.
func (m *TxPipeFixture) AllocMbuf(segCount int) (*TxPipeMbuf, error) {
	ptr := C.dataplane_ut_tx_pipe_alloc_mbuf(m.ptr, C.size_t(segCount))
	if ptr == nil {
		return nil, fmt.Errorf("failed to allocate a %d-segment mbuf: pool exhausted", segCount)
	}

	return &TxPipeMbuf{ptr: ptr}, nil
}

// Push pushes mbuf onto the fixture's pipe for a later Drain to transmit.
//
// Returns an error if the pipe or its deferred-free ring is full.
func (m *TxPipeFixture) Push(mbuf *TxPipeMbuf) error {
	if C.dataplane_ut_tx_pipe_push(m.ptr, mbuf.ptr) != 0 {
		return fmt.Errorf("tx pipe push failed: pipe or deferred-free ring full")
	}

	return nil
}

// TxPipeBatchSize mirrors the pipe's batch capacity, the most one Flush can
// hold.
const TxPipeBatchSize = 32

// PushBulk pushes a batch onto the fixture's pipe in one call, returning how
// many were accepted and the mbufs that did not enter the pipe.
//
// A short return is a normal outcome: the pipe or its deferred-free ring ran
// out of room, or a chain's segments disagreed on a refcount and was refused
// on its own. The rejected mbufs remain the caller's.
func (m *TxPipeFixture) PushBulk(mbufs []*TxPipeMbuf) (int, []*TxPipeMbuf) {
	if len(mbufs) == 0 {
		return 0, nil
	}

	ptrs := make([]*C.struct_rte_mbuf, len(mbufs))
	for idx, mbuf := range mbufs {
		ptrs[idx] = mbuf.ptr
	}

	rejected := make([]*C.struct_rte_mbuf, len(mbufs))
	var count C.size_t

	pushed := int(C.dataplane_ut_tx_pipe_push_bulk(
		m.ptr, &ptrs[0], C.size_t(len(ptrs)), &rejected[0], &count,
	))

	out := make([]*TxPipeMbuf, int(count))
	for idx := range out {
		out[idx] = &TxPipeMbuf{ptr: rejected[idx]}
	}

	return pushed, out
}

// Stage adds one mbuf to the pipe's pending batch, reporting false when the
// batch is already full and owes a Flush.
func (m *TxPipeFixture) Stage(mbuf *TxPipeMbuf) bool {
	return C.dataplane_ut_tx_pipe_stage(m.ptr, mbuf.ptr) == 0
}

// Flush hands the staged batch to the pipe, returning how many it accepted
// and the mbufs it refused, which never entered the pipe.
func (m *TxPipeFixture) Flush() (int, []*TxPipeMbuf) {
	rejected := make([]*C.struct_rte_mbuf, TxPipeBatchSize)
	var count C.size_t

	pushed := int(C.dataplane_ut_tx_pipe_flush(m.ptr, &rejected[0], &count))

	out := make([]*TxPipeMbuf, int(count))
	for idx := range out {
		out[idx] = &TxPipeMbuf{ptr: rejected[idx]}
	}

	return pushed, out
}

// Drain pops the fixture's pipe, handing at most accept mbufs of each popped
// burst to a stub transmit and freeing whatever it did not accept — the same
// rejected-tail path a short real NIC tx_burst would take.
func (m *TxPipeFixture) Drain(accept int) {
	C.dataplane_ut_tx_pipe_drain(m.ptr, C.size_t(accept))
}

// Reclaim reclaims the fixture's pending ring: consumed pipe slots plus any
// mbufs whose simulated NIC tx has completed.
func (m *TxPipeFixture) Reclaim() {
	C.dataplane_ut_tx_pipe_reclaim(m.ptr)
}

// Outstanding returns the number of mock-pool objects currently allocated
// (dequeued but not yet returned).
func (m *TxPipeFixture) Outstanding() int {
	return int(C.dataplane_ut_tx_pipe_outstanding(m.ptr))
}

// TxPipeMbuf is a handle to a chained mbuf allocated by
// TxPipeFixture.AllocMbuf.
type TxPipeMbuf struct {
	ptr *C.struct_rte_mbuf
}

// Complete simulates the NIC completing transmission of an mbuf a prior
// Drain accepted, releasing it exactly as a real driver's tx-descriptor
// reclaim would once DMA finishes.
//
// Must be called at most once per mbuf, and only for an mbuf a Drain call
// accepted rather than rejected.
func (m *TxPipeMbuf) Complete() {
	C.dataplane_ut_tx_pipe_complete(m.ptr)
}

// Segment returns the segment at chain position idx below m (0 is m
// itself), or nil past the end of the chain.
func (m *TxPipeMbuf) Segment(idx int) *TxPipeMbuf {
	ptr := C.dataplane_ut_tx_pipe_segment(m.ptr, C.size_t(idx))
	if ptr == nil {
		return nil
	}

	return &TxPipeMbuf{ptr: ptr}
}

// Refcnt reads the segment's current DPDK refcount.
func (m *TxPipeMbuf) Refcnt() uint16 {
	return uint16(C.dataplane_ut_tx_pipe_segment_refcnt(m.ptr))
}

// AddRefcnt adds delta to the segment's refcount directly, bypassing the
// pipe.
//
// A positive delta manufactures the mismatch Push rejects, a matching
// negative delta restores uniformity so the chain can be reused through the
// normal push/drain path.
func (m *TxPipeMbuf) AddRefcnt(delta int16) {
	C.dataplane_ut_tx_pipe_segment_refcnt_add(m.ptr, C.int16_t(delta))
}

// CompleteSegment releases exactly this segment's consumer-side reference,
// where Complete releases a whole chain at once.
//
// Release each segment exactly once, through this method or Complete but
// never both, and never twice on the same segment — either mistake
// double-frees it.
func (m *TxPipeMbuf) CompleteSegment() {
	C.dataplane_ut_tx_pipe_complete_segment(m.ptr)
}

// Same reports whether both handles refer to one mbuf.
func (m *TxPipeMbuf) Same(other *TxPipeMbuf) bool {
	return m.ptr == other.ptr
}
