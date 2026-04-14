package decap

import (
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb"
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
	return NewDecapService(&mockBackend{}, zap.NewNop().Sugar())
}

// flakyBackend succeeds on the first UpdateModule call and fails thereafter.
type flakyBackend struct {
	numCalls atomic.Int64
}

func (m *flakyBackend) UpdateModule(
	name string,
	prefixes []netip.Prefix,
) (ModuleHandle, error) {
	numCalls := m.numCalls.Add(1)

	if numCalls >= 2 {
		return nil, errInjectedBackend
	}

	return &mockModuleHandle{}, nil
}

func Test_DecapService_AddShow(t *testing.T) {
	service := newTestService(t)
	prefix := "10.0.0.0/24"

	{
		response, err := service.AddPrefixes(
			t.Context(),
			&decappb.AddPrefixesRequest{
				Name:     "decap0",
				Prefixes: []string{prefix},
			},
		)
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.ShowConfig(
			t.Context(),
			&decappb.ShowConfigRequest{
				Name: "decap0",
			},
		)
		require.NotNil(t, response)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff([]string{prefix}, response.Prefixes))
	}
}

func Test_DecapService_AddRemovePartial(t *testing.T) {
	service := newTestService(t)

	{
		response, err := service.AddPrefixes(
			t.Context(),
			&decappb.AddPrefixesRequest{
				Name: "decap0",
				Prefixes: []string{
					"10.0.0.0/24",
					"10.0.1.0/24",
				},
			},
		)
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.RemovePrefixes(
			t.Context(),
			&decappb.RemovePrefixesRequest{
				Name:     "decap0",
				Prefixes: []string{"10.0.0.0/24"},
			},
		)
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.ShowConfig(
			t.Context(),
			&decappb.ShowConfigRequest{
				Name: "decap0",
			},
		)
		require.NotNil(t, response)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff([]string{"10.0.1.0/24"}, response.Prefixes))
	}
}

func Test_DecapService_ListAddList(t *testing.T) {
	service := newTestService(t)
	ctx := t.Context()

	{
		response, err := service.ListConfigs(
			ctx,
			&decappb.ListConfigsRequest{},
		)
		require.NotNil(t, response)
		require.NoError(t, err)

		assert.Empty(t, response.Configs)
	}

	{
		response, err := service.AddPrefixes(
			ctx, &decappb.AddPrefixesRequest{
				Name:     "decap0",
				Prefixes: []string{"10.0.0.0/24"},
			},
		)
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.ListConfigs(ctx, &decappb.ListConfigsRequest{})
		require.NotNil(t, response)
		require.NoError(t, err)

		assert.Equal(t, []string{"decap0"}, response.Configs)
	}
}

func Test_DecapService_EmptyConfigName(t *testing.T) {
	service := newTestService(t)
	ctx := t.Context()

	t.Run("AddPrefixes", func(t *testing.T) {
		response, err := service.AddPrefixes(
			ctx,
			&decappb.AddPrefixesRequest{
				Name:     "",
				Prefixes: []string{"10.0.0.0/24"},
			},
		)
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("ShowConfig", func(t *testing.T) {
		response, err := service.ShowConfig(
			ctx,
			&decappb.ShowConfigRequest{},
		)
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("RemovePrefixes", func(t *testing.T) {
		response, err := service.RemovePrefixes(
			ctx,
			&decappb.RemovePrefixesRequest{
				Name:     "",
				Prefixes: []string{"10.0.0.0/24"},
			},
		)
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func Test_DecapService_UpdateFailureAtomic(t *testing.T) {
	service := NewDecapService(&flakyBackend{}, zap.NewNop().Sugar())
	ctx := t.Context()
	name := "decap0"

	{
		response, err := service.AddPrefixes(ctx, &decappb.AddPrefixesRequest{
			Name:     name,
			Prefixes: []string{"10.0.0.0/24"},
		})
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.AddPrefixes(ctx, &decappb.AddPrefixesRequest{
			Name: name,
			Prefixes: []string{
				"10.0.1.0/24",
			},
		})
		require.Nil(t, response)
		require.Error(t, err)
		require.Equal(t, codes.Internal, status.Code(err))
	}

	{
		response, err := service.ShowConfig(ctx, &decappb.ShowConfigRequest{Name: name})
		require.NotNil(t, response)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff([]string{"10.0.0.0/24"}, response.Prefixes))
	}
}

func Test_DecapService_DeduplicatePrefixes(t *testing.T) {
	service := newTestService(t)
	prefix := "10.0.0.0/24"

	{
		response, err := service.AddPrefixes(
			t.Context(),
			&decappb.AddPrefixesRequest{
				Name:     "decap0",
				Prefixes: []string{prefix, prefix, prefix},
			},
		)
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.ShowConfig(
			t.Context(),
			&decappb.ShowConfigRequest{Name: "decap0"},
		)
		require.NotNil(t, response)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff([]string{prefix}, response.Prefixes))
	}
}
