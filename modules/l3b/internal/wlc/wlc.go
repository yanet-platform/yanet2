// Package wlc calculates selector weights from complete eligible real samples.
//
// The caller supplies authoritative logical-session counts and binds each opaque
// identity to its full real key and incarnation within one virtual service.
// Sampling and selector preparation/publication remain separate integration work
// in #2765; physical session-list records are not a logical WLC sample.
package wlc

import (
	"fmt"
	"math/big"
)

// Real carries an eligible real's original weight and exact session count.
//
// Real identities must be nonempty and unique in the sample. Session counts must
// be present and nonnegative; callers must not mutate them during calculation.
type Real struct {
	Identity string
	Weight   uint32
	Count    *big.Int
}

// Weight associates a capped selector weight with the supplied real identity.
type Weight struct {
	Identity string
	Weight   uint32
}

// Result retains requested power separately from its effective value.
type Result struct {
	RequestedPower uint32
	EffectivePower uint32
	Weights        []Weight
}

// Calculate returns exact half-up-rounded weights in input order.
//
// All inputs are borrowed without modification. A malformed entry rejects the
// entire calculation with no result. Empty eligibility returns empty weights.
func Calculate(reals []Real, requestedPower uint32) (*Result, error) {
	effectivePower := requestedPower
	if effectivePower < 1 || effectivePower > 100 {
		effectivePower = 10
	}

	var totalWeight, totalCount, configuredWeight big.Int
	identities := map[string]struct{}{}
	for idx, real := range reals {
		if real.Identity == "" {
			return nil, fmt.Errorf("real %d: missing identity", idx)
		}
		if _, ok := identities[real.Identity]; ok {
			return nil, fmt.Errorf("real %d: duplicate identity", idx)
		}
		identities[real.Identity] = struct{}{}
		if real.Weight < 1 || real.Weight > 16777215 {
			return nil, fmt.Errorf("real %d: weight outside 1..16777215", idx)
		}
		if real.Count == nil || real.Count.Sign() < 0 {
			return nil, fmt.Errorf("real %d: missing or negative count", idx)
		}
		configuredWeight.SetUint64(uint64(real.Weight))
		totalWeight.Add(&totalWeight, &configuredWeight)
		totalCount.Add(&totalCount, real.Count)
	}

	result := &Result{
		RequestedPower: requestedPower,
		EffectivePower: effectivePower,
		Weights:        make([]Weight, len(reals)),
	}
	power := new(big.Int).SetUint64(uint64(effectivePower))
	var base, numerator, weightedCount, quotient, remainder big.Int
	for idx, real := range reals {
		weight := min(real.Weight, 100)
		if totalCount.Cmp(&totalWeight) >= 0 {
			configuredWeight.SetUint64(uint64(real.Weight))
			base.Mul(&totalCount, &configuredWeight)
			weightedCount.Mul(real.Count, &totalWeight)
			numerator.Sub(&base, &weightedCount)
			numerator.Mul(&numerator, power)
			if numerator.Cmp(&base) < 0 {
				numerator.Set(&base)
			}
			// Divide only after the exact numerator's lower bound is applied.
			quotient.QuoRem(&numerator, &totalCount, &remainder)
			remainder.Lsh(&remainder, 1)
			if remainder.Cmp(&totalCount) >= 0 {
				quotient.Add(&quotient, big.NewInt(1))
			}
			// Cap before narrowing, including for arbitrarily large counts.
			if quotient.Cmp(big.NewInt(100)) < 0 {
				weight = uint32(quotient.Uint64())
			} else {
				weight = 100
			}
		}
		result.Weights[idx] = Weight{Identity: real.Identity, Weight: weight}
	}
	return result, nil
}
