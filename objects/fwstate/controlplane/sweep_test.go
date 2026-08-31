package fwstatemap_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	cfwstate "github.com/yanet-platform/yanet2/objects/fwstate/bindings/go/cfwstate"
	fwstatemap "github.com/yanet-platform/yanet2/objects/fwstate/controlplane"
)

// sweepSource stands in for one map a sweep can consider: it answers the
// precheck from a script and records what reclamation asked of it.
type sweepSource struct {
	mu          sync.Mutex
	reclaimable []bool
	parked      uint32
	unlinkErr   error
	unlinkCalls int
	freeCalls   int
	prechecks   int
}

func (m *sweepSource) HasReclaimable(uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	answer := true
	if m.prechecks < len(m.reclaimable) {
		answer = m.reclaimable[m.prechecks]
	}
	m.prechecks++
	return answer
}

func (m *sweepSource) StaleLayerCount() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.parked
}

func (m *sweepSource) UnlinkStaleLayers(uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unlinkCalls++
	return m.unlinkErr
}

func (m *sweepSource) FreeStaleLayers() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.freeCalls++
	return nil
}

func (m *sweepSource) counts() (int, int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.prechecks, m.unlinkCalls, m.freeCalls
}

// newSweepService builds a service whose barrier only counts, so a test
// can tell a declined sweep from one that published a generation.
func newSweepService(barriers *int) *fwstatemap.FWStateMapService {
	var mu sync.Mutex
	return fwstatemap.NewFWStateMapServiceForTest(func(cfwstate.MapObjectConfig) error {
		mu.Lock()
		defer mu.Unlock()
		*barriers++
		return nil
	})
}

// Test_SweepMap_DeclinesWhenNothingToReclaim verifies that a settled map
// costs no generation barrier.
//
// This is what makes a periodic sweep affordable: an idle router would
// otherwise publish two generations per map per tick forever.
func Test_SweepMap_DeclinesWhenNothingToReclaim(t *testing.T) {
	barriers := 0
	svc := newSweepService(&barriers)
	source := &sweepSource{reclaimable: []bool{false}}

	svc.SweepMap("m", source, cfwstate.MapObjectConfig{}, 100)

	prechecks, unlinks, frees := source.counts()
	require.Equal(t, 1, prechecks, "the precheck must run")
	require.Equal(t, 0, unlinks, "a settled map must not be unlinked")
	require.Equal(t, 0, frees)
	require.Equal(t, 0, barriers, "a declined sweep must publish no generation")
}

// Test_SweepMap_ReclaimsWhenWorkIsWaiting verifies that a map the
// precheck admits goes through the full barrier-unlink-barrier-free
// sequence.
func Test_SweepMap_ReclaimsWhenWorkIsWaiting(t *testing.T) {
	barriers := 0
	svc := newSweepService(&barriers)
	source := &sweepSource{reclaimable: []bool{true}, parked: 1}

	svc.SweepMap("m", source, cfwstate.MapObjectConfig{}, 100)

	_, unlinks, frees := source.counts()
	require.Equal(t, 1, unlinks)
	require.Equal(t, 1, frees)
	require.Equal(t, 2, barriers, "one barrier before the unlink and one after")
}

// Test_SweepMap_RetriesAfterAFailedReclamation verifies that a sweep
// whose reclamation failed is attempted again by the next one.
//
// A failed reclamation is not reported to whoever triggered it, so the
// only thing that releases those layers is a later attempt.
func Test_SweepMap_RetriesAfterAFailedReclamation(t *testing.T) {
	barriers := 0
	svc := newSweepService(&barriers)
	source := &sweepSource{
		reclaimable: []bool{true, true},
		parked:      1,
		unlinkErr:   errors.New("unlink failed"),
	}

	svc.SweepMap("m", source, cfwstate.MapObjectConfig{}, 100)
	_, unlinks, frees := source.counts()
	require.Equal(t, 1, unlinks)
	require.Equal(t, 0, frees, "a failed unlink frees nothing")

	source.mu.Lock()
	source.unlinkErr = nil
	source.mu.Unlock()

	svc.SweepMap("m", source, cfwstate.MapObjectConfig{}, 200)
	_, unlinks, frees = source.counts()
	require.Equal(t, 2, unlinks, "the next sweep must try again")
	require.Equal(t, 1, frees, "the retry releases what the failure left parked")
}

// Test_RunStaleLayerSweeper_StopsWithItsContext verifies that cancelling
// the context ends the loop, which is what lets the module join it
// before the agent the sweep publishes through goes away.
func Test_RunStaleLayerSweeper_StopsWithItsContext(t *testing.T) {
	barriers := 0
	svc := newSweepService(&barriers)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.RunStaleLayerSweeper(ctx, time.Hour)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the sweeper did not stop with its context")
	}
}
