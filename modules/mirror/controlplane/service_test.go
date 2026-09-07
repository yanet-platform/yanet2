package mirror_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/controlplane/ffi"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/mirror/bindings/go/cmirror"
	mirror "github.com/yanet-platform/yanet2/modules/mirror/controlplane"
	mirrorpb "github.com/yanet-platform/yanet2/modules/mirror/controlplane/mirrorpb/v1"
)

type mockModuleHandle struct{}

func (m *mockModuleHandle) Free() error {
	return nil
}

type mockBackend struct{}

func (m *mockBackend) UpdateModule(
	name string,
	rules []cmirror.MirrorRule,
) (mirror.ModuleHandle, error) {
	return &mockModuleHandle{}, nil
}

func (m *mockBackend) DeleteModule(name string) error {
	return nil
}

// nilHandleBackend is a Backend whose UpdateModule publishes a config
// carrying no module handle.
//
// It models a Backend that reports success without producing a handle,
// which is the case the config's own nil guard exists to survive.
type nilHandleBackend struct {
	mockBackend
}

func (m *nilHandleBackend) UpdateModule(
	name string,
	rules []cmirror.MirrorRule,
) (mirror.ModuleHandle, error) {
	return nil, nil
}

// recordingBackend captures the rules handed to UpdateModule so a test can
// inspect what the service sends to shared memory.
type recordingBackend struct {
	mockBackend

	rules []cmirror.MirrorRule
}

func (m *recordingBackend) UpdateModule(
	name string,
	rules []cmirror.MirrorRule,
) (mirror.ModuleHandle, error) {
	m.rules = rules
	return &mockModuleHandle{}, nil
}

// blockingBackend stalls every module publish until released.
type blockingBackend struct {
	started chan struct{}
	release chan struct{}
}

func (m *blockingBackend) UpdateModule(
	name string,
	rules []cmirror.MirrorRule,
) (mirror.ModuleHandle, error) {
	close(m.started)
	<-m.release
	return &mockModuleHandle{}, nil
}

func (m *blockingBackend) DeleteModule(name string) error {
	return nil
}

// v4net builds an IPv4Network message from xnetip network text, in any
// form xnetip parsing accepts: CIDR or an explicit address/mask.
func v4net(s string) *commonpb.IPv4Network {
	return commonpb.NewIPv4NetworkFrom4(xnetip.MustParseNetwork4(s))
}

// v6net builds an IPv6Network message the same way.
func v6net(s string) *commonpb.IPv6Network {
	return commonpb.NewIPv6NetworkFrom6(xnetip.MustParseNetwork6(s))
}

// TestShowConfigUnknownConfig verifies that ShowConfig reports NotFound for
// a config name that was never applied.
func TestShowConfigUnknownConfig(t *testing.T) {
	svc := mirror.NewMirrorService(&mockBackend{})

	_, err := svc.ShowConfig(t.Context(), &mirrorpb.ShowConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// TestDeleteConfigUnknownConfig verifies that DeleteConfig reports NotFound
// for a config name that was never applied.
func TestDeleteConfigUnknownConfig(t *testing.T) {
	svc := mirror.NewMirrorService(&mockBackend{})

	_, err := svc.DeleteConfig(t.Context(), &mirrorpb.DeleteConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_MirrorService_UpdateConfigEmptyRules verifies that empty rule lists are
// reported as invalid client input with an actionable error.
func Test_MirrorService_UpdateConfigEmptyRules(t *testing.T) {
	tests := []struct {
		name  string
		rules []*mirrorpb.Rule
	}{
		{name: "nil rules"},
		{name: "empty rules", rules: []*mirrorpb.Rule{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := mirror.NewMirrorService(&mockBackend{})

			_, err := service.UpdateConfig(t.Context(), &mirrorpb.UpdateConfigRequest{
				Name:  "config",
				Rules: test.rules,
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			require.Equal(
				t,
				"mirror config must contain at least one rule",
				status.Convert(err).Message(),
			)
		})
	}
}

// TestUpdateConfigReplacesConfigWithoutHandle verifies that replacing a
// config that holds no module handle releases it without panicking.
func TestUpdateConfigReplacesConfigWithoutHandle(t *testing.T) {
	svc := mirror.NewMirrorService(&nilHandleBackend{})
	request := &mirrorpb.UpdateConfigRequest{
		Name: "config",
		Rules: []*mirrorpb.Rule{
			{Action: &mirrorpb.Action{Target: "device0"}},
		},
	}

	_, err := svc.UpdateConfig(t.Context(), request)
	require.NoError(t, err)

	_, err = svc.UpdateConfig(t.Context(), request)
	require.NoError(t, err)
}

// TestUpdateConfigRejectsOutOfClassMasks verifies that UpdateConfig
// enforces the filter compiler's mask classes on the network lists.
//
// A non-contiguous IPv4 mask and an IPv6 mask with a hole inside a
// 64-bit half are both rejected.
func TestUpdateConfigRejectsOutOfClassMasks(t *testing.T) {
	tests := []struct {
		name string
		rule *mirrorpb.Rule
	}{
		{
			name: "non-contiguous IPv4 source mask",
			rule: &mirrorpb.Rule{
				Action:   &mirrorpb.Action{Target: "device0"},
				Sources4: []*commonpb.IPv4Network{v4net("192.0.2.0/255.0.255.0")},
			},
		},
		{
			name: "IPv6 destination mask with a hole inside a half",
			rule: &mirrorpb.Rule{
				Action:        &mirrorpb.Action{Target: "device0"},
				Destinations6: []*commonpb.IPv6Network{v6net("2001:db8::/ffff:0:ffff::")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := mirror.NewMirrorService(&mockBackend{})

			_, err := svc.UpdateConfig(t.Context(), &mirrorpb.UpdateConfigRequest{
				Name:  "config",
				Rules: []*mirrorpb.Rule{tt.rule},
			})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

// TestUpdateConfigAcceptsV6MaskHoleAtHalfBoundary verifies that an IPv6
// mask with its hole exactly at the /64 boundary reaches the backend.
func TestUpdateConfigAcceptsV6MaskHoleAtHalfBoundary(t *testing.T) {
	backend := &recordingBackend{}
	svc := mirror.NewMirrorService(backend)

	const network = "2001:db8::/ffff:ffff:ffff:0:ffff::"

	_, err := svc.UpdateConfig(t.Context(), &mirrorpb.UpdateConfigRequest{
		Name: "config",
		Rules: []*mirrorpb.Rule{
			{
				Action:   &mirrorpb.Action{Target: "device0"},
				Sources6: []*commonpb.IPv6Network{v6net(network)},
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, backend.rules, 1)
	require.Equal(t, []xnetip.BiContiguous{xnetip.MustParseBiContiguous(network)}, backend.rules[0].Src6s)
}

// TestUpdateConfigPreservesWithinFamilyNetworkOrder verifies that the
// networks of every family-typed list reach the backend in request order.
func TestUpdateConfigPreservesWithinFamilyNetworkOrder(t *testing.T) {
	backend := &recordingBackend{}
	svc := mirror.NewMirrorService(backend)

	_, err := svc.UpdateConfig(t.Context(), &mirrorpb.UpdateConfigRequest{
		Name: "config",
		Rules: []*mirrorpb.Rule{
			{
				Action:        &mirrorpb.Action{Target: "device0"},
				Sources4:      []*commonpb.IPv4Network{v4net("192.0.2.0/24"), v4net("10.0.0.0/8")},
				Sources6:      []*commonpb.IPv6Network{v6net("2001:db8:1::/48"), v6net("2001:db8::/32")},
				Destinations4: []*commonpb.IPv4Network{v4net("203.0.113.0/24"), v4net("198.51.100.0/24")},
				Destinations6: []*commonpb.IPv6Network{v6net("2001:db8:2::/48"), v6net("2001:db8:3::/48")},
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, backend.rules, 1)
	rule := backend.rules[0]
	require.Equal(t, []xnetip.Contiguous[xnetip.Network4]{
		xnetip.MustParseContiguous4("192.0.2.0/24"),
		xnetip.MustParseContiguous4("10.0.0.0/8"),
	}, rule.Src4s)
	require.Equal(t, []xnetip.BiContiguous{
		xnetip.MustParseBiContiguous("2001:db8:1::/48"),
		xnetip.MustParseBiContiguous("2001:db8::/32"),
	}, rule.Src6s)
	require.Equal(t, []xnetip.Contiguous[xnetip.Network4]{
		xnetip.MustParseContiguous4("203.0.113.0/24"),
		xnetip.MustParseContiguous4("198.51.100.0/24"),
	}, rule.Dst4s)
	require.Equal(t, []xnetip.BiContiguous{
		xnetip.MustParseBiContiguous("2001:db8:2::/48"),
		xnetip.MustParseBiContiguous("2001:db8:3::/48"),
	}, rule.Dst6s)
}

// Run with: go test -race
func TestMirrorServiceConcurrentAccess(t *testing.T) {
	svc := mirror.NewMirrorService(&mockBackend{})
	ctx := context.Background()

	const goroutines = 10
	const iterations = 100

	g, ctx := errgroup.WithContext(ctx)

	for i := range goroutines {
		g.Go(func() error {
			for j := range iterations {
				name := fmt.Sprintf("config-%d-%d", i, j)
				_, err := svc.UpdateConfig(ctx, &mirrorpb.UpdateConfigRequest{
					Name: name,
					Rules: []*mirrorpb.Rule{
						{
							Action: &mirrorpb.Action{
								Target: "device0",
							},
						},
					},
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
				_, err := svc.ListConfigs(ctx, &mirrorpb.ListConfigsRequest{})
				if err != nil {
					return err
				}
			}
			return nil
		})
	}

	for i := range goroutines {
		g.Go(func() error {
			for j := range iterations {
				name := fmt.Sprintf("config-%d-%d", i, j)
				svc.ShowConfig(ctx, &mirrorpb.ShowConfigRequest{Name: name})
			}
			return nil
		})
	}

	for i := range goroutines {
		g.Go(func() error {
			for j := range iterations {
				name := fmt.Sprintf("config-%d-%d", i, j)
				svc.DeleteConfig(ctx, &mirrorpb.DeleteConfigRequest{Name: name})
			}
			return nil
		})
	}

	require.NoError(t, g.Wait())
}

// Test_MirrorService_ListDuringStalledUpdate verifies that listing configs
// completes while another goroutine's update is still stalled inside the
// backend publish.
func Test_MirrorService_ListDuringStalledUpdate(t *testing.T) {
	backend := &blockingBackend{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc := mirror.NewMirrorService(backend)

	g, ctx := errgroup.WithContext(t.Context())
	g.Go(func() error {
		_, err := svc.UpdateConfig(ctx, &mirrorpb.UpdateConfigRequest{
			Name:  "config",
			Rules: []*mirrorpb.Rule{{Action: &mirrorpb.Action{Target: "device0"}}},
		})
		return err
	})

	<-backend.started

	listed := make(chan struct{})
	go func() {
		defer close(listed)
		_, _ = svc.ListConfigs(t.Context(), &mirrorpb.ListConfigsRequest{})
	}()

	select {
	case <-listed:
	case <-time.After(5 * time.Second):
		t.Fatal("listing configs blocked behind a stalled module publish")
	}

	close(backend.release)
	require.NoError(t, g.Wait())
}

// stallingReclaimHandle refuses its first free, which parks it, and
// stalls the second until released.
type stallingReclaimHandle struct {
	numCalls atomic.Int64
	started  chan struct{}
	release  chan struct{}
}

func (m *stallingReclaimHandle) Free() error {
	if m.numCalls.Add(1) == 1 {
		return ffi.ErrStillReferenced
	}
	close(m.started)
	<-m.release
	return nil
}

// stallingReclaimBackend mints the stalling handle on its first publish
// and plain handles thereafter.
type stallingReclaimBackend struct {
	numCalls atomic.Int64
	handle   stallingReclaimHandle
}

// newStallingReclaimBackend returns a backend that mints the stalling
// reclaim handle on the first publish; the handle's started channel
// fires once its deferred destruction is inside the stall.
func newStallingReclaimBackend() *stallingReclaimBackend {
	return &stallingReclaimBackend{
		handle: stallingReclaimHandle{
			started: make(chan struct{}),
			release: make(chan struct{}),
		},
	}
}

func (m *stallingReclaimBackend) UpdateModule(name string, rules []cmirror.MirrorRule) (mirror.ModuleHandle, error) {
	if m.numCalls.Add(1) == 1 {
		return &m.handle, nil
	}
	return &mockModuleHandle{}, nil
}

func (m *stallingReclaimBackend) DeleteModule(name string) error {
	return nil
}

// Test_MirrorService_ListDuringStalledReclaim verifies that listing configs
// completes while a superseded handle's deferred destruction is stalled
// inside the backend.
func Test_MirrorService_ListDuringStalledReclaim(t *testing.T) {
	backend := newStallingReclaimBackend()
	svc := mirror.NewMirrorService(backend)

	_, err := svc.UpdateConfig(t.Context(), &mirrorpb.UpdateConfigRequest{Name: "config", Rules: []*mirrorpb.Rule{{Action: &mirrorpb.Action{Target: "device0"}}}})
	require.NoError(t, err)
	_, err = svc.UpdateConfig(t.Context(), &mirrorpb.UpdateConfigRequest{Name: "config", Rules: []*mirrorpb.Rule{{Action: &mirrorpb.Action{Target: "device0"}}}})
	require.NoError(t, err)

	g, _ := errgroup.WithContext(t.Context())
	g.Go(func() error {
		svc.ReclaimDeferred()
		return nil
	})

	<-backend.handle.started

	listed := make(chan struct{})
	go func() {
		defer close(listed)
		_, _ = svc.ListConfigs(t.Context(), &mirrorpb.ListConfigsRequest{})
	}()

	select {
	case <-listed:
	case <-time.After(5 * time.Second):
		t.Fatal("listing configs blocked behind a stalled deferred handle destruction")
	}

	close(backend.handle.release)
	require.NoError(t, g.Wait())
}
