package route

import (
	"bytes"
	"cmp"
	"fmt"
	"net"
	"net/netip"

	"github.com/yanet-platform/yanet2/common/go/bitset"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route/bindings/go/croute"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
)

// ModuleHandle is a handle to a route module configuration in shared
// memory.
type ModuleHandle interface {
	DumpFIB() ([]croute.FIBEntry, error)
	Free()
}

// Compile-time assertion that *croute.ModuleConfig satisfies the
// ModuleHandle interface; catches drift in the bindings layer.
var _ ModuleHandle = (*croute.ModuleConfig)(nil)

// Backend abstracts shared memory write-path operations for the route
// module.
type Backend interface {
	// UpdateModule builds a fresh ModuleConfig from the supplied FIB
	// entries and publishes it to the dataplane atomically.
	UpdateModule(name string, entries []*routepb.FIBEntry) (ModuleHandle, error)
	// DeleteModule removes a module config from the dataplane.
	DeleteModule(name string) error
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

	// Defensively dedup hardware routes per-prefix using TinyBitset:
	// the operator already feeds deduplicated entries, but the wire
	// format encodes a list-of-nexthops per prefix and we keep the
	// route module robust to mistakes upstream.
	hardwareIndex := map[HardwareRoute]uint32{}
	routeListIndex := map[bitset.TinyBitset]uint32{}

	for _, entry := range entries {
		prefix, err := netip.ParsePrefix(entry.GetPrefix())
		if err != nil {
			module.Free()
			return nil, fmt.Errorf("failed to parse prefix %q: %w", entry.GetPrefix(), err)
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
				added, err := module.AddRoute(hardwareRoute.SourceMAC[:], hardwareRoute.DestinationMAC[:], hardwareRoute.Device)
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

		if err := module.AddPrefix(prefix, listIdx); err != nil {
			module.Free()
			return nil, fmt.Errorf("failed to add prefix %q: %w", prefix, err)
		}
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		module.Free()
		return nil, fmt.Errorf("failed to update modules: %w", err)
	}

	return module, nil
}

func (m *backend) DeleteModule(name string) error {
	return m.agent.DeleteModuleConfig(name)
}

// HardwareRoute represents a route in the Layer 2 (L2) networking stack.
type HardwareRoute struct {
	// SourceMAC is the MAC address of the local interface that observed
	// the neighbour.
	SourceMAC [6]byte
	// DestinationMAC is the MAC address of the next hop.
	DestinationMAC [6]byte
	// Device is the interface name.
	Device string
}

func (m HardwareRoute) String() string {
	return fmt.Sprintf("%s -> %s", net.HardwareAddr(m.SourceMAC[:]), net.HardwareAddr(m.DestinationMAC[:]))
}

// Compare compares two hardware routes lexicographically for deterministic sorting.
func (m HardwareRoute) Compare(other HardwareRoute) int {
	if c := bytes.Compare(m.SourceMAC[:], other.SourceMAC[:]); c != 0 {
		return c
	}
	if c := bytes.Compare(m.DestinationMAC[:], other.DestinationMAC[:]); c != 0 {
		return c
	}

	return cmp.Compare(m.Device, other.Device)
}

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
