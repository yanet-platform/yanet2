package forward_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
	"github.com/yanet-platform/yanet2/modules/forward/internal/ffi"
)

type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() {}

type mockBackend struct{}

func (m *mockBackend) UpdateModule(name string, rules []ffi.ForwardRule) (forward.ModuleHandle, error) {
	return &mockModuleHandle{}, nil
}

func (m *mockBackend) DeleteModule(name string) error {
	return nil
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
