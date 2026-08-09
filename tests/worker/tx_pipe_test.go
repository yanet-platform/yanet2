// Package worker_test holds regression tests for the dataplane tx pipe
// implemented in lib/dataplane/worker/tx_pipe.c.
//
// Once a chained mbuf is pushed, the consumer owns the whole chain: it frees
// a rejected mbuf immediately, and an accepted one once
// dataplane_ut_tx_pipe_complete_tx models the NIC's tx completion. The
// producer's reclaim call never frees mbufs itself — it only advances the
// pipe's free position.
package worker_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
)

// newTxPipe constructs a TxPipe sized generously for a single test packet
// and registers its cleanup.
func newTxPipe(t *testing.T) *dataplaneut.TxPipe {
	t.Helper()

	pipe, err := dataplaneut.NewTxPipe(4)
	require.NoError(t, err)
	t.Cleanup(pipe.Free)

	return pipe
}

// Verifies that a multi-segment mbuf rejected by the consumer returns every
// segment, including the tail segments the producer's reclaim never itself
// frees, to the mempool exactly once.
func TestMultiSegmentRejectedTransmitBalancesMempool(t *testing.T) {
	pipe := newTxPipe(t)

	mbuf, err := pipe.AllocMbuf(3, 64)
	require.NoError(t, err)

	require.NoError(t, pipe.Push(mbuf))
	require.EqualValues(t, 1, pipe.Drain(0))
	pipe.Reclaim()

	require.EqualValues(t, 0, pipe.Outstanding(),
		"all segments of the rejected mbuf must be back in the mempool exactly once")
}

// Verifies the positive control for
// TestMultiSegmentRejectedTransmitBalancesMempool: a single-segment mbuf has
// no tail segments for the consumer's free to strand.
func TestSingleSegmentRejectedTransmitBalancesMempool(t *testing.T) {
	pipe := newTxPipe(t)

	mbuf, err := pipe.AllocMbuf(1, 64)
	require.NoError(t, err)

	require.NoError(t, pipe.Push(mbuf))
	require.EqualValues(t, 1, pipe.Drain(0))
	pipe.Reclaim()

	require.EqualValues(t, 0, pipe.Outstanding(),
		"the rejected single-segment mbuf must be back in the mempool exactly once")
}

// Verifies that a multi-segment mbuf accepted by the consumer returns every
// segment to the mempool exactly once, once dataplane_ut_tx_pipe_complete_tx
// models the tx completion.
func TestMultiSegmentAcceptedTransmitBalancesMempool(t *testing.T) {
	pipe := newTxPipe(t)

	mbuf, err := pipe.AllocMbuf(3, 64)
	require.NoError(t, err)

	require.NoError(t, pipe.Push(mbuf))
	require.EqualValues(t, 1, pipe.Drain(1))
	pipe.CompleteTx()
	pipe.Reclaim()

	require.EqualValues(t, 0, pipe.Outstanding(),
		"all segments of the accepted mbuf must be back in the mempool exactly once")
}

// Verifies that worker_tx_pipe_reclaim frees pipe slots so a pipe filled to
// capacity accepts pushes again.
//
// data_pipe_item_push sizes availability off f_pos, which only advances
// during reclaim: without that call, a pipe once filled stays wedged even
// after every mbuf has drained and completed.
func TestReclaimUnwedgesPipeAfterFull(t *testing.T) {
	const pipeSizeLog2 = 1
	const capacity = 1 << pipeSizeLog2

	pipe, err := dataplaneut.NewTxPipe(pipeSizeLog2)
	require.NoError(t, err)
	t.Cleanup(pipe.Free)

	for range capacity {
		mbuf, err := pipe.AllocMbuf(1, 64)
		require.NoError(t, err)
		require.NoError(t, pipe.Push(mbuf))
	}

	overflow, err := pipe.AllocMbuf(1, 64)
	require.NoError(t, err)
	require.Error(t, pipe.Push(overflow), "push must fail while the pipe is full at capacity")

	require.EqualValues(t, capacity, pipe.Drain(capacity))
	pipe.CompleteTx()
	pipe.Reclaim()

	require.NoError(t, pipe.Push(overflow), "push %d must succeed once reclaim has freed the slot filled in the first round", 0)
	for idx := 1; idx < capacity; idx++ {
		mbuf, err := pipe.AllocMbuf(1, 64)
		require.NoError(t, err)
		require.NoError(t, pipe.Push(mbuf), "push %d must succeed once reclaim has freed the slot filled in the first round", idx)
	}

	require.EqualValues(t, capacity, pipe.Drain(capacity))
	pipe.CompleteTx()
	pipe.Reclaim()

	require.EqualValues(t, 0, pipe.Outstanding(),
		"every mbuf across both rounds must be back in the mempool exactly once")
}
