package forward_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/yanet2/modules/forward/bindings/go/cforward"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	forwardpb "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
)

type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() {}

type mockBackend struct{}

func (m *mockBackend) UpdateModule(name string, rules []cforward.ForwardRule) (forward.ModuleHandle, error) {
	return &mockModuleHandle{}, nil
}

func (m *mockBackend) DeleteModule(name string) error {
	return nil
}

func (m *mockBackend) ModuleCounters(name string, counterNames []string) []forward.CounterView {
	return nil
}

// nilHandleBackend is a Backend whose UpdateModule publishes a config
// carrying no module handle.
//
// It models a Backend that reports success without producing a handle,
// which is the case the config's own nil guard exists to survive.
type nilHandleBackend struct {
	mockBackend
}

func (m *nilHandleBackend) UpdateModule(name string, rules []cforward.ForwardRule) (forward.ModuleHandle, error) {
	return nil, nil
}

// recordingBackend captures the rules handed to UpdateModule so a test can
// inspect what the service sends to shared memory.
//
// Only single-goroutine tests use it. TestForwardServiceConcurrentAccess
// uses mockBackend instead, since this field is unguarded under -race.
type recordingBackend struct {
	mockBackend

	rules []cforward.ForwardRule
}

func (m *recordingBackend) UpdateModule(name string, rules []cforward.ForwardRule) (forward.ModuleHandle, error) {
	m.rules = rules
	return &mockModuleHandle{}, nil
}

// TestShowConfigUnknownConfig verifies that ShowConfig reports NotFound for
// a config name that was never applied.
func TestShowConfigUnknownConfig(t *testing.T) {
	svc := forward.NewForwardService(&mockBackend{})

	_, err := svc.ShowConfig(t.Context(), &forwardpb.ShowConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestDeleteConfigUnknownConfig verifies that DeleteConfig reports NotFound
// for a config name that was never applied.
func TestDeleteConfigUnknownConfig(t *testing.T) {
	svc := forward.NewForwardService(&mockBackend{})

	_, err := svc.DeleteConfig(t.Context(), &forwardpb.DeleteConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestShowConfigEmptyRules verifies that ShowConfig succeeds with an empty
// rule list for a config that was applied without rules.
func TestShowConfigEmptyRules(t *testing.T) {
	svc := forward.NewForwardService(&mockBackend{})

	_, err := svc.UpdateConfig(t.Context(), &forwardpb.UpdateConfigRequest{Name: "empty"})
	require.NoError(t, err)

	response, err := svc.ShowConfig(t.Context(), &forwardpb.ShowConfigRequest{Name: "empty"})
	require.NoError(t, err)
	require.Empty(t, response.GetRules())
}

// TestUpdateConfigNilAction verifies that UpdateConfig rejects a rule with a
// nil action instead of dereferencing it.
func TestUpdateConfigNilAction(t *testing.T) {
	svc := forward.NewForwardService(&mockBackend{})

	_, err := svc.UpdateConfig(t.Context(), &forwardpb.UpdateConfigRequest{
		Name: "config",
		Rules: []*forwardpb.Rule{
			{},
		},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// TestUpdateConfigReplacesConfigWithoutHandle verifies that replacing a
// config that holds no module handle releases it without panicking.
func TestUpdateConfigReplacesConfigWithoutHandle(t *testing.T) {
	svc := forward.NewForwardService(&nilHandleBackend{})

	_, err := svc.UpdateConfig(t.Context(), &forwardpb.UpdateConfigRequest{Name: "config"})
	require.NoError(t, err)

	_, err = svc.UpdateConfig(t.Context(), &forwardpb.UpdateConfigRequest{Name: "config"})
	require.NoError(t, err)
}

// TestUpdateConfigMaterializesEmptyCounter verifies that a rule with an
// empty counter and a non-empty target is materialised to "to_" + target
// both in the rules handed to the backend and in what ShowConfig returns
// afterward.
func TestUpdateConfigMaterializesEmptyCounter(t *testing.T) {
	backend := &recordingBackend{}
	svc := forward.NewForwardService(backend)

	_, err := svc.UpdateConfig(t.Context(), &forwardpb.UpdateConfigRequest{
		Name: "config",
		Rules: []*forwardpb.Rule{
			{
				Action: &forwardpb.Action{Target: "device0"},
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, backend.rules, 1)
	require.Equal(t, "to_device0", backend.rules[0].Counter)

	response, err := svc.ShowConfig(t.Context(), &forwardpb.ShowConfigRequest{Name: "config"})
	require.NoError(t, err)
	require.Len(t, response.GetRules(), 1)
	require.Equal(t, "to_device0", response.GetRules()[0].GetAction().GetCounter())
}

// TestUpdateConfigKeepsNonEmptyCounter verifies that a rule with a
// non-empty counter is passed through verbatim, unmaterialised.
func TestUpdateConfigKeepsNonEmptyCounter(t *testing.T) {
	backend := &recordingBackend{}
	svc := forward.NewForwardService(backend)

	_, err := svc.UpdateConfig(t.Context(), &forwardpb.UpdateConfigRequest{
		Name: "config",
		Rules: []*forwardpb.Rule{
			{
				Action: &forwardpb.Action{Target: "device0", Counter: "custom"},
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, backend.rules, 1)
	require.Equal(t, "custom", backend.rules[0].Counter)

	response, err := svc.ShowConfig(t.Context(), &forwardpb.ShowConfigRequest{Name: "config"})
	require.NoError(t, err)
	require.Equal(t, "custom", response.GetRules()[0].GetAction().GetCounter())
}

// TestUpdateConfigEmptyCounterEmptyTarget verifies the degenerate case: a
// rule with both an empty counter and an empty target materialises to
// "to_" rather than being rejected.
func TestUpdateConfigEmptyCounterEmptyTarget(t *testing.T) {
	backend := &recordingBackend{}
	svc := forward.NewForwardService(backend)

	_, err := svc.UpdateConfig(t.Context(), &forwardpb.UpdateConfigRequest{
		Name: "config",
		Rules: []*forwardpb.Rule{
			{
				Action: &forwardpb.Action{},
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, backend.rules, 1)
	require.Equal(t, "to_", backend.rules[0].Counter)

	response, err := svc.ShowConfig(t.Context(), &forwardpb.ShowConfigRequest{Name: "config"})
	require.NoError(t, err)
	require.Equal(t, "to_", response.GetRules()[0].GetAction().GetCounter())
}

// TestUpdateConfigMaterializesEmptyCounterLongTarget verifies that a target
// long enough to overflow the counter-name limit still applies, with the
// counter cut to exactly that limit.
//
// The expected length (127) is asserted as a literal, not derived from
// cforward.CounterNameMaxLen, so an off-by-one in that constant would not
// make this test pass vacuously.
func TestUpdateConfigMaterializesEmptyCounterLongTarget(t *testing.T) {
	backend := &recordingBackend{}
	svc := forward.NewForwardService(backend)

	target := strings.Repeat("a", 300)

	_, err := svc.UpdateConfig(t.Context(), &forwardpb.UpdateConfigRequest{
		Name: "config",
		Rules: []*forwardpb.Rule{
			{
				Action: &forwardpb.Action{Target: target},
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, backend.rules, 1)
	require.Len(t, backend.rules[0].Counter, 127)
	require.True(t, strings.HasPrefix(backend.rules[0].Counter, "to_aaa"))

	response, err := svc.ShowConfig(t.Context(), &forwardpb.ShowConfigRequest{Name: "config"})
	require.NoError(t, err)
	require.Equal(t, backend.rules[0].Counter, response.GetRules()[0].GetAction().GetCounter())
}

// TestUpdateConfigMaterializesEmptyCounterUTF8Boundary verifies that when
// the counter-name cut would otherwise split a multi-byte rune, UpdateConfig
// backs it off to the previous rune boundary instead.
//
// The target places a two-byte "é" rune where a byte-wise cut would land on
// its continuation byte. proto.Marshal is the oracle, not utf8.ValidString
// alone, because a marshal failure is what a real ShowConfig call would hit.
func TestUpdateConfigMaterializesEmptyCounterUTF8Boundary(t *testing.T) {
	backend := &recordingBackend{}
	svc := forward.NewForwardService(backend)

	target := strings.Repeat("a", 123) + "é" + strings.Repeat("b", 10)

	_, err := svc.UpdateConfig(t.Context(), &forwardpb.UpdateConfigRequest{
		Name: "config",
		Rules: []*forwardpb.Rule{
			{
				Action: &forwardpb.Action{Target: target},
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, backend.rules, 1)
	require.True(t, utf8.ValidString(backend.rules[0].Counter), "counter must be valid UTF-8")

	response, err := svc.ShowConfig(t.Context(), &forwardpb.ShowConfigRequest{Name: "config"})
	require.NoError(t, err)

	_, err = proto.Marshal(response)
	require.NoError(t, err, "ShowConfigResponse carrying the materialised counter must marshal")
}

// Run with: go test -race
func TestForwardServiceConcurrentAccess(t *testing.T) {
	svc := forward.NewForwardService(&mockBackend{})
	ctx := context.Background()

	const goroutines = 10
	const iterations = 100

	g, ctx := errgroup.WithContext(ctx)

	for i := range goroutines {
		g.Go(func() error {
			for j := range iterations {
				name := fmt.Sprintf("config-%d-%d", i, j)
				_, err := svc.UpdateConfig(ctx, &forwardpb.UpdateConfigRequest{
					Name: name,
					Rules: []*forwardpb.Rule{
						{
							Action: &forwardpb.Action{
								Target: "device0",
							},
						},
					},
				})
				if err != nil {
					return err
				}
			}
			return nil
		})
	}

	for range goroutines {
		g.Go(func() error {
			for range iterations {
				_, err := svc.ListConfigs(ctx, &forwardpb.ListConfigsRequest{})
				if err != nil {
					return err
				}
			}
			return nil
		})
	}

	for i := range goroutines {
		g.Go(func() error {
			for j := range iterations {
				name := fmt.Sprintf("config-%d-%d", i, j)
				svc.ShowConfig(ctx, &forwardpb.ShowConfigRequest{Name: name})
			}
			return nil
		})
	}

	for i := range goroutines {
		g.Go(func() error {
			for j := range iterations {
				name := fmt.Sprintf("config-%d-%d", i, j)
				svc.DeleteConfig(ctx, &forwardpb.DeleteConfigRequest{Name: name})
			}
			return nil
		})
	}

	require.NoError(t, g.Wait())
}
