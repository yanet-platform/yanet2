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

// allocBatch allocates count mbufs of segCount segments each, failing the
// test if the mock pool cannot satisfy them.
func allocBatch(t *testing.T, fixture *dataplaneut.TxPipeFixture, count, segCount int) []*dataplaneut.TxPipeMbuf {
	t.Helper()

	mbufs := make([]*dataplaneut.TxPipeMbuf, count)
	for idx := range mbufs {
		mbuf, err := fixture.AllocMbuf(segCount)
		require.NoError(t, err)
		mbufs[idx] = mbuf
	}

	return mbufs
}

// TestPushBulkWholeBatchReclaimReturnsEveryMbufToPool verifies that a batch
// pushed in one call behaves exactly as the same mbufs pushed one at a time:
// every one survives to the drain, and a single Reclaim after their
// completions returns all of them to the pool exactly once.
func TestPushBulkWholeBatchReclaimReturnsEveryMbufToPool(t *testing.T) {
	fixture := newTxPipeFixture(t)

	const batch = 8
	mbufs := allocBatch(t, fixture, batch, 1)
	require.Equal(t, batch, fixture.Outstanding())

	pushed, rejected := fixture.PushBulk(mbufs)
	require.Equal(t, batch, pushed, "an empty pipe must accept the whole batch in one call")
	require.Empty(t, rejected)

	fixture.Drain(batch)
	require.Equal(t, batch, fixture.Outstanding(),
		"accepted mbufs must survive drain until NIC completion")

	for _, mbuf := range mbufs {
		mbuf.Complete()
	}
	fixture.Reclaim()

	require.Equal(t, 0, fixture.Outstanding())
}

// TestPushBulkMultiSegmentChainsReclaimReturnsEverySegment verifies that a
// batch of chained mbufs pins and releases every segment, not just each
// chain's head.
func TestPushBulkMultiSegmentChainsReclaimReturnsEverySegment(t *testing.T) {
	fixture := newTxPipeFixture(t)

	const batch, segments = 4, 3
	mbufs := allocBatch(t, fixture, batch, segments)
	require.Equal(t, batch*segments, fixture.Outstanding())

	pushed, _ := fixture.PushBulk(mbufs)
	require.Equal(t, batch, pushed)

	fixture.Drain(batch)
	for _, mbuf := range mbufs {
		mbuf.Complete()
	}
	fixture.Reclaim()

	require.Equal(t, 0, fixture.Outstanding(),
		"every segment of every chain must return to the pool exactly once")
}

// TestPushBulkPartialDrainAcceptBalancesBothOutcomes verifies that when a
// drain accepts only a prefix of a bulk-pushed batch, the rejected remainder
// is freed by the drain while the accepted prefix waits for completion, and
// one Reclaim balances both.
func TestPushBulkPartialDrainAcceptBalancesBothOutcomes(t *testing.T) {
	fixture := newTxPipeFixture(t)

	const batch, accept = 6, 2
	mbufs := allocBatch(t, fixture, batch, 1)

	pushed, _ := fixture.PushBulk(mbufs)
	require.Equal(t, batch, pushed)

	fixture.Drain(accept)
	for _, mbuf := range mbufs[:accept] {
		mbuf.Complete()
	}
	fixture.Reclaim()

	require.Equal(t, 0, fixture.Outstanding(),
		"accepted and rejected halves of one batch must both settle")
}

// TestPushBulkRefusesMismatchedChainAloneAndPlacesTheRest verifies that a
// chain whose segments disagree on a refcount is refused on its own while
// every other mbuf in the batch is still placed.
//
// A refused packet is requeued ahead of the ones behind it, so the pipe only
// keeps moving while a refusal stays confined to the chain that caused it.
func TestPushBulkRefusesMismatchedChainAloneAndPlacesTheRest(t *testing.T) {
	fixture := newTxPipeFixture(t)

	const batch, offender = 5, 2
	mbufs := allocBatch(t, fixture, batch, 3)

	// Lift one tail segment above its chain's baseline so the pin walk
	// refuses that chain.
	mbufs[offender].Segment(1).AddRefcnt(1)
	before := mbufs[offender].Segment(1).Refcnt()

	pushed, rejected := fixture.PushBulk(mbufs)
	require.Equal(t, batch-1, pushed,
		"every chain but the mismatched one must be placed")
	require.Len(t, rejected, 1)
	require.True(t, rejected[0].Same(mbufs[offender]),
		"the mismatched chain is the one refused")
	require.Equal(t, before, mbufs[offender].Segment(1).Refcnt(),
		"the mismatched chain's pins must be fully unwound")

	fixture.Drain(batch - 1)
	for idx, mbuf := range mbufs {
		if idx != offender {
			mbuf.Complete()
		}
	}
	fixture.Reclaim()

	require.Equal(t, 3, fixture.Outstanding(),
		"only the refused chain's segments stay the caller's")
}

// TestPushBulkMismatchedHeadDoesNotBlockTheBatch verifies that a mismatched
// chain at the head of a batch does not hold up the mbufs behind it.
//
// The head is the position that decides it: an offender further along leaves
// the packets before it already placed, so only a leading one shows whether a
// refusal stays confined to its own chain.
func TestPushBulkMismatchedHeadDoesNotBlockTheBatch(t *testing.T) {
	fixture := newTxPipeFixture(t)

	mbufs := allocBatch(t, fixture, 3, 2)
	mbufs[0].Segment(1).AddRefcnt(1)

	pushed, rejected := fixture.PushBulk(mbufs)
	require.Equal(t, 2, pushed,
		"the two sound chains must be placed despite leading with a bad one")
	require.Len(t, rejected, 1)
	require.True(t, rejected[0].Same(mbufs[0]))
}

// settleBatch drains, completes and reclaims a whole batch, leaving the pipe
// empty but its ring positions advanced.
func settleBatch(t *testing.T, fixture *dataplaneut.TxPipeFixture, mbufs []*dataplaneut.TxPipeMbuf) {
	t.Helper()

	fixture.Drain(len(mbufs))
	for _, mbuf := range mbufs {
		mbuf.Complete()
	}
	fixture.Reclaim()
}

// TestPushBulkSpansRingWrap verifies that a batch straddling the ring
// buffer's wrap point is placed whole rather than truncated at the boundary.
//
// The ring offers only the slots left before its buffer wraps, so placing a
// batch that crosses the boundary takes a second round trip.
func TestPushBulkSpansRingWrap(t *testing.T) {
	fixture := newTxPipeFixture(t)

	// Advance the ring positions two thirds of the way round, so the next
	// batch cannot be contiguous.
	const offset = 700
	offsetMbufs := allocBatch(t, fixture, offset, 1)
	pushedOffset, _ := fixture.PushBulk(offsetMbufs)
	require.Equal(t, offset, pushedOffset)
	settleBatch(t, fixture, offsetMbufs)
	require.Equal(t, 0, fixture.Outstanding())

	const batch = 600
	mbufs := allocBatch(t, fixture, batch, 1)
	pushed, rejected := fixture.PushBulk(mbufs)
	require.Equal(t, batch, pushed, "a batch spanning the wrap must be placed in full")
	require.Empty(t, rejected)

	settleBatch(t, fixture, mbufs)
	require.Equal(t, 0, fixture.Outstanding())
}

// TestPushBulkStopsWhenRingIsFull verifies that a batch larger than the ring
// is truncated to what fits, leaving the remainder with the caller.
func TestPushBulkStopsWhenRingIsFull(t *testing.T) {
	fixture := newTxPipeFixture(t)

	const ringSlots = 1 << 10
	const batch = ringSlots + 64
	mbufs := allocBatch(t, fixture, batch, 1)

	pushed, rejected := fixture.PushBulk(mbufs)
	require.Equal(t, ringSlots, pushed, "the ring must cap the batch at its slot count")
	require.Len(t, rejected, batch-ringSlots,
		"everything the ring had no room for comes back to the caller")

	settleBatch(t, fixture, mbufs[:ringSlots])
	require.Equal(t, batch-ringSlots, fixture.Outstanding(),
		"the mbufs that never entered the pipe stay the caller's")
}

// TestPushBulkStopsWhenDeferredFreeRingIsFull verifies that the deferred-free
// ring, not just the pipe, bounds a batch.
//
// Reclaim frees pipe slots as soon as the consumer has popped them, but an
// mbuf's deferred-free entry lives until its tx completes. Refilling the pipe
// without completing anything therefore backs up against the deferred-free
// ring, which is deliberately larger than the pipe — so only a batch that
// outlasts several pipe-fulls reaches this limit.
func TestPushBulkStopsWhenDeferredFreeRingIsFull(t *testing.T) {
	fixture := newTxPipeFixture(t)

	const ringSlots = 1 << 10
	const pendingSlots = ringSlots << 2

	var inFlight []*dataplaneut.TxPipeMbuf
	for len(inFlight) < pendingSlots {
		mbufs := allocBatch(t, fixture, ringSlots, 1)
		pushed, _ := fixture.PushBulk(mbufs)
		require.Equal(t, ringSlots, pushed)

		// Pop and reclaim without completing: pipe slots come back,
		// deferred-free entries do not.
		fixture.Drain(ringSlots)
		fixture.Reclaim()
		inFlight = append(inFlight, mbufs...)
	}

	overflow := allocBatch(t, fixture, 16, 1)
	pushed, rejected := fixture.PushBulk(overflow)
	require.Equal(t, 0, pushed, "a full deferred-free ring must refuse the whole batch")
	require.Len(t, rejected, len(overflow))

	for _, mbuf := range inFlight {
		mbuf.Complete()
	}
	fixture.Reclaim()
	require.Equal(t, len(overflow), fixture.Outstanding(),
		"only the refused batch may remain outstanding")
}

// TestStageFillsBatchThenRefuses verifies that staging stops accepting once
// the batch is full and resumes after a flush empties it.
func TestStageFillsBatchThenRefuses(t *testing.T) {
	fixture := newTxPipeFixture(t)

	mbufs := allocBatch(t, fixture, dataplaneut.TxPipeBatchSize+1, 1)
	for idx, mbuf := range mbufs[:dataplaneut.TxPipeBatchSize] {
		require.True(t, fixture.Stage(mbuf), "mbuf %d must stage", idx)
	}
	require.False(t, fixture.Stage(mbufs[dataplaneut.TxPipeBatchSize]),
		"a full batch must refuse further staging")

	pushed, rejected := fixture.Flush()
	require.Equal(t, dataplaneut.TxPipeBatchSize, pushed)
	require.Empty(t, rejected)

	require.True(t, fixture.Stage(mbufs[dataplaneut.TxPipeBatchSize]),
		"a flushed batch must accept staging again")

	pushed, rejected = fixture.Flush()
	require.Equal(t, 1, pushed)
	require.Empty(t, rejected)

	settleBatch(t, fixture, mbufs)
	require.Equal(t, 0, fixture.Outstanding())
}

// TestFlushReturnsRejectedRemainderInOrder verifies that when the pipe cannot
// take the whole staged batch, the refused mbufs come back to the caller in
// the order they were staged, so the caller can requeue them without
// reordering a flow.
func TestFlushReturnsRejectedRemainderInOrder(t *testing.T) {
	fixture := newTxPipeFixture(t)

	// Fill the ring so nothing staged afterwards can be accepted.
	const ringSlots = 1 << 10
	filler := allocBatch(t, fixture, ringSlots, 1)
	pushedFiller, _ := fixture.PushBulk(filler)
	require.Equal(t, ringSlots, pushedFiller)

	const staged = 4
	mbufs := allocBatch(t, fixture, staged, 1)
	for _, mbuf := range mbufs {
		require.True(t, fixture.Stage(mbuf))
	}

	pushed, rejected := fixture.Flush()
	require.Equal(t, 0, pushed, "a full pipe must accept nothing")
	require.Len(t, rejected, staged)
	for idx := range mbufs {
		require.True(t, rejected[idx].Same(mbufs[idx]),
			"rejected mbuf %d must be the one staged at that position", idx)
	}

	settleBatch(t, fixture, filler)
	require.Equal(t, staged, fixture.Outstanding(),
		"the refused mbufs never entered the pipe and are still the caller's")
}
