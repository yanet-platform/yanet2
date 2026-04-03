package cdscp

import (
	"fmt"
	"net/netip"

	"github.com/yanet-platform/yanet2/common/go/xnetip"
)

func (m *ModuleConfig) PrefixAdd(prefix netip.Prefix) error {
	addrStart := prefix.Addr()
	addrEnd := xnetip.LastAddr(prefix)

	if addrStart.Is4() {
		return m.prefixAdd4(addrStart.As4(), addrEnd.As4())
	}
	if addrStart.Is6() {
		return m.prefixAdd6(addrStart.As16(), addrEnd.As16())
	}

	return fmt.Errorf("unsupported prefix: must be either IPv4 or IPv6")
}
