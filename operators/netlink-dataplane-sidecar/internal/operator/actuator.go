package operator

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
)

// Actuator publishes observed neighbours independently of interface setup.
type Actuator struct {
	targets     []neighbour.GatewayTarget
	linkMap     map[string]string
	publication neighbour.PublicationConfig

	connections []GatewayConnection
	handle      NetlinkHandle
	closeOnce   sync.Once
	closeErr    error
}

// NewActuator takes ownership of the kernel handle and gateway connections.
//
// The common reconcile loop serializes applies. The caller must stop all
// workers before closing resources. Targets reach the same route operator.
func NewActuator(
	handle NetlinkHandle,
	targets []neighbour.GatewayTarget,
	linkMap map[string]string,
	publication neighbour.PublicationConfig,
	connections []GatewayConnection,
) *Actuator {
	return &Actuator{
		handle:      handle,
		targets:     slices.Clone(targets),
		linkMap:     maps.Clone(linkMap),
		publication: publication,
		connections: slices.Clone(connections),
	}
}

// Apply publishes only complete observations of the existing managed egress.
func (m *Actuator) Apply(ctx context.Context, snapshot State) error {
	entries, err := neighbour.Discover(ctx, m.handle, snapshot, m.linkMap)
	if err != nil {
		return fmt.Errorf("discover neighbours: %w", err)
	}
	if err := neighbour.Publish(ctx, entries, m.targets, m.publication); err != nil {
		return fmt.Errorf("publish neighbours: %w", err)
	}
	return nil
}

// Close releases every gateway connection and the shared netlink handle.
func (m *Actuator) Close() error {
	m.closeOnce.Do(func() {
		m.closeErr = closeRuntimeResources(m.connections, m.handle)
	})
	return m.closeErr
}
