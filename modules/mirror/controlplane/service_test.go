package mirror_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

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
