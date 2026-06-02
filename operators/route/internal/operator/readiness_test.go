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
		tracker.Set("neighbours", readinesspb.State_STATE_READY, nil)
	}

	s := requireScope(t, tracker, "neighbours")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
}

func TestReadiness_ExpectBirdFalse_RibImmediatelyReady(t *testing.T) {
	tracker := operator.NewReadiness([]string{"rib"})

	// When BIRD is not expected, mark rib READY immediately.
	tracker.Set("rib", readinesspb.State_STATE_READY, nil)

	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
}

func TestReadiness_GatewayObserve(t *testing.T) {
	tracker := operator.NewReadiness([]string{"gateway:gw0"})

	// Initial state is UNKNOWN.
	s := requireScope(t, tracker, "gateway:gw0")
	assert.Equal(t, readinesspb.State_STATE_UNKNOWN, s.State)

	// From UNKNOWN, a failed apply transitions to NOT_READY (never programmed).
	tracker.Observe("gateway:gw0", assert.AnError)
	s = requireScope(t, tracker, "gateway:gw0")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.Len(t, s.Reasons, 1)
	assert.Equal(t, "APPLY_FAILED", s.Reasons[0].Code)

	// Successful apply transitions to READY.
	tracker.Observe("gateway:gw0", nil)
	s = requireScope(t, tracker, "gateway:gw0")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
	assert.Empty(t, s.Reasons)

	// Failed apply from READY transitions to DEGRADED (last-good FIB still live).
	tracker.Observe("gateway:gw0", assert.AnError)
	s = requireScope(t, tracker, "gateway:gw0")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State)
	require.Len(t, s.Reasons, 1)
	assert.Equal(t, "APPLY_FAILED", s.Reasons[0].Code)

	// Recovery: successful apply from DEGRADED transitions back to READY.
	tracker.Observe("gateway:gw0", nil)
	s = requireScope(t, tracker, "gateway:gw0")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
	assert.Empty(t, s.Reasons)
}

func TestRIBReadiness_SessionStart_SYNCING(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 50 * time.Millisecond,
		SampleInterval:  20 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

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
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

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
	// Seed a route so that settled && routes>0 produces READY.
	seedRoute(t, store, "route0")
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

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
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

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
	// Seed a route so that the settled state produces READY and the session
	// end with live routes produces DEGRADED(SESSION_ENDED).
	seedRoute(t, store, "route0")
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

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
			helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

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
	// Seed a route so that the settled state produces READY.
	seedRoute(t, store, "route0")
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

	// Session A starts (id=1) — routes are present so tracker enters
	// DEGRADED(SYNCING) rather than NOT_READY(SYNCING).
	helper.OnSessionStart("route0", 1)

	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State)
	require.NotEmpty(t, s.Reasons)
	assert.Equal(t, "SYNCING", s.Reasons[0].Code)

	// Session B starts (id=2), superseding A.
	helper.OnSessionStart("route0", 2)

	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State)
	require.NotEmpty(t, s.Reasons)
	assert.Equal(t, "SYNCING", s.Reasons[0].Code)

	// Session A's stale end arrives — must be ignored because currentSession is 2.
	helper.OnSessionEnd("route0", 1)

	// State must still reflect an active session (SYNCING), not SESSION_ENDED.
	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State)
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

// TestBirdSession_InitialDegraded verifies that the bird-session scope starts
// at DEGRADED(NO_SESSION) when the helper is constructed.
func TestBirdSession_InitialDegraded(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 50 * time.Millisecond,
		SampleInterval:  20 * time.Millisecond,
		ReconnectGrace:  100 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"bird-session", "rib"})
	newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

	s := requireScope(t, tracker, "bird-session")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State)
	require.Len(t, s.Reasons, 1)
	assert.Equal(t, "NO_SESSION", s.Reasons[0].Code)
}

// TestBirdSession_SessionStartReady verifies that a session start transitions
// the bird-session scope to READY.
func TestBirdSession_SessionStartReady(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 50 * time.Millisecond,
		SampleInterval:  20 * time.Millisecond,
		ReconnectGrace:  100 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"bird-session", "rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)

	s := requireScope(t, tracker, "bird-session")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
	assert.Empty(t, s.Reasons)
}

// TestBirdSession_SessionEndReconnecting verifies that a session end
// transitions bird-session to DEGRADED(RECONNECTING).
func TestBirdSession_SessionEndReconnecting(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 50 * time.Millisecond,
		SampleInterval:  20 * time.Millisecond,
		ReconnectGrace:  500 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"bird-session", "rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)
	helper.OnSessionEnd("route0", 1)

	s := requireScope(t, tracker, "bird-session")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State)
	require.Len(t, s.Reasons, 1)
	assert.Equal(t, "RECONNECTING", s.Reasons[0].Code)
}

// TestBirdSession_GraceExpiry verifies that the ticker flips bird-session
// from RECONNECTING to DOWN after ReconnectGrace elapses.
func TestBirdSession_GraceExpiry(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 50 * time.Millisecond,
		SampleInterval:  20 * time.Millisecond,
		ReconnectGrace:  60 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"bird-session", "rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)
	helper.OnSessionEnd("route0", 1)

	ctx, cancel := context.WithCancel(t.Context())
	eg, _ := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// Wait well past grace to ensure the ticker fires after grace elapses.
	time.Sleep(200 * time.Millisecond)

	s := requireScope(t, tracker, "bird-session")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State)
	require.Len(t, s.Reasons, 1)
	assert.Equal(t, "DOWN", s.Reasons[0].Code)

	cancel()
	_ = eg.Wait()
}

// TestBirdSession_NeverNotReady verifies that bird-session never reaches
// NOT_READY regardless of session events.
func TestBirdSession_NeverNotReady(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 50 * time.Millisecond,
		SampleInterval:  20 * time.Millisecond,
		ReconnectGrace:  30 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"bird-session", "rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)
	helper.OnSessionEnd("route0", 1)

	ctx, cancel := context.WithCancel(t.Context())
	eg, _ := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// Allow grace to expire.
	time.Sleep(150 * time.Millisecond)

	s := requireScope(t, tracker, "bird-session")
	assert.NotEqual(t, readinesspb.State_STATE_NOT_READY, s.State,
		"bird-session must never reach NOT_READY")

	cancel()
	_ = eg.Wait()
}

// TestBirdSession_ReconnectFromDown verifies that a new session start from
// DEGRADED(DOWN) transitions bird-session back to READY.
func TestBirdSession_ReconnectFromDown(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 50 * time.Millisecond,
		SampleInterval:  20 * time.Millisecond,
		ReconnectGrace:  30 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"bird-session", "rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)
	helper.OnSessionEnd("route0", 1)

	ctx, cancel := context.WithCancel(t.Context())
	eg, _ := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// Allow grace to expire, reaching DOWN.
	time.Sleep(150 * time.Millisecond)
	s := requireScope(t, tracker, "bird-session")
	assert.Equal(t, "DOWN", s.Reasons[0].Code)

	// Reconnect — must return to READY.
	helper.OnSessionStart("route0", 2)

	s = requireScope(t, tracker, "bird-session")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
	assert.Empty(t, s.Reasons)

	cancel()
	_ = eg.Wait()
}

// TestRIBReadiness_SettledNoRoutes verifies the key fix: when the session is
// settled but the RIB is empty, the rib scope reports NOT_READY(NO_ROUTES).
func TestRIBReadiness_SettledNoRoutes(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 40 * time.Millisecond,
		SampleInterval:  15 * time.Millisecond,
		ReconnectGrace:  500 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	// No routes seeded — empty RIB.
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)

	ctx, cancel := context.WithCancel(t.Context())
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// Wait for the stability window to clear.
	time.Sleep(200 * time.Millisecond)

	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.Len(t, s.Reasons, 1)
	assert.Equal(t, "NO_ROUTES", s.Reasons[0].Code)

	cancel()
	_ = eg.Wait()
}

// TestRIBReadiness_SessionEndedWithRoutes verifies that READY→SessionEnd
// transitions rib to DEGRADED(SESSION_ENDED) when routes are still present.
func TestRIBReadiness_SessionEndedWithRoutes(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 30 * time.Millisecond,
		SampleInterval:  15 * time.Millisecond,
		ReconnectGrace:  500 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	seedRoute(t, store, "route0")
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)

	ctx, cancel := context.WithCancel(t.Context())
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// Wait for READY.
	time.Sleep(150 * time.Millisecond)
	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)

	helper.OnSessionEnd("route0", 1)

	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State)
	require.Len(t, s.Reasons, 1)
	assert.Equal(t, "SESSION_ENDED", s.Reasons[0].Code)

	cancel()
	_ = eg.Wait()
}

// TestRIBReadiness_TTLPurgeAfterSessionEnd verifies that when a session has
// ended (rib = DEGRADED·SESSION_ENDED with routes present) and the TTL purge
// subsequently empties the RIB, the next ticker evaluation transitions rib to
// NOT_READY(NO_ROUTES) without waiting for a new session start.
func TestRIBReadiness_TTLPurgeAfterSessionEnd(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 30 * time.Millisecond,
		SampleInterval:  15 * time.Millisecond,
		ReconnectGrace:  500 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	seedRoute(t, store, "route0")
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)

	ctx, cancel := context.WithCancel(t.Context())
	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// Wait for READY.
	time.Sleep(150 * time.Millisecond)
	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)

	// Session ends — routes still present, so rib must be DEGRADED(SESSION_ENDED).
	helper.OnSessionEnd("route0", 1)

	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State)
	require.Len(t, s.Reasons, 1)
	assert.Equal(t, "SESSION_ENDED", s.Reasons[0].Code)

	// Simulate TTL purge: remove the route from the store so that hasRoutes()
	// returns false.
	ribRef := store.GetOrCreate("route0")
	prefix := netip.MustParsePrefix("10.0.0.0/24")
	nexthop := netip.MustParseAddr("192.168.1.1")
	require.NoError(t, ribRef.RemoveUnicastRoute(prefix, nexthop, rib.RouteSourceStatic))

	// Advance at least one tick and verify rib transitions to NOT_READY(NO_ROUTES)
	// without any new session start.
	time.Sleep(60 * time.Millisecond)

	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.Len(t, s.Reasons, 1)
	assert.Equal(t, "NO_ROUTES", s.Reasons[0].Code)

	cancel()
	_ = eg.Wait()
}

// TestNeighbours_SyncThenError verifies the neighbours scope edge:
// NOT_READY(SYNCING) -> READY on first sync, then DEGRADED(RESYNC) on error,
// then READY on recovery.
func TestNeighbours_SyncThenError(t *testing.T) {
	tracker := operator.NewReadiness([]string{"neighbours"})

	// Seed initial NOT_READY(SYNCING) as operator.go does.
	tracker.Set("neighbours",
		readinesspb.State_STATE_NOT_READY,
		&readinesspb.Reason{Code: "SYNCING"},
	)

	s := requireScope(t, tracker, "neighbours")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.Len(t, s.Reasons, 1)
	assert.Equal(t, "SYNCING", s.Reasons[0].Code)

	// First sync completes.
	tracker.Set("neighbours", readinesspb.State_STATE_READY, nil)

	s = requireScope(t, tracker, "neighbours")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
	assert.Empty(t, s.Reasons)

	// Error after sync transitions to DEGRADED(RESYNC).
	tracker.Set("neighbours",
		readinesspb.State_STATE_DEGRADED,
		&readinesspb.Reason{Code: "RESYNC"},
	)

	s = requireScope(t, tracker, "neighbours")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State)
	require.Len(t, s.Reasons, 1)
	assert.Equal(t, "RESYNC", s.Reasons[0].Code)

	// Recovery transitions back to READY.
	tracker.Set("neighbours", readinesspb.State_STATE_READY, nil)

	s = requireScope(t, tracker, "neighbours")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
	assert.Empty(t, s.Reasons)
}

// TestBirdRestartHoldInvariant verifies the cross-scope invariant: during a
// bird-adapter restart within grace+TTL, both bird-session and rib are at most
// DEGRADED (never NOT_READY).
func TestBirdRestartHoldInvariant(t *testing.T) {
	cfg := ReadinessConfig{
		RateThreshold:   1000,
		StabilityWindow: 30 * time.Millisecond,
		SampleInterval:  15 * time.Millisecond,
		ReconnectGrace:  500 * time.Millisecond,
	}

	store := newRIBStore(zap.NewNop())
	seedRoute(t, store, "route0")
	tracker := operator.NewReadiness([]string{"bird-session", "rib"})
	helper := newBirdRIBReadiness(cfg, store, "route0", tracker, zap.NewNop())

	helper.OnSessionStart("route0", 1)

	ctx, cancel := context.WithCancel(t.Context())
	eg, _ := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// Wait for both scopes to reach READY.
	time.Sleep(150 * time.Millisecond)
	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
	s = requireScope(t, tracker, "bird-session")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)

	// Bird-adapter restarts — session ends. Both scopes must go to DEGRADED, not NOT_READY.
	helper.OnSessionEnd("route0", 1)

	s = requireScope(t, tracker, "bird-session")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State,
		"bird-session must be DEGRADED after session end, not NOT_READY")
	assert.NotEqual(t, readinesspb.State_STATE_NOT_READY, s.State)

	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_DEGRADED, s.State,
		"rib must be DEGRADED after session end with live routes")
	assert.NotEqual(t, readinesspb.State_STATE_NOT_READY, s.State)

	// Within grace period — still DEGRADED.
	time.Sleep(50 * time.Millisecond)
	s = requireScope(t, tracker, "bird-session")
	assert.NotEqual(t, readinesspb.State_STATE_NOT_READY, s.State,
		"bird-session must not reach NOT_READY within grace period")

	s = requireScope(t, tracker, "rib")
	assert.NotEqual(t, readinesspb.State_STATE_NOT_READY, s.State,
		"rib must not reach NOT_READY within grace period (routes still live)")

	cancel()
	_ = eg.Wait()
}

// TestStaticRIBReadiness_ReadyWhenRoutesPresent verifies that staticRIBReadiness
// sets rib=READY when a route is seeded before construction and then flips to
// NOT_READY(NO_ROUTES) once the route is removed, all without ever touching the
// bird-session scope.
func TestStaticRIBReadiness_ReadyWhenRoutesPresent(t *testing.T) {
	store := newRIBStore(zap.NewNop())
	seedRoute(t, store, "route0")

	// Register both rib and bird-session so we can assert bird-session is never set.
	tracker := operator.NewReadiness([]string{"rib", "bird-session"})

	helper := newStaticRIBReadiness(store, "route0", tracker, 20*time.Millisecond, zap.NewNop())

	// The constructor must seed rib=READY immediately (route already present).
	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)

	ctx, cancel := context.WithCancel(t.Context())
	eg, _ := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// Confirm the polling loop keeps rib READY across several ticks.
	time.Sleep(80 * time.Millisecond)
	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)

	// Remove the route: next tick must flip to NOT_READY(NO_ROUTES).
	ribRef := store.GetOrCreate("route0")
	prefix := netip.MustParsePrefix("10.0.0.0/24")
	nexthop := netip.MustParseAddr("192.168.1.1")
	require.NoError(t, ribRef.RemoveUnicastRoute(prefix, nexthop, rib.RouteSourceStatic))

	time.Sleep(80 * time.Millisecond)
	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.NotEmpty(t, s.Reasons)
	assert.Equal(t, "NO_ROUTES", s.Reasons[0].Code)

	// bird-session scope must never have been set (still UNKNOWN).
	bs := requireScope(t, tracker, "bird-session")
	assert.Equal(t, readinesspb.State_STATE_UNKNOWN, bs.State,
		"bird-session must never be touched by staticRIBReadiness")

	cancel()
	_ = eg.Wait()
}

// TestStaticRIBReadiness_ColdStart verifies that staticRIBReadiness seeds
// rib=NOT_READY(NO_ROUTES) on construction when the store is empty.
func TestStaticRIBReadiness_ColdStart(t *testing.T) {
	store := newRIBStore(zap.NewNop())
	tracker := operator.NewReadiness([]string{"rib"})

	newStaticRIBReadiness(store, "route0", tracker, 20*time.Millisecond, zap.NewNop())

	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.NotEmpty(t, s.Reasons)
	assert.Equal(t, "NO_ROUTES", s.Reasons[0].Code)
}

// TestStaticRIBReadiness_PostPreRunReady verifies the entry-evaluate fix: when
// the store is empty at construction (seeding NOT_READY/NO_ROUTES) but a route
// is added before Run starts, the first call to Run must flip rib to READY
// immediately without waiting for the first ticker tick.
func TestStaticRIBReadiness_PostPreRunReady(t *testing.T) {
	store := newRIBStore(zap.NewNop())

	// Empty store at construction — seeds NOT_READY(NO_ROUTES), mirroring the
	// real operator where PreRun has not yet populated static.routes.
	tracker := operator.NewReadiness([]string{"rib"})
	helper := newStaticRIBReadiness(store, "route0", tracker, 500*time.Millisecond, zap.NewNop())

	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_NOT_READY, s.State)
	require.NotEmpty(t, s.Reasons)
	assert.Equal(t, "NO_ROUTES", s.Reasons[0].Code)

	// Seed a route after construction, before Run — mirrors PreRun populating routes.
	seedRoute(t, store, "route0")

	// Start Run. The entry evaluate must fire synchronously before the ticker
	// loop, so rib must be READY well before the 500ms sample_interval.
	ctx, cancel := context.WithCancel(t.Context())
	eg, _ := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return helper.Run(ctx)
	})

	// 50ms is far shorter than the 500ms sample_interval; any readiness change
	// here can only come from the entry evaluate, not a ticker tick.
	require.Eventually(t, func() bool {
		s := requireScope(t, tracker, "rib")
		return s.State == readinesspb.State_STATE_READY
	}, 50*time.Millisecond, 2*time.Millisecond,
		"rib must become READY from entry evaluate before first ticker tick")

	cancel()
	_ = eg.Wait()
}

// TestStaticRIBReadiness_NoopsIgnored verifies that OnSessionStart, OnUpdate,
// and OnSessionEnd are all no-ops and do not change the rib scope.
func TestStaticRIBReadiness_NoopsIgnored(t *testing.T) {
	store := newRIBStore(zap.NewNop())
	seedRoute(t, store, "route0")
	tracker := operator.NewReadiness([]string{"rib"})

	helper := newStaticRIBReadiness(store, "route0", tracker, 20*time.Millisecond, zap.NewNop())

	s := requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)

	// Calling session callbacks must not change scope state.
	helper.OnSessionStart("route0", 1)
	helper.OnUpdate(100)
	helper.OnSessionEnd("route0", 1)

	s = requireScope(t, tracker, "rib")
	assert.Equal(t, readinesspb.State_STATE_READY, s.State)
}
