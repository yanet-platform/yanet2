// Package rulegen synthesizes production-shaped ACL rulesets, the input of the
// ACL compile stress runs and of the benchmarks that compare compiles.
package rulegen

import (
	"fmt"
	"math/rand/v2"
	"net/netip"

	"github.com/gopacket/gopacket/layers"

	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/bindings/go/filter"
	"github.com/yanet-platform/yanet2/common/go/xerror"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
)

// StressDevices are the device names stress rules are scoped to.
var StressDevices = []string{"lp.kni0.802", "lp.kni0.1600", "lp.kni0.1619", "lp.kni0.2000"}

// stressNet4 draws an IPv4 network inside one of the supernets with a
// production-like prefix length mix (many /24 and /32, some wide nets).
func stressNet4(random *rand.Rand, supernets []netip.Addr) xnetip.Contiguous[xnetip.Network4] {
	var bits int
	switch pick := random.IntN(100); {
	case pick < 30:
		bits = 32
	case pick < 60:
		bits = 24
	case pick < 75:
		bits = 16 + random.IntN(8) // /16../23
	case pick < 92:
		bits = 25 + random.IntN(7) // /25../31
	default:
		bits = 8 + random.IntN(8) // /8../15
	}
	base := supernets[random.IntN(len(supernets))].As4()
	addr := [4]byte{base[0], byte(random.IntN(256)), byte(random.IntN(256)), byte(random.IntN(256))}
	return xerror.Unwrap(xnetip.ContiguousFromCIDR4(netip.AddrFrom4(addr), bits))
}

// stressNet6 draws an IPv6 network nested in one of the /32 supernets.
func stressNet6(random *rand.Rand, supernets []netip.Addr) xnetip.BiContiguous {
	var bits int
	switch pick := random.IntN(100); {
	case pick < 40:
		bits = 64
	case pick < 65:
		bits = 48
	case pick < 80:
		bits = 56
	case pick < 92:
		bits = 128
	default:
		bits = 32 + random.IntN(16) // /32../47
	}
	addr := supernets[random.IntN(len(supernets))].As16()
	for idx := 4; idx < 16; idx++ {
		addr[idx] = byte(random.IntN(256))
	}
	network := xerror.Unwrap(xnetip.ContiguousFromCIDR6(netip.AddrFrom16(addr), bits))
	return xnetip.BiContiguousFromContiguous(network)
}

// GenerateStressRules synthesizes count production-shaped ACL rules plus
// a terminating per-device catch-all deny, deterministically from seed.
//
// Shape: device-scoped rules (one device mostly, two sometimes), 45%
// IPv6, 45% IPv4, 10% dual-family; sources drawn from a pool of about
// 0.4*count networks and destinations from a pool of about 0.08*count,
// all nested in a few dozen supernets; TCP 60%, UDP 25%, ICMP 10%, any
// protocol 5%; destination ports mostly single popular or random ports
// with some ranges, source ports rarely restricted; actions allow,
// count+deny, deny and count+allow; a counter per rule, a fifth of them
// shared by rule groups.
//
// The compiled size grows with count; rule count is the only knob.
func GenerateStressRules(seed uint64, count int) []cacl.ACLRule {
	random := rand.New(rand.NewPCG(seed, 0x5eed))

	super4 := make([]netip.Addr, 16)
	for idx := range super4 {
		super4[idx] = netip.AddrFrom4([4]byte{byte(1 + random.IntN(223)), 0, 0, 0})
	}
	super6 := make([]netip.Addr, 48)
	for idx := range super6 {
		super6[idx] = netip.AddrFrom16([16]byte{0x2a, 0x02, byte(random.IntN(256)), byte(random.IntN(256))})
	}

	src4 := make([]xnetip.Contiguous[xnetip.Network4], count*40/100+1)
	for idx := range src4 {
		src4[idx] = stressNet4(random, super4)
	}
	dst4 := make([]xnetip.Contiguous[xnetip.Network4], count*8/100+1)
	for idx := range dst4 {
		dst4[idx] = stressNet4(random, super4)
	}
	src6 := make([]xnetip.BiContiguous, count*40/100+1)
	for idx := range src6 {
		src6[idx] = stressNet6(random, super6)
	}
	dst6 := make([]xnetip.BiContiguous, count*8/100+1)
	for idx := range dst6 {
		dst6[idx] = stressNet6(random, super6)
	}

	popular := []uint16{22, 25, 53, 80, 123, 179, 443, 3128, 5432, 6379, 8080, 8443, 9090}
	dstPorts := func() filter.PortRanges {
		switch pick := random.IntN(100); {
		case pick < 50:
			port := popular[random.IntN(len(popular))]
			return filter.PortRanges{{From: port, To: port}}
		case pick < 75:
			port := uint16(1 + random.IntN(65000))
			return filter.PortRanges{{From: port, To: port}}
		case pick < 90:
			from := uint16(1024 + random.IntN(60000))
			return filter.PortRanges{{From: from, To: from + uint16(random.IntN(500))}}
		default:
			return nil
		}
	}
	srcPorts := func() filter.PortRanges {
		if random.IntN(100) < 10 {
			return filter.PortRanges{{From: 1024, To: 65535}}
		}
		return nil
	}
	actions := func() []cacl.ACLAction {
		switch pick := random.IntN(100); {
		case pick < 60:
			return []cacl.ACLAction{{Kind: cacl.ActionAllow}}
		case pick < 85:
			return []cacl.ACLAction{{Kind: cacl.ActionCount}, {Kind: cacl.ActionDeny}}
		case pick < 95:
			return []cacl.ACLAction{{Kind: cacl.ActionDeny}}
		default:
			return []cacl.ACLAction{{Kind: cacl.ActionCount}, {Kind: cacl.ActionAllow}}
		}
	}
	pick4 := func(pool []xnetip.Contiguous[xnetip.Network4], most int) []xnetip.Contiguous[xnetip.Network4] {
		picked := make([]xnetip.Contiguous[xnetip.Network4], 1+random.IntN(most))
		for idx := range picked {
			picked[idx] = pool[random.IntN(len(pool))]
		}
		return picked
	}
	pick6 := func(pool []xnetip.BiContiguous, most int) []xnetip.BiContiguous {
		picked := make([]xnetip.BiContiguous, 1+random.IntN(most))
		for idx := range picked {
			picked[idx] = pool[random.IntN(len(pool))]
		}
		return picked
	}

	rules := make([]cacl.ACLRule, 0, count+1)
	for idx := range count {
		rule := cacl.ACLRule{Actions: actions()}
		if random.IntN(100) < 20 {
			rule.Counter = fmt.Sprintf("group_%d", random.IntN(64))
		} else {
			rule.Counter = fmt.Sprintf("rule_%d", idx)
		}
		rule.Devices = filter.Devices{{Name: StressDevices[random.IntN(len(StressDevices))]}}
		if random.IntN(100) < 10 {
			rule.Devices = append(rule.Devices, filter.Device{Name: StressDevices[random.IntN(len(StressDevices))]})
		}

		family := random.IntN(100)
		has4, has6 := family < 55, family >= 45
		if has4 {
			rule.Src4s = pick4(src4, 4)
			rule.Dst4s = pick4(dst4, 2)
		}
		if has6 {
			rule.Src6s = pick6(src6, 4)
			rule.Dst6s = pick6(dst6, 2)
		}

		switch pick := random.IntN(100); {
		case pick < 60:
			rule.ProtoRanges = filter.ProtoRanges{
				filter.NewProtoRange(uint8(layers.IPProtocolTCP), filter.AnySubtype()),
			}
		case pick < 85:
			rule.ProtoRanges = filter.ProtoRanges{
				filter.NewProtoRange(uint8(layers.IPProtocolUDP), filter.AnySubtype()),
			}
		case pick < 95:
			if has4 {
				rule.ProtoRanges = append(rule.ProtoRanges, filter.NewProtoRange(
					uint8(layers.IPProtocolICMPv4), filter.ExactSubtype(layers.ICMPv4TypeEchoRequest),
				))
			}
			if has6 {
				rule.ProtoRanges = append(rule.ProtoRanges, filter.NewProtoRange(
					uint8(layers.IPProtocolICMPv6), filter.ExactSubtype(layers.ICMPv6TypeEchoRequest),
				))
			}
		default:
			// Any protocol: no proto, no port constraint.
		}
		if pick := random.IntN(100); len(rule.ProtoRanges) == 1 && pick < 85 &&
			(rule.ProtoRanges[0].From>>8 == uint16(layers.IPProtocolTCP) ||
				rule.ProtoRanges[0].From>>8 == uint16(layers.IPProtocolUDP)) {
			rule.DstPortRanges = dstPorts()
			rule.SrcPortRanges = srcPorts()
		}
		rules = append(rules, rule)
	}

	catchAll := cacl.ACLRule{
		Counter: "catch_all",
		Actions: []cacl.ACLAction{{Kind: cacl.ActionCount}, {Kind: cacl.ActionDeny}},
		Src4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Dst4s:   []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Src6s:   []xnetip.BiContiguous{filter.UnspecifiedIPv6},
		Dst6s:   []xnetip.BiContiguous{filter.UnspecifiedIPv6},
	}
	for _, name := range StressDevices {
		catchAll.Devices = append(catchAll.Devices, filter.Device{Name: name})
	}
	return append(rules, catchAll)
}
