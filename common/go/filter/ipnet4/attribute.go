package ipnet4

import (
	"net/netip"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/filterpb"
)

type IPNet struct {
	Addr netip.Addr
	Mask netip.Addr
}

type IPNets []IPNet

func FromIPNets(ipNets []*filterpb.IPNet) ([]IPNet, error) {
	result := make([]IPNet, 0, len(ipNets))

	for idx := range ipNets {
		if (len(ipNets[idx].Addr) != 4 && len(ipNets[idx].Addr) != 16) ||
			len(ipNets[idx].Addr) != len(ipNets[idx].Mask) {
			return nil, status.Error(
				codes.InvalidArgument,
				"invalid network address length")
		}

		if len(ipNets[idx].Addr) != 4 {
			continue
		}
		addr, _ := netip.AddrFromSlice(ipNets[idx].Addr)
		mask, _ := netip.AddrFromSlice(ipNets[idx].Mask)
		result = append(result, IPNet{
			Addr: addr,
			Mask: mask,
		})
	}

	return result, nil
}

func clamp(value int, from int, to int) int {
	if value < from {
		return from
	}
	if value > to {
		return to
	}
	return value
}

func buildPrefix(bits int) netip.Addr {
	maskBytes := make([]byte, 4)
	for idx := 0; idx < 4; idx++ {
		maskBytes[idx] = 0xff << clamp(idx*8+8-bits, 0, 8)
	}
	mask, _ := netip.AddrFromSlice(maskBytes)
	return mask
}

func FromIpPrefixes(ipPrefixes []*filterpb.IPPrefix) (IPNets, error) {
	result := make([]IPNet, 0, len(ipPrefixes))

	for _, ipPrefix := range ipPrefixes {
		if len(ipPrefix.Addr) != 4 && len(ipPrefix.Addr) != 16 {
			return nil,
				status.Error(
					codes.InvalidArgument,
					"invalid network address length")
		}

		if len(ipPrefix.Addr) != 4 {
			continue
		}

		addr, _ := netip.AddrFromSlice(ipPrefix.Addr)
		mask := buildPrefix(int(ipPrefix.Length))

		result = append(result, IPNet{
			Addr: addr,
			Mask: mask,
		})
	}

	return result, nil
}

func FromNetIpPrefixes(prefixes []netip.Prefix) (IPNets, error) {
	result := make([]IPNet, 0, len(prefixes))

	for _, prefix := range prefixes {
		if len(prefix.Addr().AsSlice()) != 4 {
			continue
		}

		addr := prefix.Addr()
		mask := buildPrefix(prefix.Bits())

		result = append(result, IPNet{
			Addr: addr,
			Mask: mask,
		})
	}
	return result, nil
}
