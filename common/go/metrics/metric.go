/*
Package metrics provides thread-safe metric primitives for collecting
application telemetry data including counters, gauges, and histograms.
*/
package metrics

import "maps"

type Labels map[string]string

func (a Labels) Equals(b Labels) bool {
	return maps.Equal(a, b)
}

func (a Labels) Clone() Labels {
	return maps.Clone(a)
}

type MetricID struct {
	Name   string
	Labels Labels
}

func (a MetricID) Equals(b MetricID) bool {
	return a.Name == b.Name && a.Labels.Equals(b.Labels)
}

func (a MetricID) Clone() MetricID {
	return MetricID{
		Name:   a.Name,
		Labels: a.Labels.Clone(),
	}
}

type Metric[T any] struct {
	ID    MetricID
	Value T
}
