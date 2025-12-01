package mbalancer

import "fmt"

type RealWlcInfo struct {
	VsIdx             uint64
	ActiveConnections uint64
	Weight            uint64
	WlcWeight         uint64
	Enabled           bool
}

type Wlc struct {
	// map from real index to real info
	reals         map[uint64]*RealWlcInfo
	power         uint64
	maxRealWeight uint64
}

func NewWlcInfo(power uint64, maxRealWeight uint64) *Wlc {
	return &Wlc{
		reals:         map[uint64]*RealWlcInfo{},
		power:         power,
		maxRealWeight: maxRealWeight,
	}
}

func (wlc *Wlc) UpdateOrRegisterReal(vsIdx uint64, realIdx uint64, weight uint64, connections uint64, enabled bool) {
	wlc.reals[realIdx] = &RealWlcInfo{
		VsIdx:             vsIdx,
		Weight:            weight,
		ActiveConnections: connections,
		WlcWeight:         weight,
		Enabled:           enabled,
	}
}

func (wlc *Wlc) UpdateActiveConnections(realIdx uint64, connections uint64) error {
	_, exists := wlc.reals[realIdx]
	if !exists {
		return fmt.Errorf("real not found")
	}
	real := wlc.reals[realIdx]
	real.ActiveConnections = connections
	return nil
}

func (wlc *Wlc) RecalculateWlcWeights() bool {
	// vsIdx -> connections/weights
	// typically there is only one virtual service, but...
	connectionsSum := map[uint64]uint64{}
	weightsSum := map[uint64]uint64{}
	for _, real := range wlc.reals {
		if real.Enabled {
			connectionsSum[real.VsIdx] += real.ActiveConnections
			weightsSum[real.VsIdx] += real.Weight
		}
	}
	updated := false
	for _, real := range wlc.reals {
		if real.Enabled {
			newWeight := wlc.calcWlcWeight(real.Weight, real.ActiveConnections, weightsSum[real.VsIdx], connectionsSum[real.VsIdx])
			if real.Weight != newWeight {
				real.Weight = newWeight
				updated = true
			}
		}
	}
	return updated
}

func (wlc *Wlc) calcWlcWeight(weight uint64, connections uint64, weightSum uint64, connectionsSum uint64) uint64 {
	if weight == 0 || weightSum == 0 || connectionsSum < weightSum {
		return weight
	}

	scaledConnections := connections * weightSum
	scaledWeight := weight * connectionsSum
	connectionsRatio := float64(scaledConnections) / float64(scaledWeight)

	wlcRatio := float64(wlc.power) * (1.0 - connectionsRatio)
	if wlcRatio < 1.0 {
		wlcRatio = 1.0
	}

	newWeight := min(uint64(float64(weight)*wlcRatio), wlc.maxRealWeight)
	return newWeight
}

func (wlc *Wlc) GetRealWlcWeight(realIdx uint64) uint64 {
	return wlc.reals[realIdx].WlcWeight
}
