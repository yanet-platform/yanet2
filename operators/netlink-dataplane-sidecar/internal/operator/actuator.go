package operator

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// LinkReconciler applies the managed netplan links.
type LinkReconciler interface {
	Apply(context.Context, netplan.State) error
}

// Actuator restores managed interfaces and publishes observed neighbours.
type Actuator struct {
	links       LinkReconciler
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
// The common reconcile loop serializes applies and closes resources after its
// workers stop. Targets are alternative transports to one route operator.
func NewActuator(
	links LinkReconciler,
	handle NetlinkHandle,
	targets []neighbour.GatewayTarget,
	linkMap map[string]string,
	publication neighbour.PublicationConfig,
	connections []GatewayConnection,
) *Actuator {
	return &Actuator{
		links:       links,
		handle:      handle,
		targets:     slices.Clone(targets),
		linkMap:     maps.Clone(linkMap),
		publication: publication,
		connections: slices.Clone(connections),
	}
}

// Apply publishes only after successful restoration and complete discovery.
func (m *Actuator) Apply(ctx context.Context, snapshot State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := neighbour.ValidateManagedDevices(snapshot, m.linkMap); err != nil {
		return fmt.Errorf("validate managed neighbour devices: %w", err)
	}
	if err := m.links.Apply(ctx, snapshot); err != nil {
		return fmt.Errorf("reconcile links: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	entries, err := neighbour.Discover(ctx, m.handle, snapshot, m.linkMap)
	if err != nil {
		return fmt.Errorf("discover neighbours: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
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
