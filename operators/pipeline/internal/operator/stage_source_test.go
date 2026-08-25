package operator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// drainWake consumes a single buffered wake signal if present.
func drainWake(wake <-chan struct{}) bool {
	select {
	case <-wake:
		return true
	default:
		return false
	}
}

func Test_StageQueueSource_SetStagesPublishesAndWakes(t *testing.T) {
	src := NewStageQueueSource()

	require.False(t, drainWake(src.Wake()), "wake should be empty initially")

	target, ok := src.Snapshot()
	require.False(t, ok)
	require.Nil(t, target)

	s1 := &StageConfig{Name: "s1"}
	src.SetStages([]*StageConfig{s1})

	require.True(t, drainWake(src.Wake()), "SetStages must signal wake")

	target, ok = src.Snapshot()
	require.True(t, ok)
	require.Same(t, s1, target)
}

func Test_StageQueueSource_SwitchReplacesQueue(t *testing.T) {
	src := NewStageQueueSource()

	s1 := &StageConfig{Name: "s1"}
	s2 := &StageConfig{Name: "s2"}
	src.SetStages([]*StageConfig{s1, s2})
	_ = drainWake(src.Wake())

	other := &StageConfig{Name: "other"}
	src.Switch(other)

	require.True(t, drainWake(src.Wake()))

	target, ok := src.Snapshot()
	require.True(t, ok)
	require.Same(t, other, target)
}

// Snapshot drains a pre-existing wake under the lock.
func Test_StageQueueSource_SnapshotDrainsWake(t *testing.T) {
	src := NewStageQueueSource()

	s1 := &StageConfig{Name: "s1"}
	src.SetStages([]*StageConfig{s1})

	_, ok := src.Snapshot()
	require.True(t, ok)
	require.False(t, drainWake(src.Wake()),
		"Snapshot must drain the wake announced alongside the target")
}

// Advance pops the head when more than one element is queued and
// re-wakes for the next iteration.
func Test_StageQueueSource_AdvancePopsHead(t *testing.T) {
	src := NewStageQueueSource()

	s1 := &StageConfig{Name: "s1"}
	s2 := &StageConfig{Name: "s2"}
	src.SetStages([]*StageConfig{s1, s2})
	_ = drainWake(src.Wake())

	target, ok := src.Snapshot()
	require.True(t, ok)
	require.Same(t, s1, target)

	src.Advance(s1)

	require.True(t, drainWake(src.Wake()),
		"Advance must wake the reconcile loop when more work is queued")

	target, ok = src.Snapshot()
	require.True(t, ok)
	require.Same(t, s2, target)
}

// Single-element queue is retained as the steady-state target.
func Test_StageQueueSource_AdvanceRetainsTail(t *testing.T) {
	src := NewStageQueueSource()

	s1 := &StageConfig{Name: "s1"}
	src.SetStages([]*StageConfig{s1})
	_ = drainWake(src.Wake())

	src.Advance(s1)

	require.False(t, drainWake(src.Wake()),
		"Advance on tail must not wake")

	target, ok := src.Snapshot()
	require.True(t, ok)
	require.Same(t, s1, target,
		"steady-state queue must retain the tail stage")
}

// Advance is a no-op when the head changed mid-flight.
func Test_StageQueueSource_AdvanceIgnoresHeadChange(t *testing.T) {
	src := NewStageQueueSource()

	s1 := &StageConfig{Name: "s1"}
	other := &StageConfig{Name: "other"}
	src.SetStages([]*StageConfig{s1})
	_ = drainWake(src.Wake())

	target, ok := src.Snapshot()
	require.True(t, ok)
	require.Same(t, s1, target)

	src.Switch(other)
	_ = drainWake(src.Wake())

	src.Advance(s1)

	target, ok = src.Snapshot()
	require.True(t, ok)
	require.Same(t, other, target,
		"Advance must not pop a head that no longer matches")
}

// Empty SetStages returns the source to idle.
func Test_StageQueueSource_SetStagesEmptyIdles(t *testing.T) {
	src := NewStageQueueSource()

	s1 := &StageConfig{Name: "s1"}
	src.SetStages([]*StageConfig{s1})
	_ = drainWake(src.Wake())

	src.SetStages(nil)

	target, ok := src.Snapshot()
	require.False(t, ok)
	require.Nil(t, target)
}

// TestReadiness_ScopeSpecs_MirrorNominalReconcileInterval verifies that
// each gateway scope's observation contract is the reconcile interval — the
// nominal cadence — not a backoff-inflated bound.
func TestReadiness_ScopeSpecs_MirrorNominalReconcileInterval(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Gateways = []operator.GatewayConfig{{Name: "gw0"}, {Name: "gw1"}}
	cfg.Stages = []StageConfig{{Name: "stage0"}}
	cfg.Reconcile.Interval = xcfg.MustNonZero(10 * time.Second)
	cfg.Reconcile.MaxBackoff = xcfg.MustNonZero(2 * time.Minute)

	specs := readinessScopeSpecs(cfg)

	require.Len(t, specs, 2)
	assert.Equal(t, "pipeline:gw0", specs[0].Name)
	assert.Equal(t, "pipeline:gw1", specs[1].Name)
	for _, spec := range specs {
		assert.Equal(t, 10*time.Second, spec.ExpectedObservationInterval)
	}
}

// TestReadiness_ScopeSpecs_NoStages_NoContract verifies that with no stages
// configured the reconcile loop stays idle, so the scopes declare no
// freshness contract rather than a periodic one.
func TestReadiness_ScopeSpecs_NoStages_NoContract(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Gateways = []operator.GatewayConfig{{Name: "gw0"}}
	cfg.Stages = nil

	specs := readinessScopeSpecs(cfg)

	require.Len(t, specs, 1)
	assert.Zero(t, specs[0].ExpectedObservationInterval)
}
