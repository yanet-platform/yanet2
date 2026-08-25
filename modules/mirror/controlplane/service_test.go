package mirror_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/xnetip"
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

// TestShowConfigEmptyRules verifies that ShowConfig succeeds with an empty
// rule list for a config that was applied without rules.
func TestShowConfigEmptyRules(t *testing.T) {
	svc := mirror.NewMirrorService(&mockBackend{})

	_, err := svc.UpdateConfig(t.Context(), &mirrorpb.UpdateConfigRequest{Name: "empty"})
	require.NoError(t, err)

	response, err := svc.ShowConfig(t.Context(), &mirrorpb.ShowConfigRequest{Name: "empty"})
	require.NoError(t, err)
	require.Empty(t, response.GetRules())
}

// TestUpdateConfigReplacesConfigWithoutHandle verifies that replacing a
// config that holds no module handle releases it without panicking.
func TestUpdateConfigReplacesConfigWithoutHandle(t *testing.T) {
	svc := mirror.NewMirrorService(&nilHandleBackend{})

	_, err := svc.UpdateConfig(t.Context(), &mirrorpb.UpdateConfigRequest{Name: "config"})
	require.NoError(t, err)

	_, err = svc.UpdateConfig(t.Context(), &mirrorpb.UpdateConfigRequest{Name: "config"})
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
