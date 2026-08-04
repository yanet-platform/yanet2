package mirror_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/modules/mirror/bindings/go/cmirror"
	mirror "github.com/yanet-platform/yanet2/modules/mirror/controlplane"
	mirrorpb "github.com/yanet-platform/yanet2/modules/mirror/controlplane/mirrorpb/v1"
)

type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() {}

type mockBackend struct{}

func (m *mockBackend) UpdateModule(
	name string,
	rules []cmirror.MirrorRule,
) (mirror.ModuleHandle, error) {
	return &mockModuleHandle{}, nil
}

func (m *mockBackend) DeleteModule(name string) error {
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

func (m *nilHandleBackend) UpdateModule(
	name string,
	rules []cmirror.MirrorRule,
) (mirror.ModuleHandle, error) {
	return nil, nil
}

// TestShowConfigUnknownConfig verifies that ShowConfig reports NotFound for
// a config name that was never applied.
func TestShowConfigUnknownConfig(t *testing.T) {
	svc := mirror.NewMirrorService(&mockBackend{})

	_, err := svc.ShowConfig(t.Context(), &mirrorpb.ShowConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestDeleteConfigUnknownConfig verifies that DeleteConfig reports NotFound
// for a config name that was never applied.
func TestDeleteConfigUnknownConfig(t *testing.T) {
	svc := mirror.NewMirrorService(&mockBackend{})

	_, err := svc.DeleteConfig(t.Context(), &mirrorpb.DeleteConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestShowConfigEmptyRules verifies that ShowConfig succeeds with an empty
// rule list for a config that was applied without rules.
func TestShowConfigEmptyRules(t *testing.T) {
	svc := mirror.NewMirrorService(&mockBackend{})

	_, err := svc.UpdateConfig(t.Context(), &mirrorpb.UpdateConfigRequest{Name: "empty"})
	require.NoError(t, err)

	response, err := svc.ShowConfig(t.Context(), &mirrorpb.ShowConfigRequest{Name: "empty"})
	require.NoError(t, err)
	require.Empty(t, response.GetRules())
}

// TestUpdateConfigReplacesConfigWithoutHandle verifies that replacing a
// config that holds no module handle releases it without panicking.
func TestUpdateConfigReplacesConfigWithoutHandle(t *testing.T) {
	svc := mirror.NewMirrorService(&nilHandleBackend{})

	_, err := svc.UpdateConfig(t.Context(), &mirrorpb.UpdateConfigRequest{Name: "config"})
	require.NoError(t, err)

	_, err = svc.UpdateConfig(t.Context(), &mirrorpb.UpdateConfigRequest{Name: "config"})
	require.NoError(t, err)
}

// Run with: go test -race
func TestMirrorServiceConcurrentAccess(t *testing.T) {
	svc := mirror.NewMirrorService(&mockBackend{})
	ctx := context.Background()

	const goroutines = 10
	const iterations = 100

	g, ctx := errgroup.WithContext(ctx)

	for i := range goroutines {
		g.Go(func() error {
			for j := range iterations {
				name := fmt.Sprintf("config-%d-%d", i, j)
				_, err := svc.UpdateConfig(ctx, &mirrorpb.UpdateConfigRequest{
					Name: name,
					Rules: []*mirrorpb.Rule{
						{
							Action: &mirrorpb.Action{
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
				_, err := svc.ListConfigs(ctx, &mirrorpb.ListConfigsRequest{})
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
				svc.ShowConfig(ctx, &mirrorpb.ShowConfigRequest{Name: name})
			}
			return nil
		})
	}

	for i := range goroutines {
		g.Go(func() error {
			for j := range iterations {
				name := fmt.Sprintf("config-%d-%d", i, j)
				svc.DeleteConfig(ctx, &mirrorpb.DeleteConfigRequest{Name: name})
			}
			return nil
		})
	}

	require.NoError(t, g.Wait())
}
