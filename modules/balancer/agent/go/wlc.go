package balancer

import (
	"math"

	"github.com/yanet-platform/yanet2/modules/balancer/agent/go/ffi"
)

func WlcUpdates(
	config *ffi.BalancerManagerConfig,
	graph *ffi.BalancerGraph,
	info *ffi.BalancerInfo,
) []ffi.RealUpdate {
	wlcVs := map[int]bool{}
	for _, vs := range config.Wlc.Vs {
		wlcVs[int(vs)] = true
	}

	updates := []ffi.RealUpdate{}

	for vsIdx := range config.Balancer.Handler.VirtualServices {
		if !wlcVs[vsIdx] {
			continue
		}

		vsConfig := &config.Balancer.Handler.VirtualServices[vsIdx]
		vsGraph := &graph.VirtualServices[vsIdx]
		vsInfo := &info.Vs[vsIdx]

		vsUpdates := vsWlcUpdates(&config.Wlc, vsConfig, vsGraph, vsInfo)
		updates = append(updates, vsUpdates...)
	}

	return updates
}

func vsWlcUpdates(
	wlc *ffi.BalancerManagerWlcConfig,
	vsConfig *ffi.VsConfig,
	vsGraph *ffi.GraphVs,
	vsInfo *ffi.VsInfo,
) []ffi.RealUpdate {
	realsCnt := len(vsConfig.Reals)
	if realsCnt != len(vsGraph.Reals) || realsCnt != len(vsInfo.Reals) {
		panic("invalid reals number")
	}

	connectionsSum := uint64(0)
	weightsSum := uint64(0)
	for idx := range realsCnt {
		if vsGraph.Reals[idx].Enabled {
			connectionsSum += vsInfo.Reals[idx].ActiveSessions
			weightsSum += uint64(vsConfig.Reals[idx].Weight)
		}
	}

	updates := []ffi.RealUpdate{}

	for idx := range realsCnt {
		realConfig := &vsConfig.Reals[idx]
		realGraph := &vsGraph.Reals[idx]
		realInfo := &vsInfo.Reals[idx]

		// Only generate weight updates for enabled reals
		if !realGraph.Enabled {
			continue
		}

		newWeight := calcWlcWeight(
			wlc,
			realConfig.Weight,
			realInfo.ActiveSessions,
			weightsSum,
			connectionsSum,
		)

		if newWeight != realGraph.Weight {
			updates = append(updates, ffi.RealUpdate{
				Identifier: ffi.RealIdentifier{
					Relative:     realConfig.Identifier,
					VsIdentifier: vsConfig.Identifier,
				},
				Weight:  newWeight,
				Enabled: ffi.DontUpdateRealEnabled,
			})
		}
	}

	return updates
}

func calcWlcWeight(
	wlc *ffi.BalancerManagerWlcConfig,
	weight uint16,
	connections uint64,
	weightSum uint64,
	connectionsSum uint64,
) uint16 {
	if weight == 0 || weightSum == 0 || connectionsSum < weightSum {
		return weight
	}

	scaledConnections := float64(connections) * float64(weightSum)
	scaledWeight := float64(connectionsSum) * float64(weight)
	connectionsRatio := scaledConnections / scaledWeight

	const minRatio = 1.0
	wlcRatio := math.Max(minRatio, float64(wlc.Power)*(1.0-connectionsRatio))

	newWeight := uint64(math.Round(float64(weight) * wlcRatio))
	newWeight = min(newWeight, uint64(wlc.MaxRealWeight))

	return uint16(newWeight)
}
