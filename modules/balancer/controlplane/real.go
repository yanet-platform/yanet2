package balancer

import (
	"github.com/yanet-platform/yanet2/common/go/relptr"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// realKey is a hashable identifier for a real server, usable as a map key.
type realKey struct {
	ip    [16]byte
	ipLen uint8
}

func makeRealKey(id *balancerpb.RelativeRealIdentifier) realKey {
	var k realKey
	k.ipLen = uint8(len(id.Ip))
	copy(k.ip[:], id.Ip)
	return k
}

func (r *Real) key() realKey {
	var k realKey
	proto := ipprotoIP
	addrLen := 4
	if r.Flags&RealFlagIPv6 != 0 {
		proto = ipprotoIPv6
		addrLen = 16
	}
	k.ipLen = uint8(addrLen)
	copy(k.ip[:], r.Addr.Bytes(proto))
	return k
}

// populate fills a Real from a protobuf definition and optional previous state.
// Precondition: if inheritEffectiveWeight is true, prevReal must be non-nil
// (the effective weight is read from it).
func (r *Real) populate(
	pb *balancerpb.Real,
	stableIdx uint64,
	prevReal *Real,
	inheritEffectiveWeight bool,
) {
	r.Stable_idx = stableIdx
	r.Flags = 0
	r.Weight = uint32(pb.Weight)
	writeNetAddr(&r.Addr, pb.Id.Ip)
	writeNet(&r.Src, pb.Src)
	if len(pb.Id.Ip) == 16 {
		r.Flags |= RealFlagIPv6
	}
	if prevReal != nil && prevReal.Flags&RealFlagEnabled != 0 {
		r.Flags |= RealFlagEnabled
	}
	if inheritEffectiveWeight {
		r.Effective_weight = uint32(prevReal.Effective_weight)
	} else {
		r.Effective_weight = uint32(pb.Weight)
	}
	if prevReal != nil {
		relptr.Equate(&r.Tracker_shards, &prevReal.Tracker_shards)
	}
}

func (r *Real) isRemoved() bool {
	return r.Flags&RealFlagRemoved != 0
}

func (r *Real) isEnabled() bool {
	return r.Flags&RealFlagEnabled != 0
}

func (r *Real) epoch() uint32 {
	return epochOf(r.Stable_idx)
}

func (r *Real) id() *balancerpb.RelativeRealIdentifier {
	addrLen := 4
	if r.Flags&RealFlagIPv6 != 0 {
		addrLen = 16
	}
	return &balancerpb.RelativeRealIdentifier{
		Ip:   r.Addr.Bytes(addrLen),
		Port: 0,
	}
}

func (r *Real) state(workers uint32) *balancerpb.RealState {
	activeSessions, lastPacketTimestamp := r.sessions(workers)
	return &balancerpb.RealState{
		Id:                  r.id(),
		Weight:              uint64(r.Weight),
		EffectiveWeight:     uint64(r.Effective_weight),
		Enabled:             r.isEnabled(),
		ActiveSessions:      activeSessions,
		LastPacketTimestamp: timestamppb.New(lastPacketTimestamp),
	}
}

// placeExistingReals places reals that exist in both previous and new configs into their
// original slot positions in targetReals. Each placed real is deleted from pbRealIndex,
// so after this call pbRealIndex contains only genuinely new reals for placeNewReals.
func placeExistingReals(
	pbReals []*balancerpb.Real,
	targetReals []Real,
	prevReals []Real,
	pbRealIndex map[realKey]int,
	inheritEffectiveWeights bool,
) (realsUnchanged bool) {
	realsUnchanged = true
	for idx := range prevReals {
		prevReal := &prevReals[idx]
		if prevReal.isRemoved() {
			continue
		}
		k := prevReal.key()
		if _, ok := pbRealIndex[k]; !ok {
			realsUnchanged = false
			continue
		}
		configIdx := pbRealIndex[k]
		delete(pbRealIndex, k)

		pbReal := pbReals[configIdx]
		if pbReal.Weight != uint32(prevReal.Weight) {
			realsUnchanged = false
		}
		stableIdx := prevReal.Stable_idx
		targetReals[idx].populate(pbReal, stableIdx, prevReal, inheritEffectiveWeights)
	}
	return realsUnchanged
}

// placeNewReals places genuinely new reals (remaining in pbRealIndex after placeExistingReals)
// into removed (empty) slots in targetReals. New reals have no previous state, so they
// always get prevReal=nil and inheritEffectiveWeight=false.
// Invariant: same slot-availability guarantee as placeNewVS — see its comment.
func placeNewReals(
	pbReals []*balancerpb.Real,
	targetReals []Real,
	prevReals []Real,
	pbRealIndex map[realKey]int,
) (realsUnchanged bool) {
	realsUnchanged = true

	nextRemoved := 0

	for idx := range pbReals {
		k := makeRealKey(pbReals[idx].Id)
		if _, ok := pbRealIndex[k]; !ok {
			continue
		}
		realsUnchanged = false

		for !targetReals[nextRemoved].isRemoved() {
			nextRemoved++
		}

		epoch := uint32(0)
		if nextRemoved < len(prevReals) {
			epoch = prevReals[nextRemoved].epoch() + 1
		}

		stableIdx := makeStableIdx(epoch, uint32(nextRemoved))
		targetReals[nextRemoved].populate(pbReals[idx], stableIdx, nil, false)
	}

	return realsUnchanged
}
