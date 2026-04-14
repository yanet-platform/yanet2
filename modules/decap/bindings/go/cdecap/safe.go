package cdecap

import (
	"fmt"
	"net/netip"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
)

// PrefixAdd inserts a decap prefix range into this configuration.
func (m *ModuleConfig) PrefixAdd(prefix netip.Prefix) error {
	addrStart := prefix.Addr()
	addrEnd := xnetip.LastAddr(prefix)

	if addrStart.Is4() {
		return m.addPrefixV4(addrStart.As4(), addrEnd.As4())
	}
	if addrStart.Is6() {
		return m.addPrefixV6(addrStart.As16(), addrEnd.As16())
	}
	return fmt.Errorf("unsupported prefix: must be either IPv4 or IPv6")
}
