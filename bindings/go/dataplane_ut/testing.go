package dataplaneut

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// CounterPath identifies a per-module counter inside the dataplane
// configuration tree.
type CounterPath struct {
	Device     string
	Pipeline   string
	Function   string
	Chain      string
	ModuleType string
	ModuleName string
}

// RequireModuleCounter asserts that the named per-module counter at the
// given path holds wantPackets and wantBytes.
//
// The counter is expected to be size 2: [packets, bytes]. Reads worker 0.
func RequireModuleCounter(
	t *testing.T,
	h *Harness,
	path CounterPath,
	counterName string,
	wantPackets, wantBytes uint64,
) {
	t.Helper()

	counters := h.SharedMemory().DPConfig(0).ModuleCounters(
		path.Device,
		path.Pipeline,
		path.Function,
		path.Chain,
		path.ModuleType,
		path.ModuleName,
		[]string{counterName},
	)

	byName := map[string][]uint64{}
	for _, c := range counters {
		if len(c.Values) > 0 {
			byName[c.Name] = c.Values[0]
		}
	}

	vals, ok := byName[counterName]
	require.True(t, ok, "counter %q not found in module counters", counterName)
	require.GreaterOrEqual(t, len(vals), 2, "counter %q must have at least two values (packets, bytes)", counterName)
	require.Equal(t, wantPackets, vals[0], "counter %q packet count mismatch", counterName)
	require.Equal(t, wantBytes, vals[1], "counter %q byte count mismatch", counterName)
}

// RuleCounters returns the per-rule counters at the given module position,
// optionally filtered by name, read from the module's runtime-kind counter
// storages through the tag query.
func RuleCounters(
	tb testing.TB,
	h *Harness,
	path CounterPath,
	names []string,
) []ffi.CounterInfo {
	tb.Helper()

	groups, err := h.SharedMemory().DPConfig(0).CountersByTags([]ffi.CounterTag{
		{Key: "device", Value: path.Device},
		{Key: "pipeline", Value: path.Pipeline},
		{Key: "function", Value: path.Function},
		{Key: "chain", Value: path.Chain},
		{Key: "module_type", Value: path.ModuleType},
		{Key: "module_name", Value: path.ModuleName},
		{Key: "kind", Value: "runtime"},
	}, names)
	require.NoError(tb, err)

	counters := make([]ffi.CounterInfo, 0)
	for _, group := range groups {
		counters = append(counters, group.Counters...)
	}
	return counters
}

// RequireRuleCounter asserts that the named per-rule counter at the given
// path holds wantPackets and wantBytes.
//
// The counter is expected to be size 2: [packets, bytes]. Reads worker 0.
func RequireRuleCounter(
	t *testing.T,
	h *Harness,
	path CounterPath,
	counterName string,
	wantPackets, wantBytes uint64,
) {
	t.Helper()

	counters := RuleCounters(t, h, path, []string{counterName})

	byName := map[string][]uint64{}
	for _, c := range counters {
		if len(c.Values) > 0 {
			byName[c.Name] = c.Values[0]
		}
	}

	vals, ok := byName[counterName]
	require.True(t, ok, "counter %q not found in rule counters", counterName)
	require.GreaterOrEqual(t, len(vals), 2, "counter %q must have at least two values (packets, bytes)", counterName)
	require.Equal(t, wantPackets, vals[0], "counter %q packet count mismatch", counterName)
	require.Equal(t, wantBytes, vals[1], "counter %q byte count mismatch", counterName)
}

// SingleValueCounters flattens a CounterInfo slice into a map of
// counter name -> first worker's first value.
//
// Suitable for size-1 counters (pipeline, function, chain, device counters).
// For multi-value counters (e.g. module counters with [packets, bytes]),
// use RequireModuleCounter or read counters[idx].Values directly.
func SingleValueCounters(counters []ffi.CounterInfo) map[string]uint64 {
	byName := map[string]uint64{}
	for _, c := range counters {
		if len(c.Values) > 0 && len(c.Values[0]) > 0 {
			byName[c.Name] = c.Values[0][0]
		}
	}
	return byName
}

// ValueCounters flattens a CounterInfo slice into a map of
// counter name -> first worker's values.
func ValueCounters(counters []ffi.CounterInfo) map[string][]uint64 {
	byName := map[string][]uint64{}
	for _, c := range counters {
		if len(c.Values) > 0 && len(c.Values[0]) > 0 {
			byName[c.Name] = c.Values[0]
		}
	}
	return byName
}
