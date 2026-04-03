package xnetip

import (
	"net/netip"
)

func PrefixCompare(a netip.Prefix, b netip.Prefix) int {
	if c := a.Addr().Compare(b.Addr()); c != 0 {
		return c
	}

	return a.Bits() - b.Bits()
}
