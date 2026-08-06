package metrics_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/metrics"
)

// nameValue is a minimal stand-in for a caller's tag or label type, used
// to exercise the package's generic constraints without depending on any
// concrete wire type.
type nameValue struct {
	name  string
	value string
}

func (m nameValue) GetName() string  { return m.name }
func (m nameValue) GetValue() string { return m.value }

// labeled is a minimal stand-in for a caller's metric type, used to
// exercise Filter without depending on any concrete wire type.
type labeled struct {
	name   string
	labels []nameValue
}

func (m labeled) GetLabels() []nameValue { return m.labels }

// TestMatches verifies the predicate semantics of a single tag and
// their combination with logical AND across several tags.
func TestMatches(t *testing.T) {
	tests := []struct {
		name     string
		labels   []nameValue
		tags     []nameValue
		expected bool
	}{
		{
			name:     "no tags passes any metric",
			labels:   []nameValue{{name: "device", value: "eth0"}},
			tags:     nil,
			expected: true,
		},
		{
			name:     "exact value match passes",
			labels:   []nameValue{{name: "device", value: "eth0"}},
			tags:     []nameValue{{name: "device", value: "eth0"}},
			expected: true,
		},
		{
			name:     "exact value mismatch fails",
			labels:   []nameValue{{name: "device", value: "eth0"}},
			tags:     []nameValue{{name: "device", value: "eth1"}},
			expected: false,
		},
		{
			name:     "wildcard passes when label is present",
			labels:   []nameValue{{name: "device", value: "eth0"}},
			tags:     []nameValue{{name: "device", value: "*"}},
			expected: true,
		},
		{
			name:     "wildcard fails when label is absent",
			labels:   nil,
			tags:     []nameValue{{name: "device", value: "*"}},
			expected: false,
		},
		{
			name:     "empty value passes when label is absent",
			labels:   nil,
			tags:     []nameValue{{name: "device", value: ""}},
			expected: true,
		},
		{
			name:     "empty value fails when label is present",
			labels:   []nameValue{{name: "device", value: "eth0"}},
			tags:     []nameValue{{name: "device", value: ""}},
			expected: false,
		},
		{
			name:     "empty value fails when label is present with an empty value",
			labels:   []nameValue{{name: "device", value: ""}},
			tags:     []nameValue{{name: "device", value: ""}},
			expected: false,
		},
		{
			name:     "wildcard passes when label is present with an empty value",
			labels:   []nameValue{{name: "device", value: ""}},
			tags:     []nameValue{{name: "device", value: "*"}},
			expected: true,
		},
		{
			name: "all tags must pass",
			labels: []nameValue{
				{name: "device", value: "eth0"},
				{name: "pipeline", value: "main"},
			},
			tags: []nameValue{
				{name: "device", value: "eth0"},
				{name: "pipeline", value: "main"},
			},
			expected: true,
		},
		{
			name: "a single failing tag rejects the metric",
			labels: []nameValue{
				{name: "device", value: "eth0"},
				{name: "pipeline", value: "main"},
			},
			tags: []nameValue{
				{name: "device", value: "eth0"},
				{name: "pipeline", value: "backup"},
			},
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.expected, metrics.Matches(test.labels, test.tags))
		})
	}
}

// TestFilter verifies that Filter selects the matching subset of metrics
// while preserving their relative order.
func TestFilter(t *testing.T) {
	eth0 := labeled{name: "packets", labels: []nameValue{{name: "device", value: "eth0"}}}
	eth1 := labeled{name: "packets", labels: []nameValue{{name: "device", value: "eth1"}}}
	noDevice := labeled{name: "packets"}

	metricsList := []labeled{eth0, eth1, noDevice}

	tests := []struct {
		name     string
		tags     []nameValue
		expected []labeled
	}{
		{
			name:     "no tags returns everything",
			tags:     nil,
			expected: metricsList,
		},
		{
			name:     "exact match returns only the matching subset in order",
			tags:     []nameValue{{name: "device", value: "eth1"}},
			expected: []labeled{eth1},
		},
		{
			name:     "no match returns a non-nil empty slice",
			tags:     []nameValue{{name: "device", value: "eth2"}},
			expected: []labeled{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := metrics.Filter(metricsList, test.tags)

			require.NotNil(t, result)
			require.Equal(t, test.expected, result)
		})
	}
}

// TestQuery verifies the counter-name include-list derivation from the
// counter tags attached to a metrics request, across every combination of
// structural counters, enumerable entry counters, and an unknown entry
// family.
func TestQuery(t *testing.T) {
	structural := []string{"rx", "tx"}
	entry := []string{"acl_rule", "acl_rule2"}

	tests := []struct {
		name          string
		tags          []nameValue
		opts          []metrics.QueryOption
		expectedNames []string
		expectedRead  bool
	}{
		{
			name:          "no options and no tag is not readable",
			tags:          nil,
			opts:          nil,
			expectedNames: nil,
			expectedRead:  false,
		},
		{
			name:          "no counter tag unions structural and entry names",
			tags:          nil,
			opts:          []metrics.QueryOption{metrics.WithStructuralCounters(structural), metrics.WithEntryCounters(entry)},
			expectedNames: []string{"rx", "tx", "acl_rule", "acl_rule2"},
			expectedRead:  true,
		},
		{
			name:          "no counter tag with only entry counters reads the entry set",
			tags:          nil,
			opts:          []metrics.QueryOption{metrics.WithEntryCounters(entry)},
			expectedNames: entry,
			expectedRead:  true,
		},
		{
			name:          "no counter tag with only structural counters reads the structural set",
			tags:          nil,
			opts:          []metrics.QueryOption{metrics.WithStructuralCounters(structural)},
			expectedNames: structural,
			expectedRead:  true,
		},
		{
			name:          "no counter tag with an unknown entry family among enumerable counters is unrestricted",
			tags:          nil,
			opts:          []metrics.QueryOption{metrics.WithStructuralCounters(structural), metrics.WithEntryCounters(entry), metrics.WithUnknownEntryCounters()},
			expectedNames: nil,
			expectedRead:  true,
		},
		{
			name:          "absent counter reads the structural set",
			tags:          []nameValue{{name: "counter", value: ""}},
			opts:          []metrics.QueryOption{metrics.WithStructuralCounters(structural), metrics.WithEntryCounters(entry)},
			expectedNames: structural,
			expectedRead:  true,
		},
		{
			name:          "absent counter with no structural names skips the read",
			tags:          []nameValue{{name: "counter", value: ""}},
			opts:          []metrics.QueryOption{metrics.WithEntryCounters(entry)},
			expectedNames: nil,
			expectedRead:  false,
		},
		{
			name:          "wildcard counter reads the entry set in full",
			tags:          []nameValue{{name: "counter", value: "*"}},
			opts:          []metrics.QueryOption{metrics.WithStructuralCounters(structural), metrics.WithEntryCounters(entry)},
			expectedNames: entry,
			expectedRead:  true,
		},
		{
			name:          "wildcard counter with an empty entry set is not readable",
			tags:          []nameValue{{name: "counter", value: "*"}},
			opts:          []metrics.QueryOption{metrics.WithStructuralCounters(structural)},
			expectedNames: nil,
			expectedRead:  false,
		},
		{
			name:          "exact counter naming an entry member reads only that name",
			tags:          []nameValue{{name: "counter", value: "acl_rule"}},
			opts:          []metrics.QueryOption{metrics.WithStructuralCounters(structural), metrics.WithEntryCounters(entry)},
			expectedNames: []string{"acl_rule"},
			expectedRead:  true,
		},
		{
			name:          "exact counter naming a non-member is not readable",
			tags:          []nameValue{{name: "counter", value: "rx"}},
			opts:          []metrics.QueryOption{metrics.WithStructuralCounters(structural), metrics.WithEntryCounters(entry)},
			expectedNames: nil,
			expectedRead:  false,
		},
		{
			name: "conflicting exact counters are unsatisfiable",
			tags: []nameValue{
				{name: "counter", value: "acl_rule"},
				{name: "counter", value: "acl_rule2"},
			},
			opts:          []metrics.QueryOption{metrics.WithEntryCounters(entry)},
			expectedNames: nil,
			expectedRead:  false,
		},
		{
			name: "duplicate exact counters reduce to that name",
			tags: []nameValue{
				{name: "counter", value: "acl_rule"},
				{name: "counter", value: "acl_rule"},
			},
			opts:          []metrics.QueryOption{metrics.WithEntryCounters(entry)},
			expectedNames: []string{"acl_rule"},
			expectedRead:  true,
		},
		{
			name: "wildcard combined with an exact value reduces to that value",
			tags: []nameValue{
				{name: "counter", value: "*"},
				{name: "counter", value: "acl_rule"},
			},
			opts:          []metrics.QueryOption{metrics.WithEntryCounters(entry)},
			expectedNames: []string{"acl_rule"},
			expectedRead:  true,
		},
		{
			name: "absent combined with an exact value is unsatisfiable",
			tags: []nameValue{
				{name: "counter", value: ""},
				{name: "counter", value: "acl_rule"},
			},
			opts:          []metrics.QueryOption{metrics.WithEntryCounters(entry)},
			expectedNames: nil,
			expectedRead:  false,
		},
		{
			name:          "unknown entry collector with no tag is unrestricted",
			tags:          nil,
			opts:          []metrics.QueryOption{metrics.WithStructuralCounters(structural), metrics.WithUnknownEntryCounters()},
			expectedNames: nil,
			expectedRead:  true,
		},
		{
			name:          "unknown entry collector with a wildcard is unrestricted",
			tags:          []nameValue{{name: "counter", value: "*"}},
			opts:          []metrics.QueryOption{metrics.WithUnknownEntryCounters()},
			expectedNames: nil,
			expectedRead:  true,
		},
		{
			name:          "unknown entry collector with an exact tag reads only that name",
			tags:          []nameValue{{name: "counter", value: "unregistered_rule"}},
			opts:          []metrics.QueryOption{metrics.WithUnknownEntryCounters()},
			expectedNames: []string{"unregistered_rule"},
			expectedRead:  true,
		},
		{
			name:          "wildcard counter with unknown entry set wins over the entry set",
			tags:          []nameValue{{name: "counter", value: "*"}},
			opts:          []metrics.QueryOption{metrics.WithEntryCounters(entry), metrics.WithUnknownEntryCounters()},
			expectedNames: nil,
			expectedRead:  true,
		},
		{
			name:          "exact counter naming a non-member with unknown entry set wins over the entry set",
			tags:          []nameValue{{name: "counter", value: "unregistered_rule"}},
			opts:          []metrics.QueryOption{metrics.WithEntryCounters(entry), metrics.WithUnknownEntryCounters()},
			expectedNames: []string{"unregistered_rule"},
			expectedRead:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			names, read := metrics.Query(test.tags, test.opts...)

			require.Equal(t, test.expectedRead, read)
			require.Equal(t, test.expectedNames, names)
		})
	}
}
