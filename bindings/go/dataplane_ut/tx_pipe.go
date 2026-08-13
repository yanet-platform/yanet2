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
