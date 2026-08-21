package cdecap

import (
	"errors"
	"net/netip"

	"github.com/yanet-platform/xnetip"
)

// PrefixAdd inserts a decap prefix range into this configuration.
func (m *ModuleConfig) PrefixAdd(prefix netip.Prefix) error {
	network, ok := xnetip.NetworkFromPrefix(prefix)
	if !ok {
		return errors.New("unsupported prefix: must be either IPv4 or IPv6")
	}

	addrStart := prefix.Addr()
	addrEnd := network.LastAddr()

	if addrStart.Is4() {
		return m.addPrefixV4(addrStart.As4(), addrEnd.As4())
	}
	return m.addPrefixV6(addrStart.As16(), addrEnd.As16())
}
