package operator

import (
	"errors"
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
	src := NewStageQueueSource([]string{"gw0"})

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
	src := NewStageQueueSource([]string{"gw0"})

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
	src := NewStageQueueSource([]string{"gw0"})

	s1 := &StageConfig{Name: "s1"}
	src.SetStages([]*StageConfig{s1})

	_, ok := src.Snapshot()
	require.True(t, ok)
	require.False(t, drainWake(src.Wake()),
		"Snapshot must drain the wake announced alongside the target")
}

// Test_StageQueueSource_OnStageApplied_WaitsForEveryGateway verifies that a stage
// applied by only some of the gateways stays the head, without a wake.
func Test_StageQueueSource_OnStageApplied_WaitsForEveryGateway(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0", "gw1"})
	first := &StageConfig{Name: "first"}
	source.SetStages([]*StageConfig{first, {Name: "second"}})
	_ = drainWake(source.Wake())

	source.OnStageApplied("gw0", first, nil)

	require.False(t, drainWake(source.Wake()))
	head, ok := source.Snapshot()
	require.True(t, ok)
	require.Same(t, first, head)
}

// Test_StageQueueSource_OnStageApplied_PopsHeadAppliedEverywhere verifies that the
// queue exposes the next stage and wakes the reconcile loop once the last
// gateway applied the head.
func Test_StageQueueSource_OnStageApplied_PopsHeadAppliedEverywhere(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0", "gw1"})
	first := &StageConfig{Name: "first"}
	second := &StageConfig{Name: "second"}
	source.SetStages([]*StageConfig{first, second})
	_ = drainWake(source.Wake())

	source.OnStageApplied("gw0", first, nil)
	source.OnStageApplied("gw1", first, nil)

	require.True(t, drainWake(source.Wake()))
	head, ok := source.Snapshot()
	require.True(t, ok)
	require.Same(t, second, head)
}

// Test_StageQueueSource_OnStageApplied_NextHeadWaitsForEveryGateway verifies that
// the stage exposed by an advance is not advanced past by the gateway that
// applied it first.
func Test_StageQueueSource_OnStageApplied_NextHeadWaitsForEveryGateway(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0", "gw1"})
	first := &StageConfig{Name: "first"}
	second := &StageConfig{Name: "second"}
	source.SetStages([]*StageConfig{first, second, {Name: "third"}})
	source.OnStageApplied("gw0", first, nil)
	source.OnStageApplied("gw1", first, nil)
	_ = drainWake(source.Wake())

	source.OnStageApplied("gw1", second, nil)

	head, ok := source.Snapshot()
	require.True(t, ok)
	require.Same(t, second, head)
}

// Test_StageQueueSource_OnStageApplied_FailureHoldsHeadBack verifies that a gateway
// whose latest apply of the head failed holds the head back, even though an
// earlier apply of it succeeded.
func Test_StageQueueSource_OnStageApplied_FailureHoldsHeadBack(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0", "gw1"})
	first := &StageConfig{Name: "first"}
	source.SetStages([]*StageConfig{first, {Name: "second"}})

	source.OnStageApplied("gw0", first, nil)
	source.OnStageApplied("gw0", first, errors.New("gateway unavailable"))
	source.OnStageApplied("gw1", first, nil)

	head, ok := source.Snapshot()
	require.True(t, ok)
	require.Same(t, first, head)
}

// Test_StageQueueSource_OnStageApplying_HoldsHeadBack verifies that a
// gateway still applying the head holds it back, even though its previous
// apply of the head succeeded.
func Test_StageQueueSource_OnStageApplying_HoldsHeadBack(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0", "gw1"})
	first := &StageConfig{Name: "first"}
	source.SetStages([]*StageConfig{first, {Name: "second"}})
	source.OnStageApplied("gw0", first, nil)

	source.OnStageApplying("gw0", first)
	source.OnStageApplied("gw1", first, nil)

	head, ok := source.Snapshot()
	require.True(t, ok)
	require.Same(t, first, head)
}

// Test_StageQueueSource_OnStageApplied_RetainsTail verifies that the last stage
// stays the steady-state target once every gateway applied it.
func Test_StageQueueSource_OnStageApplied_RetainsTail(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0", "gw1"})
	last := &StageConfig{Name: "last"}
	source.SetStages([]*StageConfig{last})
	_ = drainWake(source.Wake())

	source.OnStageApplied("gw0", last, nil)
	source.OnStageApplied("gw1", last, nil)

	require.False(t, drainWake(source.Wake()))
	head, ok := source.Snapshot()
	require.True(t, ok)
	require.Same(t, last, head)
}

// Test_StageQueueSource_OnStageApplied_IgnoresReplacedHead verifies that a stage
// applied after the queue was replaced does not pop the new head.
func Test_StageQueueSource_OnStageApplied_IgnoresReplacedHead(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0"})
	replaced := &StageConfig{Name: "replaced"}
	current := &StageConfig{Name: "current"}
	source.SetStages([]*StageConfig{replaced, {Name: "next"}})
	source.SetStages([]*StageConfig{current, {Name: "after"}})

	source.OnStageApplied("gw0", replaced, nil)

	head, ok := source.Snapshot()
	require.True(t, ok)
	require.Same(t, current, head)
}

// Test_StageQueueSource_SetStages_ForgetsAppliedGateways verifies that a
// replaced queue waits for every gateway again, even when its head is the
// stage some of them applied before.
func Test_StageQueueSource_SetStages_ForgetsAppliedGateways(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0", "gw1"})
	first := &StageConfig{Name: "first"}
	second := &StageConfig{Name: "second"}
	source.SetStages([]*StageConfig{first, second})
	source.OnStageApplied("gw0", first, nil)

	source.SetStages([]*StageConfig{first, second})
	source.OnStageApplied("gw1", first, nil)

	head, ok := source.Snapshot()
	require.True(t, ok)
	require.Same(t, first, head)
}

// Empty SetStages returns the source to idle.
func Test_StageQueueSource_SetStagesEmptyIdles(t *testing.T) {
	src := NewStageQueueSource([]string{"gw0"})

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
