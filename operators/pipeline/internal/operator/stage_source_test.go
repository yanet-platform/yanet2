package operator

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
)

// headOf returns the stage the queue publishes as the desired state, nil
// before the first one.
func headOf(source *StageQueueSource) *StageConfig {
	return source.Latest().Load().Value
}

// Test_StageQueueSource_SetStages_PublishesHead verifies that nothing is
// published before the first stages are set, and their head after.
func Test_StageQueueSource_SetStages_PublishesHead(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0"})
	require.Zero(t, source.Latest().Load().Number)

	first := &StageConfig{Name: "first"}
	source.SetStages([]*StageConfig{first, {Name: "second"}})

	require.Same(t, first, headOf(source))
}

// Test_StageQueueSource_Switch_PublishesStage verifies that a switch
// publishes the new stage in place of the queue.
func Test_StageQueueSource_Switch_PublishesStage(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0"})
	source.SetStages([]*StageConfig{{Name: "first"}, {Name: "second"}})

	other := &StageConfig{Name: "other"}
	source.Switch(other)

	require.Same(t, other, headOf(source))
}

// Test_StageQueueSource_OnStageApplied_WaitsForEveryGateway verifies that a
// stage applied by only some of the gateways stays the head, and nothing new
// is published.
func Test_StageQueueSource_OnStageApplied_WaitsForEveryGateway(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0", "gw1"})
	first := &StageConfig{Name: "first"}
	source.SetStages([]*StageConfig{first, {Name: "second"}})
	before := source.Latest().Load()

	source.OnStageApplied("gw0", first, nil)

	after := source.Latest().Load()
	require.Equal(t, before.Number, after.Number)
	require.Same(t, first, after.Value)
}

// Test_StageQueueSource_OnStageApplied_PopsHeadAppliedEverywhere verifies
// that the queue publishes the next stage once the last gateway applied the
// head.
func Test_StageQueueSource_OnStageApplied_PopsHeadAppliedEverywhere(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0", "gw1"})
	first := &StageConfig{Name: "first"}
	second := &StageConfig{Name: "second"}
	source.SetStages([]*StageConfig{first, second})

	source.OnStageApplied("gw0", first, nil)
	source.OnStageApplied("gw1", first, nil)

	require.Same(t, second, headOf(source))
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

	source.OnStageApplied("gw1", second, nil)

	require.Same(t, second, headOf(source))
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

	require.Same(t, first, headOf(source))
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

	require.Same(t, first, headOf(source))
}

// Test_StageQueueSource_OnStageApplied_RetainsTail verifies that the last stage
// stays the steady-state target once every gateway applied it.
func Test_StageQueueSource_OnStageApplied_RetainsTail(t *testing.T) {
	source := NewStageQueueSource([]string{"gw0", "gw1"})
	last := &StageConfig{Name: "last"}
	source.SetStages([]*StageConfig{last})
	before := source.Latest().Load()

	source.OnStageApplied("gw0", last, nil)
	source.OnStageApplied("gw1", last, nil)

	after := source.Latest().Load()
	require.Equal(t, before.Number, after.Number)
	require.Same(t, last, after.Value)
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

	require.Same(t, current, headOf(source))
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

	require.Same(t, first, headOf(source))
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
