package blackhole

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	blackholepb "github.com/yanet-platform/yanet2/modules/blackhole/controlplane/blackholepb/v1"
)

var errInjectedBackend = errors.New("injected backend failure")

type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() {}

type mockBackend struct{}

func (m *mockBackend) UpdateModule(name string) (ModuleHandle, error) {
	return &mockModuleHandle{}, nil
}

func (m *mockBackend) DeleteModule(name string) error {
	return nil
}

func newTestService(t *testing.T) *BlackholeService {
	t.Helper()
	return NewBlackholeService(&mockBackend{})
}

// flakyBackend succeeds on the first UpdateModule call and fails thereafter.
type flakyBackend struct {
	numCalls atomic.Int64
}

func (m *flakyBackend) UpdateModule(name string) (ModuleHandle, error) {
	if m.numCalls.Add(1) >= 2 {
		return nil, errInjectedBackend
	}
	return &mockModuleHandle{}, nil
}

func (m *flakyBackend) DeleteModule(name string) error {
	return nil
}

func Test_BlackholeService_UpdateAndShow(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.UpdateConfig(t.Context(), &blackholepb.UpdateConfigRequest{Name: "blackhole0"})
	require.NotNil(t, resp)
	require.NoError(t, err)

	show, err := svc.ShowConfig(t.Context(), &blackholepb.ShowConfigRequest{Name: "blackhole0"})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Equal(t, "blackhole0", show.Name)
}

func Test_BlackholeService_ListUpdateList(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	list, err := svc.ListConfigs(ctx, &blackholepb.ListConfigsRequest{})
	require.NotNil(t, list)
	require.NoError(t, err)
	assert.Empty(t, list.Configs)

	_, err = svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{Name: "blackhole0"})
	require.NoError(t, err)

	list, err = svc.ListConfigs(ctx, &blackholepb.ListConfigsRequest{})
	require.NotNil(t, list)
	require.NoError(t, err)
	assert.Equal(t, []string{"blackhole0"}, list.Configs)
}

func Test_BlackholeService_DeleteConfig(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	_, err := svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{Name: "blackhole0"})
	require.NoError(t, err)

	resp, err := svc.DeleteConfig(ctx, &blackholepb.DeleteConfigRequest{Name: "blackhole0"})
	require.NoError(t, err)
	require.True(t, resp.Deleted)

	_, err = svc.ShowConfig(ctx, &blackholepb.ShowConfigRequest{Name: "blackhole0"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func Test_BlackholeService_DeleteMissing(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.DeleteConfig(t.Context(), &blackholepb.DeleteConfigRequest{Name: "absent"})
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func Test_BlackholeService_EmptyConfigName(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	t.Run("UpdateConfig", func(t *testing.T) {
		resp, err := svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("ShowConfig", func(t *testing.T) {
		resp, err := svc.ShowConfig(ctx, &blackholepb.ShowConfigRequest{})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("DeleteConfig", func(t *testing.T) {
		resp, err := svc.DeleteConfig(ctx, &blackholepb.DeleteConfigRequest{})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func Test_BlackholeService_UpdateFailureAtomic(t *testing.T) {
	svc := NewBlackholeService(&flakyBackend{})
	ctx := t.Context()
	name := "blackhole0"

	_, err := svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{Name: name})
	require.NoError(t, err)

	_, err = svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{Name: name})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))

	show, err := svc.ShowConfig(ctx, &blackholepb.ShowConfigRequest{Name: name})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Equal(t, name, show.Name)
}

func Test_BlackholeService_ConcurrentAccess(t *testing.T) {
	svc := newTestService(t)

	const goroutines = 10
	const iterations = 100

	g, ctx := errgroup.WithContext(t.Context())

	for idx := range goroutines {
		g.Go(func() error {
			for jdx := range iterations {
				name := fmt.Sprintf("config-%d-%d", idx, jdx)
				_, err := svc.UpdateConfig(ctx, &blackholepb.UpdateConfigRequest{Name: name})
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
				_, err := svc.ListConfigs(ctx, &blackholepb.ListConfigsRequest{})
				if err != nil {
					return err
				}
			}
			return nil
		})
	}

	for idx := range goroutines {
		g.Go(func() error {
			for jdx := range iterations {
				name := fmt.Sprintf("config-%d-%d", idx, jdx)
				svc.ShowConfig(ctx, &blackholepb.ShowConfigRequest{Name: name})
			}
			return nil
		})
	}

	require.NoError(t, g.Wait())
}
