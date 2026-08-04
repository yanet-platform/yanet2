package forward_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
