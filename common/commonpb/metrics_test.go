package commonpb

import (
	"math"
	"testing"

	"github.com/yanet-platform/yanet2/common/go/metrics"
)

func TestMetricValueToProto_Counter(t *testing.T) {
	var counter metrics.Counter
	counter.Add(42)

	result := MetricValueToProto(&counter)

	mc, ok := result.(*Metric_Counter)
	if !ok {
		t.Fatalf("expected *Metric_Counter, got %T", result)
	}
	if mc.Counter != 42 {
		t.Errorf("expected counter value 42, got %d", mc.Counter)
	}
}

func TestMetricValueToProto_Gauge(t *testing.T) {
	var gauge metrics.Gauge
	gauge.Store(3.14)

	result := MetricValueToProto(&gauge)

	mg, ok := result.(*Metric_Gauge)
	if !ok {
		t.Fatalf("expected *Metric_Gauge, got %T", result)
	}
	if mg.Gauge != 3.14 {
		t.Errorf("expected gauge value 3.14, got %f", mg.Gauge)
	}
}

func TestMetricValueToProto_Histogram(t *testing.T) {
	histogram := metrics.NewHistogram([]float64{10, 50, 100})
	histogram.Observe(5)   // bucket 0 (<=10)
	histogram.Observe(25)  // bucket 1 (<=50)
	histogram.Observe(75)  // bucket 2 (<=100)
	histogram.Observe(200) // bucket 3 (+Inf)

	result := MetricValueToProto(histogram)

	mh, ok := result.(*Metric_Histogram)
	if !ok {
		t.Fatalf("expected *Metric_Histogram, got %T", result)
	}

	h := mh.Histogram
	if len(h.Buckets) != 4 {
		t.Fatalf("expected 4 buckets, got %d", len(h.Buckets))
	}

	// Check bucket bounds and counts
	expectedBounds := []float64{10, 50, 100, math.Inf(1)}
	expectedCounts := []uint64{1, 1, 1, 1}

	for i, bucket := range h.Buckets {
		if bucket.UpperBound != expectedBounds[i] {
			t.Errorf("bucket %d: expected upper bound %f, got %f", i, expectedBounds[i], bucket.UpperBound)
		}
		if bucket.Count != expectedCounts[i] {
			t.Errorf("bucket %d: expected count %d, got %d", i, expectedCounts[i], bucket.Count)
		}
	}

	if h.TotalCount != 4 {
		t.Errorf("expected total count 4, got %d", h.TotalCount)
	}
}

func TestMetricLabelsToProto(t *testing.T) {
	labels := []metrics.Label{
		{Name: "env", Value: "prod"},
		{Name: "region", Value: "us-east"},
	}

	result := MetricLabelsToProto(labels)

	if len(result) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(result))
	}

	if result[0].Name != "env" || result[0].Value != "prod" {
		t.Errorf("label 0: expected env=prod, got %s=%s", result[0].Name, result[0].Value)
	}
	if result[1].Name != "region" || result[1].Value != "us-east" {
		t.Errorf("label 1: expected region=us-east, got %s=%s", result[1].Name, result[1].Value)
	}
}

func TestMetricLabelsToProto_Empty(t *testing.T) {
	result := MetricLabelsToProto(nil)

	if len(result) != 0 {
		t.Errorf("expected empty slice, got %d labels", len(result))
	}
}

func TestMetricRefsToProto_Counter(t *testing.T) {
	m := metrics.NewMetricMap[*metrics.Counter]()

	id1 := metrics.MetricID{Name: "requests", Labels: []metrics.Label{{Name: "method", Value: "GET"}}}
	id2 := metrics.MetricID{Name: "requests", Labels: []metrics.Label{{Name: "method", Value: "POST"}}}

	c1 := m.GetOrCreate(id1, func() *metrics.Counter { return &metrics.Counter{} })
	c2 := m.GetOrCreate(id2, func() *metrics.Counter { return &metrics.Counter{} })

	c1.Add(100)
	c2.Add(50)

	refs := m.Metrics()
	result := MetricRefsToProto(refs)

	if len(result) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(result))
	}

	// Find metrics by label value
	var getMetric, postMetric *Metric
	for _, m := range result {
		if len(m.Labels) > 0 && m.Labels[0].Value == "GET" {
			getMetric = m
		} else if len(m.Labels) > 0 && m.Labels[0].Value == "POST" {
			postMetric = m
		}
	}

	if getMetric == nil || postMetric == nil {
		t.Fatal("could not find expected metrics")
	}

	if getMetric.Name != "requests" {
		t.Errorf("expected name 'requests', got '%s'", getMetric.Name)
	}
	if getMetric.GetCounter() != 100 {
		t.Errorf("expected GET counter 100, got %d", getMetric.GetCounter())
	}
	if postMetric.GetCounter() != 50 {
		t.Errorf("expected POST counter 50, got %d", postMetric.GetCounter())
	}
}

func TestMetricRefsToProto_Gauge(t *testing.T) {
	m := metrics.NewMetricMap[*metrics.Gauge]()

	id := metrics.MetricID{Name: "temperature", Labels: []metrics.Label{{Name: "location", Value: "cpu"}}}
	g := m.GetOrCreate(id, func() *metrics.Gauge { return &metrics.Gauge{} })
	g.Store(65.5)

	refs := m.Metrics()
	result := MetricRefsToProto(refs)

	if len(result) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(result))
	}

	if result[0].Name != "temperature" {
		t.Errorf("expected name 'temperature', got '%s'", result[0].Name)
	}
	if result[0].GetGauge() != 65.5 {
		t.Errorf("expected gauge 65.5, got %f", result[0].GetGauge())
	}
	if len(result[0].Labels) != 1 || result[0].Labels[0].Name != "location" || result[0].Labels[0].Value != "cpu" {
		t.Errorf("unexpected labels: %+v", result[0].Labels)
	}
}

func TestMetricRefsToProto_Histogram(t *testing.T) {
	m := metrics.NewMetricMap[*metrics.Histogram]()

	id := metrics.MetricID{Name: "latency", Labels: []metrics.Label{{Name: "endpoint", Value: "/api"}}}
	h := m.GetOrCreate(id, func() *metrics.Histogram { return metrics.NewHistogram([]float64{10, 50, 100}) })
	h.Observe(5)
	h.Observe(25)
	h.Observe(75)

	refs := m.Metrics()
	result := MetricRefsToProto(refs)

	if len(result) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(result))
	}

	if result[0].Name != "latency" {
		t.Errorf("expected name 'latency', got '%s'", result[0].Name)
	}

	hist := result[0].GetHistogram()
	if hist == nil {
		t.Fatal("expected histogram value")
	}

	if len(hist.Buckets) != 4 {
		t.Fatalf("expected 4 buckets, got %d", len(hist.Buckets))
	}

	if hist.TotalCount != 3 {
		t.Errorf("expected total count 3, got %d", hist.TotalCount)
	}
}

func TestMetricRefsToProto_Empty(t *testing.T) {
	m := metrics.NewMetricMap[*metrics.Counter]()

	refs := m.Metrics()
	result := MetricRefsToProto(refs)

	if len(result) != 0 {
		t.Errorf("expected empty slice, got %d metrics", len(result))
	}
}

func TestMetricRefsToProto_LiveUpdates(t *testing.T) {
	m := metrics.NewMetricMap[*metrics.Counter]()

	id := metrics.MetricID{Name: "test"}
	c := m.GetOrCreate(id, func() *metrics.Counter { return &metrics.Counter{} })
	c.Add(10)

	// Get refs and convert
	refs := m.Metrics()
	result1 := MetricRefsToProto(refs)

	if result1[0].GetCounter() != 10 {
		t.Errorf("expected counter 10, got %d", result1[0].GetCounter())
	}

	// Update the counter
	c.Add(5)

	// Convert again - should see updated value
	refs = m.Metrics()
	result2 := MetricRefsToProto(refs)

	if result2[0].GetCounter() != 15 {
		t.Errorf("expected counter 15, got %d", result2[0].GetCounter())
	}
}
