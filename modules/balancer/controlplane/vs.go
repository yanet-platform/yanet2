package balancer

import (
	"bytes"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/yanet-platform/yanet2/common/commonpb"
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

func (vs *VS) free(agent *Agent) {
	yanetAgent := agent.AsYanetAgent()

	vs.freeACL(agent)
	vs.freeRealSelector(agent)
	vs.freeSessionTrackers(agent)

	// Rule counter IDs.
	ruleCounterIDs := relptr.Slice(&vs.Rule_counter_ids, vs.Allowed_sources_count)
	yanet.FreeSlice(yanetAgent, ruleCounterIDs)

	// Allowed sources.t
	allowedSources := relptr.Slice(&vs.Allowed_sources, vs.Allowed_sources_count)
	freeAllowedSources(agent, allowedSources)
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
	agent *Agent,
	pbSources []*balancerpb.AllowedSources,
) error {
	count := len(pbSources)
	if count == 0 {
		return nil
	}

	srcs := yanet.AllocSlice[AllowedSource](agent.AsYanetAgent(), count)
	if srcs == nil {
		return errNoAgentMemory
	}

	// Explicit zero-init for safe freeing.
	for idx := range srcs {
		srcs[idx] = AllowedSource{}
	}

	for i, pb := range pbSources {
		if err := srcs[i].populate(agent, pb); err != nil {
			freeAllowedSources(agent, srcs)
			yanet.FreeSlice(agent.AsYanetAgent(), srcs)
			return err
		}
	}

	relptr.SetSlice(&vs.Allowed_sources, srcs)
	vs.Allowed_sources_count = uint32(count)

	return nil
}

func freeAllowedSources(agent *Agent, allowedSources []AllowedSource) {
	for i := range allowedSources {
		allowedSources[i].free(agent)
	}
}

func (as *AllowedSource) free(agent *Agent) {
	yanetAgent := agent.AsYanetAgent()

	// Nets.
	nets := relptr.Slice(&as.Nets, as.Nets_count)
	yanet.FreeSlice(yanetAgent, nets)

	// Port ranges.
	portRanges := relptr.Slice(&as.Port_ranges, as.Port_ranges_count)
	yanet.FreeSlice(yanetAgent, portRanges)
}

func (as *AllowedSource) populate(agent *Agent, pbSrc *balancerpb.AllowedSources) error {
	// Nets.
	nets := pbSrc.Nets
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
	ports := pbSrc.Ports
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
	if pbSrc.Tag != nil {
		tag := *pbSrc.Tag
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
func (vs *VS) populatePeers(agent *Agent, peers [][]byte) error {
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

// placeExistingVS places virtual services that exist in both the previous and new configs
// into their original slot positions in targetVs. Each placed VS is deleted from vsMap,
// so after this call vsMap contains only genuinely new VSes for placeNewVS to handle.
func placeExistingVS(
	ph *PacketHandler,
	agent *Agent,
	pbVS []*balancerpb.VirtualService,
	targetVs []VS,
	prevVs []VS,
	vsMap map[vsKey]int,
	reuseReport *balancerpb.ReuseReport,
) (oldIPv4VsMatches bool, oldIPv6VsMatches bool, err error) {
	oldIPv4VsMatches = true
	oldIPv6VsMatches = true

	for idx := range prevVs {
		prev := &prevVs[idx]
		if prev.isRemoved() {
			continue
		}

		k := prev.key()
		pbIdx, ok := vsMap[k]
		if !ok {
			// This VS not present in new config
			if k.addrLen == 4 {
				oldIPv4VsMatches = false
			} else {
				oldIPv6VsMatches = false
			}
			continue
		}

		delete(vsMap, k)

		target := &targetVs[idx]
		target.Flags &^= uint16(VSFlagRemoved)
		report, err := target.populate(agent, pbVS[pbIdx], prev.Stable_idx, prev, ph)
		if err != nil {
			return false, false, NewError("vs %s: %w", prev, err)
		}

		reuseReport.VsReuseReports = append(reuseReport.VsReuseReports, report)
	}

	return oldIPv4VsMatches, oldIPv6VsMatches, nil
}

// placeNewVS places virtual services that are new in the config (remaining in vsMap
// after placeExistingVS) into removed (empty) slots in targetVs.
// Invariant: there are always enough removed slots because targetVs has len(vsList) slots,
// placeExistingVS and placeNewVS together account for exactly len(vsList) entries,
// and each entry fills exactly one slot.
func placeNewVS(
	ph *PacketHandler,
	agent *Agent,
	vsList []*balancerpb.VirtualService,
	targetVs []VS,
	prevVs []VS,
	vsMap map[vsKey]int,
	reuseReport *balancerpb.ReuseReport,
) (noNewIPv4Vs bool, noNewIPv6Vs bool, err error) {
	noNewIPv4Vs = true
	noNewIPv6Vs = true

	nextRemoved := 0
	for idx, vs := range vsList {
		k := makeVsKey(vs.Id)
		if _, ok := vsMap[k]; !ok {
			continue
		}

		if k.addrLen == 4 {
			noNewIPv4Vs = false
		} else {
			noNewIPv6Vs = false
		}

		for !targetVs[nextRemoved].isRemoved() {
			nextRemoved++
		}

		epoch := uint32(0)
		if nextRemoved < len(prevVs) {
			epoch = prevVs[nextRemoved].epoch() + 1
		}
		stableIdx := makeStableIdx(epoch, uint32(nextRemoved))

		target := &targetVs[nextRemoved]
		target.Flags &^= uint16(VSFlagRemoved)
		report, err := target.populate(agent, vsList[idx], stableIdx, nil, ph)
		if err != nil {
			return false, false, NewError("vs %s: %w", vsIDToString(vs.Id), err)
		}

		nextRemoved++

		reuseReport.VsReuseReports = append(reuseReport.VsReuseReports, report)
	}

	return noNewIPv4Vs, noNewIPv6Vs, nil
}

// protoVsFlagsToC converts protobuf VsFlags to the C bit field value.
func protoVsFlagsToC(f *balancerpb.VsFlags, s balancerpb.VsScheduler) uint16 {
	var flags uint16
	if f.PureL3 {
		flags |= VSFlagPureL3
	}
	if f.FixMss {
		flags |= VSFlagFixMSS
	}
	if f.Gre {
		flags |= VSFlagGRE
	}
	if f.Ops {
		flags |= VSFlagOPS
	}

	switch s {
	case balancerpb.VsScheduler_WLC:
		flags |= VSFlagWLC
		flags |= VSFlagRoundRobin
	case balancerpb.VsScheduler_WRR:
		flags |= VSFlagRoundRobin
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
// This is guaranteed by validatePacketHandlerConfig -> validateVS -> validateAllowedSources.
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
	agent *Agent,
	pbReals []*balancerpb.Real,
	prevVs *VS,
) (reuseSelector bool, err error) {
	wlcChanged := prevVs != nil && vs.isWLC() != prevVs.isWLC()
	wrrChanged := prevVs != nil && vs.isWRR() != prevVs.isWRR()

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
		stableIdx := uint64(0)
		if idx < len(prevReals) {
			stableIdx = prevReals[idx].Stable_idx
		}
		newReals[idx] = Real{
			Flags:      RealFlagRemoved,
			Stable_idx: stableIdx,
		}
	}

	pbRealIndex := make(map[realKey]int, len(pbReals))
	for idx := range pbReals {
		k := makeRealKey(pbReals[idx].Id)
		pbRealIndex[k] = idx
	}

	inheritEffectiveWeights := !wlcChanged
	inheritWRR := !wrrChanged

	prevRealsUnchanged := placeExistingReals(
		pbReals,
		newReals,
		prevReals,
		pbRealIndex,
		inheritEffectiveWeights,
	)
	noNewReals := placeNewReals(
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
	// 3. inheritEffectiveWeights: WLC was not just changed, so effective weights
	//    were inherited from the previous config.
	// 4. inheritWRR: WRR flag was not just changed, so selector logic inherited.
	return prevRealsUnchanged && noNewReals && inheritEffectiveWeights && inheritWRR, nil
}

func (vs *VS) populate(
	agent *Agent,
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
	vs.Flags |= protoVsFlagsToC(pb.Flags, pb.Scheduler)
	if pb.Scheduler == balancerpb.VsScheduler_WRR {
		vs.Flags |= VSFlagRoundRobin
	}

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

	if err := vs.setSessionsTrackers(agent); err != nil {
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

func (vs *VS) flags() *balancerpb.VsFlags {
	return &balancerpb.VsFlags{
		PureL3: vs.Flags&VSFlagPureL3 != 0,
		FixMss: vs.Flags&VSFlagFixMSS != 0,
		Gre:    vs.Flags&VSFlagGRE != 0,
		Ops:    vs.Flags&VSFlagOPS != 0,
	}
}

func (vs *VS) isWRR() bool {
	return vs.Flags&VSFlagRoundRobin != 0
}

func (vs *VS) scheduler() balancerpb.VsScheduler {
	if vs.isWLC() {
		return balancerpb.VsScheduler_WLC
	}
	if vs.isWRR() {
		return balancerpb.VsScheduler_WRR
	}
	return balancerpb.VsScheduler_SH
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
	isV6 := vs.Ip_proto == ipprotoIPv6
	vsState := &balancerpb.VsState{
		Id:                  vs.id(),
		Flags:               vs.flags(),
		Scheduler:           vs.scheduler(),
		Reals:               realsState,
		ActiveSessions:      activeSessions,
		LastPacketTimestamp: timestamppb.New(lastPacketTimestamp),
		AllowedSrcsConfig:   restoreAllowedSources(vs, isV6),
		Peers:               restorePeers(vs),
	}
	return vsState
}

func formatVS(proto balancerpb.TransportProto, addr []byte, port uint32) string {
	protoStr := "TCP"
	if proto == balancerpb.TransportProto_UDP {
		protoStr = "UDP"
	}
	addrStr := net.IP(addr).String()
	if len(addr) == 16 {
		addrStr = fmt.Sprintf("[%s]", addrStr)
	}
	return fmt.Sprintf("%s:%d/%s", addrStr, port, protoStr)
}

func vsIDToString(id *balancerpb.VsIdentifier) string {
	return formatVS(id.Proto, id.Addr, id.Port)
}

func (vs *VS) String() string {
	return formatVS(
		transportProtoToPB(vs.Transport_proto),
		vs.Addr.Bytes(int(vs.Ip_proto)),
		uint32(vs.Port),
	)
}

func (vs *VS) labels() []*commonpb.Label {
	labels := make([]*commonpb.Label, 0, 3)

	vip := vs.Addr.Bytes(int(vs.Ip_proto))
	labels = append(labels, &commonpb.Label{Name: "vip", Value: net.IP(vip).String()})

	port := vs.Port
	labels = append(labels, &commonpb.Label{Name: "vs_port", Value: strconv.Itoa(int(port))})

	proto := "UDP"
	if vs.Transport_proto == ipprotoTCP {
		proto = "TCP"
	}
	labels = append(labels, &commonpb.Label{Name: "proto", Value: proto})

	return labels
}
