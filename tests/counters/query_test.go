package counters_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
)

// Memory sizes and topology for the query harness.
const (
	queryCPSize    = 32 * datasize.MB
	queryDPSize    = 4 * datasize.MB
	queryAgentSize = 2 * datasize.MB

	queryPipelineCount = 20

	queryPatternMaxLen    = 512
	queryPatternMaxEngine = 64
)

func installQuerySurface(t *testing.T) *ffi.DPConfig {
	t.Helper()

	h, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:      uint64(queryCPSize),
		DPMemory:      uint64(queryDPSize),
		WorkerCount:   1,
		Devices:       []string{"port0"},
		DevicesToLoad: []string{"plain"},
	})
	require.NoError(t, err)
	t.Cleanup(h.Free)

	agent, err := h.SharedMemory().AgentAttach("query-probe", 0, queryAgentSize)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	input := make([]ffi.DevicePipelineConfig, queryPipelineCount)
	for idx := range queryPipelineCount {
		name := fmt.Sprintf("query-pipeline-%d", idx)
		require.NoError(t, agent.UpdatePipeline(ffi.PipelineConfig{Name: name}))
		input[idx] = ffi.DevicePipelineConfig{Name: name, Weight: 1}
	}
	_, err = plain.UpdateDevices(agent, []ffi.DeviceConfig{{
		Name:  "port0",
		Input: input,
	}})
	require.NoError(t, err)

	return h.SharedMemory().DPConfig(0)
}

func queryNames(t *testing.T, dp *ffi.DPConfig, query []string) []string {
	t.Helper()

	groups, err := dp.CountersByTags(nil, query)
	require.NoError(t, err)

	names := make([]string, 0)
	for _, group := range groups {
		for _, counter := range group.Counters {
			names = append(names, counter.Name)
		}
	}

	return names
}

// A pattern selects a subset of the surface and nothing outside it.
func TestCountersByTagsPatternSelectsSubset(t *testing.T) {
	dp := installQuerySurface(t)

	all := queryNames(t, dp, nil)
	require.NotEmpty(t, all, "the installed surface registered no counters")

	require.Equal(t, all, queryNames(t, dp, []string{".*"}),
		"the catch-all pattern must agree with imposing no constraint",
	)

	prefix := all[0][:1]
	selected := queryNames(t, dp, []string{regexp.QuoteMeta(prefix) + ".*"})

	require.NotEmpty(t, selected, "prefix %q selected nothing", prefix)
	require.Less(t, len(selected), len(all),
		"prefix %q selected the whole surface", prefix,
	)
	for _, name := range selected {
		require.True(t, strings.HasPrefix(name, prefix),
			"%q does not start with %q", name, prefix,
		)
	}
}

// An empty pattern list and an absent one are the same request here.
func TestCountersByTagsEmptyQueryMatchesEverything(t *testing.T) {
	dp := installQuerySurface(t)

	all := queryNames(t, dp, nil)
	require.NotEmpty(t, all)

	require.Equal(t, all, queryNames(t, dp, []string{}))
}

// Several patterns combine as alternatives rather than as a conjunction.
func TestCountersByTagsPatternsCombineAsAlternatives(t *testing.T) {
	dp := installQuerySurface(t)

	all := queryNames(t, dp, nil)
	require.NotEmpty(t, all)

	exact := all[0]
	engine := "[a-z_]+"

	onlyExact := queryNames(t, dp, []string{exact})
	onlyEngine := queryNames(t, dp, []string{engine})
	union := queryNames(t, dp, []string{exact, engine})

	require.NotEmpty(t, onlyExact, "exact name %q selected nothing", exact)
	require.GreaterOrEqual(t, len(union), len(onlyExact))
	require.GreaterOrEqual(t, len(union), len(onlyEngine))
	require.LessOrEqual(t, len(union), len(onlyExact)+len(onlyEngine))
	require.LessOrEqual(t, len(union), len(all))
}

// A long list of exact names is still read, only engine patterns being capped.
func TestCountersByTagsAcceptsLongExactNameList(t *testing.T) {
	dp := installQuerySurface(t)

	all := queryNames(t, dp, nil)
	require.Greater(t, len(all), queryPatternMaxEngine,
		"the surface must hold more counters than the engine limit",
	)

	seen := map[string]struct{}{}
	names := make([]string, 0)
	for _, name := range all {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	real := len(names)

	for len(names) <= queryPatternMaxEngine {
		names = append(names, fmt.Sprintf("no_such_counter_%d", len(names)))
	}
	require.Greater(t, len(names), queryPatternMaxEngine)

	require.Equal(t, all, queryNames(t, dp, names),
		"a list of %d exact names (%d of them real) must select the "+
			"whole surface",
		len(names), real,
	)
}

// A query the matcher refuses fails the call rather than returning nothing.
func TestCountersByTagsRejectsBadQuery(t *testing.T) {
	dp := installQuerySurface(t)

	tooManyEngines := make([]string, queryPatternMaxEngine+1)
	for idx := range tooManyEngines {
		tooManyEngines[idx] = "[a-z]+_drop"
	}

	for _, tc := range []struct {
		name  string
		query []string
	}{
		{name: "unbalanced group", query: []string{"acl_(unclosed"}},
		{name: "inverted class", query: []string{"[z-a]"}},
		{
			name:  "over the length limit",
			query: []string{strings.Repeat("a", queryPatternMaxLen+1)},
		},
		{name: "over the engine limit", query: tooManyEngines},
		{name: "embedded NUL", query: []string{"rx\x00.*"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groups, err := dp.CountersByTags(nil, tc.query)
			require.Error(t, err)
			require.ErrorIs(t, err, ffi.ErrInvalidQuery)
			require.Nil(t, groups)
		})
	}
}

// tagFieldLen mirrors the fixed size of the C counter_tag key and value
// fields.
const tagFieldLen = 80

// A tag the fixed-size C fields cannot carry fails the call instead of
// being silently truncated into a predicate for a different tag set.
func TestCountersByTagsRejectsBadTag(t *testing.T) {
	dp := installQuerySurface(t)

	for _, tc := range []struct {
		name string
		tag  ffi.CounterTag
	}{
		{
			name: "over-long key",
			tag:  ffi.CounterTag{Key: strings.Repeat("k", tagFieldLen)},
		},
		{
			name: "over-long value",
			tag: ffi.CounterTag{
				Key:   "device",
				Value: strings.Repeat("v", tagFieldLen),
			},
		},
		{
			name: "embedded NUL",
			tag:  ffi.CounterTag{Key: "device", Value: "port0\x00x"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			groups, err := dp.CountersByTags([]ffi.CounterTag{tc.tag}, nil)
			require.Error(t, err)
			require.ErrorIs(t, err, ffi.ErrInvalidTag)
			require.Nil(t, groups)
		})
	}
}
