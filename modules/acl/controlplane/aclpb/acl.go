package aclpb

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"net/netip"
)

func (m *IPNet) AsLogValue() any {
	if m == nil {
		return "::/0"
	}

	addr, _ := netip.AddrFromSlice(m.Addr)
	mask, _ := netip.AddrFromSlice(m.Mask)

	switch {
	case addr.Is4() && mask.Is4():
		maskU32 := binary.BigEndian.Uint32(mask.AsSlice())
		cidr := 32 - bits.TrailingZeros32(maskU32)

		switch {
		case cidr == 32:
			return addr.String()
		case cidr != bits.OnesCount32(maskU32):
			return fmt.Sprintf("%s/%s", addr, mask)
		default:
			return fmt.Sprintf("%s/%d", addr, cidr)
		}
	case addr.Is6() && mask.Is6():
		maskU64Hi := binary.BigEndian.Uint64(mask.AsSlice()[:8])
		maskU64Lo := binary.BigEndian.Uint64(mask.AsSlice()[8:])

		cidrHi := 64 - bits.TrailingZeros64(maskU64Hi)
		cidrLo := 64 - bits.TrailingZeros64(maskU64Lo)

		cidr := cidrHi + cidrLo
		switch {
		case cidr == 128:
			return addr.String()
		case cidrHi != bits.OnesCount64(maskU64Hi) || cidrLo != bits.OnesCount64(maskU64Lo):
			return fmt.Sprintf("%s/%s", addr, mask)
		default:
			return fmt.Sprintf("%s/%d", addr, cidr)
		}
	default:
		return fmt.Sprintf("%s/%s", addr, mask)
	}
}
