package operator

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
)

// LinkReconciler applies the managed netplan links.
type LinkReconciler interface {
	Apply(context.Context, netplan.State) error
}

// RouteReconciler applies a complete route snapshot to managed links.
type RouteReconciler interface {
	Apply(context.Context, []route.Route, netplan.State) error
}

// NetplanLoader reads the current netplan document for a reconcile pass.
type NetplanLoader func(string) (netplan.State, error)

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
) error

type actuatorOptions struct {
	LoadNetplan        NetplanLoader
	DiscoverNeighbours NeighbourDiscoverer
	PublishNeighbours  NeighbourPublisher
}

func newActuatorOptions() *actuatorOptions {
	return &actuatorOptions{
		LoadNetplan:        netplan.ParseFile,
		DiscoverNeighbours: neighbour.Discover,
		PublishNeighbours:  neighbour.Publish,
	}
}

// ActuatorOption configures an Actuator's external operations.
type ActuatorOption func(*actuatorOptions)

// WithActuatorNetplanLoader replaces filesystem-backed netplan loading.
func WithActuatorNetplanLoader(loader NetplanLoader) ActuatorOption {
	return func(options *actuatorOptions) {
		options.LoadNetplan = loader
	}
}

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

// Actuator reconciles host links, routes, and discovered neighbours.
type Actuator struct {
	netplanPath string
	links       LinkReconciler
	routes      RouteReconciler
	backend     neighbour.Backend
	targets     []neighbour.GatewayTarget
	linkMap     map[string]string

	loadNetplan        NetplanLoader
	discoverNeighbours NeighbourDiscoverer
	publishNeighbours  NeighbourPublisher

	connections []GatewayConnection
	handle      NetlinkHandle
	closeOnce   sync.Once
	closeErr    error
}

// NewActuator constructs an actuator from independently injectable operations.
func NewActuator(
	netplanPath string,
	links LinkReconciler,
	routes RouteReconciler,
	backend neighbour.Backend,
	targets []neighbour.GatewayTarget,
	linkMap map[string]string,
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
		netplanPath:        netplanPath,
		links:              links,
		routes:             routes,
		backend:            backend,
		targets:            copiedTargets,
		linkMap:            copiedLinkMap,
		loadNetplan:        opts.LoadNetplan,
		discoverNeighbours: opts.DiscoverNeighbours,
		publishNeighbours:  opts.PublishNeighbours,
	}
}

// SetRuntimeResources transfers cleanup ownership to the actuator.
func (m *Actuator) SetRuntimeResources(connections []GatewayConnection, handle NetlinkHandle) {
	m.connections = connections
	m.handle = handle
}

// Apply reads the latest netplan and reconciles links before dependent state.
func (m *Actuator) Apply(ctx context.Context, snapshot State) (applyErr error) {
	routesApplied := false
	if snapshot.Initialized {
		defer func() {
			if routesApplied {
				snapshot.RouteUpdate.Complete(nil)
			} else {
				snapshot.RouteUpdate.Complete(applyErr)
			}
		}()
	}

	netplanState, err := m.loadNetplan(m.netplanPath)
	if err != nil {
		return fmt.Errorf("load netplan state: %w", err)
	}
	if len(m.targets) != 0 {
		if err := neighbour.ValidateManagedDeviceOwnership(netplanState, m.linkMap, m.targets); err != nil {
			return fmt.Errorf("validate managed neighbour devices: %w", err)
		}
	}
	if err := m.links.Apply(ctx, netplanState); err != nil {
		return fmt.Errorf("reconcile links: %w", err)
	}

	if snapshot.Initialized {
		if err := m.routes.Apply(ctx, snapshot.Routes, netplanState); err != nil {
			applyErr = errors.Join(applyErr, fmt.Errorf("reconcile routes: %w", err))
		} else {
			routesApplied = true
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(applyErr, err)
	}

	entries, err := m.discoverNeighbours(m.backend, netplanState, m.linkMap)
	if err != nil {
		return errors.Join(applyErr, fmt.Errorf("discover neighbours: %w", err))
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(applyErr, err)
	}
	if err := m.publishNeighbours(ctx, entries, m.targets); err != nil {
		applyErr = errors.Join(applyErr, fmt.Errorf("publish neighbours: %w", err))
	}
	return applyErr
}

// Close releases every gateway connection and the shared netlink handle.
func (m *Actuator) Close() error {
	m.closeOnce.Do(func() {
		m.closeErr = closeRuntimeResources(m.connections, m.handle)
	})
	return m.closeErr
}
