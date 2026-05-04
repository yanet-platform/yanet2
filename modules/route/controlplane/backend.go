package route

import (
	"fmt"
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
	hardwareIndex := map[hardwareKey]uint32{}
	routeListIndex := map[bitset.TinyBitset]uint32{}

	for _, entry := range entries {
		prefix, err := netip.ParsePrefix(entry.GetPrefix())
		if err != nil {
			module.Free()
			return nil, fmt.Errorf("failed to parse prefix %q: %w", entry.GetPrefix(), err)
		}

		key := bitset.TinyBitset{}
		for _, nh := range entry.GetNexthops() {
			hk, err := newHardwareKey(nh)
			if err != nil {
				module.Free()
				return nil, fmt.Errorf("failed to parse nexthop %v: %w", nh, err)
			}

			idx, ok := hardwareIndex[hk]
			if !ok {
				added, err := module.AddRoute(hk.SrcMAC[:], hk.DstMAC[:], hk.Device)
				if err != nil {
					module.Free()
					return nil, fmt.Errorf("failed to add hardware route: %w", err)
				}
				idx = uint32(added)
				hardwareIndex[hk] = idx
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

// hardwareKey is a comparable form of a hardware route used to
// deduplicate AddRoute calls.
type hardwareKey struct {
	SrcMAC [6]byte
	DstMAC [6]byte
	Device string
}

func newHardwareKey(nh *routepb.FIBNexthop) (hardwareKey, error) {
	src := nh.GetSrcMac()
	if src == nil {
		return hardwareKey{}, fmt.Errorf("src_mac is required")
	}
	dst := nh.GetDstMac()
	if dst == nil {
		return hardwareKey{}, fmt.Errorf("dst_mac is required")
	}
	device := nh.GetDevice()
	if device == "" {
		return hardwareKey{}, fmt.Errorf("device is required")
	}
	return hardwareKey{
		SrcMAC: src.EUI48(),
		DstMAC: dst.EUI48(),
		Device: device,
	}, nil
}
