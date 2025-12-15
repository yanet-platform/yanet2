package lib

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
		return WlcConfig{}, fmt.Errorf("wlc config is required")
	}

	// validate
	if proto.MaxRealWeight > math.MaxUint16 {
		return WlcConfig{}, fmt.Errorf(
			"max real weight can not exceed %d",
			math.MaxUint16,
		)
	}

	// validate max real weight
	if proto.MaxRealWeight == 0 {
		return WlcConfig{}, fmt.Errorf("max real weight can not be 0")
	}

	// validate update period
	if proto.UpdatePeriod == nil {
		return WlcConfig{}, fmt.Errorf("update period is required")
	}

	// validate power
	if proto.WlcPower == 0 {
		return WlcConfig{}, fmt.Errorf("WLC power can not be 0")
	}

	return WlcConfig{
		Power:         proto.WlcPower,
		MaxRealWeight: uint16(proto.MaxRealWeight),
		UpdatePeriodMs: uint32(
			time.Duration(proto.UpdatePeriod.AsDuration().Milliseconds()),
		),
	}, nil
}

// IntoProto converts WlcConfig to protobuf message
func (w WlcConfig) IntoProto() *balancerpb.WlcConfig {
	return &balancerpb.WlcConfig{
		WlcPower:      w.Power,
		MaxRealWeight: uint32(w.MaxRealWeight),
		UpdatePeriod: durationpb.New(
			time.Duration(w.UpdatePeriodMs) * time.Millisecond,
		),
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
		real := &vs.Reals[realIdx]
		if real.Enabled {
			connectionsSum += uint64(activeSessions[real.Identifier])
			weightsSum += uint64(real.Weight)
		}
	}

	updated := false
	for realIdx := range vs.Reals {
		real := &vs.Reals[realIdx]
		newWeight := uint16(0)
		if real.Enabled {
			newWeight = calcWlcWeight(
				wlc,
				real.Weight,
				activeSessions[real.Identifier],
				weightsSum,
				connectionsSum,
			)
		}
		if real.EffectiveWeight != newWeight {
			real.EffectiveWeight = newWeight
			updated = true
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

	scaledConnections := float64(connections) * float64(weightSum)
	scaledWeight := float64(connectionsSum) * float64(weight)
	connectionsRatio := scaledConnections / scaledWeight

	const minRatio = 1.0
	wlcRatio := math.Max(minRatio, float64(wlc.Power)*(1.0-connectionsRatio))

	newWeight := uint64(math.Round(float64(weight) * wlcRatio))
	newWeight = min(newWeight, uint64(wlc.MaxRealWeight))

	return uint16(newWeight)
}
