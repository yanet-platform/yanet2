package metrics

import "slices"

// NameValue is satisfied by any tag or label that reports a name and a
// value.
type NameValue interface {
	GetName() string
	GetValue() string
}

// Labeled is satisfied by any metric that reports its labels as a slice
// of L.
type Labeled[L NameValue] interface {
	GetLabels() []L
}

// counterLabel is the metric label whose value identifies the individual
// dataplane counter behind a per-entry metric (e.g. one ACL rule).
const counterLabel = "counter"

// Matches reports whether labels satisfies every tag.
//
// A tag without a corresponding label passes only if its value is
// empty. A tag with a corresponding label passes if its value is "*",
// or is non-empty and equals the label's value. Tags are combined with
// logical AND, and a nil or empty tags list always matches.
func Matches[L NameValue, T NameValue](labels []L, tags []T) bool {
	for _, tag := range tags {
		if !matchesOne(labels, tag) {
			return false
		}
	}

	return true
}

func matchesOne[L NameValue, T NameValue](labels []L, tag T) bool {
	for _, label := range labels {
		if label.GetName() != tag.GetName() {
			continue
		}

		if tag.GetValue() == "" {
			return false
		}

		return tag.GetValue() == "*" || label.GetValue() == tag.GetValue()
	}

	return tag.GetValue() == ""
}

// Filter returns the subset of metrics whose labels satisfy every tag,
// preserving their relative order.
//
// A nil or empty tags list returns metrics unchanged. The result is
// always non-nil, even when nothing matches, so callers can embed it
// directly in a response.
func Filter[M Labeled[L], L NameValue, T NameValue](metrics []M, tags []T) []M {
	if len(tags) == 0 {
		return metrics
	}

	selected := make([]M, 0, len(metrics))
	for _, metric := range metrics {
		if Matches(metric.GetLabels(), tags) {
			selected = append(selected, metric)
		}
	}

	return selected
}

// QueryOption declares one part of a collector's counter shape to Query.
type QueryOption func(*queryOptions)

// queryOptions is the collector's counter shape, assembled from the
// options passed to Query.
type queryOptions struct {
	// Structural holds the fixed counters whose metrics carry no
	// "counter" label.
	Structural []string
	// Entry holds the per-entry counters whose metrics carry a
	// "counter" label, enumerated up front.
	Entry []string
	// UnknownEntry reports that per-entry counters exist but cannot be
	// enumerated, so a read that includes them must stay unrestricted.
	UnknownEntry bool
}

// WithStructuralCounters declares the collector's fixed counters, whose
// metrics carry no "counter" label.
//
// Omitted, the collector has no structural counter family.
func WithStructuralCounters(names []string) QueryOption {
	return func(o *queryOptions) {
		o.Structural = names
	}
}

// WithEntryCounters declares the collector's per-entry counters — those
// whose metrics carry a "counter" label — enumerable up front.
//
// Omitted, the collector has no per-entry counters. Its names must be
// disjoint from WithStructuralCounters', since Query unions the two sets
// without deduping.
func WithEntryCounters(names []string) QueryOption {
	return func(o *queryOptions) {
		o.Entry = names
	}
}

// WithUnknownEntryCounters declares that the collector has per-entry
// counters whose names it cannot enumerate, because the dataplane creates
// them.
//
// It wins over WithEntryCounters when both are set.
func WithUnknownEntryCounters() QueryOption {
	return func(o *queryOptions) {
		o.UnknownEntry = true
	}
}

// Query derives the counter-name include-list for the dataplane counter
// read from a metrics request's tags and the collector's counter shape.
//
// The second return says whether there is anything to read — false means
// skip the read entirely. A nil or empty first return paired with true
// happens only under WithUnknownEntryCounters, meaning leave the read
// unrestricted. Otherwise a readable result is always non-empty, safe to
// hand straight to ModuleCounters. Query only decides what to read —
// Matches stays the correctness gate applied to each emitted metric.
func Query[T NameValue](tags []T, opts ...QueryOption) ([]string, bool) {
	var o queryOptions
	for _, opt := range opts {
		opt(&o)
	}

	var values []string
	for _, tag := range tags {
		if tag.GetName() == counterLabel {
			values = append(values, tag.GetValue())
		}
	}

	if len(values) == 0 {
		if o.UnknownEntry {
			return nil, true
		}
		names := union(o.Structural, o.Entry)
		return names, len(names) > 0
	}

	value, ok := reduceValues(values)
	if !ok {
		return nil, false
	}

	switch value {
	case "":
		return o.Structural, len(o.Structural) > 0
	case "*":
		if o.UnknownEntry {
			return nil, true
		}
		return o.Entry, len(o.Entry) > 0
	default:
		if o.UnknownEntry || slices.Contains(o.Entry, value) {
			return []string{value}, true
		}
		return nil, false
	}
}

// union returns the combined structural and entry counter names.
//
// It returns the non-empty side unchanged rather than allocating, and
// only concatenates when both sides hold names.
func union(structural, entry []string) []string {
	switch {
	case len(entry) == 0:
		return structural
	case len(structural) == 0:
		return entry
	default:
		return slices.Concat(structural, entry)
	}
}

// reduceValues combines counter-tag values with logical AND, returning the
// single effective predicate they imply.
//
// Absent ("") is incompatible with any other value, and two different
// exact values can never both hold. Either case reports the second
// return as false. Otherwise present-any ("*") yields to any exact
// value it is paired with, since matching a specific name also
// satisfies "present".
func reduceValues(values []string) (string, bool) {
	distinct := map[string]struct{}{}
	for _, v := range values {
		distinct[v] = struct{}{}
	}

	if len(distinct) == 1 {
		for v := range distinct {
			return v, true
		}
	}

	if _, hasAbsent := distinct[""]; hasAbsent {
		return "", false
	}

	delete(distinct, "*")
	if len(distinct) != 1 {
		return "", false
	}

	for v := range distinct {
		return v, true
	}

	return "", false
}
