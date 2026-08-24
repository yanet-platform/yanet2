package commonpb

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"

	"github.com/yanet-platform/xnetip"
)

// NewBiContiguousIPv6NetworkFromBiContiguous creates a
// BiContiguousIPv6Network from an xnetip bi-contiguous network.
//
// The conversion is total: the network is masked by its own invariant.
// The inverse of ToBiContiguous.
func NewBiContiguousIPv6NetworkFromBiContiguous(net xnetip.BiContiguous) *BiContiguousIPv6Network {
	return &BiContiguousIPv6Network{
		Addr:        NewIPv6Address(net.Network().Addr().As16()),
		HiPrefixLen: uint32(net.HighPrefixLen()),
		LoPrefixLen: uint32(net.LowPrefixLen()),
	}
}

// ParseBiContiguousIPv6Network parses s as an IPv6 network whose two
// 64-bit mask halves are independently contiguous.
//
// Host bits are masked off. The CIDR form, the explicit address/mask
// form, and a bare address promoted to a host route are all accepted.
// A mask with a hole inside either half is rejected, as is an IPv4
// input.
func ParseBiContiguousIPv6Network(s string) (*BiContiguousIPv6Network, error) {
	net, err := xnetip.ParseBiContiguous(s)
	if err != nil {
		return nil, fmt.Errorf("failed to parse IP network: %w", err)
	}
	return NewBiContiguousIPv6NetworkFromBiContiguous(net), nil
}

// ToBiContiguous converts the BiContiguousIPv6Network to an xnetip
// bi-contiguous network.
//
// Returns an error if addr is absent or either half's prefix length
// exceeds 64. Every in-range length pair is a valid mask, so no mask
// shape is ever rejected. The returned network is masked, so host bits
// never leak out even if the message was constructed by hand. The
// inverse of NewBiContiguousIPv6NetworkFromBiContiguous.
func (m *BiContiguousIPv6Network) ToBiContiguous() (xnetip.BiContiguous, error) {
	if m.GetAddr() == nil {
		return xnetip.BiContiguous{}, fmt.Errorf("missing network address")
	}
	if m.GetHiPrefixLen() > 64 || m.GetLoPrefixLen() > 64 {
		return xnetip.BiContiguous{}, fmt.Errorf(
			"invalid prefix lengths %d/%d: each half must not exceed 64",
			m.GetHiPrefixLen(), m.GetLoPrefixLen(),
		)
	}

	var mask [16]byte
	binary.BigEndian.PutUint64(mask[:8], leadingOnes64(int(m.GetHiPrefixLen())))
	binary.BigEndian.PutUint64(mask[8:], leadingOnes64(int(m.GetLoPrefixLen())))

	net, err := xnetip.BiContiguousFrom(m.GetAddr().ToAddr(), netip.AddrFrom16(mask))
	if err != nil {
		return xnetip.BiContiguous{}, fmt.Errorf("failed to build IP network: %w", err)
	}
	return net, nil
}

// leadingOnes64 returns the 64-bit mask half holding a leading run of
// length one bits.
//
// The caller guarantees length is within 0 through 64. A shift by the
// full width is well defined in Go and yields the zero mask.
func leadingOnes64(length int) uint64 {
	return ^uint64(0) << (64 - length)
}

// AsLogValue implements xgrpc.ProtoLogValue for compact gRPC logging.
func (m *BiContiguousIPv6Network) AsLogValue() any {
	net, err := m.ToBiContiguous()
	if err != nil {
		return "invalid"
	}

	return net.String()
}

// MarshalJSON serializes the network as a bare string, the same form
// the Rust side renders.
//
// The CIDR form appears when the mask is globally contiguous, the
// address/mask form otherwise.
func (m *BiContiguousIPv6Network) MarshalJSON() ([]byte, error) {
	net, err := m.ToBiContiguous()
	if err != nil {
		return nil, err
	}
	return json.Marshal(net.String())
}

// UnmarshalJSON accepts a bare string in any form
// ParseBiContiguousIPv6Network accepts.
func (m *BiContiguousIPv6Network) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		return fmt.Errorf("empty IP network is not allowed")
	}

	parsed, err := ParseBiContiguousIPv6Network(raw)
	if err != nil {
		return err
	}

	m.Addr = parsed.Addr
	m.HiPrefixLen = parsed.HiPrefixLen
	m.LoPrefixLen = parsed.LoPrefixLen
	return nil
}
