package balancer

import (
	"bytes"
	"time"

	"github.com/yanet-platform/yanet2/common/go/relptr"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// vsKey is a hashable identifier for a virtual service, usable as a map key.
type vsKey struct {
	addr    [16]byte
	addrLen uint8
	port    uint16
	proto   uint8
}

// vsSlot tracks the position of a virtual service and its reals in the
// shared-memory arrays, enabling O(1) lookups by identity.
type vsSlot struct {
	// Position of this VS in the packet handler's VS array.
	index int
	// Maps real server identity to its position in this VS's reals array.
	realSlots map[realKey]int
}

func makeVsKey(id *balancerpb.VsIdentifier) vsKey {
	var k vsKey
	k.addrLen = uint8(len(id.Addr))
	copy(k.addr[:], id.Addr)
	k.port = uint16(id.Port)
	k.proto = transportProtoToC(id.Proto)
	return k
}

func (vs *VS) key() vsKey {
	var k vsKey
	k.addrLen = 4
	if vs.Ip_proto == ipprotoIPv6 {
		k.addrLen = 16
	}
	copy(k.addr[:], vs.Addr.Bytes(int(vs.Ip_proto)))
	k.port = uint16(vs.Port)
	k.proto = uint8(vs.Transport_proto)
	return k
}

func (vs *VS) free(agent *BalancerAgent) {
	yanetAgent := agent.AsYanetAgent()

	// Compiled filters.
	vs.freeACL(agent)
	vs.freeRealSelector(agent)
	vs.freeSessionTracker(agent)

	// Rule counter IDs.
	ruleCounterIds := relptr.Slice(&vs.Rule_counter_ids, vs.Allowed_sources_count)
	yanet.FreeSlice(yanetAgent, ruleCounterIds)

	// Allowed sources.
	allowedSources := relptr.Slice(&vs.Allowed_sources, vs.Allowed_sources_count)
	for i := range allowedSources {
		nets := relptr.Slice(&allowedSources[i].Nets, allowedSources[i].Nets_count)
		yanet.FreeSlice(yanetAgent, nets)
		portRanges := relptr.Slice(
			&allowedSources[i].Port_ranges,
			allowedSources[i].Port_ranges_count,
		)
		yanet.FreeSlice(yanetAgent, portRanges)
	}
	yanet.FreeSlice(yanetAgent, allowedSources)

	// Peers.
	peersV4 := relptr.Slice(&vs.Peers_v4, vs.Peers_v4_count)
	yanet.FreeSlice(yanetAgent, peersV4)
	peersV6 := relptr.Slice(&vs.Peers_v6, vs.Peers_v6_count)
	yanet.FreeSlice(yanetAgent, peersV6)

	// Reals.
	reals := relptr.Slice(&vs.Reals, vs.Reals_count)
	yanet.FreeSlice(yanetAgent, reals)
}

// populateAllowedSources allocates and fills the allowed sources array for a VS.
func (vs *VS) populateAllowedSources(
	agent *BalancerAgent,
	sources []*balancerpb.AllowedSources,
) error {
	count := len(sources)

	if count == 0 {
		return nil
	}

	srcs := yanet.AllocSlice[AllowedSource](agent.AsYanetAgent(), count)
	if srcs == nil {
		return errNoAgentMemory
	}

	// Explicit zero-init: AllocSlice returns shared memory which is not
	// guaranteed to be zeroed by Go's allocator.
	for idx := range srcs {
		srcs[idx] = AllowedSource{}
	}

	for i, ps := range sources {
		if err := srcs[i].populate(agent, ps); err != nil {
			return err
		}
	}

	relptr.SetSlice(&vs.Allowed_sources, srcs)
	vs.Allowed_sources_count = uint32(count)

	return nil
}

func (as *AllowedSource) populate(agent *BalancerAgent, src *balancerpb.AllowedSources) error {
	// Nets.
	nets := src.Nets
	if len(nets) > 0 {
		netSlice := yanet.AllocSlice[Net](agent.AsYanetAgent(), len(nets))
		if netSlice == nil {
			return errNoAgentMemory
		}
		relptr.SetSlice(&as.Nets, netSlice)
		for j, n := range nets {
			writeNet(&netSlice[j], n)
		}
		as.Nets_count = uint32(len(nets))
	}

	// Port ranges.
	ports := src.Ports
	if len(ports) > 0 {
		prSlice := yanet.AllocSlice[PortRange](agent.AsYanetAgent(), len(ports))
		if prSlice == nil {
			return errNoAgentMemory
		}
		relptr.SetSlice(&as.Port_ranges, prSlice)
		for j, p := range ports {
			prSlice[j].From = uint16(p.From)
			prSlice[j].To = uint16(p.To)
		}
		as.Port_ranges_count = uint32(len(ports))
	}

	// Tag (null-terminated C string in fixed-size buffer).
	if src.Tag != nil {
		tag := *src.Tag
		for i := range tag {
			as.Tag[i] = int8(tag[i])
		}
		as.Tag[len(tag)] = 0
	} else {
		as.Tag[0] = 0
	}

	return nil
}

// populatePeers allocates and fills the peer address arrays for a VS.
func (vs *VS) populatePeers(agent *BalancerAgent, peers [][]byte) error {
	v4Count := 0
	v6Count := 0
	for _, addr := range peers {
		if len(addr) == 4 {
			v4Count++
		} else {
			v6Count++
		}
	}

	// First, allocate slice. Then, set slice and count in the shared memory atomically.
	if v4Count > 0 {
		slice := yanet.AllocSlice[Net4Addr](agent.AsYanetAgent(), v4Count)
		if slice == nil {
			return errNoAgentMemory
		}
		j := 0
		for _, addr := range peers {
			if len(addr) == 4 {
				writeNet4Addr(&slice[j], addr)
				j++
			}
		}
		relptr.SetSlice(&vs.Peers_v4, slice)
		vs.Peers_v4_count = uint32(v4Count)
	}

	if v6Count > 0 {
		slice := yanet.AllocSlice[Net6Addr](agent.AsYanetAgent(), v6Count)
		if slice == nil {
			return errNoAgentMemory
		}
		j := 0
		for _, addr := range peers {
			if len(addr) == 16 {
				writeNet6Addr(&slice[j], addr)
				j++
			}
		}
		relptr.SetSlice(&vs.Peers_v6, slice)
		vs.Peers_v6_count = uint32(v6Count)
	}

	return nil
}

// protoVsFlagsToC converts protobuf VsFlags to the C bit field value.
func protoVsFlagsToC(f *balancerpb.VsFlags) uint16 {
	var flags uint16
	if f.GetPureL3() {
		flags |= VSFlagPureL3
	}
	if f.GetFixMss() {
		flags |= VSFlagFixMSS
	}
	if f.GetGre() {
		flags |= VSFlagGRE
	}
	if f.GetOps() {
		flags |= VSFlagOPS
	}
	if f.GetWlc() {
		flags |= VSFlagWLC
	}
	return flags
}

func allowedSourcesEqual(
	prevAllowedSrc *AllowedSource,
	curAllowedSrc *balancerpb.AllowedSources,
	ipproto int,
) bool {
	prevNets := relptr.Slice(&prevAllowedSrc.Nets, prevAllowedSrc.Nets_count)
	curNets := curAllowedSrc.Nets
	if len(prevNets) != len(curNets) {
		return false
	}

	for i := range prevAllowedSrc.Nets_count {
		prevNet := &prevNets[i]
		curNet := curNets[i]
		if !bytes.Equal(prevNet.AddrBytes(ipproto), curNet.Addr) {
			return false
		}
		if !bytes.Equal(prevNet.MaskBytes(ipproto), curNet.Mask) {
			return false
		}
	}

	prevPr := relptr.Slice(&prevAllowedSrc.Port_ranges, prevAllowedSrc.Port_ranges_count)
	curPr := curAllowedSrc.Ports
	if len(prevPr) != len(curPr) {
		return false
	}
	for i := range prevAllowedSrc.Port_ranges_count {
		prevPortRange := &prevPr[i]
		curPortRange := curPr[i]
		if prevPortRange.From != uint16(curPortRange.From) ||
			prevPortRange.To != uint16(curPortRange.To) {
			return false
		}
	}
	return true
}

// canReuseACL checks whether the previous VS's compiled ACL can be reused.
// Precondition: allowed sources in both prev and new config must be in the same sort order.
// This is guaranteed by validatePacketHandlerConfig → validateVS → validateAllowedSources.
func canReuseACL(prevVs *VS, pbVs *balancerpb.VirtualService) bool {
	if prevVs == nil {
		return false
	}
	prevAllowedSrcs := relptr.Slice(&prevVs.Allowed_sources, prevVs.Allowed_sources_count)
	curAllowedSrcs := pbVs.AllowedSrcs
	if len(prevAllowedSrcs) != len(curAllowedSrcs) {
		return false
	}
	for i := range prevVs.Allowed_sources_count {
		prevAllowedSrc := &prevAllowedSrcs[i]
		curAllowedSrc := pbVs.AllowedSrcs[i]
		if !allowedSourcesEqual(prevAllowedSrc, curAllowedSrc, int(prevVs.Ip_proto)) {
			return false
		}
	}
	return true
}

func (vs *VS) isWLC() bool {
	return vs.Flags&VSFlagWLC != 0
}

func (vs *VS) populateReals(
	agent *BalancerAgent,
	pbReals []*balancerpb.Real,
	prevVs *VS,
) (reuseSelector bool, err error) {
	wlcDisabled := prevVs != nil && !vs.isWLC() && prevVs.isWLC()
	schedulerChanged := prevVs != nil && vs.scheduler() != prevVs.scheduler()

	var prevReals []Real
	if prevVs != nil {
		prevReals = relptr.Slice(&prevVs.Reals, prevVs.Reals_count)
	}

	realSlotCount := max(len(pbReals), len(prevReals))
	newReals := yanet.AllocSlice[Real](agent.AsYanetAgent(), realSlotCount)
	if newReals == nil {
		return false, errNoAgentMemory
	}
	for idx := range newReals {
		newReals[idx] = Real{
			Flags: RealFlagRemoved,
		}
	}

	pbRealIndex := make(map[realKey]int, len(pbReals))
	for idx := range pbReals {
		k := makeRealKey(pbReals[idx].Id)
		pbRealIndex[k] = idx
	}

	inheritEffectiveWeights := !wlcDisabled

	prevRealsUnchanged := placeExistingReals(
		pbReals,
		newReals,
		prevReals,
		pbRealIndex,
		inheritEffectiveWeights,
	)
	newRealsUnchanged := placeNewReals(
		pbReals,
		newReals,
		prevReals,
		pbRealIndex,
	)

	vs.Reals_count = uint32(len(newReals))
	relptr.SetSlice(&vs.Reals, newReals)

	// The real selector can be reused only when all four conditions hold:
	// 1. prevRealsUnchanged: old reals were not changed.
	// 2. newRealsUnchanged: no new reals were placed.
	// 3. inheritEffectiveWeights: WLC was not just disabled, so effective weights
	//    were inherited from the previous config.
	// 4. !schedulerChanged: the scheduler was not just changed.
	return prevRealsUnchanged && newRealsUnchanged && inheritEffectiveWeights &&
		!schedulerChanged, nil
}

func (vs *VS) populate(
	agent *BalancerAgent,
	pb *balancerpb.VirtualService,
	stableIdx uint64,
	prevVs *VS,
	handler *PacketHandler,
) (*balancerpb.VsReuseReport, error) {
	vs.Stable_idx = stableIdx

	// Set identifier fields. The caller must have cleared VSFlagRemoved before calling;
	// vs.Flags is expected to have no other flags set at this point.
	writeNetAddr(&vs.Addr, pb.Id.Addr)
	vs.Port = uint16(pb.Id.Port)
	vs.Transport_proto = transportProtoToC(pb.Id.Proto)
	vs.Ip_proto = ipprotoIP
	if len(pb.Id.Addr) == 16 {
		vs.Ip_proto = ipprotoIPv6
	}
	vs.Flags |= protoVsFlagsToC(pb.Flags)

	if err := vs.populateAllowedSources(agent, pb.AllowedSrcs); err != nil {
		return nil, err
	}

	if err := vs.populatePeers(agent, pb.Peers); err != nil {
		return nil, err
	}

	reuseSelector, err := vs.populateReals(agent, pb.Reals, prevVs)
	if err != nil {
		return nil, err
	}

	if err := vs.setSessionsTracker(agent); err != nil {
		return nil, err
	}

	if reuseSelector {
		relptr.Equate(&vs.Selector, &prevVs.Selector)
	} else if err := vs.updateRealSelector(&handler.Rcu, agent); err != nil {
		return nil, err
	}

	reuseACL := canReuseACL(prevVs, pb)
	if reuseACL {
		relptr.Equate(&vs.Acl, &prevVs.Acl)
	} else if err := vs.setACL(agent); err != nil {
		return nil, err
	}

	return &balancerpb.VsReuseReport{
		VsIdentifier:   pb.Id,
		AclReused:      reuseACL,
		SelectorReused: reuseSelector,
	}, nil
}

func (vs *VS) epoch() uint32 {
	return epochOf(vs.Stable_idx)
}

func (vs *VS) isRemoved() bool {
	return vs.Flags&VSFlagRemoved != 0
}

func (vs *VS) id() *balancerpb.VsIdentifier {
	return &balancerpb.VsIdentifier{
		Addr:  vs.Addr.Bytes(int(vs.Ip_proto)),
		Port:  uint32(vs.Port),
		Proto: transportProtoToPB(vs.Transport_proto),
	}
}

func transportProtoToPB(proto uint8) balancerpb.TransportProto {
	protoPB := balancerpb.TransportProto_TCP
	if proto == ipprotoUDP {
		protoPB = balancerpb.TransportProto_UDP
	}
	return protoPB
}

func vsFlags(flags uint16) *balancerpb.VsFlags {
	return &balancerpb.VsFlags{
		PureL3: flags&VSFlagPureL3 != 0,
		FixMss: flags&VSFlagFixMSS != 0,
		Gre:    flags&VSFlagGRE != 0,
		Ops:    flags&VSFlagOPS != 0,
		Wlc:    flags&VSFlagWLC != 0,
	}
}

func (vs *VS) schedulerRoundRobin() bool {
	return vs.Flags&VSFlagRoundRobin != 0
}

func (vs *VS) scheduler() balancerpb.VsScheduler {
	if vs.schedulerRoundRobin() {
		return balancerpb.VsScheduler_ROUND_ROBIN
	}
	return balancerpb.VsScheduler_SOURCE_HASH
}

func (vs *VS) state(workers uint32, now time.Time) *balancerpb.VsState {
	reals := relptr.Slice(&vs.Reals, vs.Reals_count)
	activeSessions := uint64(0)
	lastPacketTimestamp := time.Unix(0, 0)
	realsState := make([]*balancerpb.RealState, len(reals))
	for realIdx := range reals {
		if reals[realIdx].isRemoved() {
			continue
		}
		r := reals[realIdx].state(workers, now)
		if r.LastPacketTimestamp.AsTime().After(lastPacketTimestamp) {
			lastPacketTimestamp = r.LastPacketTimestamp.AsTime()
		}
		activeSessions += r.ActiveSessions
		realsState[realIdx] = r
	}
	vsState := &balancerpb.VsState{
		Id:                  vs.id(),
		Flags:               vsFlags(vs.Flags),
		Scheduler:           vs.scheduler(),
		Reals:               realsState,
		ActiveSessions:      activeSessions,
		LastPacketTimestamp: timestamppb.New(lastPacketTimestamp),
	}
	return vsState
}
