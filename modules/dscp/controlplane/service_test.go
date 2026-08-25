package dscp

import (
	"fmt"
	"net/netip"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
)

var errBackendFailure = fmt.Errorf("backend failure")

// mustNetworks returns structured networks for valid CIDR test inputs.
func mustNetworks(t *testing.T, prefixes ...string) []*commonpb.IPPrefix {
	t.Helper()

	networks := make([]*commonpb.IPPrefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		network, err := commonpb.NewIPPrefixFromPrefix(netip.MustParsePrefix(prefix))
		require.NoError(t, err)
		networks = append(networks, network)
	}

	return networks
}

// networkStrings returns canonical CIDR text for valid structured networks.
func networkStrings(t *testing.T, networks []*commonpb.IPPrefix) []string {
	t.Helper()

	prefixes := make([]string, 0, len(networks))
	for _, network := range networks {
		prefix, err := network.ToPrefix()
		require.NoError(t, err)
		prefixes = append(prefixes, prefix.String())
	}

	return prefixes
}

// malformedNetwork returns a network with an invalid address length.
func malformedNetwork() *commonpb.IPPrefix {
	return &commonpb.IPPrefix{
		Addr:      &commonpb.IPAddress{Addr: []byte{10, 0, 0}},
		PrefixLen: 24,
	}
}

type mockModuleHandle struct {
}

func (m *mockModuleHandle) Free() error {
	return nil
}

type mockBackend struct{}

func (m *mockBackend) UpdateModule(
	name string,
	prefixes []netip.Prefix,
	flag uint8,
	mark uint8,
) (ModuleHandle, error) {
	return &mockModuleHandle{}, nil
}

func newTestService(t *testing.T) *DscpService {
	t.Helper()
	return NewDscpService(&mockBackend{})
}

type flakyBackend struct {
	mu       sync.Mutex
	numCalls int
	backend  mockBackend
}

func (m *flakyBackend) UpdateModule(
	name string,
	prefixes []netip.Prefix,
	flag uint8,
	mark uint8,
) (ModuleHandle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.numCalls++
	if m.numCalls > 1 {
		return nil, errBackendFailure
	}

	return m.backend.UpdateModule(name, prefixes, flag, mark)
}

// Test_DscpService_ListShowAddRemoveSetMarking verifies that prefix mutations
// and marking updates remain visible through the structured response.
func Test_DscpService_ListShowAddRemoveSetMarking(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	ctx := t.Context()

	{
		response, err := service.ListConfigs(ctx, &dscppb.ListConfigsRequest{})
		require.NotNil(t, response)
		require.NoError(t, err)
		assert.Empty(t, response.Configs)
	}

	{
		response, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
			Name:     "dscp0",
			Prefixes: mustNetworks(t, "10.0.0.0/24", "2001:db8::/32"),
		})
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.ListConfigs(ctx, &dscppb.ListConfigsRequest{})
		require.NotNil(t, response)
		require.NoError(t, err)
		assert.Equal(t, []string{"dscp0"}, response.Configs)
	}

	{
		response, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: "dscp0"})
		require.NotNil(t, response)
		require.NoError(t, err)
		assert.Empty(t, response.Config.DscpConfig)
		assert.Equal(
			t,
			[]string{"10.0.0.0/24", "2001:db8::/32"},
			networkStrings(t, response.Config.Prefixes),
		)
	}

	{
		response, err := service.SetDscpMarking(ctx, &dscppb.SetDscpMarkingRequest{
			Name: "dscp0",
			DscpConfig: &dscppb.DscpConfig{
				Flag: 2,
				Mark: 8,
			},
		})
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: "dscp0"})
		require.NotNil(t, response)
		require.NoError(t, err)
		assert.Equal(t, uint32(2), response.Config.DscpConfig.Flag)
		assert.Equal(t, uint32(8), response.Config.DscpConfig.Mark)
	}

	{
		response, err := service.RemovePrefixes(ctx, &dscppb.RemovePrefixesRequest{
			Name:     "dscp0",
			Prefixes: mustNetworks(t, "10.0.0.0/24"),
		})
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: "dscp0"})
		require.NotNil(t, response)
		require.NoError(t, err)
		assert.Equal(
			t,
			[]string{"2001:db8::/32"},
			networkStrings(t, response.Config.Prefixes),
		)
	}
}

// Test_DscpService_RequestValidation verifies that invalid names, networks,
// and marking values are rejected with InvalidArgument.
func Test_DscpService_RequestValidation(t *testing.T) {
	t.Parallel()
	service := newTestService(t)
	ctx := t.Context()

	t.Run("ShowConfigInvalidName", func(t *testing.T) {
		response, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: ""})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("AddPrefixesInvalidName", func(t *testing.T) {
		response, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
			Name:     "",
			Prefixes: mustNetworks(t, "10.0.0.0/24"),
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("RemovePrefixesInvalidName", func(t *testing.T) {
		response, err := service.RemovePrefixes(ctx, &dscppb.RemovePrefixesRequest{
			Name:     "",
			Prefixes: mustNetworks(t, "10.0.0.0/24"),
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetDscpMarkingNoDSCPConfig", func(t *testing.T) {
		response, err := service.SetDscpMarking(ctx, &dscppb.SetDscpMarkingRequest{
			Name: "dscp0",
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("AddPrefixesInvalidPrefix", func(t *testing.T) {
		response, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
			Name:     "dscp0",
			Prefixes: []*commonpb.IPPrefix{malformedNetwork()},
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("RemovePrefixesInvalidPrefix", func(t *testing.T) {
		response, err := service.RemovePrefixes(ctx, &dscppb.RemovePrefixesRequest{
			Name:     "dscp0",
			Prefixes: []*commonpb.IPPrefix{malformedNetwork()},
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetDscpMarkingInvalidFlag", func(t *testing.T) {
		response, err := service.SetDscpMarking(ctx, &dscppb.SetDscpMarkingRequest{
			Name: "dscp0",
			DscpConfig: &dscppb.DscpConfig{
				Flag: 3,
				Mark: 8,
			},
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetDscpMarkingInvalidMark", func(t *testing.T) {
		response, err := service.SetDscpMarking(ctx, &dscppb.SetDscpMarkingRequest{
			Name: "dscp0",
			DscpConfig: &dscppb.DscpConfig{
				Flag: 1,
				Mark: 64,
			},
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

// Test_DscpService_NoUpdateOnFailure verifies that a failed backend update
// leaves the last successfully published configuration visible.
func Test_DscpService_NoUpdateOnFailure(t *testing.T) {
	t.Parallel()

	backend := &flakyBackend{}
	service := NewDscpService(backend)
	ctx := t.Context()
	name := "dscp0"

	_, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
		Name:     name,
		Prefixes: mustNetworks(t, "10.0.0.0/24"),
	})
	require.NoError(t, err)

	_, err = service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
		Name:     name,
		Prefixes: mustNetworks(t, "20.0.0.0/24"),
	})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))

	response, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: name})
	require.NotNil(t, response)
	require.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.0/24"}, networkStrings(t, response.Config.Prefixes))
}

// Test_DscpService_ConcurrentAccess verifies that concurrent mutations and
// reads complete without a data race.
func Test_DscpService_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	service := newTestService(t)
	ctx := t.Context()

	const goroutines = 10
	const iterations = 100

	group, _ := errgroup.WithContext(ctx)
	for i := range goroutines {
		group.Go(func() error {
			name := fmt.Sprintf("cfg-%d", i%3)
			for j := range iterations {
				if j%3 == 0 {
					if _, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
						Name:     name,
						Prefixes: mustNetworks(t, fmt.Sprintf("10.%d.%d.0/24", i, j)),
					}); err != nil {
						return err
					}
					continue
				}
				if j%3 == 1 {
					if _, err := service.RemovePrefixes(ctx, &dscppb.RemovePrefixesRequest{
						Name:     name,
						Prefixes: mustNetworks(t, fmt.Sprintf("10.%d.%d.0/24", i, j)),
					}); err != nil {
						return err
					}
					continue
				}
				_, err := service.SetDscpMarking(ctx, &dscppb.SetDscpMarkingRequest{
					Name: name,
					DscpConfig: &dscppb.DscpConfig{
						Flag: uint32(j % 2),
						Mark: uint32(j % 16),
					},
				})
				if err != nil {
					return err
				}
			}
			return nil
		})
	}

	require.NoError(t, group.Wait())
}
