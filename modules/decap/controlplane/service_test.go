package decap

import (
	"errors"
	"fmt"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/google/go-cmp/cmp"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

var errInjectedBackend = errors.New("injected backend failure")

type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() {}

type mockBackend struct{}

func (m *mockBackend) UpdateModule(
	name string,
	prefixes []netip.Prefix,
) (ModuleHandle, error) {
	return &mockModuleHandle{}, nil
}

func newTestService(t *testing.T) *DecapService {
	t.Helper()
	return NewDecapService(&mockBackend{})
}

// flakyBackend succeeds on the first UpdateModule call and fails thereafter.
type flakyBackend struct {
	numCalls atomic.Int64
}

func (m *flakyBackend) UpdateModule(
	name string,
	prefixes []netip.Prefix,
) (ModuleHandle, error) {
	if m.numCalls.Add(1) >= 2 {
		return nil, errInjectedBackend
	}
	return &mockModuleHandle{}, nil
}

func Test_DecapService_UpdateAndShow(t *testing.T) {
	svc := newTestService(t)
	prefix := "10.0.0.0/24"

	resp, err := svc.UpdateConfig(t.Context(), &decappb.UpdateConfigRequest{
		Name:     "decap0",
		Prefixes: []string{prefix},
	})
	require.NotNil(t, resp)
	require.NoError(t, err)

	show, err := svc.ShowConfig(t.Context(), &decappb.ShowConfigRequest{Name: "decap0"})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]string{prefix}, show.Prefixes))
}

func Test_DecapService_UpdateFullyReplaces(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.UpdateConfig(t.Context(), &decappb.UpdateConfigRequest{
		Name:     "decap0",
		Prefixes: []string{"10.0.0.0/24", "10.0.1.0/24"},
	})
	require.NoError(t, err)

	_, err = svc.UpdateConfig(t.Context(), &decappb.UpdateConfigRequest{
		Name:     "decap0",
		Prefixes: []string{"192.168.0.0/16"},
	})
	require.NoError(t, err)

	show, err := svc.ShowConfig(t.Context(), &decappb.ShowConfigRequest{Name: "decap0"})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]string{"192.168.0.0/16"}, show.Prefixes))
}

func Test_DecapService_ListUpdateList(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	list, err := svc.ListConfigs(ctx, &decappb.ListConfigsRequest{})
	require.NotNil(t, list)
	require.NoError(t, err)
	assert.Empty(t, list.Configs)

	_, err = svc.UpdateConfig(ctx, &decappb.UpdateConfigRequest{
		Name:     "decap0",
		Prefixes: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)

	list, err = svc.ListConfigs(ctx, &decappb.ListConfigsRequest{})
	require.NotNil(t, list)
	require.NoError(t, err)
	assert.Equal(t, []string{"decap0"}, list.Configs)
}

func Test_DecapService_EmptyConfigName(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	t.Run("UpdateConfig", func(t *testing.T) {
		resp, err := svc.UpdateConfig(ctx, &decappb.UpdateConfigRequest{
			Name:     "",
			Prefixes: []string{"10.0.0.0/24"},
		})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("ShowConfig", func(t *testing.T) {
		resp, err := svc.ShowConfig(ctx, &decappb.ShowConfigRequest{})
		require.Nil(t, resp)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func Test_DecapService_InvalidPrefix(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.UpdateConfig(t.Context(), &decappb.UpdateConfigRequest{
		Name:     "decap0",
		Prefixes: []string{"not-a-prefix"},
	})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func Test_DecapService_UpdateFailureAtomic(t *testing.T) {
	svc := NewDecapService(&flakyBackend{})
	ctx := t.Context()
	name := "decap0"

	_, err := svc.UpdateConfig(ctx, &decappb.UpdateConfigRequest{
		Name:     name,
		Prefixes: []string{"10.0.0.0/24"},
	})
	require.NoError(t, err)

	_, err = svc.UpdateConfig(ctx, &decappb.UpdateConfigRequest{
		Name:     name,
		Prefixes: []string{"10.0.1.0/24"},
	})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))

	show, err := svc.ShowConfig(ctx, &decappb.ShowConfigRequest{Name: name})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]string{"10.0.0.0/24"}, show.Prefixes))
}

func Test_DecapService_DeduplicatePrefixes(t *testing.T) {
	svc := newTestService(t)
	prefix := "10.0.0.0/24"

	_, err := svc.UpdateConfig(t.Context(), &decappb.UpdateConfigRequest{
		Name:     "decap0",
		Prefixes: []string{prefix, prefix, prefix},
	})
	require.NoError(t, err)

	show, err := svc.ShowConfig(t.Context(), &decappb.ShowConfigRequest{Name: "decap0"})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]string{prefix}, show.Prefixes))
}

func Test_DecapService_ConcurrentAccess(t *testing.T) {
	svc := newTestService(t)

	const goroutines = 10
	const iterations = 100

	g, ctx := errgroup.WithContext(t.Context())

	for idx := range goroutines {
		g.Go(func() error {
			for jdx := range iterations {
				name := fmt.Sprintf("config-%d-%d", idx, jdx)
				_, err := svc.UpdateConfig(ctx, &decappb.UpdateConfigRequest{
					Name:     name,
					Prefixes: []string{"10.0.0.0/24"},
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
				_, err := svc.ListConfigs(ctx, &decappb.ListConfigsRequest{})
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
				svc.ShowConfig(ctx, &decappb.ShowConfigRequest{Name: name})
			}
			return nil
		})
	}

	require.NoError(t, g.Wait())
}
