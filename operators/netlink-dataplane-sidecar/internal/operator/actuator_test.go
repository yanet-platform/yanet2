package operator_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
)

type linkReconcilerFunc func(context.Context, netplan.State) error

func (m linkReconcilerFunc) Apply(ctx context.Context, state netplan.State) error {
	return m(ctx, state)
}

type routeReconcilerFunc func(context.Context, []route.Route, netplan.State) error

func (m routeReconcilerFunc) Apply(
	ctx context.Context,
	routes []route.Route,
	state netplan.State,
) error {
	return m(ctx, routes, state)
}

type actuatorFunc func(context.Context, sidecaroperator.State) error

func (m actuatorFunc) Apply(ctx context.Context, state sidecaroperator.State) error {
	return m(ctx, state)
}

func (actuatorFunc) Close() error {
	return nil
}

// Test_Actuator_OrdersOperations verifies that links precede route installation
// and neighbour refresh, while an uninitialized snapshot skips route changes.
func Test_Actuator_OrdersOperations(t *testing.T) {
	operations := []string{}
	netplanState := netplan.State{Links: []netplan.Link{{Name: "kni0"}}}
	links := linkReconcilerFunc(func(context.Context, netplan.State) error {
		operations = append(operations, "links")
		return nil
	})
	routes := routeReconcilerFunc(func(
		context.Context,
		[]route.Route,
		netplan.State,
	) error {
		operations = append(operations, "routes")
		return nil
	})
	actuator := sidecaroperator.NewActuator(
		"/test/netplan.yaml",
		links,
		routes,
		nil,
		nil,
		map[string]string{"kni0": "logical0"},
		sidecaroperator.WithActuatorNetplanLoader(func(path string) (netplan.State, error) {
			require.Equal(t, "/test/netplan.yaml", path)
			operations = append(operations, "load")
			return netplanState, nil
		}),
		sidecaroperator.WithActuatorNeighbourDiscoverer(func(
			neighbour.Backend,
			netplan.State,
			map[string]string,
		) ([]neighbour.Entry, error) {
			operations = append(operations, "discover")
			return nil, nil
		}),
		sidecaroperator.WithActuatorNeighbourPublisher(func(
			context.Context,
			[]neighbour.Entry,
			[]neighbour.GatewayTarget,
		) error {
			operations = append(operations, "publish")
			return nil
		}),
	)

	require.NoError(t, actuator.Apply(t.Context(), sidecaroperator.State{}))
	require.Equal(t, []string{"load", "links", "discover", "publish"}, operations)

	operations = nil
	require.NoError(t, actuator.Apply(t.Context(), sidecaroperator.State{
		Initialized: true,
	}))
	require.Equal(
		t,
		[]string{"load", "links", "routes", "discover", "publish"},
		operations,
	)
}

// Test_Actuator_JoinsRouteAndPublishErrors verifies that both failures reach
// the caller and pending update, while the store reverts to uninitialized state.
func Test_Actuator_JoinsRouteAndPublishErrors(t *testing.T) {
	routeErr := errors.New("route failure")
	publishErr := errors.New("publish failure")
	store := route.NewStore()
	update, err := store.ReplaceTracked(nil)
	require.NoError(t, err)
	snapshot, ok := sidecaroperator.NewSource(store).Snapshot()
	require.True(t, ok)
	actuator := sidecaroperator.NewActuator(
		"/test/netplan.yaml",
		linkReconcilerFunc(func(context.Context, netplan.State) error { return nil }),
		routeReconcilerFunc(func(
			context.Context,
			[]route.Route,
			netplan.State,
		) error {
			return routeErr
		}),
		nil,
		nil,
		nil,
		sidecaroperator.WithActuatorNetplanLoader(func(string) (netplan.State, error) {
			return netplan.State{}, nil
		}),
		sidecaroperator.WithActuatorNeighbourDiscoverer(func(
			neighbour.Backend,
			netplan.State,
			map[string]string,
		) ([]neighbour.Entry, error) {
			return nil, nil
		}),
		sidecaroperator.WithActuatorNeighbourPublisher(func(
			context.Context,
			[]neighbour.Entry,
			[]neighbour.GatewayTarget,
		) error {
			return publishErr
		}),
	)

	err = actuator.Apply(t.Context(), snapshot)

	require.ErrorIs(t, err, routeErr)
	require.ErrorIs(t, err, publishErr)
	require.ErrorIs(t, update.Wait(t.Context()), routeErr)
	require.ErrorIs(t, update.Wait(t.Context()), publishErr)
	reverted, ok := sidecaroperator.NewSource(store).Snapshot()
	require.True(t, ok)
	require.False(t, reverted.Initialized)
	require.Empty(t, reverted.Routes)
}

// Test_Actuator_NeighbourFailurePreservesCommittedRoutes verifies that a
// neighbour refresh failure cannot reject or forget installed kernel routes.
func Test_Actuator_NeighbourFailurePreservesCommittedRoutes(t *testing.T) {
	for _, failure := range []string{"discovery", "publication"} {
		t.Run(failure, func(t *testing.T) {
			refreshErr := errors.New("neighbour refresh failed")
			store := route.NewStore()
			desired := []route.Route{{
				Prefix:    netip.MustParsePrefix("192.0.2.0/24"),
				Nexthop:   netip.MustParseAddr("192.0.2.1"),
				Interface: "kni0",
			}}
			update, err := store.ReplaceTracked(desired)
			require.NoError(t, err)
			snapshot, ok := sidecaroperator.NewSource(store).Snapshot()
			require.True(t, ok)
			attempts := 0
			actuator := sidecaroperator.NewActuator(
				"/test/netplan.yaml",
				linkReconcilerFunc(func(context.Context, netplan.State) error { return nil }),
				routeReconcilerFunc(func(
					ctx context.Context,
					routes []route.Route,
					state netplan.State,
				) error {
					require.Equal(t, desired, routes)
					attempts++
					return nil
				}),
				nil,
				nil,
				nil,
				sidecaroperator.WithActuatorNetplanLoader(func(string) (netplan.State, error) {
					return netplan.State{}, nil
				}),
				sidecaroperator.WithActuatorNeighbourDiscoverer(func(
					neighbour.Backend,
					netplan.State,
					map[string]string,
				) ([]neighbour.Entry, error) {
					if failure == "discovery" {
						return nil, refreshErr
					}
					return nil, nil
				}),
				sidecaroperator.WithActuatorNeighbourPublisher(func(
					ctx context.Context,
					entries []neighbour.Entry,
					targets []neighbour.GatewayTarget,
				) error {
					if attempts == 1 {
						// The successful gateway is refreshed before acknowledgement.
						waitCtx, cancel := context.WithCancel(ctx)
						cancel()
						require.ErrorIs(t, update.Wait(waitCtx), context.Canceled)
					}
					return refreshErr
				}),
			)

			require.ErrorIs(t, actuator.Apply(t.Context(), snapshot), refreshErr)
			require.NoError(t, update.Wait(t.Context()))
			next, ok := sidecaroperator.NewSource(store).Snapshot()
			require.True(t, ok)
			require.True(t, next.Initialized)
			require.Equal(t, desired, next.Routes)
			require.ErrorIs(t, actuator.Apply(t.Context(), next), refreshErr)
			require.Equal(t, 2, attempts)
		})
	}
}

// Test_Actuator_ValidatesLogicalDevices verifies that colliding logical device
// mappings fail before the link reconciler can mutate kernel state.
func Test_Actuator_ValidatesLogicalDevices(t *testing.T) {
	linksCalled := false
	actuator := sidecaroperator.NewActuator(
		"/test/netplan.yaml",
		linkReconcilerFunc(func(context.Context, netplan.State) error {
			linksCalled = true
			return nil
		}),
		routeReconcilerFunc(func(context.Context, []route.Route, netplan.State) error {
			return nil
		}),
		nil,
		[]neighbour.GatewayTarget{{
			TableName: "netlink-dataplane-test",
		}},
		map[string]string{"kni0": "kni1"},
		sidecaroperator.WithActuatorNetplanLoader(func(string) (netplan.State, error) {
			return netplan.State{Links: []netplan.Link{{Name: "kni0"}, {Name: "kni1"}}}, nil
		}),
	)

	err := actuator.Apply(t.Context(), sidecaroperator.State{})
	require.ErrorContains(t, err, `duplicate logical device "kni1"`)
	require.False(t, linksCalled)
}

// Test_Actuator_RetriesCommittedSnapshot verifies that a rejected route update
// wakes reconciliation with the last committed routes instead of retrying it.
func Test_Actuator_RetriesCommittedSnapshot(t *testing.T) {
	previousRoute := route.Route{
		Prefix:    netip.MustParsePrefix("192.0.2.0/24"),
		Nexthop:   netip.MustParseAddr("192.0.2.1"),
		Interface: "kni0",
	}
	rejectedRoute := route.Route{
		Prefix:    netip.MustParsePrefix("198.51.100.0/24"),
		Nexthop:   netip.MustParseAddr("198.51.100.1"),
		Interface: "kni0",
	}
	store := route.NewStore()
	previousUpdate, err := store.ReplaceTracked([]route.Route{previousRoute})
	require.NoError(t, err)
	previousUpdate.Complete(nil)
	<-store.Wake()
	rejectedUpdate, err := store.ReplaceTracked([]route.Route{rejectedRoute})
	require.NoError(t, err)
	<-store.Wake()

	rejection := errors.New("kernel rejected route snapshot")
	attempts := make(chan sidecaroperator.State, 2)
	loop := commonoperator.NewReconciler[sidecaroperator.State](
		actuatorFunc(func(_ context.Context, state sidecaroperator.State) error {
			attempts <- state
			if len(state.Routes) == 1 && state.Routes[0] == rejectedRoute {
				state.RouteUpdate.Complete(rejection)
				return rejection
			}
			return nil
		}),
		sidecaroperator.NewSource(store),
		commonoperator.WithReconcileBackoff(time.Hour, time.Hour),
		commonoperator.WithReconcileInterval(time.Hour),
	)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	result := make(chan error, 1)
	go func() {
		result <- loop.Run(ctx)
	}()

	nextAttempt := func() sidecaroperator.State {
		select {
		case state := <-attempts:
			return state
		case <-ctx.Done():
			t.Fatal("timed out waiting for reconcile attempt")
			return sidecaroperator.State{}
		}
	}
	first := nextAttempt()
	require.Equal(t, []route.Route{rejectedRoute}, first.Routes)
	second := nextAttempt()
	require.Equal(t, []route.Route{previousRoute}, second.Routes)
	require.ErrorIs(t, rejectedUpdate.Wait(t.Context()), rejection)
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
}
