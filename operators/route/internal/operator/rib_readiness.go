package operator

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/readinesspb"
)

// ribReadinessHelper drives the "rib" readiness scope based on the FeedRIB
// update rate and plateau settle.
//
// The scope becomes READY when the update rate has stayed below RateThreshold
// continuously for StabilityWindow. The bulkSettled flag is sticky within a
// session: a spike after settling does not flip the scope back to NOT_READY.
type ribReadinessHelper struct {
	store      *RIBStore
	configName string
	tracker    *operator.Readiness

	// counter is the total number of route updates applied in the current
	// session. Driven by OnUpdate from the FeedRIB hot path; read by Run.
	counter atomic.Int64

	mu             sync.Mutex
	currentSession uint64
	sessionActive  bool
	belowSince     time.Time // zero when rate is currently above threshold
	bulkSettled    bool

	rateThreshold   float64
	stabilityWindow time.Duration
	sampleInterval  time.Duration

	log *zap.Logger
}

// newRIBReadinessHelper constructs a helper from the supplied config.
//
// The helper is wired to the given tracker's "rib" scope.
func newRIBReadinessHelper(
	cfg ReadinessConfig,
	store *RIBStore,
	configName string,
	tracker *operator.Readiness,
	log *zap.Logger,
) *ribReadinessHelper {
	return &ribReadinessHelper{
		store:           store,
		configName:      configName,
		tracker:         tracker,
		rateThreshold:   cfg.RateThreshold,
		stabilityWindow: cfg.StabilityWindow,
		sampleInterval:  cfg.SampleInterval,
		log:             log,
	}
}

// hasRoutes reports whether the tracked config's RIB currently holds any
// routes.
//
// It uses the O(1) RIB stats snapshot, so it is cheap to call every tick.
func (m *ribReadinessHelper) hasRoutes() bool {
	r, ok := m.store.Get(m.configName)
	return ok && r.Stats().Routes > 0
}

// syncingState returns the state to report while a session is still syncing.
//
// While routes are present we report DEGRADED so the announcer keeps serving
// the still-valid routes; an empty RIB (cold start with nothing to announce)
// reports NOT_READY.
func (m *ribReadinessHelper) syncingState() readinesspb.State {
	if m.hasRoutes() {
		return readinesspb.State_STATE_DEGRADED
	}
	return readinesspb.State_STATE_NOT_READY
}

// OnSessionStart is called by FeedRIB after ribRef.NewSession() opens a new
// BIRD import stream for the given config name.
//
// sessionID is the id returned by ribRef.NewSession and is stored so that a
// stale OnSessionEnd from the superseded stream can be ignored. Reports
// DEGRADED rather than NOT_READY when routes are already present, so a resync
// does not withdraw live announcements.
func (m *ribReadinessHelper) OnSessionStart(name string, sessionID uint64) {
	if name != m.configName {
		return
	}

	m.mu.Lock()
	m.currentSession = sessionID
	m.sessionActive = true
	m.bulkSettled = false
	m.belowSince = time.Time{}
	m.mu.Unlock()
	m.counter.Store(0)

	m.tracker.Set("rib", m.syncingState(),
		&readinesspb.Reason{Code: "SYNCING"},
	)
}

// OnUpdate is called by FeedRIB after each successfully applied route, with n
// equal to the number of routes applied in that batch.
func (m *ribReadinessHelper) OnUpdate(n int) {
	m.counter.Add(int64(n))
}

// OnSessionEnd is called by FeedRIB when the stream ends for the given config.
//
// sessionID must match the id passed to the most recent OnSessionStart for
// name; if it does not, the call is from a superseded stream and is silently
// ignored, preventing a stale end from clobbering the state of the active
// session. When the session id matches, the scope is set to DEGRADED rather
// than NOT_READY because routes remain in the RIB and the dataplane continues
// forwarding; they are purged only later by the TTL-based CleanupTask.
func (m *ribReadinessHelper) OnSessionEnd(name string, sessionID uint64) {
	if name != m.configName {
		return
	}

	m.mu.Lock()
	if sessionID != m.currentSession {
		m.mu.Unlock()
		return
	}
	m.sessionActive = false
	m.bulkSettled = false
	m.mu.Unlock()

	m.tracker.Set("rib", readinesspb.State_STATE_DEGRADED,
		&readinesspb.Reason{Code: "SESSION_ENDED"},
	)
}

// Run drives the periodic sampling loop. It returns when ctx is cancelled.
func (m *ribReadinessHelper) Run(ctx context.Context) error {
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

			m.mu.Lock()
			active := m.sessionActive
			settled := m.bulkSettled

			if !settled {
				if rate < m.rateThreshold {
					if m.belowSince.IsZero() {
						m.belowSince = now
					} else if now.Sub(m.belowSince) >= m.stabilityWindow {
						m.bulkSettled = true
						settled = true
					}
				} else {
					// Rate spiked above threshold; reset the continuous window.
					m.belowSince = time.Time{}
				}
			}
			m.mu.Unlock()

			var (
				nextState readinesspb.State
				reason    *readinesspb.Reason
			)

			switch {
			case !active:
				// No active session — keep the DEGRADED state OnSessionEnd set.
				continue
			case !settled:
				nextState = m.syncingState()
				reason = &readinesspb.Reason{Code: "SYNCING"}
			default:
				nextState = readinesspb.State_STATE_READY
			}

			if nextState == readinesspb.State_STATE_READY {
				m.tracker.Set("rib", nextState)
			} else {
				m.tracker.Set("rib", nextState, reason)
			}

			m.log.Debug("rib readiness tick",
				zap.Float64("rate", rate),
				zap.Bool("bulk_settled", settled),
				zap.String("state", nextState.String()),
			)
		}
	}
}
