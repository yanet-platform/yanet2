package operator

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// LinkReconciler applies the managed netplan links.
type LinkReconciler interface {
	Apply(context.Context, netplan.State) error
}

// NeighbourDiscoverer takes a complete managed kernel-neighbour snapshot.
type NeighbourDiscoverer func(
	neighbour.Backend,
	netplan.State,
	map[string]string,
) ([]neighbour.Entry, error)

// NeighbourPublisher applies discovered neighbours to every gateway target.
type NeighbourPublisher func(
	context.Context,
	[]neighbour.Entry,
	[]neighbour.GatewayTarget,
	neighbour.PublicationConfig,
) error

type actuatorOptions struct {
	DiscoverNeighbours NeighbourDiscoverer
	PublishNeighbours  NeighbourPublisher
}

func newActuatorOptions() *actuatorOptions {
	return &actuatorOptions{
		DiscoverNeighbours: neighbour.Discover,
		PublishNeighbours:  neighbour.Publish,
	}
}

// ActuatorOption configures an Actuator's external operations.
type ActuatorOption func(*actuatorOptions)

// WithActuatorNeighbourDiscoverer replaces netlink neighbour discovery.
func WithActuatorNeighbourDiscoverer(discoverer NeighbourDiscoverer) ActuatorOption {
	return func(options *actuatorOptions) {
		options.DiscoverNeighbours = discoverer
	}
}

// WithActuatorNeighbourPublisher replaces route-operator neighbour RPCs.
func WithActuatorNeighbourPublisher(publisher NeighbourPublisher) ActuatorOption {
	return func(options *actuatorOptions) {
		options.PublishNeighbours = publisher
	}
}

// Actuator restores managed interfaces and publishes observed neighbours.
type Actuator struct {
	links       LinkReconciler
	backend     neighbour.Backend
	targets     []neighbour.GatewayTarget
	linkMap     map[string]string
	publication neighbour.PublicationConfig
	applySlot   chan struct{}

	discoverNeighbours NeighbourDiscoverer
	publishNeighbours  NeighbourPublisher

	connections []GatewayConnection
	handle      NetlinkHandle
	closeOnce   sync.Once
	closeErr    error
}

// NewActuator constructs an actuator from independently injectable operations.
func NewActuator(
	links LinkReconciler,
	backend neighbour.Backend,
	targets []neighbour.GatewayTarget,
	linkMap map[string]string,
	publication neighbour.PublicationConfig,
	options ...ActuatorOption,
) *Actuator {
	opts := newActuatorOptions()
	for _, option := range options {
		option(opts)
	}

	copiedTargets := append([]neighbour.GatewayTarget(nil), targets...)
	copiedLinkMap := make(map[string]string, len(linkMap))
	maps.Copy(copiedLinkMap, linkMap)

	return &Actuator{
		links:              links,
		backend:            backend,
		targets:            copiedTargets,
		linkMap:            copiedLinkMap,
		publication:        publication,
		applySlot:          make(chan struct{}, 1),
		discoverNeighbours: opts.DiscoverNeighbours,
		publishNeighbours:  opts.PublishNeighbours,
	}
}

// SetRuntimeResources transfers cleanup ownership to the actuator.
func (m *Actuator) SetRuntimeResources(connections []GatewayConnection, handle NetlinkHandle) {
	m.connections = connections
	m.handle = handle
}

// Apply publishes only after successful restoration and complete discovery.
func (m *Actuator) Apply(ctx context.Context, snapshot State) error {
	select {
	case m.applySlot <- struct{}{}:
		defer func() { <-m.applySlot }()
	case <-ctx.Done():
		return ctx.Err()
	}
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

	entries, err := m.discoverNeighbours(m.backend, snapshot, m.linkMap)
	if err != nil {
		return fmt.Errorf("discover neighbours: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.publishNeighbours(ctx, entries, m.targets, m.publication); err != nil {
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
