package operator

import (
	"testing"
	"time"

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

// verifies that stage activity selects the production freshness contract for
// every gateway scope without depending on scope order.
func Test_ReadinessScopeSpecs_StageActivityContracts(t *testing.T) {
	tests := []struct {
		name             string
		stages           []StageConfig
		expectedInterval time.Duration
	}{
		{
			name:             "active stages use nominal reconcile interval",
			stages:           []StageConfig{{Name: "stage0"}},
			expectedInterval: 10 * time.Second,
		},
		{
			name:             "no stages declare no freshness contract",
			stages:           nil,
			expectedInterval: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			config.Gateways = []operator.GatewayConfig{{Name: "gw0"}, {Name: "gw1"}}
			config.Stages = test.stages
			config.Reconcile.Interval = xcfg.MustNonZero(10 * time.Second)
			config.Reconcile.MaxBackoff = xcfg.MustNonZero(2 * time.Minute)

			intervalsByName := map[string]time.Duration{}
			for _, scopeSpec := range readinessScopeSpecs(config) {
				intervalsByName[scopeSpec.Name] = scopeSpec.ExpectedObservationInterval
			}
			require.Equal(t, map[string]time.Duration{
				"pipeline:gw0": test.expectedInterval,
				"pipeline:gw1": test.expectedInterval,
			}, intervalsByName)
		})
	}
}
