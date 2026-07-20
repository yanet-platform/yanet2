package metrics

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

// Query derives the counter-name include-list a collector should pass to
// the dataplane counter read, from the tags attached to a metrics request.
//
// It reduces every tag named counterLabel with logical AND into a
// single effective predicate. An absent value ("") means only the
// fixed, structural counters that carry no counter label. A
// present-any value ("*") means every per-entry counter. Any other
// value names an exact counter. No counter tag at all falls back to
// defaultNames, read unconditionally.
// An absent predicate resolves to fixedNames, with the second return
// reporting whether there are any to read at all. A present-any
// predicate resolves to defaultNames read in full, since Matches still
// drops the structural metrics that lack the label at emission time.
// Two counter tags that can never both hold (e.g. two different exact
// values) make the request unsatisfiable: the second return is false
// and the caller must skip the counter read and emit no per-entry
// metrics. Query only decides what to read, while Matches remains the
// correctness gate applied to each emitted metric.
func Query[T NameValue](tags []T, defaultNames, fixedNames []string) ([]string, bool) {
	var values []string
	for _, tag := range tags {
		if tag.GetName() == counterLabel {
			values = append(values, tag.GetValue())
		}
	}

	if len(values) == 0 {
		return defaultNames, true
	}

	value, ok := reduceValues(values)
	if !ok {
		return nil, false
	}

	switch value {
	case "":
		return fixedNames, len(fixedNames) > 0
	case "*":
		return defaultNames, true
	default:
		return []string{value}, true
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
