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
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
)

// mustPrefixes4 returns family-typed IPv4 prefix messages for valid CIDR
// test inputs.
func mustPrefixes4(t *testing.T, cidrs ...string) []*commonpb.IPv4Prefix {
	t.Helper()
	networks, err := commonpb.NewIPv4PrefixesFromPrefixes(parsePrefixes(cidrs))
	require.NoError(t, err)
	return networks
}

// mustPrefixes6 is mustPrefixes4 for IPv6 prefix messages.
func mustPrefixes6(t *testing.T, cidrs ...string) []*commonpb.IPv6Prefix {
	t.Helper()
	networks, err := commonpb.NewIPv6PrefixesFromPrefixes(parsePrefixes(cidrs))
	require.NoError(t, err)
	return networks
}

// parsePrefixes parses valid CIDR test inputs.
func parsePrefixes(cidrs []string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		out = append(out, netip.MustParsePrefix(cidr))
	}
	return out
}

// prefixStrings returns canonical CIDR text for valid prefix messages of
// either family.
func prefixStrings[T interface{ ToPrefix() (netip.Prefix, error) }](t *testing.T, networks []T) []string {
	t.Helper()
	strs := make([]string, 0, len(networks))
	for _, network := range networks {
		prefix, err := network.ToPrefix()
		require.NoError(t, err)
		strs = append(strs, prefix.String())
	}
	return strs
}

var errInjectedBackend = errors.New("injected backend failure")

type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() error {
	return nil
}

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
		Name:      "decap0",
		Prefixes4: mustPrefixes4(t, prefix),
		Prefixes6: mustPrefixes6(t, "2001:db8::/32"),
	})
	require.NotNil(t, resp)
	require.NoError(t, err)

	show, err := svc.ShowConfig(t.Context(), &decappb.ShowConfigRequest{Name: "decap0"})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Len(t, show.Prefixes4, 1)
	require.Equal(t, uint32(0x0a000000), show.Prefixes4[0].GetAddr().GetAddr())
	require.Equal(t, uint32(24), show.Prefixes4[0].GetPrefixLen())
	require.Equal(t, []string{"2001:db8::/32"}, prefixStrings(t, show.Prefixes6))
}

func Test_DecapService_UpdateFullyReplaces(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.UpdateConfig(t.Context(), &decappb.UpdateConfigRequest{
		Name:      "decap0",
		Prefixes4: mustPrefixes4(t, "10.0.0.0/24", "10.0.1.0/24"),
		Prefixes6: mustPrefixes6(t, "2001:db8::/32"),
	})
	require.NoError(t, err)

	_, err = svc.UpdateConfig(t.Context(), &decappb.UpdateConfigRequest{
		Name:      "decap0",
		Prefixes4: mustPrefixes4(t, "192.168.0.0/16"),
	})
	require.NoError(t, err)

	show, err := svc.ShowConfig(t.Context(), &decappb.ShowConfigRequest{Name: "decap0"})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]string{"192.168.0.0/16"}, prefixStrings(t, show.Prefixes4)))
	require.Empty(t, show.Prefixes6)
}

func Test_DecapService_ListUpdateList(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	list, err := svc.ListConfigs(ctx, &decappb.ListConfigsRequest{})
	require.NotNil(t, list)
	require.NoError(t, err)
	assert.Empty(t, list.Configs)

	_, err = svc.UpdateConfig(ctx, &decappb.UpdateConfigRequest{
		Name:      "decap0",
		Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
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
			Name:      "",
			Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
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
	tests := []struct {
		name    string
		request *decappb.UpdateConfigRequest
	}{
		{
			name: "missing IPv4 address",
			request: &decappb.UpdateConfigRequest{
				Name:      "decap0",
				Prefixes4: []*commonpb.IPv4Prefix{{PrefixLen: 24}},
			},
		},
		{
			name: "IPv4 prefix length exceeds address bit length",
			request: &decappb.UpdateConfigRequest{
				Name:      "decap0",
				Prefixes4: []*commonpb.IPv4Prefix{{Addr: &commonpb.IPv4Address{Addr: 0x0a000000}, PrefixLen: 33}},
			},
		},
		{
			name: "IPv6 prefix length exceeds address bit length",
			request: &decappb.UpdateConfigRequest{
				Name:      "decap0",
				Prefixes6: []*commonpb.IPv6Prefix{{Addr: &commonpb.IPv6Address{}, PrefixLen: 129}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestService(t)

			resp, err := svc.UpdateConfig(t.Context(), tt.request)
			require.Nil(t, resp)
			require.Equal(t, codes.InvalidArgument, status.Code(err))

			list, err := svc.ListConfigs(t.Context(), &decappb.ListConfigsRequest{})
			require.NoError(t, err)
			assert.Empty(t, list.Configs)
		})
	}
}

func Test_DecapService_UpdateFailureAtomic(t *testing.T) {
	svc := NewDecapService(&flakyBackend{})
	ctx := t.Context()
	name := "decap0"

	_, err := svc.UpdateConfig(ctx, &decappb.UpdateConfigRequest{
		Name:      name,
		Prefixes4: mustPrefixes4(t, "10.0.0.0/24"),
	})
	require.NoError(t, err)

	_, err = svc.UpdateConfig(ctx, &decappb.UpdateConfigRequest{
		Name:      name,
		Prefixes4: mustPrefixes4(t, "10.0.1.0/24"),
	})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))

	show, err := svc.ShowConfig(ctx, &decappb.ShowConfigRequest{Name: name})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]string{"10.0.0.0/24"}, prefixStrings(t, show.Prefixes4)))
}

func Test_DecapService_DeduplicatePrefixes(t *testing.T) {
	svc := newTestService(t)
	prefix := "10.0.0.0/24"

	_, err := svc.UpdateConfig(t.Context(), &decappb.UpdateConfigRequest{
		Name:      "decap0",
		Prefixes4: mustPrefixes4(t, prefix, prefix, prefix),
	})
	require.NoError(t, err)

	show, err := svc.ShowConfig(t.Context(), &decappb.ShowConfigRequest{Name: "decap0"})
	require.NotNil(t, show)
	require.NoError(t, err)
	require.Empty(t, cmp.Diff([]string{prefix}, prefixStrings(t, show.Prefixes4)))
}

func Test_DecapService_ConcurrentAccess(t *testing.T) {
	svc := newTestService(t)

	const goroutines = 10
	const iterations = 100

	networks := mustPrefixes4(t, "10.0.0.0/24")

	g, ctx := errgroup.WithContext(t.Context())

	for idx := range goroutines {
		g.Go(func() error {
			for jdx := range iterations {
				name := fmt.Sprintf("config-%d-%d", idx, jdx)
				_, err := svc.UpdateConfig(ctx, &decappb.UpdateConfigRequest{
					Name:      name,
					Prefixes4: networks,
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
