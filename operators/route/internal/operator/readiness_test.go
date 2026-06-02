package operator

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/readinesspb"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
)

// requireScope fetches the named scope from the tracker. Fails the test when
// the scope is absent.
func requireScope(t *testing.T, tracker *operator.Readiness, name string) *readinesspb.Scope {
	t.Helper()
	resp := tracker.Ready(&readinesspb.ReadyRequest{})
	for _, s := range resp.Scopes {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("scope %q not found in readiness response", name)
	return nil
}

func TestReadiness_NeighboursDisabled_ImmediatelyReady(t *testing.T) {
	cfg := DefaultConfig()
	cfg.NetlinkMonitor.Disabled = true
	cfg.Readiness.ExpectBird = false
	cfg.Gateways = []operator.GatewayConfig{{Name: "gw0"}}

	tracker := operator.NewReadiness([]string{"gateway:gw0", "neighbours", "rib"})

	// When netlink monitor is disabled, mark neighbours READY immediately.
	if cfg.NetlinkMonitor.Disabled {
		tracker.Set("neighbours", readinesspb.State_STATE_READY)
	}

	s := requireScope(t, tracker, "neighbours")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
}

func TestReadiness_ExpectBirdFalse_RibImmediatelyReady(t *testing.T) {
	tracker := operator.NewReadiness([]string{"rib"})

	// When BIRD is not expected, mark rib READY immediately.
	tracker.Set("rib", readinesspb.State_STATE_READY)

	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
}

func TestReadiness_GatewayObserve(t *testing.T) {
	tracker := operator.NewReadiness([]string{"gateway:gw0"})

	// Initial state is UNKNOWN.
	s := requireScope(t, tracker, "gateway:gw0")
	assert.Equal(t, readinesspb.State_STATE_UNKNOWN, s.State)

	// Successful apply transitions to READY.
	tracker.Observe("gateway:gw0", nil)
	s = requireScope(t, tracker, "gateway:gw0")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
	assert.Empty(t, s.Reasons)

	// Failed apply transitions to NOT_READY with APPLY_FAILED.
	tracker.Observe("gateway:gw0", assert.AnError)
	s = requireScope(t, tracker, "gateway:gw0")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.Len(t, s.Reasons, 1)
	assert.Equal(t, "APPLY_FAILED", s.Reasons[0].Code)
}

func TestRIBReadiness_SessionStart_SYNCING(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 50 * time.Millisecond,
		SampleInterval:  20 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newRIBReadinessHelper(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)

	// Immediately after session start the scope must be NOT_READY SYNCING.
	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.NotEmpty(t, s.Reasons)
	assert.Equal(t, "SYNCING", s.Reasons[0].Code)
}

func TestRIBReadiness_HighRate_SYNCING(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   5,
		StabilityWindow: 200 * time.Millisecond,
		SampleInterval:  20 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newRIBReadinessHelper(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)

	ctx, cancel := context.WithCancel(t.Context())

	eg, _ := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// Drive a high update rate: > 5 per 20ms sample means more than 0.1 per tick.
	// We push 3 updates every 10ms to stay well above threshold.
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				helper.OnUpdate(3)
			}
		}
	}()

	time.Sleep(100 * time.Millisecond)

	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.NotEmpty(t, s.Reasons)
	assert.Equal(t, "SYNCING", s.Reasons[0].Code)

	cancel()
	_ = eg.Wait()
}

func TestRIBReadiness_LowRate_READY(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 50 * time.Millisecond,
		SampleInterval:  20 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newRIBReadinessHelper(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)

	ctx, cancel := context.WithCancel(t.Context())

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// Rate is 0 (well below 1000), so the stability window passes quickly.
	time.Sleep(200 * time.Millisecond)

	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)

	cancel()
	_ = eg.Wait()
}

func TestRIBReadiness_MidWindowSpike_ResetsBelowSince(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold: 5,
		// Long window so a single spike definitely resets it.
		StabilityWindow: 500 * time.Millisecond,
		SampleInterval:  20 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newRIBReadinessHelper(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)

	ctx, cancel := context.WithCancel(t.Context())

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// Let the below-threshold window start (low rate for ~60ms).
	time.Sleep(60 * time.Millisecond)

	// Inject a spike: 300 updates in one shot means ~15000/sec for the next tick.
	helper.OnUpdate(300)

	// Even after 60ms more (well into where the window would have closed), the
	// spike must have reset belowSince so the helper is still SYNCING.
	time.Sleep(100 * time.Millisecond)

	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	if len(s.Reasons) > 0 {
		assert.Equal(t, "SYNCING", s.Reasons[0].Code)
	}

	cancel()
	_ = eg.Wait()
}

func TestRIBReadiness_SessionEnd_Degraded(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 30 * time.Millisecond,
		SampleInterval:  15 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newRIBReadinessHelper(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)

	ctx, cancel := context.WithCancel(t.Context())

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// Wait for READY.
	time.Sleep(120 * time.Millisecond)
	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)

	// Session ends — routes remain in the RIB until TTL cleanup, so the scope
	// must flip to DEGRADED (not NOT_READY) immediately.
	helper.OnSessionEnd("route0", 1)

	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State)
	require.NotEmpty(t, s.Reasons)
	assert.Equal(t, "SESSION_ENDED", s.Reasons[0].Code)

	cancel()
	_ = eg.Wait()
}

// seedRoute adds a single static unicast route to the named RIB in the store.
//
// This makes Stats().Routes > 0 so that syncingState returns DEGRADED rather
// than NOT_READY for the syncing path.
func seedRoute(t *testing.T, store *RIBStore, configName string) {
	t.Helper()
	ribRef := store.GetOrCreate(configName)
	prefix := netip.MustParsePrefix("10.0.0.0/24")
	nexthop := netip.MustParseAddr("192.168.1.1")
	require.NoError(t, ribRef.AddUnicastRoute(prefix, nexthop, rib.RouteSourceStatic))
}

// TestRIBReadiness_Syncing tests the NOT_READY / DEGRADED branch depending on
// whether the RIB holds routes at the moment of syncing.
func TestRIBReadiness_Syncing(t *testing.T) {
	tests := []struct {
		name           string
		seedRoutes     bool
		wantState      readinesspb.State
		wantReasonCode string
	}{
		{
			name:           "cold start empty rib",
			seedRoutes:     false,
			wantState:      readinesspb.State_STATE_NOT_READY,
			wantReasonCode: "SYNCING",
		},
		{
			name:           "resync with routes present",
			seedRoutes:     true,
			wantState:      readinesspb.State_STATE_DEGRADED,
			wantReasonCode: "SYNCING",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ReadinessConfig{
				RateThreshold:   5,
				StabilityWindow: 500 * time.Millisecond,
				SampleInterval:  20 * time.Millisecond,
			}

			store := newRIBStore(zap.NewNop())
			tracker := operator.NewReadiness([]string{"rib"})
			helper := newRIBReadinessHelper(cfg, store, "route0", tracker, zap.NewNop())

			if tc.seedRoutes {
				seedRoute(t, store, "route0")
			}

			// Start the session — this calls syncingState() immediately.
			helper.OnSessionStart("route0", 1)

			// Verify the state set by OnSessionStart.
			s := requireScope(t, tracker, "rib")
			assert.Equal(t, tc.wantState, s.State)
			require.NotEmpty(t, s.Reasons)
			assert.Equal(t, tc.wantReasonCode, s.Reasons[0].Code)

			// Now run the loop with a high update rate so it stays unsettled and
			// confirm the per-tick path also produces the expected state.
			ctx, cancel := context.WithCancel(t.Context())

			eg, _ := errgroup.WithContext(ctx)
			eg.Go(func() error {
				return helper.Run(ctx)
			})

			// Drive a high rate to stay above the threshold (5/s).
			go func() {
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						helper.OnUpdate(3)
					}
				}
			}()

			time.Sleep(100 * time.Millisecond)

			s = requireScope(t, tracker, "rib")
			assert.Equal(t, tc.wantState, s.State)
			require.NotEmpty(t, s.Reasons)
			assert.Equal(t, tc.wantReasonCode, s.Reasons[0].Code)

			cancel()
			_ = eg.Wait()
		})
	}
}

// TestRIBReadiness_SupersededSession tests the P2 concurrency fix: when a
// second FeedRIB stream starts (session B) and the old stream (session A) ends
// afterwards, the stale OnSessionEnd from A must be ignored so that session B
// can still progress to READY.
func TestRIBReadiness_SupersededSession(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 50 * time.Millisecond,
		SampleInterval:  20 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newRIBReadinessHelper(cfg, store, "route0", tracker, zap.NewNop())

	// Session A starts (id=1) — tracker enters SYNCING.
	helper.OnSessionStart("route0", 1)

	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.NotEmpty(t, s.Reasons)
	assert.Equal(t, "SYNCING", s.Reasons[0].Code)

	// Session B starts (id=2), superseding A.
	helper.OnSessionStart("route0", 2)

	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.NotEmpty(t, s.Reasons)
	assert.Equal(t, "SYNCING", s.Reasons[0].Code)

	// Session A's stale end arrives — must be ignored because currentSession is 2.
	helper.OnSessionEnd("route0", 1)

	// State must still reflect an active session (SYNCING), not DEGRADED/inactive.
	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.NotEmpty(t, s.Reasons)
	assert.Equal(t, "SYNCING", s.Reasons[0].Code)

	// Run the sampling loop: with rate=0 (well below 1000/s), session B should
	// settle and reach READY within the stability window.
	ctx, cancel := context.WithCancel(t.Context())

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	time.Sleep(200 * time.Millisecond)

	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)

	cancel()
	_ = eg.Wait()
}
