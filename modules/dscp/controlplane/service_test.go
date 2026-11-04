package dscp

import (
	"context"
	"fmt"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
	"github.com/yanet-platform/yanet2/controlplane/crud"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
)

var errBackendFailure = fmt.Errorf("backend failure")

// mustPrefixes4 returns family-typed IPv4 prefix messages for valid CIDR
// test inputs.
func mustPrefixes4(t *testing.T, prefixes ...string) []*commonpb.IPv4Prefix {
	t.Helper()

	networks, err := commonpb.NewIPv4PrefixesFromPrefixes(parsePrefixes(prefixes))
	require.NoError(t, err)
	return networks
}

// mustPrefixes6 is mustPrefixes4 for IPv6 prefix messages.
func mustPrefixes6(t *testing.T, prefixes ...string) []*commonpb.IPv6Prefix {
	t.Helper()

	networks, err := commonpb.NewIPv6PrefixesFromPrefixes(parsePrefixes(prefixes))
	require.NoError(t, err)
	return networks
}

// parsePrefixes parses valid CIDR test inputs.
func parsePrefixes(prefixes []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		out = append(out, netip.MustParsePrefix(prefix))
	}
	return out
}

// prefixStrings returns canonical CIDR text for valid prefix messages of
// either family.
func prefixStrings[T interface{ ToPrefix() (netip.Prefix, error) }](t *testing.T, networks []T) []string {
	t.Helper()

	prefixes := make([]string, 0, len(networks))
	for _, network := range networks {
		prefix, err := network.ToPrefix()
		require.NoError(t, err)
		prefixes = append(prefixes, prefix.String())
	}

	return prefixes
}

// mockModuleHandle is a shared-memory handle whose release always succeeds.
type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() error {
	return nil
}

// mockBackend publishes every config, recording a copy of each, hands out
// a fresh handle and finds nothing to delete in the dataplane.
type mockBackend struct {
	configs []*config
}

func (m *mockBackend) Update(name string, payload *config) (crud.Handle, error) {
	m.configs = append(m.configs, payload.Clone())
	return &mockModuleHandle{}, nil
}

func (m *mockBackend) Delete(name string) error {
	return ffi.ErrNotFound
}

func newTestService(t *testing.T) *DscpService {
	t.Helper()
	return NewDscpService(&mockBackend{})
}

// flakyBackend publishes the first config and fails every later update.
type flakyBackend struct {
	numCalls atomic.Int64
}

func (m *flakyBackend) Update(name string, payload *config) (crud.Handle, error) {
	if m.numCalls.Add(1) > 1 {
		return nil, errBackendFailure
	}
	return &mockModuleHandle{}, nil
}

func (m *flakyBackend) Delete(name string) error {
	return nil
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
			Name:      "dscp0",
			Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
			Prefixes6: mustPrefixes6(t, "2001:db8::/32"),
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
		assert.Equal(t, []string{"10.0.0.0/24"}, prefixStrings(t, response.Config.Prefixes4))
		assert.Equal(t, []string{"2001:db8::/32"}, prefixStrings(t, response.Config.Prefixes6))
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
			Name:      "dscp0",
			Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
		})
		require.NotNil(t, response)
		require.NoError(t, err)
	}

	{
		response, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: "dscp0"})
		require.NotNil(t, response)
		require.NoError(t, err)
		assert.Empty(t, response.Config.Prefixes4)
		assert.Equal(t, []string{"2001:db8::/32"}, prefixStrings(t, response.Config.Prefixes6))
	}
}

// Test_DscpService_AddRemovePrefixes_RejectsUnparsablePrefix verifies that a
// prefix the converter cannot parse is rejected with InvalidArgument.
func Test_DscpService_AddRemovePrefixes_RejectsUnparsablePrefix(t *testing.T) {
	t.Parallel()
	service := newTestService(t)
	ctx := t.Context()

	t.Run("AddPrefixesInvalidPrefix", func(t *testing.T) {
		response, err := service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
			Name:      "dscp0",
			Prefixes4: []*commonpb.IPv4Prefix{{PrefixLen: 24}},
		})
		require.Nil(t, response)
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("RemovePrefixesInvalidPrefix", func(t *testing.T) {
		response, err := service.RemovePrefixes(ctx, &dscppb.RemovePrefixesRequest{
			Name:      "dscp0",
			Prefixes6: []*commonpb.IPv6Prefix{{PrefixLen: 64}},
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
		Name:      name,
		Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
	})
	require.NoError(t, err)

	_, err = service.AddPrefixes(ctx, &dscppb.AddPrefixesRequest{
		Name:      name,
		Prefixes4: mustPrefixes4(t, "20.0.0.0/24"),
	})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))

	response, err := service.ShowConfig(ctx, &dscppb.ShowConfigRequest{Name: name})
	require.NotNil(t, response)
	require.NoError(t, err)
	assert.Equal(t, []string{"10.0.0.0/24"}, prefixStrings(t, response.Config.Prefixes4))
}

// Test_DscpService_DeleteConfig_MissingConfig verifies that deleting a
// config that was never created returns NotFound.
func Test_DscpService_DeleteConfig_MissingConfig(t *testing.T) {
	t.Parallel()
	service := newTestService(t)

	response, err := service.DeleteConfig(t.Context(), &dscppb.DeleteConfigRequest{Name: "dscp0"})
	require.Nil(t, response)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// updateConfig serves an update as the gateway does, request validation
// ahead of the handler, and returns the status the caller would see.
func updateConfig(t *testing.T, service *DscpService, request *dscppb.UpdateConfigRequest) error {
	t.Helper()

	_, err := xgrpc.ValidateUnaryInterceptor()(
		t.Context(),
		request,
		&grpc.UnaryServerInfo{},
		func(ctx context.Context, message any) (any, error) {
			return service.UpdateConfig(ctx, message.(*dscppb.UpdateConfigRequest))
		},
	)
	return err
}

// Test_DscpService_UpdateConfig_ReplacesWholeConfig verifies that an update
// of a published config publishes exactly the config it carries, its
// prefixes sorted and duplicate-free and an unset marking the zero one.
func Test_DscpService_UpdateConfig_ReplacesWholeConfig(t *testing.T) {
	t.Parallel()

	previous := &dscppb.Config{
		Prefixes4:  mustPrefixes4(t, "10.0.0.0/24", "10.0.1.0/24"),
		Prefixes6:  mustPrefixes6(t, "2001:db8::/32"),
		DscpConfig: &dscppb.DscpConfig{Flag: 2, Mark: 8},
	}

	cases := []struct {
		name   string
		config *dscppb.Config
		want   *dscppb.Config
	}{
		{
			name: "explicit value in every field, prefixes unsorted and repeated",
			config: &dscppb.Config{
				Prefixes4:  mustPrefixes4(t, "192.168.0.0/16", "10.0.1.0/24", "192.168.0.0/16"),
				Prefixes6:  mustPrefixes6(t, "2001:db8:2::/48", "2001:db8:1::/48"),
				DscpConfig: &dscppb.DscpConfig{Flag: 1, Mark: 46},
			},
			want: &dscppb.Config{
				Prefixes4:  mustPrefixes4(t, "10.0.1.0/24", "192.168.0.0/16"),
				Prefixes6:  mustPrefixes6(t, "2001:db8:1::/48", "2001:db8:2::/48"),
				DscpConfig: &dscppb.DscpConfig{Flag: 1, Mark: 46},
			},
		},
		{
			name: "fields left out reset to defaults",
			config: &dscppb.Config{
				Prefixes4: mustPrefixes4(t, "10.0.1.0/24", "192.168.0.0/16"),
			},
			want: &dscppb.Config{
				Prefixes4:  mustPrefixes4(t, "10.0.1.0/24", "192.168.0.0/16"),
				DscpConfig: &dscppb.DscpConfig{},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			service := newTestService(t)
			require.NoError(t, updateConfig(t, service, &dscppb.UpdateConfigRequest{
				Name:   "dscp0",
				Config: previous,
			}))

			require.NoError(t, updateConfig(t, service, &dscppb.UpdateConfigRequest{
				Name:   "dscp0",
				Config: tc.config,
			}))

			response, err := service.ShowConfig(t.Context(), &dscppb.ShowConfigRequest{Name: "dscp0"})
			require.NoError(t, err)
			require.True(t, proto.Equal(tc.want, response.GetConfig()), "want %v, got %v", tc.want, response.GetConfig())
		})
	}
}

// Test_DscpService_UpdateConfig_InvalidKeepsPreviousConfig verifies that an
// update its validation or its prefix conversion rejects fails with
// InvalidArgument, publishes none of its prefixes and leaves the previous
// config in place.
func Test_DscpService_UpdateConfig_InvalidKeepsPreviousConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		config *dscppb.Config
	}{
		{
			name:   "no config",
			config: nil,
		},
		{
			name: "IPv4 prefix without an address",
			config: &dscppb.Config{
				Prefixes4: []*commonpb.IPv4Prefix{{PrefixLen: 24}},
			},
		},
		{
			name: "IPv6 prefix longer than its address beside a valid IPv4 prefix",
			config: &dscppb.Config{
				Prefixes4: mustPrefixes4(t, "192.168.0.0/16"),
				Prefixes6: []*commonpb.IPv6Prefix{{Addr: &commonpb.IPv6Address{}, PrefixLen: 129}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			backend := &mockBackend{}
			service := NewDscpService(backend)
			require.NoError(t, updateConfig(t, service, &dscppb.UpdateConfigRequest{
				Name: "dscp0",
				Config: &dscppb.Config{
					Prefixes4:  mustPrefixes4(t, "10.0.0.0/24"),
					DscpConfig: &dscppb.DscpConfig{Flag: 2, Mark: 8},
				},
			}))
			before, err := service.ShowConfig(t.Context(), &dscppb.ShowConfigRequest{Name: "dscp0"})
			require.NoError(t, err)

			err = updateConfig(t, service, &dscppb.UpdateConfigRequest{
				Name:   "dscp0",
				Config: tc.config,
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err))

			require.Len(t, backend.configs, 1, "a rejected update must publish nothing")
			after, err := service.ShowConfig(t.Context(), &dscppb.ShowConfigRequest{Name: "dscp0"})
			require.NoError(t, err)
			require.True(t, proto.Equal(before, after), "a rejected update must keep the previous config")
		})
	}
}
