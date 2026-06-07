package fuzzing

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb"
)

// genCorpusText synthesises a deterministic services config with vsCount
// virtual servers, each carrying realsPerVS reals. The textual addresses
// are stable so failed assertions point at concrete VS/real keys.
func genCorpusText(vsCount, realsPerVS int) string {
	var b strings.Builder
	for vs := 1; vs <= vsCount; vs++ {
		b.WriteString("virtual_server 10.0.0.")
		writeInt(&b, vs)
		b.WriteString(" 80 {\n  protocol TCP\n  lvs_sched wrr\n")
		for real := 1; real <= realsPerVS; real++ {
			b.WriteString("  real_server 10.1.")
			writeInt(&b, vs)
			b.WriteString(".")
			writeInt(&b, real)
			b.WriteString(" 8080 { weight 1 }\n")
		}
		b.WriteString("}\n")
	}
	return b.String()
}

func writeInt(b *strings.Builder, v int) {
	if v < 0 {
		b.WriteByte('-')
		v = -v
	}
	if v >= 10 {
		writeInt(b, v/10)
	}
	b.WriteByte(byte('0' + v%10))
}

// newModelFromText parses a synthetic corpus and returns its model.
func newModelFromText(t *testing.T, text string) *Model {
	t.Helper()
	corpus, err := ParseServicesCorpusFromReader("synthetic.conf", strings.NewReader(text))
	require.NoError(t, err)
	return NewModel(corpus)
}

// TestOperationGenerator covers the deterministic seed contract: two
// generators built with the same seed against equivalent models produce
// the same first operation sequence.
func TestOperationGenerator(t *testing.T) {
	const n = 5
	const seed = int64(12345)
	const steps = 25

	text := genCorpusText(10, 6)

	mA := newModelFromText(t, text)
	mB := newModelFromText(t, text)
	genA := NewOperationGenerator(mA, n, seed)
	genB := NewOperationGenerator(mB, n, seed)

	for opNum := uint64(1); opNum <= steps; opNum++ {
		opA := genA.Generate(opNum)
		opB := genB.Generate(opNum)

		require.Equal(t, opA.Type, opB.Type, "op %d type", opNum)
		require.Equal(t, opA.OpNum, opB.OpNum, "op %d num", opNum)
		switch opA.Type {
		case OpUpdate:
			// Bootstrap operation has no payload.
		case OpUpdateVS:
			require.NotNil(t, opA.UpdateVS)
			require.NotNil(t, opB.UpdateVS)
			assert.Equal(t, opA.UpdateVS.Key, opB.UpdateVS.Key, "op %d update_vs key", opNum)
			assert.Equal(
				t,
				opA.UpdateVS.Scheduler,
				opB.UpdateVS.Scheduler,
				"op %d scheduler",
				opNum,
			)
			assert.Equal(t, opA.UpdateVS.Flags, opB.UpdateVS.Flags, "op %d flags", opNum)
			assert.Equal(
				t,
				len(opA.UpdateVS.AllowedSources),
				len(opB.UpdateVS.AllowedSources),
				"op %d acl count",
				opNum,
			)
			assert.Equal(
				t,
				len(opA.UpdateVS.Reals),
				len(opB.UpdateVS.Reals),
				"op %d real count",
				opNum,
			)
		case OpDeleteVS, OpDeleteVSNoop:
			require.NotNil(t, opA.DeleteVS)
			require.NotNil(t, opB.DeleteVS)
			assert.Equal(t, opA.DeleteVS.Keys, opB.DeleteVS.Keys, "op %d delete keys", opNum)
		case OpUpdateReals:
			require.NotNil(t, opA.UpdateReals)
			require.NotNil(t, opB.UpdateReals)
			assert.Equal(t, opA.UpdateReals, opB.UpdateReals, "op %d update_reals payload", opNum)
		}

		require.NoError(t, mA.Apply(opA))
		require.NoError(t, mB.Apply(opB))
	}
}

// TestOperationCadenceDeletePrecedesUpdate locks in the operation
// precedence rule with cold-start bootstrap: op 1 is Update; for N=5,
// ops 2..4 are UpdateReals, op 5 is UpdateVS, and op 10 is DeleteVS (or
// its lower-bound no-op variant), never an UpdateVS.
func TestOperationCadenceDeletePrecedesUpdate(t *testing.T) {
	model := newModelFromText(t, genCorpusText(10, 6))
	gen := NewOperationGenerator(model, 5, 12345)

	op1 := gen.Generate(1)
	assert.Equal(t, OpUpdate, op1.Type, "op 1 must be Update bootstrap")
	require.NoError(t, model.Apply(op1))

	for opNum := uint64(2); opNum <= 4; opNum++ {
		op := gen.Generate(opNum)
		assert.Equal(t, OpUpdateReals, op.Type, "op %d must be UpdateReals", opNum)
		require.NoError(t, model.Apply(op))
	}

	op5 := gen.Generate(5)
	assert.Equal(t, OpUpdateVS, op5.Type, "op 5 must be UpdateVS")
	require.NoError(t, model.Apply(op5))

	for opNum := uint64(6); opNum <= 9; opNum++ {
		op := gen.Generate(opNum)
		assert.Equal(t, OpUpdateReals, op.Type, "op %d must be UpdateReals", opNum)
		require.NoError(t, model.Apply(op))
	}

	op10 := gen.Generate(10)
	assert.True(t,
		op10.Type == OpDeleteVS || op10.Type == OpDeleteVSNoop,
		"op 10 must be DeleteVS or DeleteVS no-op, got %v", op10.Type,
	)
	require.NoError(t, model.Apply(op10))
}

// TestOperationGeneratorBounds drives the generator for at least 1000
// operations and asserts every invariant: active VS count, active real
// subsets, weight envelope, ACL count, and immutable VS identity. The
// lower-bound no-op summary contract is exercised by
// TestOperationGeneratorLowerBoundNoop.
func TestOperationGeneratorBounds(t *testing.T) {
	const vsCount = 12
	const realsPerVS = 8
	const n = uint64(5)
	const steps = uint64(2000)

	text := genCorpusText(vsCount, realsPerVS)
	model := newModelFromText(t, text)
	gen := NewOperationGenerator(model, n, 12345)

	minActive := model.MinActive()
	originalCount := model.OriginalCount()

	for opNum := uint64(1); opNum <= steps; opNum++ {
		op := gen.Generate(opNum)

		switch op.Type {
		case OpUpdate:
			assert.Equal(t, uint64(1), op.OpNum, "bootstrap update must only occur on op 1")
		case OpDeleteVS:
			require.NotNil(t, op.DeleteVS)
			require.NotEmpty(
				t,
				op.DeleteVS.Keys,
				"DeleteVS must carry keys; the no-op variant uses OpDeleteVSNoop",
			)
			for _, k := range op.DeleteVS.Keys {
				require.True(t, model.IsActive(k), "op %d deletes inactive VS %v", opNum, k)
			}

		case OpDeleteVSNoop:
			require.NotNil(t, op.DeleteVS)
			assert.Empty(t, op.DeleteVS.Keys, "no-op must carry empty VS list")
			assert.Equal(t, model.ActiveCount(), op.DeleteVS.ActiveCount)
			assert.Equal(t, minActive, op.DeleteVS.MinActive)

		case OpUpdateVS:
			require.NotNil(t, op.UpdateVS)
			p := op.UpdateVS
			// Identity must match a known original VS.
			orig := model.OriginalVS(p.Key)
			require.NotNil(t, orig, "op %d UpdateVS targets unknown VS %v", opNum, p.Key)
			assert.Equal(t, orig.Key, p.Key, "VS identity is immutable")
			assert.Equal(t, orig.Key.Proto, p.Key.Proto, "proto is immutable")
			// Real subset bounds.
			originalReals := model.OriginalReals(p.Key)
			minReals := model.MinActiveReals(p.Key)
			assert.GreaterOrEqual(t, len(p.Reals), minReals,
				"op %d real subset below 80%% bound for VS %v", opNum, p.Key)
			assert.LessOrEqual(t, len(p.Reals), len(originalReals),
				"op %d real subset exceeds original for VS %v", opNum, p.Key)
			seen := map[RealKey]bool{}
			for _, r := range p.Reals {
				require.False(t, seen[r.Key], "op %d duplicate real %v", opNum, r.Key)
				seen[r.Key] = true
				assert.NotNil(t, model.OriginalReal(p.Key, r.Key),
					"op %d real %v not in original set for VS %v", opNum, r.Key, p.Key)
				assert.GreaterOrEqual(t, r.Weight, uint32(1), "op %d weight under 1", opNum)
				assert.LessOrEqual(t, r.Weight, uint32(30), "op %d weight over 30", opNum)
			}
			minEnabled, maxEnabled := enabledCountBounds(len(p.Reals))
			enabledCount := 0
			for _, r := range p.Reals {
				if r.Enabled {
					enabledCount++
				}
			}
			assert.GreaterOrEqual(
				t,
				enabledCount,
				minEnabled,
				"op %d UpdateVS enabled reals below 40%% for VS %v",
				opNum,
				p.Key,
			)
			assert.LessOrEqual(
				t,
				enabledCount,
				maxEnabled,
				"op %d UpdateVS enabled reals above 60%% for VS %v",
				opNum,
				p.Key,
			)
			// Scheduler must be one of the supported values.
			assert.Contains(t, schedulerChoices, p.Scheduler, "op %d unknown scheduler", opNum)
			// ACL count in 1..5.
			assert.GreaterOrEqual(t, len(p.AllowedSources), 1, "op %d ACL count below 1", opNum)
			assert.LessOrEqual(t, len(p.AllowedSources), 5, "op %d ACL count above 5", opNum)
			for _, cidr := range p.AllowedSources {
				assert.Len(t, cidr.Addr, 4)
				assert.Len(t, cidr.Mask, 4)
			}

		case OpUpdateReals:
			require.NotNil(t, op.UpdateReals)
			p := op.UpdateReals
			if len(p.Batches) == 0 {
				break
			}
			seenVS := map[VsKey]bool{}
			for _, batch := range p.Batches {
				require.False(t, seenVS[batch.Key], "op %d duplicate VS batch", opNum)
				seenVS[batch.Key] = true
				vs := model.ActiveVS(batch.Key)
				require.NotNil(t, vs, "op %d UpdateReals targets inactive VS %v", opNum, batch.Key)
				assert.NotEmpty(
					t,
					batch.Updates,
					"op %d UpdateReals batch must carry updates",
					opNum,
				)
				resultEnabled := 0
				for _, rk := range vs.Reals() {
					real := vs.Real(rk)
					require.NotNil(t, real)
					if real.Enabled {
						resultEnabled++
					}
				}
				for _, u := range batch.Updates {
					assert.True(
						t,
						vs.HasReal(u.Key),
						"op %d updates real %v not in active subset for VS %v",
						opNum,
						u.Key,
						batch.Key,
					)
					if u.Enabled != nil {
						if *u.Enabled {
							if current := vs.Real(u.Key); current != nil && !current.Enabled {
								resultEnabled++
							}
						} else {
							if current := vs.Real(u.Key); current != nil && current.Enabled {
								resultEnabled--
							}
						}
					}
					if u.Weight != nil {
						assert.GreaterOrEqual(t, *u.Weight, uint32(1))
						assert.LessOrEqual(t, *u.Weight, uint32(30))
					}
					assert.True(t, u.Enabled != nil || u.Weight != nil,
						"op %d update emits no observable change", opNum)
				}
				minEnabled, maxEnabled := enabledCountBounds(len(vs.Reals()))
				assert.GreaterOrEqual(
					t,
					resultEnabled,
					minEnabled,
					"op %d UpdateReals result below 40%% enabled for VS %v",
					opNum,
					batch.Key,
				)
				assert.LessOrEqual(
					t,
					resultEnabled,
					maxEnabled,
					"op %d UpdateReals result above 60%% enabled for VS %v",
					opNum,
					batch.Key,
				)
			}
		}

		require.NoError(t, model.Apply(op))

		// Active VS bounds must hold after every commit.
		active := model.ActiveCount()
		assert.GreaterOrEqual(
			t,
			active,
			minActive,
			"active count dropped below 80%% bound at op %d",
			opNum,
		)
		assert.LessOrEqual(t, active, originalCount, "active count exceeded 100%% at op %d", opNum)

		// All active VSes must continue to honour the 80-100% real bound.
		for _, vsKey := range model.ActiveOrder() {
			vs := model.ActiveVS(vsKey)
			require.NotNil(t, vs)
			minReals := model.MinActiveReals(vsKey)
			origReals := len(model.OriginalReals(vsKey))
			assert.GreaterOrEqual(t, len(vs.Reals()), minReals,
				"op %d VS %v real subset under 80%%", opNum, vsKey)
			assert.LessOrEqual(t, len(vs.Reals()), origReals,
				"op %d VS %v real subset exceeds original", opNum, vsKey)
			for _, rk := range vs.Reals() {
				assert.NotNil(t, model.OriginalReal(vsKey, rk),
					"op %d VS %v contains real %v outside original set", opNum, vsKey, rk)
				rs := vs.Real(rk)
				require.NotNil(t, rs)
				assert.GreaterOrEqual(
					t,
					rs.Weight,
					uint32(1),
					"op %d weight under 1 on VS %v real %v",
					opNum,
					vsKey,
					rk,
				)
				assert.LessOrEqual(
					t,
					rs.Weight,
					uint32(30),
					"op %d weight over 30 on VS %v real %v",
					opNum,
					vsKey,
					rk,
				)
			}
		}
	}

}

func TestOperationGeneratorUpdateRealsCoversMultipleVS(t *testing.T) {
	model := newModelFromText(t, genCorpusText(6, 4))
	gen := NewOperationGenerator(model, 10, 12345)

	require.Equal(t, OpUpdate, gen.Generate(1).Type)
	op := gen.Generate(2)
	require.Equal(t, OpUpdateReals, op.Type)
	require.NotNil(t, op.UpdateReals)
	require.GreaterOrEqual(t, len(op.UpdateReals.Batches), 2)

	seen := map[VsKey]bool{}
	for _, batch := range op.UpdateReals.Batches {
		require.NotEmpty(t, batch.Updates)
		seen[batch.Key] = true
	}
	assert.GreaterOrEqual(t, len(seen), 2)
}

func TestOperationGeneratorUpdateVSChangesRealSetWhenPossible(t *testing.T) {
	model := newModelFromText(t, genCorpusText(1, 8))
	gen := NewOperationGenerator(model, 10, 12345)

	require.Equal(t, OpUpdate, gen.Generate(1).Type)
	first := gen.Generate(10)
	require.Equal(t, OpUpdateVS, first.Type)
	require.NotNil(t, first.UpdateVS)
	require.NoError(t, model.Apply(first))

	current := model.ActiveVS(first.UpdateVS.Key)
	require.NotNil(t, current)
	require.Greater(
		t,
		len(model.OriginalReals(first.UpdateVS.Key)),
		model.MinActiveReals(first.UpdateVS.Key),
	)

	second := gen.Generate(30)
	require.Equal(t, OpUpdateVS, second.Type)
	require.NotNil(t, second.UpdateVS)
	assert.False(
		t,
		sameRealKeySet(current.Reals(), realMemberKeys(second.UpdateVS.Reals)),
		"UpdateVS should change the present real set when alternatives exist",
	)
}

func realMemberKeys(in []RealMember) []RealKey {
	out := make([]RealKey, 0, len(in))
	for _, r := range in {
		out = append(out, r.Key)
	}
	return out
}

// TestOperationGeneratorLowerBoundNoop forces the active set to its
// 80% floor by manually deleting VSes, then verifies the generator
// emits the documented no-op DeleteVS (empty key list) and matching
// summary string at the next DeleteVS cadence.
func TestOperationGeneratorLowerBoundNoop(t *testing.T) {
	const vsCount = 10
	const realsPerVS = 4
	const n = uint64(5)

	text := genCorpusText(vsCount, realsPerVS)
	model := newModelFromText(t, text)
	gen := NewOperationGenerator(model, n, 12345)

	minActive := model.MinActive()
	require.Equal(t, 8, minActive, "ceil(80%% of 10) must be 8")

	// Drive active count down to the floor by removing arbitrary keys.
	for model.ActiveCount() > minActive {
		key := model.ActiveOrder()[0]
		require.True(t, model.RemoveActiveVS(key))
	}
	require.Equal(t, minActive, model.ActiveCount())

	op := gen.Generate(2 * n)
	require.Equal(t, OpDeleteVSNoop, op.Type, "at lower bound, DeleteVS cadence must yield a no-op")
	require.NotNil(t, op.DeleteVS)
	assert.Empty(t, op.DeleteVS.Keys)
	assert.Equal(t, minActive, op.DeleteVS.ActiveCount)
	assert.Equal(t, minActive, op.DeleteVS.MinActive)

	summary := DeleteVSNoopSummary(op.DeleteVS.ActiveCount, op.DeleteVS.MinActive)
	assert.Contains(t, summary, "delete_vs noop lower_bound")
	assert.Contains(t, summary, "active=8")
	assert.Contains(t, summary, "min=8")

	require.NoError(t, model.Apply(op), "no-op apply must be a no-op")
	assert.Equal(t, minActive, model.ActiveCount(), "no-op apply must not change the active set")
}

// TestModelInitialState covers the initial VS/real bookkeeping NewModel
// produces. Both the active-set bounds and per-VS real ordering match the
// parser's view, so the downstream comparator can rely on them.
func TestModelInitialState(t *testing.T) {
	model := newModelFromText(t, genCorpusText(5, 4))

	assert.Equal(t, 5, model.OriginalCount())
	assert.Equal(t, 5, model.ActiveCount())
	assert.Equal(t, 4, model.MinActive(), "ceil(80%% of 5) = 4")

	for _, key := range model.OriginalOrder() {
		assert.True(t, model.IsActive(key))
		assert.Equal(t, 4, model.OriginalRealCount(key))
		assert.Equal(t, 4, model.MinActiveReals(key), "ceil(80%% of 4) = 4")

		vs := model.ActiveVS(key)
		require.NotNil(t, vs)
		assert.Equal(t, balancerpb.VsScheduler_WRR, vs.Scheduler)
		assert.Empty(t, vs.AllowedSources)
		assert.Equal(t, 4, len(vs.Reals()))
		for _, rk := range vs.Reals() {
			rs := vs.Real(rk)
			require.NotNil(t, rs)
			assert.True(t, rs.Enabled)
			assert.Equal(t, uint32(1), rs.Weight)
		}
	}
}

// TestModelInitialWeightsUnclamped pins the contract that NewModel
// preserves parser/corpus weights verbatim — including values outside
// the 1..30 envelope. Task 6 startup sends these same weights to
// UpdateConfig, so the expected model must mirror them exactly until a
// generated UpdateVS/UpdateReals overwrites the real. The 1..30
// envelope applies only to generator-emitted updates, which is verified
// separately by TestOperationGeneratorBounds.
func TestModelInitialWeightsUnclamped(t *testing.T) {
	const corpus = `virtual_server 10.0.0.1 80 {
  protocol TCP
  lvs_sched wrr
  real_server 10.1.0.1 8080 { weight 42 }
  real_server 10.1.0.2 8080 { weight 999 }
}
`
	model := newModelFromText(t, corpus)
	key := model.OriginalOrder()[0]
	vs := model.ActiveVS(key)
	require.NotNil(t, vs)

	reals := vs.Reals()
	require.Len(t, reals, 2)
	assert.Equal(t, uint32(42), vs.Real(reals[0]).Weight, "first real must keep parsed weight 42")
	assert.Equal(
		t,
		uint32(999),
		vs.Real(reals[1]).Weight,
		"second real must keep parsed weight 999",
	)

	gen := NewOperationGenerator(model, 1, 12345)
	for opNum := uint64(1); opNum <= 200; opNum++ {
		op := gen.Generate(opNum)
		if op.Type == OpUpdateVS {
			for _, r := range op.UpdateVS.Reals {
				assert.GreaterOrEqual(t, r.Weight, uint32(1), "generated UpdateVS weight under 1")
				assert.LessOrEqual(t, r.Weight, uint32(30), "generated UpdateVS weight over 30")
			}
		}
		if op.Type == OpUpdateReals {
			for _, batch := range op.UpdateReals.Batches {
				for _, u := range batch.Updates {
					if u.Weight != nil {
						assert.GreaterOrEqual(
							t,
							*u.Weight,
							uint32(1),
							"generated UpdateReals weight under 1",
						)
						assert.LessOrEqual(
							t,
							*u.Weight,
							uint32(30),
							"generated UpdateReals weight over 30",
						)
					}
				}
			}
		}
		require.NoError(t, model.Apply(op))
	}
}

// TestModelClone confirms VSState.Clone produces an independent copy: the
// runner relies on this for the copy/apply/commit pattern Task 7 will
// implement.
func TestModelClone(t *testing.T) {
	model := newModelFromText(t, genCorpusText(2, 3))
	key := model.OriginalOrder()[0]
	orig := model.ActiveVS(key)

	clone := orig.Clone()
	clone.Scheduler = balancerpb.VsScheduler_SH
	clone.Flags.Gre = true
	clone.realsByKey[clone.realsOrder[0]].Weight = 7

	assert.Equal(t, balancerpb.VsScheduler_WRR, orig.Scheduler)
	assert.False(t, orig.Flags.Gre)
	assert.Equal(t, uint32(1), orig.Real(orig.Reals()[0]).Weight)
}

// TestDeleteVSNoopSummary pins the exact summary contract the runner emits
// when DeleteVS cadence hits at the lower active-VS bound.
func TestDeleteVSNoopSummary(t *testing.T) {
	s := DeleteVSNoopSummary(8, 8)
	assert.Equal(t, "delete_vs noop lower_bound active=8 min=8", s)
}
