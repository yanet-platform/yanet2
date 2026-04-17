package utils

import (
	"math/rand/v2"
	"net/netip"

	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

func generateContinuousNet(rng *rand.Rand, len int) ([]byte, []byte) {
	addr := make([]byte, len)
	mask := make([]byte, len)
	for i := range len {
		addr[i] = byte(rng.IntN(256))
	}
	prefixLen := rng.IntN(len*8) + 1
	for i := range prefixLen {
		mask[i/8] |= byte(1 << (7 - i%8))
	}
	return addr, mask
}

// GenerateIPv4Net generates a random contiguous IPv4 network.
func GenerateIPv4Net(rng *rand.Rand) *filterpb.IPNet {
	addr, mask := generateContinuousNet(rng, 4)
	for i := range 4 {
		addr[i] = 0
		mask[i] = 0
	}
	return &filterpb.IPNet{
		Addr: addr,
		Mask: mask,
	}
}

// GenerateIPv6Net generates a random continuously IPv6 network with hole in the middle.
func GenerateIPv6Net(rng *rand.Rand) *filterpb.IPNet {
	addr1, mask1 := generateContinuousNet(rng, 8)
	addr2, mask2 := generateContinuousNet(rng, 8)
	for i := range 8 {
		addr1[i] = 0
		addr2[i] = 0
		mask1[i] = 0
		mask2[i] = 0
	}
	return &filterpb.IPNet{
		Addr: append(addr1, addr2...),
		Mask: append(mask1, mask2...),
	}
}

func GenerateNet(rng *rand.Rand) *filterpb.IPNet {
	if rng.IntN(2) == 0 {
		return GenerateIPv4Net(rng)
	}
	return GenerateIPv6Net(rng)
}

// GenerateAddressFromNet generates a random address from a network.
func GenerateAddressFromNet(rng *rand.Rand, net *filterpb.IPNet) netip.Addr {
	addr := net.Addr
	mask := net.Mask
	res := make([]byte, len(addr))
	for i := range len(addr) {
		res[i] = (addr[i] & mask[i]) | (byte(rng.IntN(256)) & ^mask[i])
	}
	resAddr, ok := netip.AddrFromSlice(res)
	if !ok {
		panic("failed to convert slice to address")
	}
	return resAddr
}

func GeneratePortRange(rng *rand.Rand) *filterpb.PortRange {
	base := 10050
	from := rng.IntN(65536-base) + base
	to := rng.IntN(65535-base) + base
	if from > to {
		from, to = to, from
	}
	return &filterpb.PortRange{
		From: uint32(from),
		To:   uint32(to),
	}
}

func GenerateAllowedSources(
	rng *rand.Rand,
	netCount int,
	portCount int,
	ipv6 bool,
) *balancerpb.AllowedSources {
	nets := make([]*filterpb.IPNet, netCount)
	for i := range nets {
		if ipv6 {
			nets[i] = GenerateIPv6Net(rng)
		} else {
			nets[i] = GenerateIPv4Net(rng)
		}
	}
	ports := make([]*filterpb.PortRange, portCount)
	for i := range ports {
		ports[i] = GeneratePortRange(rng)
	}
	return &balancerpb.AllowedSources{
		Nets:  nets,
		Ports: ports,
	}
}

func GenerateIPv4Address(rng *rand.Rand) netip.Addr {
	addr := [4]byte{}
	for i := range 4 {
		addr[i] = byte(rng.IntN(256))
	}
	return netip.AddrFrom4(addr)
}

func GenerateIPv6Address(rng *rand.Rand) netip.Addr {
	addr := [16]byte{}
	for i := range 16 {
		addr[i] = byte(rng.IntN(256))
	}
	return netip.AddrFrom16(addr)
}

func GenerateAddress(rng *rand.Rand) netip.Addr {
	if rng.IntN(2) == 0 {
		return GenerateIPv4Address(rng)
	}
	return GenerateIPv6Address(rng)
}

func GenerateReal(rng *rand.Rand) *balancerpb.Real {
	ip := GenerateAddress(rng)
	var src *filterpb.IPNet
	if ip.Is4() {
		src = &filterpb.IPNet{
			Addr: GenerateIPv4Address(rng).AsSlice(),
			Mask: GenerateIPv4Address(rng).AsSlice(),
		}
	} else {
		src = &filterpb.IPNet{
			Addr: GenerateIPv6Address(rng).AsSlice(),
			Mask: GenerateIPv6Address(rng).AsSlice(),
		}
	}
	return &balancerpb.Real{
		Id: &balancerpb.RelativeRealIdentifier{
			Ip:   ip.AsSlice(),
			Port: 0,
		},
		Weight: uint32(rng.IntN(10) + 1),
		Src:    src,
	}
}

func GenerateSrcFromAllowed(
	rng *rand.Rand,
	allowedSource *balancerpb.AllowedSources,
) (netip.Addr, uint16) {
	netID := rng.IntN(len(allowedSource.Nets))
	net := allowedSource.Nets[netID]
	addr := GenerateAddressFromNet(rng, net)
	portID := rng.IntN(len(allowedSource.Ports))
	from := allowedSource.Ports[portID].From
	to := allowedSource.Ports[portID].To
	port := uint16(rng.IntN(int(to-from+1)) + int(from))
	return addr, port
}

func GenerateAllowedSrcForVS(rng *rand.Rand, vs *balancerpb.VirtualService) (netip.Addr, uint16) {
	allowedSrcs := vs.AllowedSrcs
	allowedSrcID := rng.IntN(len(allowedSrcs))
	allowedSrc := allowedSrcs[allowedSrcID]
	return GenerateSrcFromAllowed(rng, allowedSrc)
}

func GenerateVS(
	rng *rand.Rand,
	realsCount int,
	allowedSrcCount int,
	flags *balancerpb.VsFlags,
) *balancerpb.VirtualService {
	reals := make([]*balancerpb.Real, realsCount)
	for i := range realsCount {
		reals[i] = GenerateReal(rng)
	}
	vsAddr := GenerateAddress(rng)
	allowedSrcs := make([]*balancerpb.AllowedSources, allowedSrcCount)
	for i := range allowedSrcCount {
		allowedSrcs[i] = GenerateAllowedSources(rng, rand.IntN(10)+1, rand.IntN(10)+1, !vsAddr.Is4())
	}
	proto := balancerpb.TransportProto_TCP
	if rng.IntN(2) == 0 {
		proto = balancerpb.TransportProto_UDP
	}
	minPort := 10005
	return &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr:  vsAddr.AsSlice(),
			Port:  uint32(rng.IntN(65535-minPort) + minPort),
			Proto: proto,
		},
		AllowedSrcs: allowedSrcs,
		Reals:       reals,
		Flags:       flags,
		Scheduler:   balancerpb.VsScheduler_WRR,
	}
}

func GenerateRealUpdates(vs *balancerpb.VirtualService, rng *rand.Rand) []*balancerpb.RealUpdate {
	reals := vs.Reals
	updates := make([]*balancerpb.RealUpdate, 0, 2*len(reals)/3)
	for _, r := range reals {
		var weight *uint32
		switch rng.IntN(3) {
		case 0:
			val := uint32(rng.IntN(10) + 1)
			weight = &val
		case 1:
			continue
		}
		if rng.IntN(2) == 0 {
			val := uint32(rng.IntN(10) + 1)
			weight = &val
		}
		var enable *bool
		switch rng.IntN(3) {
		case 0:
			val := false
			enable = &val
		case 1:
			val := true
			enable = &val
		}
		updates = append(updates, &balancerpb.RealUpdate{
			RealId: &balancerpb.RealIdentifier{
				Vs:   vs.Id,
				Real: r.Id,
			},
			Enable: enable,
			Weight: weight,
		})
	}

	// Enable some real

	if len(updates) > 0 {
		idx := rng.IntN(len(updates))
		enable := true
		updates[idx].Enable = &enable
	} else {
		enable := true
		updates = append(updates, &balancerpb.RealUpdate{
			RealId: &balancerpb.RealIdentifier{
				Vs:   vs.Id,
				Real: vs.Reals[0].Id,
			},
			Enable: &enable,
		})
	}

	return updates
}
