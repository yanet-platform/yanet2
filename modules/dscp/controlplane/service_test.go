package dscp

import (
	"context"
	"net/netip"
	"testing"

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

// mockModuleHandle is a shared-memory handle whose release always succeeds.
type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() error {
	return nil
}

// mockBackend publishes every config, recording each, hands out a fresh
// handle and finds nothing to delete in the dataplane.
type mockBackend struct {
	configs []*config
}

func (m *mockBackend) Update(name string, payload *config) (crud.Handle, error) {
	m.configs = append(m.configs, payload)
	return &mockModuleHandle{}, nil
}

func (m *mockBackend) Delete(name string) error {
	return ffi.ErrNotFound
}

func newTestService(t *testing.T) *DscpService {
	t.Helper()
	return NewDscpService(&mockBackend{})
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
		Prefixes4:       mustPrefixes4(t, "10.0.0.0/24", "10.0.1.0/24"),
		Prefixes6:       mustPrefixes6(t, "2001:db8::/32"),
		DscpConfig:      &dscppb.DscpConfig{Flag: 2, Mark: 8},
		SourcePrefixes4: mustPrefixes4(t, "198.51.100.0/24"),
		SourcePrefixes6: mustPrefixes6(t, "2001:db8:3::/48"),
	}

	cases := []struct {
		name   string
		config *dscppb.Config
		want   *dscppb.Config
	}{
		{
			name: "explicit value in every field, prefixes unsorted and repeated",
			config: &dscppb.Config{
				Prefixes4:       mustPrefixes4(t, "192.168.0.0/16", "10.0.1.0/24", "192.168.0.0/16"),
				Prefixes6:       mustPrefixes6(t, "2001:db8:2::/48", "2001:db8:1::/48"),
				DscpConfig:      &dscppb.DscpConfig{Flag: 1, Mark: 46},
				SourcePrefixes4: mustPrefixes4(t, "192.0.2.0/24", "10.1.0.0/16", "192.0.2.0/24"),
				SourcePrefixes6: mustPrefixes6(t, "2001:db8:4::/48", "2001:db8:3::/48", "2001:db8:4::/48"),
			},
			want: &dscppb.Config{
				Prefixes4:       mustPrefixes4(t, "10.0.1.0/24", "192.168.0.0/16"),
				Prefixes6:       mustPrefixes6(t, "2001:db8:1::/48", "2001:db8:2::/48"),
				DscpConfig:      &dscppb.DscpConfig{Flag: 1, Mark: 46},
				SourcePrefixes4: mustPrefixes4(t, "10.1.0.0/16", "192.0.2.0/24"),
				SourcePrefixes6: mustPrefixes6(t, "2001:db8:3::/48", "2001:db8:4::/48"),
			},
		},
		{
			name: "source-only config clears destinations",
			config: &dscppb.Config{
				SourcePrefixes4: mustPrefixes4(t, "192.0.2.0/24"),
				SourcePrefixes6: mustPrefixes6(t, "2001:db8:1::/48"),
				DscpConfig:      &dscppb.DscpConfig{Flag: 2, Mark: 46},
			},
			want: &dscppb.Config{
				SourcePrefixes4: mustPrefixes4(t, "192.0.2.0/24"),
				SourcePrefixes6: mustPrefixes6(t, "2001:db8:1::/48"),
				DscpConfig:      &dscppb.DscpConfig{Flag: 2, Mark: 46},
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
		{
			name: "IPv4 source prefix without an address",
			config: &dscppb.Config{
				SourcePrefixes4: []*commonpb.IPv4Prefix{{PrefixLen: 24}},
			},
		},
		{
			name: "IPv4 source prefix longer than its address",
			config: &dscppb.Config{
				SourcePrefixes4: []*commonpb.IPv4Prefix{{Addr: &commonpb.IPv4Address{}, PrefixLen: 33}},
			},
		},
		{
			name: "nil IPv6 source prefix",
			config: &dscppb.Config{
				SourcePrefixes6: []*commonpb.IPv6Prefix{nil},
			},
		},
		{
			name: "IPv6 source prefix longer than its address beside a valid destination",
			config: &dscppb.Config{
				Prefixes4:       mustPrefixes4(t, "192.168.0.0/16"),
				SourcePrefixes6: []*commonpb.IPv6Prefix{{Addr: &commonpb.IPv6Address{}, PrefixLen: 129}},
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
