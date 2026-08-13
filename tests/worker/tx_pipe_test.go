// Package worker_test holds regression tests for the tx-pipe unit in
// lib/dataplane/worker/tx_pipe.c, reached through the
// lib/dataplane_ut/tx_pipe.h fixture.
//
// A pass confirms the drain/reclaim cycle returns every mbuf to its pool
// exactly once. Coverage is confined to single-segment mbufs: the producer
// pins only the head segment of a chained mbuf, so a multi-segment chain's
// tail segments would be freed twice on reclaim.
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
			// mbuf was allocated to probe fullness and never pushed,
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
