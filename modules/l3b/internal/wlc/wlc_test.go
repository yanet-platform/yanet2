package wlc_test

import (
	"fmt"
	"math/big"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/modules/l3b/internal/wlc"
)

// Test_Calculate_ExactVectors verifies that load thresholds, original weights
// and half-up boundaries produce the specified capped outputs.
func Test_Calculate_ExactVectors(t *testing.T) {
	testCases := []struct {
		name     string
		weights  []uint32
		counts   []int64
		power    uint32
		expected []uint32
	}{
		{"empty eligibility", nil, nil, 10, nil},
		{"zero load", []uint32{1, 100, 16777215}, []int64{0, 0, 0}, 10, []uint32{1, 100, 100}},
		{"below total weight", []uint32{10, 10}, []int64{0, 19}, 10, []uint32{10, 10}},
		{"equal total weight", []uint32{10, 10}, []int64{0, 20}, 10, []uint32{100, 10}},
		{"skewed load", []uint32{10, 10}, []int64{0, 30}, 10, []uint32{100, 10}},
		{"below cap half", []uint32{10, 10}, []int64{1, 398}, 10, []uint32{99, 10}},
		{"exact cap half", []uint32{10, 10}, []int64{1, 399}, 10, []uint32{100, 10}},
		{"above cap half", []uint32{10, 10}, []int64{1, 400}, 10, []uint32{100, 10}},
		{"below interior half", []uint32{1, 1}, []int64{1, 2}, 3, []uint32{1, 1}},
		{"exact interior half", []uint32{1, 1}, []int64{1, 3}, 3, []uint32{2, 1}},
		{"above interior half", []uint32{1, 1}, []int64{1, 4}, 3, []uint32{2, 1}},
		{"original maximum weight", []uint32{1, 16777215}, []int64{1, 16777215}, 10, []uint32{1, 100}},
		{"one overloaded real", []uint32{7}, []int64{999}, 100, []uint32{7}},
		{"power one preserves weights", []uint32{10, 10}, []int64{0, 30}, 1, []uint32{10, 10}},
		{"power hundred", []uint32{1, 1}, []int64{0, 30}, 100, []uint32{100, 1}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reals := sample(tc.weights, tc.counts)
			result, err := wlc.Calculate(reals, tc.power)
			require.NoError(t, err)
			require.Len(t, result.Weights, len(reals))
			for idx, real := range reals {
				require.Equal(t, wlc.Weight{Identity: real.Identity, Weight: tc.expected[idx]}, result.Weights[idx])
			}
			require.Equal(t, oracle(reals, tc.power), result.Weights)
		})
	}
}

// Test_Calculate_PowerResolution verifies that requested uint32 power is
// retained while out-of-range requests use ten for arithmetic, even when empty.
func Test_Calculate_PowerResolution(t *testing.T) {
	for _, tc := range []struct {
		requested uint32
		effective uint32
	}{
		{0, 10}, {1, 1}, {2, 2}, {99, 99}, {100, 100},
		{101, 10}, {256, 10}, {1<<16 + 1, 10}, {^uint32(0), 10},
	} {
		t.Run(fmt.Sprint(tc.requested), func(t *testing.T) {
			for _, reals := range [][]wlc.Real{nil, sample([]uint32{1, 1}, []int64{0, 3})} {
				result, err := wlc.Calculate(reals, tc.requested)
				require.NoError(t, err)
				require.Equal(t, tc.requested, result.RequestedPower)
				require.Equal(t, tc.effective, result.EffectivePower)
				if len(reals) != 0 {
					require.Equal(t, tc.effective, result.Weights[0].Weight)
				}
			}
		})
	}
}

// Test_Calculate_InvalidInput verifies that malformed entries reject the whole
// sample, including when preceded by a valid real.
func Test_Calculate_InvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		real wlc.Real
	}{
		{"empty identity", wlc.Real{Weight: 1, Count: big.NewInt(0)}},
		{"duplicate identity", wlc.Real{Identity: "first", Weight: 1, Count: big.NewInt(0)}},
		{"zero weight", wlc.Real{Identity: "second", Count: big.NewInt(0)}},
		{"above maximum weight", wlc.Real{Identity: "second", Weight: 16777216, Count: big.NewInt(0)}},
		{"maximum uint32 weight", wlc.Real{Identity: "second", Weight: ^uint32(0), Count: big.NewInt(0)}},
		{"missing count", wlc.Real{Identity: "second", Weight: 1}},
		{"negative count", wlc.Real{Identity: "second", Weight: 1, Count: big.NewInt(-1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reals := []wlc.Real{{Identity: "first", Weight: 1, Count: big.NewInt(10)}, tc.real}
			before := cloneSample(reals)
			result, err := wlc.Calculate(reals, 10)
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, before, reals)
		})
	}
}

// Test_Calculate_LargeEquivalentCounts verifies that scaling valid loaded
// samples beyond fixed-width arithmetic preserves exact rounding.
func Test_Calculate_LargeEquivalentCounts(t *testing.T) {
	for _, counts := range [][]int64{{0, 30}, {1, 398}, {1, 399}, {1, 400}} {
		for _, shift := range []uint{0, 64, 128, 512} {
			t.Run(fmt.Sprintf("counts_%v_shift_%d", counts, shift), func(t *testing.T) {
				reals := sample([]uint32{10, 10}, counts)
				expected := oracle(reals, 10)
				for _, real := range reals {
					real.Count.Lsh(real.Count, shift)
				}
				before := cloneSample(reals)
				result, err := wlc.Calculate(reals, 10)
				require.NoError(t, err)
				require.Equal(t, expected, result.Weights)
				require.Equal(t, oracle(reals, 10), result.Weights)
				require.Equal(t, before, reals)
			})
		}
	}
}

// Test_Calculate_AssociationAndOwnership verifies that reordered reals retain
// their own outputs and borrowed aliased counts are not modified.
func Test_Calculate_AssociationAndOwnership(t *testing.T) {
	reals := sample([]uint32{3, 7, 13}, []int64{15, 15, 100})
	reals[1].Count = reals[0].Count
	reals[0].Identity = "\x00real/key/incarnation\xff"
	before := cloneSample(reals)
	result, err := wlc.Calculate(reals, 10)
	require.NoError(t, err)
	require.Equal(t, before, reals)
	require.Equal(t, oracle(reals, 10), result.Weights)
	slices.Reverse(reals)
	reordered, err := wlc.Calculate(reals, 10)
	require.NoError(t, err)
	slices.Reverse(result.Weights)
	require.Equal(t, result.Weights, reordered.Weights)
	reals[0].Identity = "changed"
	reals[0].Count.SetInt64(0)
	require.NotEqual(t, reals[0].Identity, reordered.Weights[0].Identity)
}

// Test_Calculate_ExactOracle verifies that a deterministic grid of loads and
// weights agrees with the literal rational formula, including power defaults.
func Test_Calculate_ExactOracle(t *testing.T) {
	for _, firstWeight := range []uint32{1, 2, 10, 99, 100, 101, 16777215} {
		for _, secondWeight := range []uint32{1, 7, 100, 16777215} {
			for _, firstCount := range []int64{0, 1, 19, 100, 16777215} {
				for _, secondCount := range []int64{0, 1, 20, 398, 399, 16777215} {
					for _, power := range []uint32{0, 1, 3, 10, 99, 100, 101, ^uint32(0)} {
						reals := sample([]uint32{firstWeight, secondWeight}, []int64{firstCount, secondCount})
						result, err := wlc.Calculate(reals, power)
						require.NoError(t, err)
						require.Equal(t, oracle(reals, power), result.Weights,
							"weights=%d,%d counts=%d,%d power=%d",
							firstWeight, secondWeight, firstCount, secondCount, power,
						)
					}
				}
			}
		}
	}
}

// sample supplies distinct opaque identities and owned nonnegative counts.
func sample(weights []uint32, counts []int64) []wlc.Real {
	reals := make([]wlc.Real, len(weights))
	for idx, weight := range weights {
		reals[idx] = wlc.Real{Identity: fmt.Sprintf("real-%d", idx), Weight: weight, Count: big.NewInt(counts[idx])}
	}
	return reals
}

// cloneSample snapshots borrowed counters as well as the scalar input fields.
func cloneSample(reals []wlc.Real) []wlc.Real {
	result := slices.Clone(reals)
	for idx, real := range reals {
		if real.Count != nil {
			result[idx].Count = new(big.Int).Set(real.Count)
		}
	}
	return result
}

// oracle evaluates the literal rational formula and rounds by adding one half.
//
// It deliberately does not use the production numerator transformation or its
// quotient/remainder rounding, and resolves power independently.
func oracle(reals []wlc.Real, requestedPower uint32) []wlc.Weight {
	power := int64(10)
	if requestedPower >= 1 && requestedPower <= 100 {
		power = int64(requestedPower)
	}
	totalWeight, totalCount := new(big.Rat), new(big.Rat)
	for _, real := range reals {
		totalWeight.Add(totalWeight, new(big.Rat).SetInt64(int64(real.Weight)))
		totalCount.Add(totalCount, new(big.Rat).SetInt(real.Count))
	}
	result := make([]wlc.Weight, len(reals))
	for idx, real := range reals {
		weight := new(big.Rat).SetInt64(int64(real.Weight))
		value := new(big.Rat).Set(weight)
		if totalCount.Cmp(totalWeight) >= 0 {
			ratio := new(big.Rat).Mul(new(big.Rat).SetInt(real.Count), totalWeight)
			ratio.Quo(ratio, new(big.Rat).Mul(totalCount, weight))
			factor := new(big.Rat).Sub(big.NewRat(1, 1), ratio)
			factor.Mul(factor, big.NewRat(power, 1))
			if factor.Cmp(big.NewRat(1, 1)) > 0 {
				value.Mul(weight, factor)
			}
		}
		value.Add(value, big.NewRat(1, 2))
		rounded := new(big.Int).Quo(value.Num(), value.Denom())
		if rounded.Cmp(big.NewInt(100)) > 0 {
			rounded.SetInt64(100)
		}
		result[idx] = wlc.Weight{Identity: real.Identity, Weight: uint32(rounded.Uint64())}
	}
	return result
}
