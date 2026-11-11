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

// SourcePrefixAdd extends the source selection, which must also match any
// configured destination selection.
func (m *ModuleConfig) SourcePrefixAdd(prefix netip.Prefix) error {
	prefix = prefix.Masked()
	network, ok := xnetip.NetworkFromPrefix(prefix)
	if !ok {
		return errors.New("unsupported source prefix: must be either IPv4 or IPv6")
	}
	addrStart := prefix.Addr()
	addrEnd := network.LastAddr()
	if addrStart.Is4() {
		return m.sourcePrefixAdd4(addrStart.As4(), addrEnd.As4())
	}
	return m.sourcePrefixAdd6(addrStart.As16(), addrEnd.As16())
}
