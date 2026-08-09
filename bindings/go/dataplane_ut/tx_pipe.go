package dataplaneut

/*
#include <stdint.h>

#include "lib/dataplane_ut/tx_pipe.h"
*/
import "C"

import "fmt"

// TxPipe is a handle to a dataplane_ut tx-pipe harness: one worker_tx_pipe
// plus its own malloc-backed mock mempool, letting a test drive the tx pipe
// producer/consumer paths directly.
//
// Free must be called when the test is done.
type TxPipe struct {
	ptr *C.struct_dataplane_ut_tx_pipe
}

// NewTxPipe constructs a TxPipe whose pipe holds (1 << pipeSize) entries,
// per worker_tx_pipe_init.
func NewTxPipe(pipeSize int) (*TxPipe, error) {
	ptr := C.dataplane_ut_tx_pipe_new(C.size_t(pipeSize))
	if ptr == nil {
		return nil, fmt.Errorf("failed to create tx pipe harness: dataplane_ut_tx_pipe_new returned NULL")
	}
	return &TxPipe{ptr: ptr}, nil
}

// Free tears down the harness. Nil-safe.
func (m *TxPipe) Free() {
	if m.ptr == nil {
		return
	}
	C.dataplane_ut_tx_pipe_free(m.ptr)
	m.ptr = nil
}

// TxMbuf is an opaque handle to a chained mbuf allocated from a TxPipe's
// mock mempool by AllocMbuf, to be handed to Push.
type TxMbuf struct {
	ptr *C.struct_rte_mbuf
}

// AllocMbuf allocates a chained mbuf of segCount segments from the harness
// mempool, each segment holding segSize bytes of payload.
func (m *TxPipe) AllocMbuf(segCount, segSize uint32) (*TxMbuf, error) {
	ptr := C.dataplane_ut_tx_pipe_alloc_mbuf(m.ptr, C.uint32_t(segCount), C.uint32_t(segSize))
	if ptr == nil {
		return nil, fmt.Errorf("failed to allocate mbuf: dataplane_ut_tx_pipe_alloc_mbuf returned NULL")
	}
	return &TxMbuf{ptr: ptr}, nil
}

// Push pushes mbuf into the harness pipe (producer side).
//
// Returns an error when the pipe is full.
func (m *TxPipe) Push(mbuf *TxMbuf) error {
	if C.dataplane_ut_tx_pipe_push(m.ptr, mbuf.ptr) != 0 {
		return fmt.Errorf("failed to push mbuf: pipe is full")
	}
	return nil
}

// Drain drains the harness pipe (consumer side), accepting the first
// acceptCount mbufs of each burst and freeing the rest, mirroring a NIC
// tx_burst that only transmits part of a burst.
//
// Returns the number of items drained.
func (m *TxPipe) Drain(acceptCount uint16) uint64 {
	return uint64(C.dataplane_ut_tx_pipe_drain(m.ptr, C.uint16_t(acceptCount)))
}

// CompleteTx frees every mbuf accepted by the stub xmit across prior Drain
// calls, modeling a NIC completing their tx (consumer side).
func (m *TxPipe) CompleteTx() {
	C.dataplane_ut_tx_pipe_complete_tx(m.ptr)
}

// Reclaim releases mbufs whose consumer-side tx has completed (producer
// side).
func (m *TxPipe) Reclaim() {
	C.dataplane_ut_tx_pipe_reclaim(m.ptr)
}

// Outstanding reports the number of mbuf objects currently dequeued from the
// harness mempool but not yet returned, so a test can assert exact balance
// after a push/drain/reclaim sequence.
func (m *TxPipe) Outstanding() uint64 {
	return uint64(C.dataplane_ut_tx_pipe_outstanding(m.ptr))
}
