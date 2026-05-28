package nat64

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb"
)

var errInjectedBackend = errors.New("injected backend failure")

type mockHandle struct {
	freed bool
}

func (m *mockHandle) Free() {
	m.freed = true
}

type mockBackend struct {
	configs []NAT64Config
	handles []*mockHandle
	failAt  int
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

// Test_NAT64Service_AddShowRemove verifies basic config lifecycle operations.
func Test_NAT64Service_AddShowRemove(t *testing.T) {
	backend := &mockBackend{}
	service := NewNAT64Service(backend)
	ctx := t.Context()

	prefix0 := []byte{0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	prefix1 := []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0}
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
		Ipv4:        commonpb.NewIPAddressFromAddr(ipv4),
		Ipv6:        commonpb.NewIPAddressFromAddr(ipv6),
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
	require.Equal(t, prefix1, show.GetConfig().GetPrefixes()[0].GetPrefix())
	require.Len(t, show.GetConfig().GetMappings(), 1)
	require.Equal(t, uint32(0), show.GetConfig().GetMappings()[0].GetPrefixIndex())

	_, err = service.RemoveMapping(ctx, &nat64pb.RemoveMappingRequest{
		Name: "nat64-0",
		Ipv4: commonpb.NewIPAddressFromAddr(ipv4),
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
		Prefix: []byte{0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	})
	require.NoError(t, err)
	require.Len(t, backend.configs, 1)
	require.Equal(t, MTUConfig{IPv4MTU: 1450, IPv6MTU: 1280}, backend.configs[0].MTU)
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

	prefix0 := []byte{0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	prefix1 := []byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0}

	_, err := service.AddPrefix(ctx, &nat64pb.AddPrefixRequest{Name: "nat64-0", Prefix: prefix0})
	require.NoError(t, err)
	_, err = service.AddPrefix(ctx, &nat64pb.AddPrefixRequest{Name: "nat64-0", Prefix: prefix1})
	require.Equal(t, codes.Internal, status.Code(err))

	show, err := service.ShowConfig(ctx, &nat64pb.ShowConfigRequest{Name: "nat64-0"})
	require.NoError(t, err)
	require.Len(t, show.GetConfig().GetPrefixes(), 1)
	require.Equal(t, prefix0, show.GetConfig().GetPrefixes()[0].GetPrefix())
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
