package cdscp

import (
	"errors"
	"net/netip"

	"github.com/yanet-platform/xnetip"
)

func (m *ModuleConfig) PrefixAdd(prefix netip.Prefix) error {
	network, ok := xnetip.NetworkFromPrefix(prefix)
	if !ok {
		return errors.New("unsupported prefix: must be either IPv4 or IPv6")
	}

	addrStart := prefix.Addr()
	addrEnd := network.LastAddr()

	if addrStart.Is4() {
		return m.prefixAdd4(addrStart.As4(), addrEnd.As4())
	}
	return m.prefixAdd6(addrStart.As16(), addrEnd.As16())
}
