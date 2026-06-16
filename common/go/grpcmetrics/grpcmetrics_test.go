package grpcmetrics

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
)

// fakeInfo builds a grpc.UnaryServerInfo for tests.
func fakeInfo(method string) *grpc.UnaryServerInfo {
	return &grpc.UnaryServerInfo{FullMethod: method}
}

// okHandler is a gRPC handler that succeeds.
func okHandler(_ context.Context, req any) (any, error) {
	return req, nil
}

// errHandler returns a gRPC status error with the given code.
func errHandler(code codes.Code) grpc.UnaryHandler {
	return func(_ context.Context, _ any) (any, error) {
		return nil, status.Errorf(code, "test error")
	}
}

// panicHandler panics with a string value.
func panicHandler(_ context.Context, _ any) (any, error) {
	panic("test panic")
}

// findMetric returns the metric from out matching name and the exact label set, or nil.
func findMetric(metrics []*commonpb.Metric, name string, labels map[string]string) *commonpb.Metric {
	for _, m := range metrics {
		if m.Name != name {
			continue
		}
		got := map[string]string{}
		for _, l := range m.Labels {
			got[l.Name] = l.Value
		}
		match := true
		for k, v := range labels {
			if got[k] != v {
				match = false
				break
			}
		}
		if match && len(got) == len(labels) {
			return m
		}
	}
	return nil
}

func counterValue(m *commonpb.Metric) uint64 {
	if c, ok := m.Value.(*commonpb.Metric_Counter); ok {
		return c.Counter
	}
	return 0
}

func histogramTotal(m *commonpb.Metric) uint64 {
	if h, ok := m.Value.(*commonpb.Metric_Histogram); ok {
		return h.Histogram.TotalCount
	}
	return 0
}

// TestInterceptorOK verifies a successful unary call records started, the
// handling-seconds observation, and handled with grpc_code=OK.
func TestInterceptorOK(t *testing.T) {
	serverMetrics := New()
	interceptor := serverMetrics.UnaryServerInterceptor()

	_, err := interceptor(t.Context(), nil, fakeInfo("/svc/Method"), okHandler)
	require.NoError(t, err)

	out := serverMetrics.Collect()
	baseLabels := map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "Method",
	}

	started := findMetric(out, "grpc_server_started_total", baseLabels)
	require.NotNil(t, started, "grpc_server_started_total should be present")
	assert.Equal(t, uint64(1), counterValue(started))

	handling := findMetric(out, "grpc_server_handling_seconds", baseLabels)
	require.NotNil(t, handling, "grpc_server_handling_seconds should be present")
	assert.Equal(t, uint64(1), histogramTotal(handling))

	handledLabels := map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "Method",
		"grpc_code":    codes.OK.String(),
	}
	handled := findMetric(out, "grpc_server_handled_total", handledLabels)
	require.NotNil(t, handled, "grpc_server_handled_total should be present")
	assert.Equal(t, uint64(1), counterValue(handled))
}

// TestInterceptorErrorCode verifies that a handler returning a gRPC status
// error records handled with the matching grpc_code and no OK series.
func TestInterceptorErrorCode(t *testing.T) {
	serverMetrics := New()
	interceptor := serverMetrics.UnaryServerInterceptor()

	_, err := interceptor(t.Context(), nil, fakeInfo("/svc/Method"), errHandler(codes.NotFound))
	require.Error(t, err)

	out := serverMetrics.Collect()

	handledLabels := map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "Method",
		"grpc_code":    codes.NotFound.String(),
	}
	handled := findMetric(out, "grpc_server_handled_total", handledLabels)
	require.NotNil(t, handled, "grpc_server_handled_total[NotFound] should be present")
	assert.Equal(t, uint64(1), counterValue(handled))

	okLabels := map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "Method",
		"grpc_code":    codes.OK.String(),
	}
	assert.Nil(t, findMetric(out, "grpc_server_handled_total", okLabels), "OK series should not exist")
}

// TestInterceptorLabeler verifies that extra labels from the Labeler are
// attached to all three metric families.
func TestInterceptorLabeler(t *testing.T) {
	serverMetrics := New(
		WithLabeler(func(_ string, req any) metrics.Labels {
			if s, ok := req.(string); ok {
				return metrics.Labels{"config": s}
			}
			return nil
		}),
	)
	interceptor := serverMetrics.UnaryServerInterceptor()

	_, err := interceptor(t.Context(), "mycfg", fakeInfo("/svc/Foo"), okHandler)
	require.NoError(t, err)

	out := serverMetrics.Collect()
	labels := map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "Foo",
		"config":       "mycfg",
	}

	started := findMetric(out, "grpc_server_started_total", labels)
	require.NotNil(t, started, "grpc_server_started_total should carry config label")
	assert.Equal(t, uint64(1), counterValue(started))

	handling := findMetric(out, "grpc_server_handling_seconds", labels)
	require.NotNil(t, handling, "grpc_server_handling_seconds should carry config label")

	handledLabels := map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "Foo",
		"grpc_code":    codes.OK.String(),
		"config":       "mycfg",
	}
	handled := findMetric(out, "grpc_server_handled_total", handledLabels)
	require.NotNil(t, handled, "grpc_server_handled_total should carry config label")
	assert.Equal(t, uint64(1), counterValue(handled))
}

// TestInterceptorPanicRecordedAndRePanics verifies that a panicking handler is
// recorded as grpc_code=Unknown and the panic propagates to the caller.
func TestInterceptorPanicRecordedAndRePanics(t *testing.T) {
	serverMetrics := New()
	interceptor := serverMetrics.UnaryServerInterceptor()

	assert.Panics(t, func() {
		_, _ = interceptor(t.Context(), nil, fakeInfo("/svc/Panic"), panicHandler)
	}, "interceptor should re-panic")

	out := serverMetrics.Collect()

	handledLabels := map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "Panic",
		"grpc_code":    codes.Unknown.String(),
	}
	handled := findMetric(out, "grpc_server_handled_total", handledLabels)
	require.NotNil(t, handled, "panic should be recorded as Unknown code")
	assert.Equal(t, uint64(1), counterValue(handled))

	histLabels := map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "Panic",
	}
	handling := findMetric(out, "grpc_server_handling_seconds", histLabels)
	require.NotNil(t, handling, "handling histogram should be recorded even on panic")
	assert.Equal(t, uint64(1), histogramTotal(handling))
}

// TestReconcileRetention verifies that Collect() prunes series whose config
// label is absent from the retention predicate while preserving live and
// no-config series.
func TestReconcileRetention(t *testing.T) {
	alive := map[string]struct{}{"alive": {}}

	serverMetrics := New(
		WithLabeler(func(_ string, req any) metrics.Labels {
			if s, ok := req.(string); ok {
				return metrics.Labels{"config": s}
			}
			return nil
		}),
		WithRetention(func() func(metrics.MetricID) bool {
			snapshot := alive
			return func(id metrics.MetricID) bool {
				config := id.Labels["config"]
				if config == "" {
					return true
				}
				_, ok := snapshot[config]
				return ok
			}
		}),
	)
	interceptor := serverMetrics.UnaryServerInterceptor()

	// Record calls for two configs.
	_, _ = interceptor(t.Context(), "alive", fakeInfo("/svc/M"), okHandler)
	_, _ = interceptor(t.Context(), "dead", fakeInfo("/svc/M"), okHandler)
	// Record a call without a config label.
	_, _ = interceptor(t.Context(), nil, fakeInfo("/svc/NoConfig"), okHandler)

	// Remove "dead" from the live set.
	alive = map[string]struct{}{"alive": {}}

	out := serverMetrics.Collect()

	aliveStarted := findMetric(out, "grpc_server_started_total",
		map[string]string{
			"grpc_type":    "unary",
			"grpc_service": "svc",
			"grpc_method":  "M",
			"config":       "alive",
		})
	require.NotNil(t, aliveStarted, "alive config series should be kept")

	deadStarted := findMetric(out, "grpc_server_started_total",
		map[string]string{
			"grpc_type":    "unary",
			"grpc_service": "svc",
			"grpc_method":  "M",
			"config":       "dead",
		})
	assert.Nil(t, deadStarted, "dead config series should be pruned")

	noConfig := findMetric(out, "grpc_server_started_total",
		map[string]string{
			"grpc_type":    "unary",
			"grpc_service": "svc",
			"grpc_method":  "NoConfig",
		})
	require.NotNil(t, noConfig, "no-config series should not be pruned by retention")
}

// TestNewFactory verifies that the retention provider injected at call time
// drives pruning: the alive series is kept and the dead series is removed on
// Collect().
func TestNewFactory(t *testing.T) {
	alive := map[string]struct{}{"alive": {}}

	factory := NewFactory(
		WithLabeler(func(_ string, req any) metrics.Labels {
			if s, ok := req.(string); ok {
				return metrics.Labels{"config": s}
			}
			return nil
		}),
	)

	serverMetrics := factory(func() func(metrics.MetricID) bool {
		snapshot := alive
		return func(id metrics.MetricID) bool {
			config := id.Labels["config"]
			if config == "" {
				return true
			}
			_, ok := snapshot[config]
			return ok
		}
	})
	require.NotNil(t, serverMetrics)

	interceptor := serverMetrics.UnaryServerInterceptor()
	_, err := interceptor(t.Context(), "alive", fakeInfo("/svc/M"), okHandler)
	require.NoError(t, err)
	_, err = interceptor(t.Context(), "dead", fakeInfo("/svc/M"), okHandler)
	require.NoError(t, err)

	out := serverMetrics.Collect()

	aliveMetric := findMetric(out, "grpc_server_started_total",
		map[string]string{
			"grpc_type":    "unary",
			"grpc_service": "svc",
			"grpc_method":  "M",
			"config":       "alive",
		})
	deadMetric := findMetric(out, "grpc_server_started_total",
		map[string]string{
			"grpc_type":    "unary",
			"grpc_service": "svc",
			"grpc_method":  "M",
			"config":       "dead",
		})

	require.NotNil(t, aliveMetric, "alive config series should be kept")
	assert.Nil(t, deadMetric, "dead config series should be pruned")
}

// TestParallelCallsRace exercises concurrent interceptor calls and Collect()
// invocations under the race detector to catch data races.
func TestParallelCallsRace(t *testing.T) {
	serverMetrics := New(
		WithLabeler(func(_ string, req any) metrics.Labels {
			if s, ok := req.(string); ok {
				return metrics.Labels{"config": s}
			}
			return nil
		}),
	)
	interceptor := serverMetrics.UnaryServerInterceptor()

	configs := []string{"a", "b", "c", ""}
	methods := []string{"/svc/One", "/svc/Two"}

	var wg sync.WaitGroup
	for range 50 {
		for _, config := range configs {
			for _, method := range methods {
				wg.Add(1)
				go func(config, method string) {
					defer wg.Done()
					var req any
					if config != "" {
						req = config
					}
					_, _ = interceptor(t.Context(), req, fakeInfo(method), okHandler)
				}(config, method)
			}
		}
	}

	// Concurrent Collect() calls alongside the interceptors.
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = serverMetrics.Collect()
		}()
	}

	wg.Wait()

	out := serverMetrics.Collect()
	assert.NotEmpty(t, out)
}
