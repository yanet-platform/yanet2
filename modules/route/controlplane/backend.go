package route

import (
	"fmt"
	"net/netip"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/bitset"
	"github.com/yanet-platform/yanet2/common/go/maptrie"
	cpffi "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/modules/route/internal/ffi"
	"github.com/yanet-platform/yanet2/modules/route/internal/rib"
)

// ModuleHandle is a handle to a module configuration.
type ModuleHandle interface {
	DumpFIB() ([]ffi.FIBEntry, error)
	Free()
}

// Backend abstracts shared memory operations.
type Backend interface {
	// UpdateModule resolves RIB routes against the neighbour table and
	// publishes the result to the dataplane.
	UpdateModule(
		name string,
		ribDump maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList],
		neighbours neigh.NexthopCacheView,
	) (ModuleHandle, error)
	// DeleteModule removes a module config from the dataplane.
	DeleteModule(name string) error
}

// backend is the real Backend implementation backed by shared memory.
type backend struct {
	agent *cpffi.Agent
	log   *zap.Logger
}

// NewBackend creates a Backend that operates on real shared memory.
func NewBackend(agent *cpffi.Agent, log *zap.Logger) Backend {
	return &backend{
		agent: agent,
		log:   log,
	}
}

func (m *backend) UpdateModule(
	name string,
	ribDump maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList],
	neighbours neigh.NexthopCacheView,
) (ModuleHandle, error) {
	config, err := ffi.NewModuleConfig(m.agent, name)
	if err != nil {
		m.log.Error("failed to create module config",
			zap.Error(err),
			zap.String("name", name),
		)
		return nil, fmt.Errorf("failed to create %q module config: %w", name, err)
	}

	// Statistics for summary logging.
	var stats struct {
		totalPrefixes       int
		totalRoutes         int
		skippedPrefixes     int
		neighbourNotFound   int
		hardwareRoutesAdded int
		prefixesAdded       int
	}

	hardwareRoutes := map[neigh.HardwareRoute]uint32{}
	routesListsSet := map[bitset.TinyBitset]int{}

	routeInsertionStart := time.Now()

	for prefixLen := range ribDump {
		for prefix, routesList := range ribDump[prefixLen] {
			stats.totalPrefixes++
			routesListSetKey := bitset.TinyBitset{}

			if len(routesList.Routes) == 0 {
				stats.skippedPrefixes++
				continue
			}

			stats.totalRoutes += len(routesList.Routes)

			for _, route := range routesList.Routes {
				// Lookup hwaddress for the route.
				entry, ok := neighbours.Lookup(route.NextHop.Unmap())
				if !ok {
					m.log.Warn("neighbour not found for nexthop",
						zap.Stringer("nexthop", route.NextHop),
						zap.Stringer("prefix", prefix),
						zap.String("name", name),
					)
					stats.neighbourNotFound++
					continue
				}

				if idx, ok := hardwareRoutes[entry.HardwareRoute]; ok {
					routesListSetKey.Insert(idx)
					continue
				}

				idx, err := config.AddRoute(
					entry.HardwareRoute.SourceMAC[:],
					entry.HardwareRoute.DestinationMAC[:],
					entry.HardwareRoute.Device,
				)
				if err != nil {
					m.log.Error("failed to add hardware route",
						zap.Error(err),
						zap.Stringer("hardware_route", entry.HardwareRoute),
						zap.Stringer("prefix", prefix),
						zap.String("name", name),
					)
					config.Free()
					return nil, fmt.Errorf("failed to add hardware route %v for prefix %s: %w", entry.HardwareRoute, prefix, err)
				}
				stats.hardwareRoutesAdded++
				hardwareRoutes[entry.HardwareRoute] = uint32(idx)
				routesListSetKey.Insert(uint32(idx))
			}

			if routesListSetKey.Count() == 0 {
				continue
			}

			idx, ok := routesListsSet[routesListSetKey]
			if !ok {
				routeListIdx, err := config.AddRouteList(routesListSetKey.AsSlice())
				if err != nil {
					m.log.Error("failed to add route list",
						zap.Error(err),
						zap.Uint32s("route_indices", routesListSetKey.AsSlice()),
						zap.Stringer("prefix", prefix),
						zap.String("name", name),
					)
					config.Free()
					return nil, fmt.Errorf("failed to add routes list: %w", err)
				}
				idx = routeListIdx
				routesListsSet[routesListSetKey] = idx
			}

			if err := config.AddPrefix(prefix, uint32(idx)); err != nil {
				m.log.Error("failed to add prefix",
					zap.Error(err),
					zap.Stringer("prefix", prefix),
					zap.Int("route_list_index", idx),
					zap.String("name", name),
				)
				config.Free()
				return nil, fmt.Errorf("failed to add prefix %q: %w", prefix, err)
			}
			stats.prefixesAdded++
		}
	}

	m.log.Info("finished processing routes",
		zap.String("module", name),
		zap.Int("total_prefixes", stats.totalPrefixes),
		zap.Int("total_routes", stats.totalRoutes),
		zap.Int("skipped_prefixes", stats.skippedPrefixes),
		zap.Int("neighbour_not_found", stats.neighbourNotFound),
		zap.Int("hardware_routes_added", stats.hardwareRoutesAdded),
		zap.Int("prefixes_added", stats.prefixesAdded),
		zap.Duration("processing_duration", time.Since(routeInsertionStart)),
	)

	if err := m.agent.UpdateModules([]cpffi.ModuleConfig{config.AsFFIModule()}); err != nil {
		m.log.Error("failed to update modules via FFI",
			zap.Error(err),
			zap.String("name", name),
		)
		config.Free()
		return nil, fmt.Errorf("failed to update module: %w", err)
	}

	return config, nil
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(name)
}
