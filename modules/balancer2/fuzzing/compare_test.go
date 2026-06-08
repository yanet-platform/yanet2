package fuzzing

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	balancerpb "github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb/v1"
)

// compareTextCorpus is a small two-VS, two-real fixture used across the
// CompareState tests. The IPv4 addresses match what the parser produces
// (canonicalised to 16 bytes via ::ffff:a.b.c.d) so the helper below can
// hand-build matching VsState/RealState protos without depending on the
// controlplane.
const compareTextCorpus = `
virtual_server 10.0.0.1 80 {
  protocol TCP
  lvs_sched wrr
  real_server 10.1.1.1 8080 { weight 3 }
  real_server 10.1.1.2 8080 { weight 5 }
}
virtual_server 10.0.0.2 81 {
  protocol UDP
  lvs_sched wlc
  real_server 10.2.2.1 9090 { weight 7 }
}
`

const compareRuntimeConfigName = "balancer2-fuzz"

// newCompareModel parses the shared fixture and returns the resulting
// model. The package-level corpus is deliberately small so the helper
// stays cheap to call from every subtest.
func newCompareModel(t *testing.T) *Model {
	t.Helper()
	corpus, err := ParseServicesCorpusFromReader(
		"compare.conf", strings.NewReader(compareTextCorpus),
	)
	require.NoError(t, err)
	return NewModel(corpus)
}

// newCompareConfig returns a RuntimeConfig whose ConfigName matches the
// fixture used for happy-path comparisons. Other fields are filled with
// any positive value that satisfies Validate(); the comparator only
// reads ConfigName.
func newCompareConfig() *RuntimeConfig {
	return &RuntimeConfig{
		ConfigName: compareRuntimeConfigName,
	}
}

// ipv4 returns the 4-byte big-endian representation of an IPv4 address
// expressed as four decimals. The controlplane echoes the original 4-byte
// form back through GetState; the comparator canonicalises to 16 bytes
// before matching, so the helper does the same on the test side only when
// it needs to compare model keys directly.
func ipv4(a, b, c, d byte) []byte {
	return []byte{a, b, c, d}
}

// buildVsState constructs a balancerpb.VsState the comparator would
// accept as the actual representation of one model VS. The reals slice
// must be supplied separately so individual tests can vary it.
func buildVsState(
	addr []byte,
	port uint32,
	proto balancerpb.TransportProto,
	scheduler balancerpb.VsScheduler,
	flags *balancerpb.VsFlags,
	reals []*balancerpb.RealState,
) *balancerpb.VsState {
	return &balancerpb.VsState{
		Config: &balancerpb.VsConfig{
			Id: &balancerpb.VsIdentifier{
				Addr:  addr,
				Port:  port,
				Proto: proto,
			},
			Scheduler: scheduler,
			Flags:     flags,
		},
		Reals: reals,
	}
}

// buildRealState constructs a balancerpb.RealState mirroring a model
// real. The active_sessions and stats counters are deliberately set so
// tests can prove the comparator ignores them.
func buildRealState(ip []byte, port uint32, weight uint64, enabled bool) *balancerpb.RealState {
	w := uint32(weight)
	return &balancerpb.RealState{
		Config: &balancerpb.RealConfig{
			Id: &balancerpb.RelativeRealIdentifier{
				Ip:   ip,
				Port: port,
			},
			Weight:  &w,
			Enabled: &enabled,
		},
		EffectiveWeight:     weight,
		Enabled:             enabled,
		ActiveSessions:      99,
		LastPacketTimestamp: timestamppb.Now(),
		Stats: &balancerpb.RealStats{
			Packets:         12345,
			CreatedSessions: 7,
		},
	}
}

// buildMatchingResponse returns a GetStateResponse whose single
// BalancerState mirrors the compareTextCorpus model exactly: both
// virtual servers active, all reals enabled at their corpus weights, and
// zero-valued flags. Tests can mutate the result to introduce a single
// deliberate divergence.
func buildMatchingResponse() *balancerpb.GetStateResponse {
	vs1 := buildVsState(
		ipv4(10, 0, 0, 1), 80, balancerpb.TransportProto_TCP,
		balancerpb.VsScheduler_WRR, nil,
		[]*balancerpb.RealState{
			buildRealState(ipv4(10, 1, 1, 1), 8080, 3, true),
			buildRealState(ipv4(10, 1, 1, 2), 8080, 5, true),
		},
	)
	vs2 := buildVsState(
		ipv4(10, 0, 0, 2), 81, balancerpb.TransportProto_UDP,
		balancerpb.VsScheduler_WLC, nil,
		[]*balancerpb.RealState{
			buildRealState(ipv4(10, 2, 2, 1), 9090, 7, true),
		},
	)
	return &balancerpb.GetStateResponse{
		States: []*balancerpb.BalancerState{
			{
				ConfigName:          compareRuntimeConfigName,
				SessionsStateName:   "sessions",
				ActiveSessions:      4242,
				LastPacketTimestamp: timestamppb.Now(),
				Vs:                  []*balancerpb.VsState{vs1, vs2},
				L4Stats: &balancerpb.L4Stats{
					IncomingPackets: 100,
					OutgoingPackets: 99,
				},
			},
		},
	}
}

// TestStateCompare is the umbrella acceptance test: it covers all
// state-count and config-name diagnostics plus the field-level
// extra/missing/divergence cases the plan calls out.
func TestStateCompare(t *testing.T) {
	t.Run("matches", func(t *testing.T) {
		paths := CompareState(newCompareConfig(), newCompareModel(t), buildMatchingResponse())
		assert.Empty(t, paths, "expected no mismatch paths, got %v", paths)
	})

	t.Run("zero_states", func(t *testing.T) {
		resp := &balancerpb.GetStateResponse{}
		paths := CompareState(newCompareConfig(), newCompareModel(t), resp)
		require.NotEmpty(t, paths)
		assert.Contains(t, paths[0], "balancer_state_count")
		assert.Contains(t, paths[0], "actual=0")
	})

	t.Run("multiple_states", func(t *testing.T) {
		resp := &balancerpb.GetStateResponse{
			States: []*balancerpb.BalancerState{
				{ConfigName: compareRuntimeConfigName},
				{ConfigName: compareRuntimeConfigName},
			},
		}
		paths := CompareState(newCompareConfig(), newCompareModel(t), resp)
		require.NotEmpty(t, paths)
		assert.Contains(t, paths[0], "balancer_state_count")
		assert.Contains(t, paths[0], "actual=2")
	})

	t.Run("nil_response", func(t *testing.T) {
		paths := CompareState(newCompareConfig(), newCompareModel(t), nil)
		require.NotEmpty(t, paths)
		assert.Contains(t, paths[0], "balancer_state_count")
	})

	t.Run("config_name_mismatch", func(t *testing.T) {
		resp := buildMatchingResponse()
		resp.States[0].ConfigName = "different-name"
		paths := CompareState(newCompareConfig(), newCompareModel(t), resp)
		require.NotEmpty(t, paths)
		assert.Contains(t, paths[0], "config_name")
		assert.Contains(t, paths[0], "different-name")
		assert.Contains(t, paths[0], compareRuntimeConfigName)
	})

	t.Run("extra_vs", func(t *testing.T) {
		resp := buildMatchingResponse()
		extra := buildVsState(
			ipv4(10, 0, 0, 99), 8080, balancerpb.TransportProto_TCP,
			balancerpb.VsScheduler_WRR, nil, nil,
		)
		resp.States[0].Vs = append(resp.States[0].Vs, extra)
		paths := CompareState(newCompareConfig(), newCompareModel(t), resp)
		require.NotEmpty(t, paths)
		assert.Truef(t, pathsContainPrefix(paths, "extra_vs["),
			"expected an extra_vs path in %v", paths)
	})

	t.Run("missing_vs", func(t *testing.T) {
		resp := buildMatchingResponse()
		resp.States[0].Vs = resp.States[0].Vs[:1]
		paths := CompareState(newCompareConfig(), newCompareModel(t), resp)
		require.NotEmpty(t, paths)
		assert.Truef(t, pathsContainPrefix(paths, "missing_vs["),
			"expected a missing_vs path in %v", paths)
	})

	t.Run("missing_real", func(t *testing.T) {
		resp := buildMatchingResponse()
		// Drop the second real of the first VS.
		resp.States[0].Vs[0].Reals = resp.States[0].Vs[0].Reals[:1]
		paths := CompareState(newCompareConfig(), newCompareModel(t), resp)
		require.NotEmpty(t, paths)
		assert.Truef(t, pathsContainSubstring(paths, "missing_real["),
			"expected a missing_real path in %v", paths)
	})

	t.Run("extra_real", func(t *testing.T) {
		resp := buildMatchingResponse()
		resp.States[0].Vs[0].Reals = append(resp.States[0].Vs[0].Reals,
			buildRealState(ipv4(10, 9, 9, 9), 8080, 1, true))
		paths := CompareState(newCompareConfig(), newCompareModel(t), resp)
		require.NotEmpty(t, paths)
		assert.Truef(t, pathsContainSubstring(paths, "extra_real["),
			"expected an extra_real path in %v", paths)
	})

	t.Run("enabled_mismatch", func(t *testing.T) {
		resp := buildMatchingResponse()
		resp.States[0].Vs[0].Reals[0].Enabled = false
		resp.States[0].Vs[0].Reals[0].Config.Enabled = boolPtr(false)
		paths := CompareState(newCompareConfig(), newCompareModel(t), resp)
		require.NotEmpty(t, paths)
		assert.Truef(t, pathsContainSubstring(paths, ".enabled:"),
			"expected an enabled mismatch path in %v", paths)
	})

	t.Run("scheduler_mismatch", func(t *testing.T) {
		resp := buildMatchingResponse()
		resp.States[0].Vs[0].Config.Scheduler = balancerpb.VsScheduler_SH
		paths := CompareState(newCompareConfig(), newCompareModel(t), resp)
		require.NotEmpty(t, paths)
		assert.Truef(t, pathsContainSubstring(paths, ".scheduler:"),
			"expected a scheduler mismatch path in %v", paths)
	})

	t.Run("flag_mismatch", func(t *testing.T) {
		resp := buildMatchingResponse()
		resp.States[0].Vs[0].Config.Flags = &balancerpb.VsFlags{Gre: true}
		paths := CompareState(newCompareConfig(), newCompareModel(t), resp)
		require.NotEmpty(t, paths)
		assert.Truef(t, pathsContainSubstring(paths, ".flags.gre:"),
			"expected a flags.gre mismatch path in %v", paths)
	})
}

// TestStateCompareIgnoresOrderingAndCounters verifies the comparator
// normalises ordering on both the VS and real axes and ignores all the
// counter/timestamp noise the controlplane decorates the response with.
func TestStateCompareIgnoresOrderingAndCounters(t *testing.T) {
	resp := buildMatchingResponse()

	// Reorder VSes (swap the two top-level entries).
	state := resp.States[0]
	state.Vs[0], state.Vs[1] = state.Vs[1], state.Vs[0]

	// Reorder reals inside the (formerly) first VS, which is now at index 1.
	reals := state.Vs[1].Reals
	reals[0], reals[1] = reals[1], reals[0]

	// Aggressive counter/timestamp noise that must not affect the result.
	state.ActiveSessions = 7777
	state.LastPacketTimestamp = timestamppb.Now()
	state.L4Stats = &balancerpb.L4Stats{IncomingPackets: 9999}
	state.CommonStats = &balancerpb.CommonStats{IncomingBytes: 12345}
	for _, vs := range state.Vs {
		vs.ActiveSessions = 1234
		vs.LastPacketTimestamp = timestamppb.Now()
		vs.Stats = &balancerpb.VsStats{
			IncomingPackets: 42,
			CreatedSessions: 5,
		}
		vs.AllowedSourcesStats = nil
		for _, real := range vs.Reals {
			real.ActiveSessions = 17
			real.LastPacketTimestamp = timestamppb.Now()
			real.Stats = &balancerpb.RealStats{
				Packets:         99,
				CreatedSessions: 1,
			}
		}
	}

	paths := CompareState(newCompareConfig(), newCompareModel(t), resp)
	assert.Empty(t, paths, "expected no mismatch paths, got %v", paths)
}

// TestStateCompareReportsWeightMismatch enforces the documented
// field-level diagnostic: an effective_weight divergence must be visible
// in the path string along with the offending real's identifier.
func TestStateCompareReportsWeightMismatch(t *testing.T) {
	model := newCompareModel(t)
	resp := buildMatchingResponse()

	// Mutate the effective_weight of the very first real (10.1.1.1:8080
	// belonging to 10.0.0.1:80/TCP). The model still expects the parsed
	// weight (3); the controlplane reports 99.
	resp.States[0].Vs[0].Reals[0].EffectiveWeight = 99

	paths := CompareState(newCompareConfig(), model, resp)
	require.NotEmpty(t, paths)

	// The diagnostic must identify both the field and the real.
	require.Truef(t, pathsContainSubstring(paths, ".effective_weight:"),
		"expected an effective_weight mismatch path in %v", paths)
	require.Truef(t, pathsContainSubstring(paths, "10.1.1.1:8080"),
		"expected real key in mismatch path in %v", paths)
	require.Truef(t, pathsContainSubstring(paths, "expected=3"),
		"expected expected=3 in mismatch path in %v", paths)
	require.Truef(t, pathsContainSubstring(paths, "actual=99"),
		"expected actual=99 in mismatch path in %v", paths)
}

// TestStateCompareErrorWrapsPaths exercises the convenience error
// wrapper that downstream callers may prefer over the slice form.
func TestStateCompareErrorWrapsPaths(t *testing.T) {
	t.Run("nil_on_match", func(t *testing.T) {
		err := CompareStateError(newCompareConfig(), newCompareModel(t), buildMatchingResponse())
		assert.NoError(t, err)
	})
	t.Run("error_on_mismatch", func(t *testing.T) {
		resp := buildMatchingResponse()
		resp.States[0].ConfigName = "wrong"
		err := CompareStateError(newCompareConfig(), newCompareModel(t), resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config_name")
	})
}

// TestStateCompareNilArgs guards the nil-input branches the runner
// should never hit but the package owes a safety net.
func TestStateCompareNilArgs(t *testing.T) {
	t.Run("nil_config", func(t *testing.T) {
		paths := CompareState(nil, newCompareModel(t), buildMatchingResponse())
		require.NotEmpty(t, paths)
		assert.Contains(t, paths[0], "runtime_config")
	})
	t.Run("nil_model", func(t *testing.T) {
		paths := CompareState(newCompareConfig(), nil, buildMatchingResponse())
		require.NotEmpty(t, paths)
		assert.Contains(t, paths[0], "model")
	})
}

// TestStateCompareUnclampedInitialWeight pins the carry-forward from
// Task 3: corpus weights may exceed the generator's 1..30 envelope, and
// the comparator must compare the actual model value, not assume an
// upper bound.
func TestStateCompareUnclampedInitialWeight(t *testing.T) {
	const text = `
virtual_server 10.0.0.1 80 {
  protocol TCP
  lvs_sched wrr
  real_server 10.1.1.1 8080 { weight 42 }
  real_server 10.1.1.2 8080 { weight 999 }
}
`
	corpus, err := ParseServicesCorpusFromReader("big.conf", strings.NewReader(text))
	require.NoError(t, err)
	model := NewModel(corpus)

	resp := &balancerpb.GetStateResponse{
		States: []*balancerpb.BalancerState{{
			ConfigName: compareRuntimeConfigName,
			Vs: []*balancerpb.VsState{
				buildVsState(
					ipv4(10, 0, 0, 1), 80, balancerpb.TransportProto_TCP,
					balancerpb.VsScheduler_WRR, nil,
					[]*balancerpb.RealState{
						buildRealState(ipv4(10, 1, 1, 1), 8080, 42, true),
						buildRealState(ipv4(10, 1, 1, 2), 8080, 999, true),
					},
				),
			},
		}},
	}
	paths := CompareState(newCompareConfig(), model, resp)
	assert.Empty(t, paths, "expected no mismatch for unclamped initial weights, got %v", paths)
}

// TestStateCompareAllowedSourcesCount covers the count-based ACL check.
// The model carries 2 CIDRs but the response reports 1 stats entry —
// when both sides are non-zero and disagree the comparator flags it.
func TestStateCompareAllowedSourcesCount(t *testing.T) {
	model := newCompareModel(t)
	vs := model.ActiveVS(model.ActiveOrder()[0])
	vs.AllowedSources = []CIDR{
		{Addr: []byte{10, 0, 0, 0}, Mask: []byte{0xff, 0xff, 0xff, 0}},
		{Addr: []byte{10, 1, 0, 0}, Mask: []byte{0xff, 0xff, 0, 0}},
	}

	t.Run("count_mismatch_both_populated", func(t *testing.T) {
		resp := buildMatchingResponse()
		resp.States[0].Vs[0].AllowedSourcesStats = []*balancerpb.AllowedSourcesStats{
			{Tag: "first", Passes: 1},
		}
		paths := CompareState(newCompareConfig(), model, resp)
		require.Truef(t, pathsContainSubstring(paths, ".allowed_sources_count:"),
			"expected allowed_sources_count diagnostic in %v", paths)
	})

	t.Run("populated_model_empty_stats_is_tolerated", func(t *testing.T) {
		// Counter entries lag behind config in a no-traffic run, so the
		// comparator must not flag this even though the model has 2 CIDRs.
		resp := buildMatchingResponse()
		resp.States[0].Vs[0].AllowedSourcesStats = nil
		paths := CompareState(newCompareConfig(), model, resp)
		assert.Falsef(t, pathsContainSubstring(paths, "allowed_sources_count"),
			"unexpected allowed_sources_count diagnostic in %v", paths)
	})

	t.Run("empty_model_populated_stats_flagged", func(t *testing.T) {
		freshModel := newCompareModel(t)
		resp := buildMatchingResponse()
		resp.States[0].Vs[0].AllowedSourcesStats = []*balancerpb.AllowedSourcesStats{
			{Tag: "x", Passes: 1},
		}
		paths := CompareState(newCompareConfig(), freshModel, resp)
		require.Truef(t, pathsContainSubstring(paths, ".allowed_sources_count:"),
			"expected allowed_sources_count diagnostic in %v", paths)
	})
}

// TestStateCompareInvalidIdentityIsDiagnosed exercises malformed
// addresses on the response side. The comparator should report the
// invalid_identity path rather than panic or silently drop the VS.
func TestStateCompareInvalidIdentityIsDiagnosed(t *testing.T) {
	resp := buildMatchingResponse()
	resp.States[0].Vs[0].Config.Id.Addr = []byte{1, 2, 3} // 3 bytes is illegal.
	paths := CompareState(newCompareConfig(), newCompareModel(t), resp)
	require.NotEmpty(t, paths)
	assert.Truef(t, pathsContainSubstring(paths, "invalid_identity"),
		"expected invalid_identity diagnostic in %v", paths)
}

// pathsContainPrefix reports whether any path in s starts with prefix.
func pathsContainPrefix(s []string, prefix string) bool {
	for _, p := range s {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// pathsContainSubstring reports whether any path in s contains needle.
func pathsContainSubstring(s []string, needle string) bool {
	for _, p := range s {
		if strings.Contains(p, needle) {
			return true
		}
	}
	return false
}

func boolPtr(v bool) *bool { return &v }
