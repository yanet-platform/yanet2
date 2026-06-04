package commonpb

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yanet-platform/yanet2/common/go/metrics"
)

func TestMetricValueToProto_Counter(t *testing.T) {
	var counter metrics.Counter
	counter.Add(42)

	result := MetricValueToProto(&counter)

	mc, ok := result.(*Metric_Counter)
	require.True(t, ok, "expected *Metric_Counter, got %T", result)
	assert.Equal(t, uint64(42), mc.Counter, "counter value should match")
}

func TestMetricValueToProto_Gauge(t *testing.T) {
	var gauge metrics.Gauge
	gauge.Store(3.14)

	result := MetricValueToProto(&gauge)

	mg, ok := result.(*Metric_Gauge)
	require.True(t, ok, "expected *Metric_Gauge, got %T", result)
	assert.Equal(t, 3.14, mg.Gauge, "gauge value should match")
}

func TestMetricValueToProto_Histogram(t *testing.T) {
	// Create histogram with pre-populated buckets to test conversion, not histogram logic
	histogram := metrics.NewHistogram([]float64{10, 50, 100})

	// Pre-populate buckets with known values
	histogram.Observe(5)   // bucket 0 (<=10)
	histogram.Observe(25)  // bucket 1 (<=50)
	histogram.Observe(75)  // bucket 2 (<=100)
	histogram.Observe(200) // bucket 3 (+Inf)

	result := MetricValueToProto(histogram)

	mh, ok := result.(*Metric_Histogram)
	require.True(t, ok, "expected *Metric_Histogram, got %T", result)

	h := mh.Histogram
	require.NotNil(t, h, "histogram should not be nil")

	// Verify conversion maps histogram snapshot to proto correctly
	assert.Equal(t, 4, len(h.Buckets), "should have 4 buckets")

	// Verify bucket bounds are correctly mapped
	expectedBounds := []float64{10, 50, 100, math.Inf(1)}
	for i, bucket := range h.Buckets {
		assert.Equal(t, expectedBounds[i], bucket.UpperBound, "bucket %d upper bound should match", i)
		assert.Equal(t, uint64(1), bucket.Count, "bucket %d count should match", i)
	}

	// Verify total count is correctly calculated
	assert.Equal(t, uint64(4), h.TotalCount, "total count should match")
}

func TestMetricLabelsToProto(t *testing.T) {
	labels := metrics.Labels{
		"env":    "prod",
		"region": "us-east",
	}

	result := MetricLabelsToProto(labels)

	require.Len(t, result, 2, "should have 2 labels")

	// Deterministic ordering: sorted by label name ascending.
	assert.Equal(t, "env", result[0].Name, "first label name should match")
	assert.Equal(t, "prod", result[0].Value, "first label value should match")
	assert.Equal(t, "region", result[1].Name, "second label name should match")
	assert.Equal(t, "us-east", result[1].Value, "second label value should match")
}

func TestMetricLabelsToProto_Empty(t *testing.T) {
	result := MetricLabelsToProto(nil)

	assert.Empty(t, result, "should return empty slice for nil input")
}

func TestMetricRefsToProto_Counter(t *testing.T) {
	m := metrics.NewMetricMap[*metrics.Counter]()

	id1 := metrics.MetricID{Name: "requests", Labels: metrics.Labels{"method": "GET"}}
	id2 := metrics.MetricID{Name: "requests", Labels: metrics.Labels{"method": "POST"}}

	c1 := m.GetOrCreate(id1, func() *metrics.Counter { return &metrics.Counter{} })
	c2 := m.GetOrCreate(id2, func() *metrics.Counter { return &metrics.Counter{} })

	c1.Add(100)
	c2.Add(50)

	refs := m.Metrics()
	result := MetricRefsToProto(refs)

	require.Len(t, result, 2, "should have 2 metrics")

	// Find metrics by label value
	var getMetric, postMetric *Metric
	for _, m := range result {
		if len(m.Labels) > 0 && m.Labels[0].Value == "GET" {
			getMetric = m
		} else if len(m.Labels) > 0 && m.Labels[0].Value == "POST" {
			postMetric = m
		}
	}

	require.NotNil(t, getMetric, "should find GET metric")
	require.NotNil(t, postMetric, "should find POST metric")

	assert.Equal(t, "requests", getMetric.Name, "GET metric name should match")
	assert.Equal(t, uint64(100), getMetric.GetCounter(), "GET counter value should match")
	assert.Equal(t, uint64(50), postMetric.GetCounter(), "POST counter value should match")
}

func TestMetricRefsToProto_Gauge(t *testing.T) {
	m := metrics.NewMetricMap[*metrics.Gauge]()

	id := metrics.MetricID{Name: "temperature", Labels: metrics.Labels{"location": "cpu"}}
	g := m.GetOrCreate(id, func() *metrics.Gauge { return &metrics.Gauge{} })
	g.Store(65.5)

	refs := m.Metrics()
	result := MetricRefsToProto(refs)

	require.Len(t, result, 1, "should have 1 metric")

	assert.Equal(t, "temperature", result[0].Name, "metric name should match")
	assert.Equal(t, 65.5, result[0].GetGauge(), "gauge value should match")
	require.Len(t, result[0].Labels, 1, "should have 1 label")
	assert.Equal(t, "location", result[0].Labels[0].Name, "label name should match")
	assert.Equal(t, "cpu", result[0].Labels[0].Value, "label value should match")
}

func TestMetricRefsToProto_Histogram(t *testing.T) {
	m := metrics.NewMetricMap[*metrics.Histogram]()

	id := metrics.MetricID{Name: "latency", Labels: metrics.Labels{"endpoint": "/api"}}
	h := m.GetOrCreate(id, func() *metrics.Histogram { return metrics.NewHistogram([]float64{10, 50, 100}) })

	// Observe values in different buckets:
	// bucket 0 (<=10): 5, 8
	// bucket 1 (10,50]: 25, 30
	// bucket 2 (50,100]: 75
	// bucket 3 (>100): 150, 200, 300
	h.Observe(5)
	h.Observe(8)
	h.Observe(25)
	h.Observe(30)
	h.Observe(75)
	h.Observe(150)
	h.Observe(200)
	h.Observe(300)

	refs := m.Metrics()
	result := MetricRefsToProto(refs)

	require.Len(t, result, 1, "should have 1 metric")

	assert.Equal(t, "latency", result[0].Name, "metric name should match")
	require.Len(t, result[0].Labels, 1, "should have 1 label")
	assert.Equal(t, "endpoint", result[0].Labels[0].Name, "label name should match")
	assert.Equal(t, "/api", result[0].Labels[0].Value, "label value should match")

	hist := result[0].GetHistogram()
	require.NotNil(t, hist, "histogram value should not be nil")

	// Verify bucket structure
	require.Len(t, hist.Buckets, 4, "should have 4 buckets (3 bounds + inf)")

	// Verify each bucket's upper bound and count
	// Note: counts are raw per-bucket counts, not cumulative
	assert.Equal(t, 10.0, hist.Buckets[0].UpperBound, "bucket 0 upper bound should be 10")
	assert.Equal(t, uint64(2), hist.Buckets[0].Count, "bucket 0 should have 2 observations (5, 8)")

	assert.Equal(t, 50.0, hist.Buckets[1].UpperBound, "bucket 1 upper bound should be 50")
	assert.Equal(t, uint64(2), hist.Buckets[1].Count, "bucket 1 should have 2 observations (25, 30)")

	assert.Equal(t, 100.0, hist.Buckets[2].UpperBound, "bucket 2 upper bound should be 100")
	assert.Equal(t, uint64(1), hist.Buckets[2].Count, "bucket 2 should have 1 observation (75)")

	assert.True(t, math.IsInf(hist.Buckets[3].UpperBound, 1), "bucket 3 upper bound should be +Inf")
	assert.Equal(t, uint64(3), hist.Buckets[3].Count, "bucket 3 should have 3 observations (150, 200, 300)")

	// Verify total count
	assert.Equal(t, uint64(8), hist.TotalCount, "total count should be sum of all bucket counts")

	// Verify total count matches sum of individual buckets
	var sumCounts uint64
	for _, bucket := range hist.Buckets {
		sumCounts += bucket.Count
	}
	assert.Equal(t, hist.TotalCount, sumCounts, "total count should equal sum of bucket counts")
}

func TestMetricRefsToProto_Empty(t *testing.T) {
	m := metrics.NewMetricMap[*metrics.Counter]()

	refs := m.Metrics()
	result := MetricRefsToProto(refs)

	assert.Empty(t, result, "should return empty slice for empty metric map")
}

func TestMetricRefsToProto_LiveUpdates(t *testing.T) {
	m := metrics.NewMetricMap[*metrics.Counter]()

	id := metrics.MetricID{Name: "test"}
	c := m.GetOrCreate(id, func() *metrics.Counter { return &metrics.Counter{} })
	c.Add(10)

	// Get refs and convert
	refs := m.Metrics()
	result1 := MetricRefsToProto(refs)

	assert.Equal(t, uint64(10), result1[0].GetCounter(), "initial counter value should be 10")

	// Update the counter
	c.Add(5)

	// Convert again - should see updated value
	refs = m.Metrics()
	result2 := MetricRefsToProto(refs)

	assert.Equal(t, uint64(15), result2[0].GetCounter(), "updated counter value should be 15")
}
