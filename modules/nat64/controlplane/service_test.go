package nat64

import (
	"context"
	"errors"
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

var errInjectedBackend = errors.New("injected backend failure")

// mockHandle is a shared-memory handle whose release always succeeds.
type mockHandle struct{}

func (m *mockHandle) Free() error {
	return nil
}

// mockBackend records a copy of every published config and can be set to
// fail every update from a chosen 1-based publish on; it finds nothing to
// delete in the dataplane.
type mockBackend struct {
	configs []NAT64Config
	failAt  int
}

func (m *mockBackend) Update(name string, cfg *NAT64Config) (crud.Handle, error) {
	if m.failAt != 0 && len(m.configs)+1 == m.failAt {
		return nil, errInjectedBackend
	}

	m.configs = append(m.configs, cfg.Clone())
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

// Test_NAT64Service_AddShowRemove verifies basic config lifecycle operations.
func Test_NAT64Service_AddShowRemove(t *testing.T) {
	backend := &mockBackend{}
	service := NewNAT64Service(backend)
	ctx := t.Context()

	prefix0 := mustIPv6Prefix(t, "64:ff9b::/96")
	prefix1 := mustIPv6Prefix(t, "2001:db8::/96")
	ipv4 := netip.MustParseAddr("192.0.2.1")
	ipv6 := netip.MustParseAddr("2001:db8::1")

	_, err := service.AddPrefix(ctx, &nat64pb.AddPrefixRequest{Name: "nat64-0", Prefix: prefix0})
	require.NoError(t, err)
	_, err = service.AddPrefix(ctx, &nat64pb.AddPrefixRequest{Name: "nat64-0", Prefix: prefix1})
	require.NoError(t, err)
	_, err = service.AddMapping(ctx, &nat64pb.AddMappingRequest{
		Name:        "nat64-0",
		Ipv4:        commonpb.NewIPv4Address(ipv4.As4()),
		Ipv6:        commonpb.NewIPv6Address(ipv6.As16()),
		PrefixIndex: 1,
	})
	require.NoError(t, err)

	show, err := service.ShowConfig(ctx, &nat64pb.ShowConfigRequest{Name: "nat64-0"})
	require.NoError(t, err)
	require.Len(t, show.GetConfig().GetPrefixes(), 2)
	require.Len(t, show.GetConfig().GetMappings(), 1)

	_, err = service.RemovePrefix(ctx, &nat64pb.RemovePrefixRequest{Name: "nat64-0", Prefix: prefix0})
	require.NoError(t, err)

	show, err = service.ShowConfig(ctx, &nat64pb.ShowConfigRequest{Name: "nat64-0"})
	require.NoError(t, err)
	require.Len(t, show.GetConfig().GetPrefixes(), 1)
	require.Equal(t, prefix1, show.GetConfig().GetPrefixes()[0])
	require.Len(t, show.GetConfig().GetMappings(), 1)
	require.Equal(t, uint32(0), show.GetConfig().GetMappings()[0].GetPrefixIndex())

	_, err = service.RemoveMapping(ctx, &nat64pb.RemoveMappingRequest{
		Name: "nat64-0",
		Ipv4: commonpb.NewIPv4Address(ipv4.As4()),
	})
	require.NoError(t, err)

	show, err = service.ShowConfig(ctx, &nat64pb.ShowConfigRequest{Name: "nat64-0"})
	require.NoError(t, err)
	require.Empty(t, show.GetConfig().GetMappings())
}

// Test_NAT64Service_SetMTUPassedToBackend verifies MTU values reach backend.
func Test_NAT64Service_SetMTUPassedToBackend(t *testing.T) {
	backend := &mockBackend{}
	service := NewNAT64Service(backend)

	_, err := service.SetMTU(t.Context(), &nat64pb.SetMTURequest{
		Name: "nat64-0",
		Mtu: &nat64pb.MTUConfig{
			Ipv4Mtu: 1450,
			Ipv6Mtu: 1280,
		},
	})
	require.NoError(t, err)
	require.Len(t, backend.configs, 1)
	require.Equal(t, MTUConfig{IPv4MTU: 1450, IPv6MTU: 1280}, backend.configs[0].MTU)
}

// Test_NAT64Service_AddPrefixDefaultMTU verifies default MTU on new config.
func Test_NAT64Service_AddPrefixDefaultMTU(t *testing.T) {
	backend := &mockBackend{}
	service := NewNAT64Service(backend)

	_, err := service.AddPrefix(t.Context(), &nat64pb.AddPrefixRequest{
		Name:   "nat64-0",
		Prefix: mustIPv6Prefix(t, "64:ff9b::/96"),
	})
	require.NoError(t, err)
	require.Len(t, backend.configs, 1)
	require.Equal(t, MTUConfig{IPv4MTU: 1450, IPv6MTU: 1280}, backend.configs[0].MTU)
}

// Test_NAT64Service_AddPrefix_ExistingIsNoop verifies that adding a prefix
// already in the config does not duplicate it or republish the module.
func Test_NAT64Service_AddPrefix_ExistingIsNoop(t *testing.T) {
	backend := &mockBackend{}
	service := NewNAT64Service(backend)
	prefix := mustIPv6Prefix(t, "64:ff9b::/96")

	_, err := service.AddPrefix(t.Context(), &nat64pb.AddPrefixRequest{
		Name:   "nat64-0",
		Prefix: prefix,
	})
	require.NoError(t, err)

	_, err = service.AddPrefix(t.Context(), &nat64pb.AddPrefixRequest{
		Name:   "nat64-0",
		Prefix: prefix,
	})
	require.NoError(t, err)

	show, err := service.ShowConfig(t.Context(), &nat64pb.ShowConfigRequest{Name: "nat64-0"})
	require.NoError(t, err)
	require.Len(t, show.GetConfig().GetPrefixes(), 1)
	require.Equal(t, prefix, show.GetConfig().GetPrefixes()[0])
	require.Len(t, backend.configs, 1)
}

// Test_NAT64Service_AddMapping_ExistingIPv4Replaced verifies that adding a
// mapping for an already mapped IPv4 replaces the entry instead of adding one.
func Test_NAT64Service_AddMapping_ExistingIPv4Replaced(t *testing.T) {
	backend := &mockBackend{}
	service := NewNAT64Service(backend)
	ipv4 := netip.MustParseAddr("192.0.2.1")
	ipv6First := netip.MustParseAddr("2001:db8::1")
	ipv6Second := netip.MustParseAddr("2001:db8::2")

	_, err := service.AddPrefix(t.Context(), &nat64pb.AddPrefixRequest{
		Name:   "nat64-0",
		Prefix: mustIPv6Prefix(t, "64:ff9b::/96"),
	})
	require.NoError(t, err)
	_, err = service.AddPrefix(t.Context(), &nat64pb.AddPrefixRequest{
		Name:   "nat64-0",
		Prefix: mustIPv6Prefix(t, "2001:db8::/96"),
	})
	require.NoError(t, err)

	_, err = service.AddMapping(t.Context(), &nat64pb.AddMappingRequest{
		Name:        "nat64-0",
		Ipv4:        commonpb.NewIPv4Address(ipv4.As4()),
		Ipv6:        commonpb.NewIPv6Address(ipv6First.As16()),
		PrefixIndex: 0,
	})
	require.NoError(t, err)

	_, err = service.AddMapping(t.Context(), &nat64pb.AddMappingRequest{
		Name:        "nat64-0",
		Ipv4:        commonpb.NewIPv4Address(ipv4.As4()),
		Ipv6:        commonpb.NewIPv6Address(ipv6Second.As16()),
		PrefixIndex: 1,
	})
	require.NoError(t, err)

	show, err := service.ShowConfig(t.Context(), &nat64pb.ShowConfigRequest{Name: "nat64-0"})
	require.NoError(t, err)
	require.Len(t, show.GetConfig().GetMappings(), 1)
	require.Equal(t, commonpb.NewIPv4Address(ipv4.As4()), show.GetConfig().GetMappings()[0].GetIpv4())
	require.Equal(t, commonpb.NewIPv6Address(ipv6Second.As16()), show.GetConfig().GetMappings()[0].GetIpv6())
	require.Equal(t, uint32(1), show.GetConfig().GetMappings()[0].GetPrefixIndex())

	last := backend.configs[len(backend.configs)-1]
	require.Len(t, last.Mappings, 1)
	require.Equal(t, ipv4, last.Mappings[0].IPv4)
	require.Equal(t, ipv6Second, last.Mappings[0].IPv6)
	require.Equal(t, uint32(1), last.Mappings[0].PrefixIndex)
}

// Test_NAT64Service_AddMapping_UpsertPublishedLast verifies that an upserted
// mapping is published last, so a shared IPv6 resolves to the newest mapping.
func Test_NAT64Service_AddMapping_UpsertPublishedLast(t *testing.T) {
	backend := &mockBackend{}
	service := NewNAT64Service(backend)
	ipv4First := netip.MustParseAddr("192.0.2.1")
	ipv4Second := netip.MustParseAddr("192.0.2.2")
	ipv6First := netip.MustParseAddr("2001:db8::1")
	ipv6Second := netip.MustParseAddr("2001:db8::2")

	_, err := service.AddPrefix(t.Context(), &nat64pb.AddPrefixRequest{
		Name:   "nat64-0",
		Prefix: mustIPv6Prefix(t, "64:ff9b::/96"),
	})
	require.NoError(t, err)

	_, err = service.AddMapping(t.Context(), &nat64pb.AddMappingRequest{
		Name:        "nat64-0",
		Ipv4:        commonpb.NewIPv4Address(ipv4First.As4()),
		Ipv6:        commonpb.NewIPv6Address(ipv6First.As16()),
		PrefixIndex: 0,
	})
	require.NoError(t, err)

	_, err = service.AddMapping(t.Context(), &nat64pb.AddMappingRequest{
		Name:        "nat64-0",
		Ipv4:        commonpb.NewIPv4Address(ipv4Second.As4()),
		Ipv6:        commonpb.NewIPv6Address(ipv6Second.As16()),
		PrefixIndex: 0,
	})
	require.NoError(t, err)

	_, err = service.AddMapping(t.Context(), &nat64pb.AddMappingRequest{
		Name:        "nat64-0",
		Ipv4:        commonpb.NewIPv4Address(ipv4First.As4()),
		Ipv6:        commonpb.NewIPv6Address(ipv6Second.As16()),
		PrefixIndex: 0,
	})
	require.NoError(t, err)

	last := backend.configs[len(backend.configs)-1]
	require.Equal(t, []Mapping{
		{IPv4: ipv4Second, IPv6: ipv6Second, PrefixIndex: 0},
		{IPv4: ipv4First, IPv6: ipv6Second, PrefixIndex: 0},
	}, last.Mappings)
}

// Test_NAT64Service_PrefixMutation_Invalid verifies that both mutations reject
// malformed and non-/96 prefixes.
func Test_NAT64Service_PrefixMutation_Invalid(t *testing.T) {
	testCases := []struct {
		name   string
		prefix *commonpb.IPv6Prefix
	}{
		{name: "missing address", prefix: &commonpb.IPv6Prefix{PrefixLen: 96}},
		{name: "non-96 prefix", prefix: mustIPv6Prefix(t, "2001:db8::/64")},
		{
			name: "overlong prefix",
			prefix: &commonpb.IPv6Prefix{
				Addr:      commonpb.NewIPv6Address([16]byte{}),
				PrefixLen: 129,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			service := NewNAT64Service(&mockBackend{})

			t.Run("add", func(t *testing.T) {
				_, err := service.AddPrefix(t.Context(), &nat64pb.AddPrefixRequest{
					Name:   "nat64-0",
					Prefix: tc.prefix,
				})
				require.Equal(t, codes.InvalidArgument, status.Code(err))
			})

			t.Run("remove", func(t *testing.T) {
				_, err := service.RemovePrefix(t.Context(), &nat64pb.RemovePrefixRequest{
					Name:   "nat64-0",
					Prefix: tc.prefix,
				})
				require.Equal(t, codes.InvalidArgument, status.Code(err))
			})
		})
	}
}

// Test_NAT64Service_SetDropUnknownDefaultMTU verifies default MTU is preserved.
func Test_NAT64Service_SetDropUnknownDefaultMTU(t *testing.T) {
	backend := &mockBackend{}
	service := NewNAT64Service(backend)

	_, err := service.SetDropUnknown(t.Context(), &nat64pb.SetDropUnknownRequest{
		Name:               "nat64-0",
		DropUnknownPrefix:  true,
		DropUnknownMapping: true,
	})
	require.NoError(t, err)
	require.Len(t, backend.configs, 1)
	require.Equal(t, MTUConfig{IPv4MTU: 1450, IPv6MTU: 1280}, backend.configs[0].MTU)
}

// Test_NAT64Service_SetMTUExplicitZero verifies zero MTU is kept.
func Test_NAT64Service_SetMTUExplicitZero(t *testing.T) {
	backend := &mockBackend{}
	service := NewNAT64Service(backend)

	_, err := service.SetMTU(t.Context(), &nat64pb.SetMTURequest{
		Name: "nat64-0",
		Mtu: &nat64pb.MTUConfig{
			Ipv4Mtu: 0,
			Ipv6Mtu: 0,
		},
	})
	require.NoError(t, err)
	require.Len(t, backend.configs, 1)
	require.Equal(t, MTUConfig{IPv4MTU: 0, IPv6MTU: 0}, backend.configs[0].MTU)
}

// Test_NAT64Service_UpdateFailureAtomic verifies failed updates do not mutate cache.
func Test_NAT64Service_UpdateFailureAtomic(t *testing.T) {
	backend := &mockBackend{failAt: 2}
	service := NewNAT64Service(backend)
	ctx := t.Context()

	prefix0 := mustIPv6Prefix(t, "64:ff9b::/96")
	prefix1 := mustIPv6Prefix(t, "2001:db8::/96")

	_, err := service.AddPrefix(ctx, &nat64pb.AddPrefixRequest{Name: "nat64-0", Prefix: prefix0})
	require.NoError(t, err)
	_, err = service.AddPrefix(ctx, &nat64pb.AddPrefixRequest{Name: "nat64-0", Prefix: prefix1})
	require.Equal(t, codes.Internal, status.Code(err))

	show, err := service.ShowConfig(ctx, &nat64pb.ShowConfigRequest{Name: "nat64-0"})
	require.NoError(t, err)
	require.Len(t, show.GetConfig().GetPrefixes(), 1)
	require.Equal(t, prefix0, show.GetConfig().GetPrefixes()[0])
}

// Test_NAT64Service_MappingAddressInvalid verifies that an IPv4-mapped IPv6
// address is rejected after the typed address conversion.
func Test_NAT64Service_MappingAddressInvalid(t *testing.T) {
	backend := &mockBackend{}
	service := NewNAT64Service(backend)
	ctx := t.Context()

	// A valid prefix keeps the prefix-index guard out of the way, so an
	// InvalidArgument below can come only from the address checks.
	_, err := service.AddPrefix(ctx, &nat64pb.AddPrefixRequest{
		Name:   "nat64-0",
		Prefix: mustIPv6Prefix(t, "64:ff9b::/96"),
	})
	require.NoError(t, err)

	ipv4 := commonpb.NewIPv4Address(netip.MustParseAddr("192.0.2.1").As4())
	mapped := commonpb.NewIPv6Address(netip.MustParseAddr("::ffff:192.0.2.1").As16())

	_, err = service.AddMapping(ctx, &nat64pb.AddMappingRequest{Name: "nat64-0", Ipv4: ipv4, Ipv6: mapped})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	// Only the prefix update reached the backend: no mapping landed.
	require.Len(t, backend.configs, 1)
	require.Empty(t, backend.configs[0].Mappings)
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

// Test_NAT64Service_Remove_NotFound verifies that a missing prefix or mapping
// leaves the published config intact, including after a successful deletion.
func Test_NAT64Service_Remove_NotFound(t *testing.T) {
	for _, element := range []string{"prefix", "mapping"} {
		for _, scenario := range []string{"unknown config", "absent element", "already removed"} {
			t.Run(element+"/"+scenario, func(t *testing.T) {
				backend := &mockBackend{}
				service := NewNAT64Service(backend)
				prefix := mustIPv6Prefix(t, "64:ff9b::/96")
				ipv4 := commonpb.NewIPv4Address([4]byte{192, 0, 2, 1})
				name := "nat64-0"
				_, err := service.AddPrefix(t.Context(), &nat64pb.AddPrefixRequest{Name: name, Prefix: prefix})
				require.NoError(t, err)
				_, err = service.AddMapping(t.Context(), &nat64pb.AddMappingRequest{
					Name: name, Ipv4: ipv4,
					Ipv6: commonpb.NewIPv6Address(netip.MustParseAddr("2001:db8::1").As16()),
				})
				require.NoError(t, err)

				remove := func() error {
					if element == "prefix" {
						_, err := service.RemovePrefix(t.Context(), &nat64pb.RemovePrefixRequest{Name: name, Prefix: prefix})
						return err
					}
					_, err := service.RemoveMapping(t.Context(), &nat64pb.RemoveMappingRequest{Name: name, Ipv4: ipv4})
					return err
				}
				switch scenario {
				case "unknown config":
					name = "missing"
				case "absent element":
					prefix = mustIPv6Prefix(t, "2001:db8::/96")
					ipv4 = commonpb.NewIPv4Address([4]byte{192, 0, 2, 2})
				case "already removed":
					require.NoError(t, remove())
					show, err := service.ShowConfig(t.Context(), &nat64pb.ShowConfigRequest{Name: name})
					require.NoError(t, err)
					if element == "prefix" {
						require.Empty(t, show.GetConfig().GetPrefixes())
					}
					require.Empty(t, show.GetConfig().GetMappings())
				}

				before, err := service.ShowConfig(t.Context(), &nat64pb.ShowConfigRequest{Name: "nat64-0"})
				require.NoError(t, err)
				updates := len(backend.configs)
				require.Equal(t, codes.NotFound, status.Code(remove()))
				after, err := service.ShowConfig(t.Context(), &nat64pb.ShowConfigRequest{Name: "nat64-0"})
				require.NoError(t, err)
				require.True(t, proto.Equal(before, after), "a rejected removal must preserve the config")
				require.Equal(t, updates, len(backend.configs))
				configs, err := service.ListConfigs(t.Context(), &nat64pb.ListConfigsRequest{})
				require.NoError(t, err)
				require.Equal(t, []string{"nat64-0"}, configs.GetConfigs())
			})
		}
	}
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
