package route

import (
	"errors"
	"fmt"

	"github.com/yanet-platform/yanet2/common/go/bitset"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route/bindings/go/croute"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/hwroute"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// ModuleHandle is a handle to a route module configuration in shared
// memory, owned by the control plane until it is freed.
type ModuleHandle interface {
	// Publish upserts the module config into a new configuration
	// generation. The table object it links must already be published.
	Publish() error
	Free() error
}

// FIBHandle is a handle to a forwarding table object in shared memory,
// owned by the control plane until it is freed.
type FIBHandle interface {
	// Publish upserts the object into a new configuration generation.
	Publish() error
	// ActiveNexthopCounterNames returns the deduplicated, sorted set of
	// per-nexthop counter names reachable through the resolved FIB.
	//
	// The only realistic failure is a control-plane allocation failure.
	ActiveNexthopCounterNames() ([]string, error)
	// RouteCount returns the number of distinct hardware nexthops the
	// table resolves prefixes to.
	RouteCount() uint64
	// FIBRangeCountV4 returns the number of IPv4 FIB ranges, equivalent
	// to counting the IPv4 entries of a dump but far cheaper.
	FIBRangeCountV4() uint64
	// FIBRangeCountV6 returns the number of IPv6 FIB ranges, equivalent
	// to counting the IPv6 entries of a dump but far cheaper.
	FIBRangeCountV6() uint64
	Free() error
}

// Compile-time assertions that the bindings satisfy the handle
// interfaces. Catch drift in the bindings layer.
var (
	_ ModuleHandle = (*croute.ModuleConfig)(nil)
	_ FIBHandle    = (*croute.FIBObject)(nil)
)

// Published describes what the dataplane holds for a config name: the
// module's device table by index, whether its table object is out and
// the sizes of that table.
type Published struct {
	Devices         []string
	FIB             bool
	FIBRangeCountV4 uint64
	FIBRangeCountV6 uint64
	NexthopCount    uint64
}

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
//
// A config is a module config and a table object published under the
// same name. The object names egress devices by their index in the
// module's device table, so the table only ever grows and a module is
// rebuilt only when a table needs a device it lacks.
type Backend interface {
	// Published reads what is published under the name. The error wraps
	// ffi.ErrNotFound when no module config is published under it.
	Published(name string) (Published, error)
	// NewModule builds a module config linking the devices, without
	// publishing it. It returns the module's device table by index,
	// which may hold entries the module links on its own.
	NewModule(name string, devices []string) (ModuleHandle, []string, error)
	// NewFIB builds a table object from the supplied FIB ranges, naming
	// devices by their index in the table, without publishing it.
	NewFIB(name string, devices []string, entries []*routepb.FIBEntry) (FIBHandle, error)
	// DeleteModule removes a module config from the dataplane.
	DeleteModule(name string) error
	// DeleteFIB removes a table object from the dataplane.
	DeleteFIB(name string) error
	// DumpFIB reads the published table of the named config. The error
	// wraps ffi.ErrNotFound when the config is not published.
	DumpFIB(name string) ([]croute.FIBEntry, error)
	// ModuleCounters reads the named counters back from every position
	// at which the named config is installed.
	ModuleCounters(name string, counterNames []string) []CounterView
	// NexthopCounters reads the named per-nexthop counters, which the
	// module counts on its link to the table object, from every position
	// at which the named config is installed.
	NexthopCounters(name string, counterNames []string) []CounterView
}

// ErrTooManyNexthops reports a FIB that resolves to more distinct hardware
// nexthops than one route config can index.
var ErrTooManyNexthops = errors.New("too many distinct nexthops")

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

func (m *backend) Published(name string) (Published, error) {
	snapshot, err := croute.OpenSnapshot(m.agent, name)
	if err != nil {
		return Published{}, err
	}
	defer snapshot.Close()
	return Published{
		Devices:         snapshot.Devices(),
		FIB:             snapshot.HasFIB(),
		FIBRangeCountV4: snapshot.FIBRangeCountV4(),
		FIBRangeCountV6: snapshot.FIBRangeCountV6(),
		NexthopCount:    snapshot.RouteCount(),
	}, nil
}

func (m *backend) DumpFIB(name string) ([]croute.FIBEntry, error) {
	snapshot, err := croute.OpenSnapshot(m.agent, name)
	if err != nil {
		return nil, err
	}
	defer snapshot.Close()
	return snapshot.Entries()
}

func (m *backend) NewModule(name string, devices []string) (ModuleHandle, []string, error) {
	module, err := croute.NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create module config: %w", err)
	}

	table, err := linkDevices(module, devices)
	if err != nil {
		if freeErr := module.Free(); freeErr != nil {
			return nil, nil, fmt.Errorf("failed to free abandoned config: %w", freeErr)
		}
		return nil, nil, err
	}
	return module, table, nil
}

// linkDevices links the devices into the module and returns its device
// table by index, as the module hands the indexes out.
//
// An empty name is the module's own any-device entry, present in a table
// read back from the dataplane, and is skipped.
func linkDevices(module *croute.ModuleConfig, devices []string) ([]string, error) {
	var table []string
	for _, device := range devices {
		if device == "" {
			continue
		}
		idx, err := module.LinkDevice(device)
		if err != nil {
			return nil, err
		}
		if int(idx) >= len(table) {
			table = append(table, make([]string, int(idx)+1-len(table))...)
		}
		table[idx] = device
	}
	return table, nil
}

func (m *backend) NewFIB(name string, devices []string, entries []*routepb.FIBEntry) (FIBHandle, error) {
	fib, err := croute.NewFIBObject(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create fib object: %w", err)
	}

	if err := buildFIB(fib, devices, entries); err != nil {
		if freeErr := fib.Free(); freeErr != nil {
			return nil, fmt.Errorf("failed to free abandoned fib object: %w", freeErr)
		}
		return nil, err
	}
	return fib, nil
}

// buildFIB fills the table object from the FIB ranges, naming devices by
// their index in the table.
func buildFIB(fib *croute.FIBObject, devices []string, entries []*routepb.FIBEntry) error {
	deviceIndex := make(map[string]uint32, len(devices))
	for idx, device := range devices {
		deviceIndex[device] = uint32(idx)
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
			return fmt.Errorf("failed to parse range: %w", err)
		}
		if start.Compare(end) > 0 {
			return fmt.Errorf("range start %q is greater than end %q", start, end)
		}

		key := bitset.TinyBitset{}
		for _, nh := range entry.GetNexthops() {
			hardwareRoute, err := newHardwareRoute(nh)
			if err != nil {
				return fmt.Errorf("failed to parse nexthop %v: %w", nh, err)
			}

			idx, ok := hardwareIndex[hardwareRoute]
			if !ok {
				if len(hardwareIndex) >= bitset.MaxBits {
					return fmt.Errorf("%w: a route config indexes at most %d", ErrTooManyNexthops, bitset.MaxBits)
				}
				device, ok := deviceIndex[hardwareRoute.Device]
				if !ok {
					return fmt.Errorf("device %q is not in the module's device table", hardwareRoute.Device)
				}
				// Read from nh, not hardwareRoute: RouteService already
				// rejects two different counter names for one identity, so
				// the first nexthop seen carries the name every later one
				// for this identity would have agreed on.
				added, err := fib.AddRoute(hardwareRoute.SourceMAC[:], hardwareRoute.DestinationMAC[:], device, nh.GetCounter())
				if err != nil {
					return fmt.Errorf("failed to add hardware route: %w", err)
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
			added, err := fib.AddRouteList(key.AsSlice())
			if err != nil {
				return fmt.Errorf("failed to add route list: %w", err)
			}
			listIdx = uint32(added)
			routeListIndex[key] = listIdx
		}

		if err := fib.AddRange(start, end, listIdx); err != nil {
			return fmt.Errorf("failed to add range [%s, %s]: %w", start, end, err)
		}
	}
	return nil
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(moduleType, name)
}

func (m *backend) DeleteFIB(name string) error {
	return m.agent.DeleteObject(fibObjectType, name)
}

func (m *backend) ModuleCounters(name string, counterNames []string) []CounterView {
	return m.counters(name, counterNames, func(d, p, f, c, mt, mn string, q []string) []ffi.CounterInfo {
		return m.agent.DPConfig().ModuleCounters(d, p, f, c, mt, mn, q)
	})
}

func (m *backend) NexthopCounters(name string, counterNames []string) []CounterView {
	return m.counters(name, counterNames, func(d, p, f, c, mt, mn string, q []string) []ffi.CounterInfo {
		infos, err := m.agent.DPConfig().ModuleObjectLinkCounters(d, p, f, c, mt, mn, fibObjectType, name, q)
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
