package operator

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
)

// newRIBReadiness returns a birdRIBReadiness when cfg.ExpectBird is true,
// and a staticRIBReadiness otherwise.
func newRIBReadiness(
	cfg ReadinessConfig,
	store *RIBStore,
	name string,
	tracker *readiness.Tracker,
	log *zap.Logger,
) ribReadiness {
	if cfg.ExpectBird {
		return newBirdRIBReadiness(cfg, store, name, tracker, log)
	}
	return newStaticRIBReadiness(store, name, tracker, cfg.SampleInterval, log)
}

// birdRIBReadiness drives the "bird-session" and "rib" readiness scopes
// from the shared FeedRIB callbacks and a single sampling ticker.
//
// The "bird-session" scope reflects transport liveness and never reaches
// NOT_READY. The "rib" scope reflects route-content readiness and gates
// READY on both a settled update rate and a non-empty RIB.
//
// The bulk-settle gate (RateThreshold/StabilityWindow) is sticky within a
// session — a rate spike after settling does not flip the rib scope back to
// NOT_READY. It resets on each SessionStart.
type birdRIBReadiness struct {
	store      *RIBStore
	configName string
	tracker    *readiness.Tracker

	// counter is the total number of route updates applied in the current
	// session. Written by OnUpdate on the FeedRIB hot path; read by Run.
	counter atomic.Int64

	// mu guards session/settle fields and the consistency of rib/bird-session
	// scope updates. tracker.Set calls for these two scopes are always made
	// while holding mu so that a tick and a session-edge event cannot
	// interleave and produce an inconsistent last-write.
	mu             sync.Mutex
	currentSession uint64
	sessionActive  bool
	lastSessionEnd time.Time // zero while a session is active
	belowSince     time.Time // zero when rate is currently above threshold
	bulkSettled    bool

	rateThreshold   float64
	stabilityWindow time.Duration
	sampleInterval  time.Duration
	reconnectGrace  time.Duration

	log *zap.Logger
}

// newBirdRIBReadiness constructs a helper from the supplied config.
//
// The helper drives both the "bird-session" and "rib" tracker scopes.
// It seeds bird-session to DEGRADED(NO_SESSION) and rib to
// NOT_READY(SYNCING) immediately on construction.
func newBirdRIBReadiness(
	cfg ReadinessConfig,
	store *RIBStore,
	configName string,
	tracker *readiness.Tracker,
	log *zap.Logger,
) *birdRIBReadiness {
	m := &birdRIBReadiness{
		store:           store,
		configName:      configName,
		tracker:         tracker,
		rateThreshold:   cfg.RateThreshold,
		stabilityWindow: cfg.StabilityWindow,
		sampleInterval:  cfg.SampleInterval,
		reconnectGrace:  cfg.ReconnectGrace,
		log:             log,
	}

	// Seed initial states: bird-session starts DEGRADED(NO_SESSION),
	// rib starts NOT_READY(SYNCING).
	tracker.SetWithReason("bird-session",
		readinesspb.State_STATE_DEGRADED,
		&readinesspb.Reason{Code: "NO_SESSION"},
	)
	tracker.SetWithReason("rib",
		readinesspb.State_STATE_NOT_READY,
		&readinesspb.Reason{Code: "SYNCING"},
	)

	return m
}

// hasRoutes reports whether the tracked config's RIB currently holds any routes.
//
// It uses the O(1) RIB stats snapshot, so it is cheap to call every tick.
func (m *birdRIBReadiness) hasRoutes() bool {
	r, ok := m.store.Get(m.configName)
	return ok && r.Stats().Routes > 0
}

// ribSyncingState returns the rib state to report while a session is still syncing.
//
// While routes are present the rib is DEGRADED (announcer keeps serving the
// still-valid routes). An empty RIB reports NOT_READY.
func (m *birdRIBReadiness) ribSyncingState() readinesspb.State {
	if m.hasRoutes() {
		return readinesspb.State_STATE_DEGRADED
	}
	return readinesspb.State_STATE_NOT_READY
}

// OnSessionStart is called by FeedRIB after a new BIRD import stream opens
// for the given config name.
//
// sessionID is the id returned by ribRef.NewSession and is stored so that a
// stale OnSessionEnd from a superseded stream can be ignored. The bird-session
// scope transitions to READY; the rib scope resets the bulk-settle gate and
// enters the syncing state.
func (m *birdRIBReadiness) OnSessionStart(name string, sessionID uint64) {
	if name != m.configName {
		return
	}

	m.counter.Store(0)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentSession = sessionID
	m.sessionActive = true
	m.lastSessionEnd = time.Time{}
	m.bulkSettled = false
	m.belowSince = time.Time{}

	m.tracker.Set("bird-session", readinesspb.State_STATE_READY)
	m.tracker.SetWithReason("rib", m.ribSyncingState(),
		&readinesspb.Reason{Code: "SYNCING"},
	)
}

// OnUpdate is called by FeedRIB after each successfully applied route batch,
// with n equal to the number of routes applied.
func (m *birdRIBReadiness) OnUpdate(n int) {
	m.counter.Add(int64(n))
}

// OnSessionEnd is called by FeedRIB when the stream ends for the given config.
//
// sessionID must match the id passed to the most recent OnSessionStart for
// name; if it does not, the call is from a superseded stream and is silently
// ignored to prevent a stale end from clobbering the active session state.
// When the session id matches, bird-session transitions to
// DEGRADED(RECONNECTING) and rib to DEGRADED(SESSION_ENDED) — routes remain
// under TTL so no extinguish occurs.
func (m *birdRIBReadiness) OnSessionEnd(name string, sessionID uint64) {
	if name != m.configName {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if sessionID != m.currentSession {
		return
	}
	m.sessionActive = false
	m.bulkSettled = false
	m.lastSessionEnd = time.Now()

	m.tracker.SetWithReason("bird-session",
		readinesspb.State_STATE_DEGRADED,
		&readinesspb.Reason{Code: "RECONNECTING"},
	)
	m.tracker.SetWithReason("rib",
		readinesspb.State_STATE_DEGRADED,
		&readinesspb.Reason{Code: "SESSION_ENDED"},
	)
}

// Run drives the periodic sampling loop. It returns when ctx is cancelled.
func (m *birdRIBReadiness) Run(ctx context.Context) error {
	ticker := time.NewTicker(m.sampleInterval)
	defer ticker.Stop()

	var prevCount int64

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			cur := m.counter.Load()
			delta := cur - prevCount
			prevCount = cur
			rate := float64(delta) / m.sampleInterval.Seconds()
			m.tick(now, rate)
		}
	}
}

// tick runs one sample-interval evaluation under m.mu.
//
// All tracker.Set calls for the rib and bird-session scopes are made while
// the lock is held, so a concurrent session-edge event cannot interleave
// with an in-flight tick and produce a stale clobber. hasRoutes and
// tracker.Set are safe to call under m.mu — neither calls back into the
// helper, so there is no lock-ordering issue.
func (m *birdRIBReadiness) tick(now time.Time, rate float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-stamp bird-session freshness each tick; tick re-confirms session liveness.
	m.tracker.Touch("bird-session")

	if !m.bulkSettled {
		if rate < m.rateThreshold {
			if m.belowSince.IsZero() {
				m.belowSince = now
			} else if now.Sub(m.belowSince) >= m.stabilityWindow {
				m.bulkSettled = true
			}
		} else {
			// Rate spiked above threshold; reset the continuous window.
			m.belowSince = time.Time{}
		}
	}

	if !m.sessionActive {
		// Update the bird-session reason when the reconnect grace elapses.
		if !m.lastSessionEnd.IsZero() && now.Sub(m.lastSessionEnd) >= m.reconnectGrace {
			m.tracker.SetWithReason("bird-session",
				readinesspb.State_STATE_DEGRADED,
				&readinesspb.Reason{Code: "DOWN"},
			)
		}

		// Re-evaluate rib from route presence so that a TTL purge that
		// empties the RIB is reflected without waiting for a new session.
		if !m.hasRoutes() {
			m.tracker.SetWithReason("rib",
				readinesspb.State_STATE_NOT_READY,
				&readinesspb.Reason{Code: "NO_ROUTES"},
			)
		} else {
			m.tracker.SetWithReason("rib",
				readinesspb.State_STATE_DEGRADED,
				&readinesspb.Reason{Code: "SESSION_ENDED"},
			)
		}
		return
	}

	var (
		nextState readinesspb.State
		reason    *readinesspb.Reason
	)

	switch {
	case !m.bulkSettled:
		nextState = m.ribSyncingState()
		reason = &readinesspb.Reason{Code: "SYNCING"}
	case !m.hasRoutes():
		nextState = readinesspb.State_STATE_NOT_READY
		reason = &readinesspb.Reason{Code: "NO_ROUTES"}
	default:
		nextState = readinesspb.State_STATE_READY
	}

	if reason == nil {
		m.tracker.Set("rib", nextState)
	} else {
		m.tracker.SetWithReason("rib", nextState, reason)
	}

	m.log.Debug("rib readiness tick",
		zap.Float64("rate", rate),
		zap.Bool("bulk_settled", m.bulkSettled),
		zap.String("state", nextState.String()),
	)
}

// staticRIBReadiness drives only the "rib" readiness scope based on
// whether the store holds any routes, without any BIRD session awareness.
//
// OnSessionStart, OnUpdate, and OnSessionEnd are no-ops: FeedRIB route
// updates still get applied by RouteService regardless, but session events
// carry no meaning for the readiness model when BIRD is not expected.
// Run evaluates hasRoutes() once on entry and then on every SampleInterval
// tick, setting rib=READY when routes>0, else NOT_READY(NO_ROUTES). The
// bird-session scope is never touched.
type staticRIBReadiness struct {
	store          *RIBStore
	configName     string
	tracker        *readiness.Tracker
	sampleInterval time.Duration
	log            *zap.Logger
}

// newStaticRIBReadiness constructs a static rib-readiness implementation.
//
// The initial rib scope is seeded immediately at construction — READY if
// routes are already present, NOT_READY(NO_ROUTES) otherwise — so there is
// no UNKNOWN gap before the first tick.
func newStaticRIBReadiness(
	store *RIBStore,
	configName string,
	tracker *readiness.Tracker,
	sampleInterval time.Duration,
	log *zap.Logger,
) *staticRIBReadiness {
	m := &staticRIBReadiness{
		store:          store,
		configName:     configName,
		tracker:        tracker,
		sampleInterval: sampleInterval,
		log:            log,
	}

	// Seed the initial rib state so there is no UNKNOWN window before Run starts.
	m.evaluate()

	return m
}

// OnSessionStart is a no-op for static deployments.
func (m *staticRIBReadiness) OnSessionStart(_ string, _ uint64) {}

// OnUpdate is a no-op for static deployments.
func (m *staticRIBReadiness) OnUpdate(_ int) {}

// OnSessionEnd is a no-op for static deployments.
func (m *staticRIBReadiness) OnSessionEnd(_ string, _ uint64) {}

// Run drives the periodic rib scope re-evaluation. It returns when ctx is cancelled.
func (m *staticRIBReadiness) Run(ctx context.Context) error {
	// Evaluate once on entry to reflect the post-PreRun state immediately, since
	// static.routes are seeded by PreRun, which runs before Run starts.
	m.evaluate()

	ticker := time.NewTicker(m.sampleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.evaluate()
		}
	}
}

// evaluate sets the rib scope to READY when routes are present, else NOT_READY(NO_ROUTES).
func (m *staticRIBReadiness) evaluate() {
	r, ok := m.store.Get(m.configName)
	if ok && r.Stats().Routes > 0 {
		m.tracker.Set("rib", readinesspb.State_STATE_READY)
	} else {
		m.tracker.SetWithReason("rib",
			readinesspb.State_STATE_NOT_READY,
			&readinesspb.Reason{Code: "NO_ROUTES"},
		)
	}
}
