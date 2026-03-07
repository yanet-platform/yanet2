// Package metrics provides thread-safe metric primitives for collecting
// application telemetry data including counters, gauges, and histograms.
package metrics

import "slices"

type Label struct {
	Name  string
	Value string
}

type MetricID struct {
	Name   string
	Labels []Label
}

func (a MetricID) EqualOrdered(b MetricID) bool {
	return a.Name == b.Name && slices.Equal(a.Labels, b.Labels)
}

type Metric[T any] struct {
	ID    MetricID
	Value T
}

type IsMetricValue interface {
	isMetricValue()
}

func (*Counter) isMetricValue() {}

func (*Gauge) isMetricValue() {}

func (*Histogram) isMetricValue() {}
