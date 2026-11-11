package nat64

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
	nat64pb "github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb/v1"
)

// mockHandle is a shared-memory handle whose release always succeeds.
type mockHandle struct{}

func (m *mockHandle) Free() error {
	return nil
}

// mockBackend records every published config and finds nothing to delete in
// the dataplane.
type mockBackend struct {
	configs []NAT64Config
}

func (m *mockBackend) Update(name string, cfg *NAT64Config) (crud.Handle, error) {
	m.configs = append(m.configs, *cfg)
	return &mockHandle{}, nil
}

func mustIPv6Prefix(t *testing.T, value string) *commonpb.IPv6Prefix {
	t.Helper()

	prefix, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix(value))
	require.NoError(t, err)
	return prefix
}

// mustMapping returns the mapping message for valid address text.
func mustMapping(ipv4, ipv6 string, prefixIndex uint32) *nat64pb.Mapping {
	return &nat64pb.Mapping{
		Ipv4:        commonpb.NewIPv4Address(netip.MustParseAddr(ipv4).As4()),
		Ipv6:        commonpb.NewIPv6Address(netip.MustParseAddr(ipv6).As16()),
		PrefixIndex: prefixIndex,
	}
}

func (m *mockBackend) Delete(name string) error {
	return ffi.ErrNotFound
}

// Test_NAT64Service_DeleteConfig_MissingConfig verifies that deleting a
// config that was never created returns NotFound.
func Test_NAT64Service_DeleteConfig_MissingConfig(t *testing.T) {
	service := NewNAT64Service(&mockBackend{})
	ctx := t.Context()

	resp, err := service.DeleteConfig(ctx, &nat64pb.DeleteConfigRequest{Name: "nat64-0"})
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// updateConfig serves an update as the gateway does, request validation
// ahead of the handler, and returns the status the caller would see.
func updateConfig(t *testing.T, service *NAT64Service, request *nat64pb.UpdateConfigRequest) error {
	t.Helper()

	_, err := xgrpc.ValidateUnaryInterceptor()(
		t.Context(),
		request,
		&grpc.UnaryServerInfo{},
		func(ctx context.Context, message any) (any, error) {
			return service.UpdateConfig(ctx, message.(*nat64pb.UpdateConfigRequest))
		},
	)
	return err
}

// Test_NAT64Service_UpdateConfig_ReplacesWholeConfig verifies that an update
// of a published config publishes exactly the config it carries, the fields
// it leaves out at the defaults of a new config.
func Test_NAT64Service_UpdateConfig_ReplacesWholeConfig(t *testing.T) {
	previous := &nat64pb.Config{
		Prefixes: []*commonpb.IPv6Prefix{
			mustIPv6Prefix(t, "64:ff9b::/96"),
			mustIPv6Prefix(t, "2001:db8::/96"),
		},
		Mappings: []*nat64pb.Mapping{
			mustMapping("192.0.2.1", "2001:db8::1", 0),
			mustMapping("192.0.2.2", "2001:db8::2", 1),
		},
		Mtu:                &nat64pb.MTUConfig{Ipv4Mtu: 1500, Ipv6Mtu: 1500},
		DropUnknownMapping: true,
	}
	explicit := &nat64pb.Config{
		Prefixes: []*commonpb.IPv6Prefix{
			mustIPv6Prefix(t, "2001:db8:1::/96"),
			mustIPv6Prefix(t, "64:ff9b::/96"),
		},
		Mappings: []*nat64pb.Mapping{
			mustMapping("192.0.2.3", "2001:db8:1::3", 1),
			mustMapping("192.0.2.1", "2001:db8:1::1", 0),
		},
		Mtu:               &nat64pb.MTUConfig{Ipv4Mtu: 1400, Ipv6Mtu: 1300},
		DropUnknownPrefix: true,
	}

	cases := []struct {
		name   string
		config *nat64pb.Config
		want   *nat64pb.Config
	}{
		{
			name:   "explicit value in every field",
			config: explicit,
			want:   explicit,
		},
		{
			name: "fields left out reset to defaults",
			config: &nat64pb.Config{
				Prefixes: []*commonpb.IPv6Prefix{mustIPv6Prefix(t, "2001:db8::/96")},
				Mappings: []*nat64pb.Mapping{mustMapping("192.0.2.2", "2001:db8::2", 0)},
			},
			want: &nat64pb.Config{
				Prefixes: []*commonpb.IPv6Prefix{mustIPv6Prefix(t, "2001:db8::/96")},
				Mappings: []*nat64pb.Mapping{mustMapping("192.0.2.2", "2001:db8::2", 0)},
				Mtu:      &nat64pb.MTUConfig{Ipv4Mtu: 1450, Ipv6Mtu: 1280},
			},
		},
		{
			name:   "explicit zero MTU is kept",
			config: &nat64pb.Config{Mtu: &nat64pb.MTUConfig{}},
			want:   &nat64pb.Config{Mtu: &nat64pb.MTUConfig{}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := NewNAT64Service(&mockBackend{})
			require.NoError(t, updateConfig(t, service, &nat64pb.UpdateConfigRequest{
				Name:   "nat64-0",
				Config: previous,
			}))

			require.NoError(t, updateConfig(t, service, &nat64pb.UpdateConfigRequest{
				Name:   "nat64-0",
				Config: tc.config,
			}))

			show, err := service.ShowConfig(t.Context(), &nat64pb.ShowConfigRequest{Name: "nat64-0"})
			require.NoError(t, err)
			require.True(t, proto.Equal(tc.want, show.GetConfig()), "want %v, got %v", tc.want, show.GetConfig())
		})
	}
}

// Test_NAT64Service_UpdateConfig_InvalidKeepsPreviousConfig verifies that an
// update its validation or its conversion rejects fails with
// InvalidArgument, publishes nothing and leaves the previous config in
// place.
func Test_NAT64Service_UpdateConfig_InvalidKeepsPreviousConfig(t *testing.T) {
	cases := []struct {
		name   string
		config *nat64pb.Config
	}{
		{
			name:   "no config",
			config: nil,
		},
		{
			name: "prefix shorter than /96",
			config: &nat64pb.Config{
				Prefixes: []*commonpb.IPv6Prefix{mustIPv6Prefix(t, "2001:db8::/64")},
			},
		},
		{
			name: "prefix listed twice",
			config: &nat64pb.Config{
				Prefixes: []*commonpb.IPv6Prefix{
					mustIPv6Prefix(t, "64:ff9b::/96"),
					mustIPv6Prefix(t, "64:ff9b::/96"),
				},
			},
		},
		{
			name: "mapping without an IPv4 address",
			config: &nat64pb.Config{
				Prefixes: []*commonpb.IPv6Prefix{mustIPv6Prefix(t, "64:ff9b::/96")},
				Mappings: []*nat64pb.Mapping{
					{Ipv6: commonpb.NewIPv6Address(netip.MustParseAddr("2001:db8::3").As16())},
				},
			},
		},
		{
			name: "mapping without an IPv6 address",
			config: &nat64pb.Config{
				Prefixes: []*commonpb.IPv6Prefix{mustIPv6Prefix(t, "64:ff9b::/96")},
				Mappings: []*nat64pb.Mapping{
					{Ipv4: commonpb.NewIPv4Address(netip.MustParseAddr("192.0.2.3").As4())},
				},
			},
		},
		{
			name: "mapping IPv6 address is IPv4-mapped",
			config: &nat64pb.Config{
				Prefixes: []*commonpb.IPv6Prefix{mustIPv6Prefix(t, "64:ff9b::/96")},
				Mappings: []*nat64pb.Mapping{mustMapping("192.0.2.3", "::ffff:192.0.2.3", 0)},
			},
		},
		{
			name: "mapping prefix index past the prefixes",
			config: &nat64pb.Config{
				Prefixes: []*commonpb.IPv6Prefix{mustIPv6Prefix(t, "64:ff9b::/96")},
				Mappings: []*nat64pb.Mapping{mustMapping("192.0.2.3", "2001:db8::3", 1)},
			},
		},
		{
			name: "IPv4 address mapped twice",
			config: &nat64pb.Config{
				Prefixes: []*commonpb.IPv6Prefix{mustIPv6Prefix(t, "64:ff9b::/96")},
				Mappings: []*nat64pb.Mapping{
					mustMapping("192.0.2.3", "2001:db8::3", 0),
					mustMapping("192.0.2.3", "2001:db8::4", 0),
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &mockBackend{}
			service := NewNAT64Service(backend)
			require.NoError(t, updateConfig(t, service, &nat64pb.UpdateConfigRequest{
				Name: "nat64-0",
				Config: &nat64pb.Config{
					Prefixes: []*commonpb.IPv6Prefix{mustIPv6Prefix(t, "2001:db8::/96")},
					Mappings: []*nat64pb.Mapping{mustMapping("192.0.2.1", "2001:db8::1", 0)},
				},
			}))
			before, err := service.ShowConfig(t.Context(), &nat64pb.ShowConfigRequest{Name: "nat64-0"})
			require.NoError(t, err)

			err = updateConfig(t, service, &nat64pb.UpdateConfigRequest{
				Name:   "nat64-0",
				Config: tc.config,
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err))

			require.Len(t, backend.configs, 1, "a rejected update must publish nothing")
			after, err := service.ShowConfig(t.Context(), &nat64pb.ShowConfigRequest{Name: "nat64-0"})
			require.NoError(t, err)
			require.True(t, proto.Equal(before, after), "a rejected update must keep the previous config")
		})
	}
}
