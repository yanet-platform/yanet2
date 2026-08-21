package route

import (
	"fmt"

	"github.com/yanet-platform/yanet2/common/go/bitset"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route/bindings/go/croute"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/hwroute"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// ModuleHandle is a handle to a route module configuration in shared
// memory.
type ModuleHandle interface {
	DumpFIB() ([]croute.FIBEntry, error)
	// ActiveNexthopCounterNames returns the deduplicated, sorted set of
	// per-nexthop counter names reachable through the resolved FIB.
	//
	// The only realistic failure is a control-plane allocation failure.
	ActiveNexthopCounterNames() ([]string, error)
	// RouteCount returns the number of distinct hardware nexthops the
	// config resolves prefixes to.
	RouteCount() uint64
	// FIBRangeCountV4 returns the number of IPv4 FIB ranges, equivalent
	// to counting the IPv4 entries of DumpFIB but far cheaper.
	FIBRangeCountV4() uint64
	// FIBRangeCountV6 returns the number of IPv6 FIB ranges, equivalent
	// to counting the IPv6 entries of DumpFIB but far cheaper.
	FIBRangeCountV6() uint64
	Free()
}

// Compile-time assertion that *croute.ModuleConfig satisfies the
// ModuleHandle interface. Catches drift in the bindings layer.
var _ ModuleHandle = (*croute.ModuleConfig)(nil)

// CounterView is a single dataplane counter read back from one position at
// which a route config is installed.
type CounterView struct {
	Device   string
	Pipeline string
	Function string
	Chain    string
	Name     string
	// Values holds the counter slots per worker instance, indexed as
	// [instance][slot].
	Values [][]uint64
}

// Backend abstracts shared memory write-path operations for the route
// module.
type Backend interface {
	// UpdateModule builds a fresh ModuleConfig from the supplied FIB
	// ranges and publishes it to the dataplane atomically.
	UpdateModule(name string, entries []*routepb.FIBEntry) (ModuleHandle, error)
	// DeleteModule removes a module config from the dataplane.
	DeleteModule(name string) error
	// ModuleCounters reads the named counters back from every position
	// at which the named config is installed.
	ModuleCounters(name string, counterNames []string) []CounterView
	// RuntimeModuleCounters reads the named runtime counters (the
	// config's per-nexthop registry) from every position at which the
	// named config is installed.
	RuntimeModuleCounters(name string, counterNames []string) []CounterView
}

// backend is the real Backend implementation backed by shared memory.
type backend struct {
	agent *ffi.Agent
}

// NewBackend creates a Backend that operates on real shared memory.
func NewBackend(agent *ffi.Agent) Backend {
	return &backend{
		agent: agent,
	}
}

func (m *backend) UpdateModule(name string, entries []*routepb.FIBEntry) (ModuleHandle, error) {
	module, err := croute.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)
	}

	// Defensively dedup hardware routes per-range using TinyBitset:
	// the operator already feeds deduplicated entries, but the wire
	// format encodes a list-of-nexthops per range and we keep the
	// route module robust to mistakes upstream.
	hardwareIndex := map[HardwareRoute]uint32{}
	routeListIndex := map[bitset.TinyBitset]uint32{}

	for _, entry := range entries {
		start, end, err := entry.GetRange().ToRange()
		if err != nil {
			module.Free()
			return nil, fmt.Errorf("failed to parse range: %w", err)
		}
		if start.Compare(end) > 0 {
			module.Free()
			return nil, fmt.Errorf("range start %q is greater than end %q", start, end)
		}

		key := bitset.TinyBitset{}
		for _, nh := range entry.GetNexthops() {
			hardwareRoute, err := newHardwareRoute(nh)
			if err != nil {
				module.Free()
				return nil, fmt.Errorf("failed to parse nexthop %v: %w", nh, err)
			}

			idx, ok := hardwareIndex[hardwareRoute]
			if !ok {
				// Read from nh, not hardwareRoute: RouteService already
				// rejects two different counter names for one identity, so
				// the first nexthop seen carries the name every later one
				// for this identity would have agreed on.
				added, err := module.AddRoute(hardwareRoute.SourceMAC[:], hardwareRoute.DestinationMAC[:], hardwareRoute.Device, nh.GetCounter())
				if err != nil {
					module.Free()
					return nil, fmt.Errorf("failed to add hardware route: %w", err)
				}
				idx = uint32(added)
				hardwareIndex[hardwareRoute] = idx
			}
			key.Insert(idx)
		}

		if key.Count() == 0 {
			continue
		}

		listIdx, ok := routeListIndex[key]
		if !ok {
			added, err := module.AddRouteList(key.AsSlice())
			if err != nil {
				module.Free()
				return nil, fmt.Errorf("failed to add route list: %w", err)
			}
			listIdx = uint32(added)
			routeListIndex[key] = listIdx
		}

		if err := module.AddRange(start, end, listIdx); err != nil {
			module.Free()
			return nil, fmt.Errorf("failed to add range [%s, %s]: %w", start, end, err)
		}
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		module.Free()
		return nil, fmt.Errorf("failed to update modules: %w", err)
	}

	return module, nil
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(moduleType, name)
}

func (m *backend) ModuleCounters(name string, counterNames []string) []CounterView {
	return m.counters(name, counterNames, func(d, p, f, c, mt, mn string, q []string) []ffi.CounterInfo {
		return m.agent.DPConfig().ModuleCounters(d, p, f, c, mt, mn, q)
	})
}

func (m *backend) RuntimeModuleCounters(name string, counterNames []string) []CounterView {
	return m.counters(name, counterNames, func(d, p, f, c, mt, mn string, q []string) []ffi.CounterInfo {
		infos, err := m.agent.DPConfig().ModuleRuntimeCounters(d, p, f, c, mt, mn, q)
		if err != nil {
			return nil
		}
		return infos
	})
}

// counters reads the selected counters from every position at which the
// named config is installed, through the supplied per-position read.
func (m *backend) counters(
	name string,
	counterNames []string,
	read func(string, string, string, string, string, string, []string) []ffi.CounterInfo,
) []CounterView {
	dpConfig := m.agent.DPConfig()

	var views []CounterView
	for pos := range dpConfig.AllModulePositions(moduleType) {
		if pos.ModuleName != name {
			continue
		}

		infos := read(
			pos.Device,
			pos.Pipeline,
			pos.Function,
			pos.Chain,
			moduleType,
			name,
			counterNames,
		)
		for _, info := range infos {
			views = append(views, CounterView{
				Device:   pos.Device,
				Pipeline: pos.Pipeline,
				Function: pos.Function,
				Chain:    pos.Chain,
				Name:     info.Name,
				Values:   info.Values,
			})
		}
	}

	return views
}

// HardwareRoute is the dataplane's Layer 2 forwarding identity.
//
// The type lives in the leaf hwroute package so that consumers needing only
// the identity, such as the route operator, do not link the cgo shared-memory
// stack behind this package; the alias keeps this package's public API intact.
type HardwareRoute = hwroute.HardwareRoute

func newHardwareRoute(nh *routepb.FIBNexthop) (HardwareRoute, error) {
	src := nh.GetSrcMac()
	if src == nil {
		return HardwareRoute{}, fmt.Errorf("src_mac is required")
	}
	dst := nh.GetDstMac()
	if dst == nil {
		return HardwareRoute{}, fmt.Errorf("dst_mac is required")
	}
	device := nh.GetDevice()
	if device == "" {
		return HardwareRoute{}, fmt.Errorf("device is required")
	}
	return HardwareRoute{
		SourceMAC:      src.EUI48(),
		DestinationMAC: dst.EUI48(),
		Device:         device,
	}, nil
}
