package neigh

import (
	"net/netip"

	"github.com/yanet-platform/yanet2/common/go/rcucache"
)

// DeviceScope is an immutable egress-device restriction.
//
// Its zero value accepts every device, including an unnamed device.
type DeviceScope struct {
	devices map[string]struct{}
}

// NewDeviceScope copies the nonempty device names into an exact-match scope.
//
// Empty names are ignored; an effectively empty list accepts every device.
func NewDeviceScope(devices []string) DeviceScope {
	names := map[string]struct{}{}
	for _, device := range devices {
		if device != "" {
			names[device] = struct{}{}
		}
	}
	return DeviceScope{devices: names}
}

// Allows reports whether the egress device belongs to the scope.
func (m DeviceScope) Allows(device string) bool {
	if len(m.devices) == 0 {
		return true
	}
	_, ok := m.devices[device]
	return ok
}

// filter preserves unrestricted views and copies only eligible entries otherwise.
func (m DeviceScope) filter(view NexthopCacheView) NexthopCacheView {
	if len(m.devices) == 0 {
		return view
	}
	filtered := map[netip.Addr]NeighbourEntry{}
	for address, entry := range view.Iter() {
		if m.Allows(entry.HardwareRoute.Device) {
			filtered[address] = entry
		}
	}
	return rcucache.NewCache(filtered).View()
}
