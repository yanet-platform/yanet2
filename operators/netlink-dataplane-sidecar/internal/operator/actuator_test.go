package operator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	sidecaroperator "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/operator"
)

// linkReconcilerFunc exposes restoration success or failure without kernel I/O.
type linkReconcilerFunc func(context.Context, netplan.State) error

func (m linkReconcilerFunc) Apply(ctx context.Context, state netplan.State) error {
	return m(ctx, state)
}

// Test_Actuator_CompleteSnapshotBoundary verifies that restoration and discovery
// failures preserve published data, while a genuinely complete empty dump clears it.
func Test_Actuator_CompleteSnapshotBoundary(t *testing.T) {
	for _, failure := range []string{"none", "links", "discovery", "publication", "cancellation"} {
		t.Run(failure, func(t *testing.T) {
			operations := []string{}
			injected := errors.New("injected failure")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			actuator := sidecaroperator.NewActuator(
				linkReconcilerFunc(func(context.Context, netplan.State) error {
					operations = append(operations, "links")
					if failure == "links" {
						return injected
					}
					if failure == "cancellation" {
						cancel()
					}
					return nil
				}), nil, nil, nil, neighbour.PublicationConfig{},
				sidecaroperator.WithActuatorNeighbourDiscoverer(func(neighbour.Backend, netplan.State, map[string]string) ([]neighbour.Entry, error) {
					operations = append(operations, "discovery")
					if failure == "discovery" {
						return nil, injected
					}
					return nil, nil
				}),
				sidecaroperator.WithActuatorNeighbourPublisher(func(context.Context, []neighbour.Entry, []neighbour.GatewayTarget, neighbour.PublicationConfig) error {
					operations = append(operations, "publication")
					if failure == "publication" {
						return injected
					}
					return nil
				}),
			)
			err := actuator.Apply(ctx, sidecaroperator.State{})
			switch failure {
			case "none":
				require.NoError(t, err)
				require.Equal(t, []string{"links", "discovery", "publication"}, operations)
			case "cancellation":
				require.ErrorIs(t, err, context.Canceled)
				require.Equal(t, []string{"links"}, operations)
			case "links":
				require.ErrorIs(t, err, injected)
				require.Equal(t, []string{"links"}, operations)
			case "discovery":
				require.ErrorIs(t, err, injected)
				require.Equal(t, []string{"links", "discovery"}, operations)
			case "publication":
				require.ErrorIs(t, err, injected)
				require.Equal(t, []string{"links", "discovery", "publication"}, operations)
			}
		})
	}
}
