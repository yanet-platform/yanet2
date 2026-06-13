package route_mpls

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

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/modules/route-mpls/bindings/go/croutempls"
	routemplspb "github.com/yanet-platform/yanet2/modules/route-mpls/controlplane/routemplspb/v1"
)

var errInjectedBackend = errors.New("injected backend failure")

type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() {}

type mockBackend struct{}

func (m *mockBackend) UpdateModule(name string, rules []croutempls.Rule) (ModuleHandle, error) {
	return &mockModuleHandle{}, nil
}

func (m *mockBackend) DeleteModule(name string) error {
	return nil
}

// flakyBackend succeeds on the first UpdateModule call and fails thereafter.
type flakyBackend struct {
	numCalls atomic.Int64
}

func (m *flakyBackend) UpdateModule(name string, rules []croutempls.Rule) (ModuleHandle, error) {
	if m.numCalls.Add(1) >= 2 {
		return nil, errInjectedBackend
	}
	return &mockModuleHandle{}, nil
}

func (m *flakyBackend) DeleteModule(name string) error {
	return nil
}

func newTestService(t *testing.T) *RouteMPLSService {
	t.Helper()
	return NewRouteMPLSService(&mockBackend{})
}

// makeRule builds a proto Rule for use in test requests.
func makeRule(t *testing.T, cidr string, dst string, label uint32) *routemplspb.Rule {
	t.Helper()

	p := netip.MustParsePrefix(cidr)
	addrBytes := p.Addr().AsSlice()

	return &routemplspb.Rule{
		Prefix: &filterpb.IPPrefix{
			Addr:   addrBytes,
			Length: uint32(p.Bits()),
		},
		Nexthop: &routemplspb.NextHop{
			Label:         label,
			SourceIp:      commonpb.NewIPAddressFromAddr(netip.MustParseAddr("192.0.2.1")),
			DestinationIp: commonpb.NewIPAddressFromAddr(netip.MustParseAddr(dst)),
			Weight:        1,
			Counter:       "test-counter",
		},
	}
}

func Test_RouteMPLSService_CreateConfig_HappyPath(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.CreateConfig(t.Context(), &routemplspb.CreateConfigRequest{
		Name:  "mpls0",
		Rules: []*routemplspb.Rule{makeRule(t, "10.0.0.0/24", "203.0.113.1", 100)},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)

	list, err := svc.ListConfigs(t.Context(), &routemplspb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{"mpls0"}, list.Configs)
}

func Test_RouteMPLSService_CreateConfig_EmptyName(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.CreateConfig(t.Context(), &routemplspb.CreateConfigRequest{
		Name:  "",
		Rules: []*routemplspb.Rule{makeRule(t, "10.0.0.0/24", "203.0.113.1", 100)},
	})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func Test_RouteMPLSService_UpdateConfig_UpdateAndWithdraw(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	// Create initial config with one prefix.
	_, err := svc.CreateConfig(ctx, &routemplspb.CreateConfigRequest{
		Name:  "mpls0",
		Rules: []*routemplspb.Rule{makeRule(t, "10.0.0.0/24", "203.0.113.1", 100)},
	})
	require.NoError(t, err)

	// Add a second prefix via UpdateConfig.
	_, err = svc.UpdateConfig(ctx, &routemplspb.UpdateConfigRequest{
		Name: "mpls0",
		Updates: []*routemplspb.UpdateEvent{
			{Event: &routemplspb.UpdateEvent_Update{
				Update: makeRule(t, "10.0.1.0/24", "203.0.113.2", 200),
			}},
		},
	})
	require.NoError(t, err)

	show, err := svc.ShowConfig(ctx, &routemplspb.ShowConfigRequest{Name: "mpls0"})
	require.NoError(t, err)
	assert.Len(t, show.Rules, 2)

	// Withdraw the first prefix.
	_, err = svc.UpdateConfig(ctx, &routemplspb.UpdateConfigRequest{
		Name: "mpls0",
		Updates: []*routemplspb.UpdateEvent{
			{Event: &routemplspb.UpdateEvent_Withdraw{
				Withdraw: makeRule(t, "10.0.0.0/24", "203.0.113.1", 100),
			}},
		},
	})
	require.NoError(t, err)

	show, err = svc.ShowConfig(ctx, &routemplspb.ShowConfigRequest{Name: "mpls0"})
	require.NoError(t, err)
	assert.Len(t, show.Rules, 1)
}

func Test_RouteMPLSService_ShowConfig_NotFound(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.ShowConfig(t.Context(), &routemplspb.ShowConfigRequest{Name: "nonexistent"})
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func Test_RouteMPLSService_ShowConfig_EmptyName(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.ShowConfig(t.Context(), &routemplspb.ShowConfigRequest{Name: ""})
	require.Nil(t, resp)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func Test_RouteMPLSService_ListConfigs_Sorted(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	for _, name := range []string{"mpls2", "mpls0", "mpls1"} {
		_, err := svc.CreateConfig(ctx, &routemplspb.CreateConfigRequest{
			Name:  name,
			Rules: []*routemplspb.Rule{makeRule(t, "10.0.0.0/24", "203.0.113.1", 100)},
		})
		require.NoError(t, err)
	}

	list, err := svc.ListConfigs(ctx, &routemplspb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{"mpls0", "mpls1", "mpls2"}, list.Configs)
}

func Test_RouteMPLSService_DeleteConfig_NotFound(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.DeleteConfig(t.Context(), &routemplspb.DeleteConfigRequest{Name: "nonexistent"})
	require.Nil(t, resp)
	require.Equal(t, codes.NotFound, status.Code(err))
}

func Test_RouteMPLSService_DeleteConfig_HappyPath(t *testing.T) {
	svc := newTestService(t)
	ctx := t.Context()

	_, err := svc.CreateConfig(ctx, &routemplspb.CreateConfigRequest{
		Name:  "mpls0",
		Rules: []*routemplspb.Rule{makeRule(t, "10.0.0.0/24", "203.0.113.1", 100)},
	})
	require.NoError(t, err)

	resp, err := svc.DeleteConfig(ctx, &routemplspb.DeleteConfigRequest{Name: "mpls0"})
	require.NoError(t, err)
	require.NotNil(t, resp)

	list, err := svc.ListConfigs(ctx, &routemplspb.ListConfigsRequest{})
	require.NoError(t, err)
	assert.Empty(t, list.Configs)
}

func Test_RouteMPLSService_CreateConfig_BackendFailure(t *testing.T) {
	svc := NewRouteMPLSService(&flakyBackend{})
	ctx := t.Context()

	// First call succeeds.
	_, err := svc.CreateConfig(ctx, &routemplspb.CreateConfigRequest{
		Name:  "mpls0",
		Rules: []*routemplspb.Rule{makeRule(t, "10.0.0.0/24", "203.0.113.1", 100)},
	})
	require.NoError(t, err)

	// Second call fails; old config must be preserved.
	_, err = svc.CreateConfig(ctx, &routemplspb.CreateConfigRequest{
		Name:  "mpls0",
		Rules: []*routemplspb.Rule{makeRule(t, "10.0.1.0/24", "203.0.113.2", 200)},
	})
	require.Error(t, err)
	require.Equal(t, codes.Internal, status.Code(err))

	// The original config is still accessible.
	show, err := svc.ShowConfig(ctx, &routemplspb.ShowConfigRequest{Name: "mpls0"})
	require.NoError(t, err)
	assert.Len(t, show.Rules, 1)
}

// Test_RouteMPLSService_ConcurrentAccess runs with -race to detect data races.
func Test_RouteMPLSService_ConcurrentAccess(t *testing.T) {
	svc := newTestService(t)

	const goroutines = 10
	const iterations = 100

	g, ctx := errgroup.WithContext(t.Context())

	for idx := range goroutines {
		g.Go(func() error {
			for jdx := range iterations {
				name := fmt.Sprintf("config-%d-%d", idx, jdx)
				_, err := svc.CreateConfig(ctx, &routemplspb.CreateConfigRequest{
					Name:  name,
					Rules: []*routemplspb.Rule{makeRule(t, "10.0.0.0/24", "203.0.113.1", 100)},
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
				_, err := svc.ListConfigs(ctx, &routemplspb.ListConfigsRequest{})
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
				svc.ShowConfig(ctx, &routemplspb.ShowConfigRequest{Name: name})
			}
			return nil
		})
	}

	for idx := range goroutines {
		g.Go(func() error {
			for jdx := range iterations {
				name := fmt.Sprintf("config-%d-%d", idx, jdx)
				svc.DeleteConfig(ctx, &routemplspb.DeleteConfigRequest{Name: name})
			}
			return nil
		})
	}

	require.NoError(t, g.Wait())
}
