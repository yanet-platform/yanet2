package nat64

import (
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	nat64pb "github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb/v1"
)

var errInjectedBackend = errors.New("injected backend failure")

type mockHandle struct {
	freed      bool
	freedCount atomic.Int64
}

func (m *mockHandle) Free() error {
	m.freed = true
	m.freedCount.Add(1)
	return nil
}

// refusingOnceHandle refuses its first Free with ffi.ErrStillReferenced, then succeeds.
type refusingOnceHandle struct {
	numCalls atomic.Int64
	freed    atomic.Int64
}

func (m *refusingOnceHandle) Free() error {
	if m.numCalls.Add(1) == 1 {
		return ffi.ErrStillReferenced
	}
	m.freed.Add(1)
	return nil
}

type mockBackend struct {
	configs     []NAT64Config
	handles     []*mockHandle
	failAt      int
	deletedName string
}

func (m *mockBackend) UpdateModule(name string, cfg *NAT64Config) (ModuleHandle, error) {
	if m.failAt != 0 && len(m.configs)+1 == m.failAt {
		return nil, errInjectedBackend
	}

	handle := &mockHandle{}
	m.configs = append(m.configs, cfg.Clone())
	m.handles = append(m.handles, handle)
	return handle, nil
}

func mustIPv6Prefix(t *testing.T, value string) *commonpb.IPv6Prefix {
	t.Helper()

	prefix, err := commonpb.NewIPv6PrefixFromPrefix(netip.MustParsePrefix(value))
	require.NoError(t, err)
	return prefix
}

func (m *mockBackend) DeleteModule(name string) error {
	m.deletedName = name
	return nil
}

// refusingDeleteBackend updates like mockBackend but refuses every delete,
// modeling a config still referenced by a live generation.
type refusingDeleteBackend struct {
	mockBackend
}

func (m *refusingDeleteBackend) DeleteModule(name string) error {
	m.deletedName = name
	return errInjectedBackend
}

// parkingBackend updates like mockBackend but refuses to release the first
// handle it mints, so that handle must be parked before it can be reclaimed.
type parkingBackend struct {
	mockBackend
	numCalls atomic.Int64
	first    refusingOnceHandle
}

func (m *parkingBackend) UpdateModule(name string, cfg *NAT64Config) (ModuleHandle, error) {
	if m.numCalls.Add(1) == 1 {
		return &m.first, nil
	}
	return m.mockBackend.UpdateModule(name, cfg)
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
	require.True(t, backend.handles[0].freed)
	require.False(t, backend.handles[1].freed)
	_, err = service.AddMapping(ctx, &nat64pb.AddMappingRequest{
		Name:        "nat64-0",
		Ipv4:        commonpb.NewIPv4Address(ipv4.As4()),
		Ipv6:        commonpb.NewIPv6Address(ipv6.As16()),
		PrefixIndex: 1,
	})
	require.NoError(t, err)
	require.True(t, backend.handles[1].freed)
	require.False(t, backend.handles[2].freed)

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

// Test_NAT64Service_PrefixMutation_Invalid verifies that both mutations reject
// missing, malformed, and non-/96 prefixes.
func Test_NAT64Service_PrefixMutation_Invalid(t *testing.T) {
	testCases := []struct {
		name   string
		prefix *commonpb.IPv6Prefix
	}{
		{name: "missing prefix"},
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

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service := NewNAT64Service(&mockBackend{})

			t.Run("add", func(t *testing.T) {
				_, err := service.AddPrefix(t.Context(), &nat64pb.AddPrefixRequest{
					Name:   "nat64-0",
					Prefix: testCase.prefix,
				})
				require.Equal(t, codes.InvalidArgument, status.Code(err))
			})

			t.Run("remove", func(t *testing.T) {
				_, err := service.RemovePrefix(t.Context(), &nat64pb.RemovePrefixRequest{
					Name:   "nat64-0",
					Prefix: testCase.prefix,
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
	require.False(t, backend.handles[0].freed)
}

// Test_NAT64Service_InvalidMTU verifies invalid MTU is rejected.
func Test_NAT64Service_InvalidMTU(t *testing.T) {
	service := NewNAT64Service(&mockBackend{})

	resp, err := service.SetMTU(t.Context(), &nat64pb.SetMTURequest{
		Name: "nat64-0",
		Mtu:  &nat64pb.MTUConfig{Ipv4Mtu: 65536},
	})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// Test_NAT64Service_MappingAddressInvalid verifies missing and
// IPv4-mapped mapping addresses are rejected.
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
	ipv6 := commonpb.NewIPv6Address(netip.MustParseAddr("2001:db8::1").As16())
	mapped := commonpb.NewIPv6Address(netip.MustParseAddr("::ffff:192.0.2.1").As16())

	_, err = service.AddMapping(ctx, &nat64pb.AddMappingRequest{Name: "nat64-0", Ipv6: ipv6})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = service.AddMapping(ctx, &nat64pb.AddMappingRequest{Name: "nat64-0", Ipv4: ipv4})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = service.AddMapping(ctx, &nat64pb.AddMappingRequest{Name: "nat64-0", Ipv4: ipv4, Ipv6: mapped})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = service.RemoveMapping(ctx, &nat64pb.RemoveMappingRequest{Name: "nat64-0"})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	// Only the prefix update reached the backend: no mapping landed.
	require.Len(t, backend.configs, 1)
	require.Empty(t, backend.configs[0].Mappings)
}

// Test_NAT64Service_DeleteConfig_InvalidName verifies that deleting a
// config with an empty name is rejected.
func Test_NAT64Service_DeleteConfig_InvalidName(t *testing.T) {
	service := NewNAT64Service(&mockBackend{})
	ctx := t.Context()

	resp, err := service.DeleteConfig(ctx, &nat64pb.DeleteConfigRequest{})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
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

// Test_NAT64Service_DeleteConfig_RemovesConfig verifies that deleting an
// unreferenced config removes it from ListConfigs and from Show.
func Test_NAT64Service_DeleteConfig_RemovesConfig(t *testing.T) {
	backend := &mockBackend{}
	service := NewNAT64Service(backend)
	ctx := t.Context()

	_, err := service.AddPrefix(ctx, &nat64pb.AddPrefixRequest{
		Name:   "nat64-0",
		Prefix: mustIPv6Prefix(t, "64:ff9b::/96"),
	})
	require.NoError(t, err)
	handle := backend.handles[0]

	resp, err := service.DeleteConfig(ctx, &nat64pb.DeleteConfigRequest{Name: "nat64-0"})
	require.NotNil(t, resp)
	require.NoError(t, err)
	require.Equal(t, "nat64-0", backend.deletedName)
	require.Equal(t, int64(1), handle.freedCount.Load())

	list, err := service.ListConfigs(ctx, &nat64pb.ListConfigsRequest{})
	require.NoError(t, err)
	require.Empty(t, list.Configs)

	show, err := service.ShowConfig(ctx, &nat64pb.ShowConfigRequest{Name: "nat64-0"})
	require.Nil(t, show)
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_NAT64Service_DeleteConfig_Referenced verifies that a backend refusal
// surfaces as Internal and leaves the config in place.
func Test_NAT64Service_DeleteConfig_Referenced(t *testing.T) {
	backend := &refusingDeleteBackend{}
	service := NewNAT64Service(backend)
	ctx := t.Context()

	_, err := service.AddPrefix(ctx, &nat64pb.AddPrefixRequest{
		Name:   "nat64-0",
		Prefix: mustIPv6Prefix(t, "64:ff9b::/96"),
	})
	require.NoError(t, err)

	resp, err := service.DeleteConfig(ctx, &nat64pb.DeleteConfigRequest{Name: "nat64-0"})
	require.Nil(t, resp)
	require.Equal(t, codes.Internal, status.Code(err))
	require.Equal(t, "nat64-0", backend.deletedName)

	list, err := service.ListConfigs(ctx, &nat64pb.ListConfigsRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"nat64-0"}, list.Configs)

	show, err := service.ShowConfig(ctx, &nat64pb.ShowConfigRequest{Name: "nat64-0"})
	require.NotNil(t, show)
	require.NoError(t, err)
}

// Test_NAT64Service_DeleteConfig_ParksThenReclaims verifies a handle whose
// release is refused at delete time is parked, then reclaimed on the next update.
func Test_NAT64Service_DeleteConfig_ParksThenReclaims(t *testing.T) {
	backend := &parkingBackend{}
	service := NewNAT64Service(backend)
	ctx := t.Context()

	_, err := service.AddPrefix(ctx, &nat64pb.AddPrefixRequest{
		Name:   "nat64-0",
		Prefix: mustIPv6Prefix(t, "64:ff9b::/96"),
	})
	require.NoError(t, err)

	resp, err := service.DeleteConfig(ctx, &nat64pb.DeleteConfigRequest{Name: "nat64-0"})
	require.NotNil(t, resp)
	require.NoError(t, err)
	require.Len(t, service.deferred, 1)
	require.Equal(t, int64(0), backend.first.freed.Load())

	// A later successful update reclaims deferred handles first.
	_, err = service.AddPrefix(ctx, &nat64pb.AddPrefixRequest{
		Name:   "nat64-1",
		Prefix: mustIPv6Prefix(t, "64:ff9b::/96"),
	})
	require.NoError(t, err)
	require.Empty(t, service.deferred)
	require.Equal(t, int64(1), backend.first.freed.Load())
}
