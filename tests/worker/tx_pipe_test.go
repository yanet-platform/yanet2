// Package worker_test holds regression tests for the tx-pipe unit in
// lib/dataplane/worker/tx_pipe.c, reached through the
// lib/dataplane_ut/tx_pipe.h fixture.
//
// A pass confirms the drain/reclaim cycle returns every mbuf to its pool
// exactly once, across chain shapes and segment-release orders.
package worker_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
)

// newTxPipeFixture constructs a fixture and registers its teardown.
func newTxPipeFixture(t *testing.T) *dataplaneut.TxPipeFixture {
	t.Helper()

	fixture, err := dataplaneut.NewTxPipeFixture()
	require.NoError(t, err)
	t.Cleanup(fixture.Free)

	return fixture
}

// TestPushRejectedReclaimReturnsMbufToPool verifies that a single-segment
// mbuf a Drain call rejects outright (accept=0) is fully released once
// Reclaim runs: the drain frees it as an untransmitted tail, and reclaim's
// own FIFO pass over the deferred-free ring observes the drop and does not
// double free it.
func TestPushRejectedReclaimReturnsMbufToPool(t *testing.T) {
	fixture := newTxPipeFixture(t)

	mbuf, err := fixture.AllocMbuf(1)
	require.NoError(t, err)
	require.Equal(t, 1, fixture.Outstanding())

	require.NoError(t, fixture.Push(mbuf))

	fixture.Drain(0)
	fixture.Reclaim()

	require.Equal(t, 0, fixture.Outstanding())
}

// TestPushAcceptedCompleteReclaimReturnsMbufToPool verifies the accepted
// path: a Drain call that transmits the mbuf (accept=1) leaves it alive
// until a simulated NIC completion, and only then does Reclaim release it.
func TestPushAcceptedCompleteReclaimReturnsMbufToPool(t *testing.T) {
	fixture := newTxPipeFixture(t)

	mbuf, err := fixture.AllocMbuf(1)
	require.NoError(t, err)

	require.NoError(t, fixture.Push(mbuf))

	fixture.Drain(1)
	require.Equal(t, 1, fixture.Outstanding(), "an accepted mbuf must survive drain until NIC completion")

	mbuf.Complete()
	fixture.Reclaim()

	require.Equal(t, 0, fixture.Outstanding())
}

// TestDrainPartialAcceptWithinBurst verifies that when one Drain call pops a
// burst of several pushed mbufs but a stub transmit accepts only a prefix,
// the accepted prefix survives for later completion while the rejected
// remainder is freed immediately — and that a single Reclaim afterwards
// balances both outcomes across the whole burst.
func TestDrainPartialAcceptWithinBurst(t *testing.T) {
	fixture := newTxPipeFixture(t)

	const burstSize = 3
	mbufs := make([]*dataplaneut.TxPipeMbuf, burstSize)
	for idx := range mbufs {
		mbuf, err := fixture.AllocMbuf(1)
		require.NoError(t, err)
		require.NoError(t, fixture.Push(mbuf))
		mbufs[idx] = mbuf
	}
	require.Equal(t, burstSize, fixture.Outstanding())

	fixture.Drain(1)
	// A rejected mbuf only loses its push-time pin here. The deferred-free
	// ring is FIFO, so releasing it back to the pool waits for Reclaim to
	// walk past the still-outstanding accepted head entry ahead of it.
	require.Equal(t, burstSize, fixture.Outstanding())

	mbufs[0].Complete()
	fixture.Reclaim()

	require.Equal(t, 0, fixture.Outstanding())
}

// TestReclaimFreesPipeSlotsForReuse verifies that filling the pipe to
// capacity blocks further pushes, and that draining, completing, and
// reclaiming every outstanding mbuf frees its slots so the pipe accepts
// pushes again.
func TestReclaimFreesPipeSlotsForReuse(t *testing.T) {
	fixture := newTxPipeFixture(t)

	var mbufs []*dataplaneut.TxPipeMbuf
	for {
		mbuf, err := fixture.AllocMbuf(1)
		require.NoError(t, err)

		if err := fixture.Push(mbuf); err != nil {
			// The mbuf was allocated to probe fullness and never pushed,
			// so it holds no extra pin — Complete releases it back
			// to the pool exactly like a plain single-owner free.
			mbuf.Complete()
			break
		}
		mbufs = append(mbufs, mbuf)
	}
	require.NotEmpty(t, mbufs, "the pipe must accept at least one push before filling up")

	fixture.Drain(len(mbufs))
	for _, mbuf := range mbufs {
		mbuf.Complete()
	}
	fixture.Reclaim()
	require.Equal(t, 0, fixture.Outstanding())

	freed, err := fixture.AllocMbuf(1)
	require.NoError(t, err)
	require.NoError(t, fixture.Push(freed), "a push after reclaim should find a free pipe slot")

	fixture.Drain(1)
	freed.Complete()
	fixture.Reclaim()
}

// TestPushRejectedMultiSegmentReclaimReturnsMbufToPool verifies that a
// multi-segment mbuf a Drain call rejects outright returns every segment to
// the pool exactly once via Reclaim.
func TestPushRejectedMultiSegmentReclaimReturnsMbufToPool(t *testing.T) {
	fixture := newTxPipeFixture(t)

	const segCount = 3
	mbuf, err := fixture.AllocMbuf(segCount)
	require.NoError(t, err)
	require.Equal(t, segCount, fixture.Outstanding())

	require.NoError(t, fixture.Push(mbuf))

	fixture.Drain(0)
	fixture.Reclaim()

	require.Equal(t, 0, fixture.Outstanding())
}

// TestPushAcceptedMultiSegmentCompleteReclaimReturnsMbufToPool verifies that
// a multi-segment mbuf accepted by Drain returns every segment to the pool
// exactly once, after Complete and Reclaim run.
func TestPushAcceptedMultiSegmentCompleteReclaimReturnsMbufToPool(t *testing.T) {
	fixture := newTxPipeFixture(t)

	const segCount = 3
	mbuf, err := fixture.AllocMbuf(segCount)
	require.NoError(t, err)

	require.NoError(t, fixture.Push(mbuf))

	fixture.Drain(1)
	require.Equal(t, segCount, fixture.Outstanding(), "an accepted chain must survive drain until NIC completion")

	mbuf.Complete()
	fixture.Reclaim()

	require.Equal(t, 0, fixture.Outstanding())
}

// TestReclaimIsOrderIndependentAcrossSegmentReleaseOrder verifies that
// reclaim waits for every segment of a drained chain to be released before
// freeing any of them, in any release order.
//
// It releases head-first, the order that defeats a readiness check which
// inspects only the head instead of the whole chain.
func TestReclaimIsOrderIndependentAcrossSegmentReleaseOrder(t *testing.T) {
	fixture := newTxPipeFixture(t)

	const segCount = 3
	mbuf, err := fixture.AllocMbuf(segCount)
	require.NoError(t, err)

	require.NoError(t, fixture.Push(mbuf))
	fixture.Drain(1)

	segments := make([]*dataplaneut.TxPipeMbuf, segCount)
	for idx := range segments {
		segments[idx] = mbuf.Segment(idx)
		require.NotNil(t, segments[idx])
	}

	for released := 1; released <= segCount; released++ {
		segments[released-1].CompleteSegment()
		fixture.Reclaim()

		if released < segCount {
			require.Equal(t, segCount, fixture.Outstanding(),
				"no segment may return to the pool until every segment of the chain is released")
			continue
		}

		require.Equal(t, 0, fixture.Outstanding(),
			"every segment must be back in the pool exactly once the last one is released")
	}
}

// pushNonUniformChain builds a segCount-segment chain, bumps the refcount
// of the segment at mismatchIdx (>= 1) so it disagrees with the head,
// pushes it expecting rejection, and returns the segments with their
// pre-push refcounts.
func pushNonUniformChain(t *testing.T, fixture *dataplaneut.TxPipeFixture, segCount, mismatchIdx int) ([]*dataplaneut.TxPipeMbuf, []uint16) {
	t.Helper()

	mbuf, err := fixture.AllocMbuf(segCount)
	require.NoError(t, err)

	segments := make([]*dataplaneut.TxPipeMbuf, segCount)
	for idx := range segments {
		segments[idx] = mbuf.Segment(idx)
		require.NotNil(t, segments[idx])
	}

	segments[mismatchIdx].AddRefcnt(1)

	before := make([]uint16, segCount)
	for idx, segment := range segments {
		before[idx] = segment.Refcnt()
	}

	require.Error(t, fixture.Push(segments[0]), "push must reject a chain whose segments do not all share the head's refcount")

	return segments, before
}

// TestPushRejectsMismatchOnFirstTailSegment verifies that a mismatch on the
// first tail segment leaves every segment's refcount unchanged, and that
// the pipe still accepts a push afterward.
func TestPushRejectsMismatchOnFirstTailSegment(t *testing.T) {
	fixture := newTxPipeFixture(t)

	segments, before := pushNonUniformChain(t, fixture, 3, 1)
	for idx, segment := range segments {
		require.Equal(t, before[idx], segment.Refcnt(),
			"segment %d refcount must be unchanged by the rejected push and its unwind", idx)
	}

	// Restore uniformity and confirm the pipe isn't wedged.
	segments[1].AddRefcnt(-1)
	require.NoError(t, fixture.Push(segments[0]))
	fixture.Drain(1)
	segments[0].Complete()
	fixture.Reclaim()

	require.Equal(t, 0, fixture.Outstanding(),
		"the recovered chain must be back in the pool exactly once")
}

// TestPushRejectsMismatchOnLaterTailSegment verifies that a mismatch on a
// later tail segment also leaves every segment's refcount unchanged and the
// pipe still usable afterward.
//
// By this point Push has already pinned two segments, so this is the case
// that catches an unwind collapsed to a single decrement — the first-tail
// case passes vacuously, since it only had the head pinned.
func TestPushRejectsMismatchOnLaterTailSegment(t *testing.T) {
	fixture := newTxPipeFixture(t)

	segments, before := pushNonUniformChain(t, fixture, 3, 2)
	for idx, segment := range segments {
		require.Equal(t, before[idx], segment.Refcnt(),
			"segment %d refcount must be unchanged by the rejected push and its unwind", idx)
	}

	// Restore uniformity and confirm the pipe isn't wedged.
	segments[2].AddRefcnt(-1)
	require.NoError(t, fixture.Push(segments[0]))
	fixture.Drain(1)
	segments[0].Complete()
	fixture.Reclaim()

	require.Equal(t, 0, fixture.Outstanding(),
		"the recovered chain must be back in the pool exactly once")
}
