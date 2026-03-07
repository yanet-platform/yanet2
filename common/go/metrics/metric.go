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

type IsMetricValue interface {
	isMetricValue()
}

func (c *Counter) isMetricValue() {}

func (g *Gauge) isMetricValue() {}

func (h *Histogram) isMetricValue() {}
