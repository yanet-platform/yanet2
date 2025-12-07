package module

import (
	"fmt"
	"math"
	"time"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

type WlcConfig struct {
	Power          uint64
	MaxRealWeight  uint16
	UpdatePeriodMs uint32
}

func NewWlcConfigFromProto(proto *balancerpb.WlcConfig) (WlcConfig, error) {
	if proto == nil {
		// Return default WLC config when not provided
		return WlcConfig{
			Power:          10,
			MaxRealWeight:  1000,
			UpdatePeriodMs: 500,
		}, nil
	}
	if proto.MaxRealWeight > math.MaxUint16 {
		return WlcConfig{}, fmt.Errorf(
			"max real weight can not exceed %d",
			math.MaxUint16,
		)
	}
	return WlcConfig{
		Power:          proto.WlcPower,
		MaxRealWeight:  uint16(proto.MaxRealWeight),
		UpdatePeriodMs: uint32(proto.UpdatePeriod.AsDuration().Milliseconds()),
	}, nil
}

// IntoProto converts WlcConfig to protobuf message
func (w WlcConfig) IntoProto() *balancerpb.WlcConfig {
	return &balancerpb.WlcConfig{
		WlcPower:      w.Power,
		MaxRealWeight: uint32(w.MaxRealWeight),
		UpdatePeriod:  durationpb.New(time.Duration(w.UpdatePeriodMs) * time.Millisecond),
	}
}

////////////////////////////////////////////////////////////////////////////////

// Calculate effective weights of reals and returns true if some weights changed.
func (vs *VirtualService) UpdateEffectiveWeights(
	wlc *WlcConfig,
	activeSessions map[RealIdentifier]uint,
) bool {
	if vs.Scheduler != SchedulerWLC {
		return false
	}
	connectionsSum := uint64(0)
	weightsSum := uint64(0)
	for realIdx := range vs.Reals {
		real := vs.Reals[realIdx]
		if real.Enabled {
			connectionsSum += uint64(activeSessions[real.Identifier])
			weightsSum += uint64(real.EffectiveWeight)
		}
	}

	updated := false
	for realIdx := range vs.Reals {
		real := vs.Reals[realIdx]
		if real.Enabled {
			newWeight := calcWlcWeight(
				wlc,
				real.Weight,
				activeSessions[real.Identifier],
				weightsSum,
				connectionsSum,
			)

			if real.EffectiveWeight != newWeight {
				real.EffectiveWeight = newWeight
				updated = true
			}
		}
	}

	return updated
}

func calcWlcWeight(
	wlc *WlcConfig,
	weight uint16,
	connections uint,
	weightSum uint64,
	connectionsSum uint64,
) uint16 {
	if weight == 0 || weightSum == 0 || connectionsSum < weightSum {
		return weight
	}

	scaledConnections := uint64(connections) * weightSum
	scaledWeight := uint64(weight) * connectionsSum
	connectionsRatio := float64(scaledConnections) / float64(scaledWeight)

	wlcRatio := float64(wlc.Power) * (1.0 - connectionsRatio)
	if wlcRatio < 1.0 {
		wlcRatio = 1.0
	}

	newWeight := min(
		uint64(float64(weight)*wlcRatio),
		uint64(wlc.MaxRealWeight),
	)
	return uint16(newWeight)
}
