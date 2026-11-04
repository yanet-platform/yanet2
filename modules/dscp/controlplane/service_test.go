package dscp

import (
	"fmt"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
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

// mockBackend publishes every config, hands out a fresh handle and finds
// nothing to delete in the dataplane.
type mockBackend struct{}

func (m *mockBackend) Update(name string, payload *config) (crud.Handle, error) {
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
