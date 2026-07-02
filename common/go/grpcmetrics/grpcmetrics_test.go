package grpcmetrics

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/metrics"
)

// fakeInfo builds a grpc.UnaryServerInfo for tests.
func fakeInfo(method string) *grpc.UnaryServerInfo {
	return &grpc.UnaryServerInfo{FullMethod: method}
}

// fakeServerStream is a minimal grpc.ServerStream for tests that never sends
// or receives messages.
type fakeServerStream struct {
	ctx context.Context
}

func (m *fakeServerStream) Context() context.Context     { return m.ctx }
func (m *fakeServerStream) SetHeader(metadata.MD) error  { return nil }
func (m *fakeServerStream) SendHeader(metadata.MD) error { return nil }
func (m *fakeServerStream) SetTrailer(metadata.MD)       {}
func (m *fakeServerStream) SendMsg(any) error            { return nil }
func (m *fakeServerStream) RecvMsg(any) error            { return nil }

// okStreamHandler is a gRPC stream handler that succeeds.
func okStreamHandler(_ any, _ grpc.ServerStream) error {
	return nil
}

// errStreamHandler returns a gRPC status error with the given code.
func errStreamHandler(code codes.Code) grpc.StreamHandler {
	return func(_ any, _ grpc.ServerStream) error {
		return status.Errorf(code, "test stream error")
	}
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

// TestStreamInterceptorOK verifies that a successful stream call records
// started, the handling-seconds observation, and handled with grpc_code=OK,
// labeled with the grpc_type matching the stream's direction flags.
func TestStreamInterceptorOK(t *testing.T) {
	tests := []struct {
		name           string
		isClientStream bool
		isServerStream bool
		wantType       string
	}{
		{
			name:           "bidi stream",
			isClientStream: true,
			isServerStream: true,
			wantType:       "bidi_stream",
		},
		{
			name:           "client stream",
			isClientStream: true,
			isServerStream: false,
			wantType:       "client_stream",
		},
		{
			name:           "server stream",
			isClientStream: false,
			isServerStream: true,
			wantType:       "server_stream",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serverMetrics := New()
			interceptor := serverMetrics.StreamServerInterceptor()

			info := &grpc.StreamServerInfo{
				FullMethod:     "/svc/Method",
				IsClientStream: test.isClientStream,
				IsServerStream: test.isServerStream,
			}
			err := interceptor(nil, &fakeServerStream{ctx: t.Context()}, info, okStreamHandler)
			require.NoError(t, err)

			out := serverMetrics.Collect()
			baseLabels := map[string]string{
				"grpc_type":    test.wantType,
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
				"grpc_type":    test.wantType,
				"grpc_service": "svc",
				"grpc_method":  "Method",
				"grpc_code":    codes.OK.String(),
			}
			handled := findMetric(out, "grpc_server_handled_total", handledLabels)
			require.NotNil(t, handled, "grpc_server_handled_total should be present")
			assert.Equal(t, uint64(1), counterValue(handled))
		})
	}
}

// TestStreamInterceptorErrorCode verifies that a stream handler returning a
// gRPC status error records handled with the matching grpc_code and no OK
// series.
func TestStreamInterceptorErrorCode(t *testing.T) {
	serverMetrics := New()
	interceptor := serverMetrics.StreamServerInterceptor()

	info := &grpc.StreamServerInfo{
		FullMethod:     "/svc/Method",
		IsClientStream: true,
		IsServerStream: true,
	}
	err := interceptor(nil, &fakeServerStream{ctx: t.Context()}, info, errStreamHandler(codes.NotFound))
	require.Error(t, err)

	out := serverMetrics.Collect()

	handledLabels := map[string]string{
		"grpc_type":    "bidi_stream",
		"grpc_service": "svc",
		"grpc_method":  "Method",
		"grpc_code":    codes.NotFound.String(),
	}
	handled := findMetric(out, "grpc_server_handled_total", handledLabels)
	require.NotNil(t, handled, "grpc_server_handled_total[NotFound] should be present")
	assert.Equal(t, uint64(1), counterValue(handled))

	okLabels := map[string]string{
		"grpc_type":    "bidi_stream",
		"grpc_service": "svc",
		"grpc_method":  "Method",
		"grpc_code":    codes.OK.String(),
	}
	assert.Nil(t, findMetric(out, "grpc_server_handled_total", okLabels), "OK series should not exist")
}

// TestStreamInterceptorLabelerReceivesNilReq verifies that the Labeler is
// invoked with a nil req for stream calls.
func TestStreamInterceptorLabelerReceivesNilReq(t *testing.T) {
	var gotReq any
	sawReq := false

	serverMetrics := New(
		WithLabeler(func(_ string, req any) metrics.Labels {
			gotReq = req
			sawReq = true
			return nil
		}),
	)
	interceptor := serverMetrics.StreamServerInterceptor()

	info := &grpc.StreamServerInfo{
		FullMethod:     "/svc/Method",
		IsClientStream: true,
		IsServerStream: true,
	}
	err := interceptor(nil, &fakeServerStream{ctx: t.Context()}, info, okStreamHandler)
	require.NoError(t, err)

	require.True(t, sawReq, "labeler should have been invoked")
	assert.Nil(t, gotReq, "labeler should receive a nil req for stream calls")
}

// TestPerServiceMethodLimitUnary verifies that WithPerServiceMethodLimit caps
// the distinct grpc_method values recorded per service on the unary path:
// methods admitted before the cap keep their own name, methods past the cap
// fold into the overflow label, and repeat calls to an already-tracked
// method are unaffected.
func TestPerServiceMethodLimitUnary(t *testing.T) {
	serverMetrics := New(WithPerServiceMethodLimit(2))
	interceptor := serverMetrics.UnaryServerInterceptor()

	call := func(method string) {
		_, err := interceptor(t.Context(), nil, fakeInfo(method), okHandler)
		require.NoError(t, err)
	}

	call("/svc/One")
	call("/svc/Two")
	call("/svc/Three")
	call("/svc/One")

	out := serverMetrics.Collect()

	for _, method := range []string{"One", "Two"} {
		started := findMetric(out, "grpc_server_started_total", map[string]string{
			"grpc_type":    "unary",
			"grpc_service": "svc",
			"grpc_method":  method,
		})
		require.NotNil(t, started, "method %s below the limit should keep its own name", method)
	}

	one := findMetric(out, "grpc_server_started_total", map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "One",
	})
	require.NotNil(t, one)
	assert.Equal(t, uint64(2), counterValue(one),
		"repeat call to a known method should still record under its own name")

	three := findMetric(out, "grpc_server_started_total", map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "Three",
	})
	assert.Nil(t, three, "a method past the limit must not appear under its own name")

	overflowLabels := map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "other",
	}
	overflowStarted := findMetric(out, "grpc_server_started_total", overflowLabels)
	require.NotNil(t, overflowStarted, "a method past the limit should be recorded under the overflow label")
	assert.Equal(t, uint64(1), counterValue(overflowStarted))

	overflowHandling := findMetric(out, "grpc_server_handling_seconds", overflowLabels)
	require.NotNil(t, overflowHandling, "handling-seconds should also use the overflow label")
	assert.Equal(t, uint64(1), histogramTotal(overflowHandling))

	overflowHandledLabels := map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "other",
		"grpc_code":    codes.OK.String(),
	}
	overflowHandled := findMetric(out, "grpc_server_handled_total", overflowHandledLabels)
	require.NotNil(t, overflowHandled, "handled should also use the overflow label")
	assert.Equal(t, uint64(1), counterValue(overflowHandled))
}

// TestPerServiceMethodLimitStream verifies that the overflow label applies
// consistently on the stream path too, across all three metric families.
func TestPerServiceMethodLimitStream(t *testing.T) {
	serverMetrics := New(WithPerServiceMethodLimit(1))
	interceptor := serverMetrics.StreamServerInterceptor()

	info := func(method string) *grpc.StreamServerInfo {
		return &grpc.StreamServerInfo{FullMethod: method, IsClientStream: true, IsServerStream: true}
	}

	require.NoError(t, interceptor(nil, &fakeServerStream{ctx: t.Context()}, info("/svc/One"), okStreamHandler))
	require.NoError(t, interceptor(nil, &fakeServerStream{ctx: t.Context()}, info("/svc/Two"), okStreamHandler))

	out := serverMetrics.Collect()

	overflowLabels := map[string]string{
		"grpc_type":    "bidi_stream",
		"grpc_service": "svc",
		"grpc_method":  "other",
	}
	assert.NotNil(t, findMetric(out, "grpc_server_started_total", overflowLabels),
		"started should use the overflow label")
	assert.NotNil(t, findMetric(out, "grpc_server_handling_seconds", overflowLabels),
		"handling-seconds should use the overflow label")

	overflowHandledLabels := map[string]string{
		"grpc_type":    "bidi_stream",
		"grpc_service": "svc",
		"grpc_method":  "other",
		"grpc_code":    codes.OK.String(),
	}
	assert.NotNil(t, findMetric(out, "grpc_server_handled_total", overflowHandledLabels),
		"handled should use the overflow label")

	assert.Nil(t, findMetric(out, "grpc_server_started_total", map[string]string{
		"grpc_type":    "bidi_stream",
		"grpc_service": "svc",
		"grpc_method":  "Two",
	}), "the method past the limit must not appear under its own name")
}

// TestPerServiceMethodLimitUnlimitedByDefault verifies that a ServerMetrics
// with no WithPerServiceMethodLimit option never folds methods into the
// overflow label, regardless of how many distinct methods a service sees.
func TestPerServiceMethodLimitUnlimitedByDefault(t *testing.T) {
	serverMetrics := New()
	interceptor := serverMetrics.UnaryServerInterceptor()

	for _, method := range []string{"/svc/One", "/svc/Two", "/svc/Three", "/svc/Four"} {
		_, err := interceptor(t.Context(), nil, fakeInfo(method), okHandler)
		require.NoError(t, err)
	}

	out := serverMetrics.Collect()

	overflow := findMetric(out, "grpc_server_started_total", map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "svc",
		"grpc_method":  "other",
	})
	assert.Nil(t, overflow, "no method should be folded into the overflow label without a limit")

	for _, method := range []string{"One", "Two", "Three", "Four"} {
		started := findMetric(out, "grpc_server_started_total", map[string]string{
			"grpc_type":    "unary",
			"grpc_service": "svc",
			"grpc_method":  method,
		})
		assert.NotNil(t, started, "method %s should keep its own name", method)
	}
}

// TestPerServiceMethodLimitPerService verifies that the cap is tracked
// independently per grpc_service: exhausting one service's method slots does
// not affect another service's cap.
func TestPerServiceMethodLimitPerService(t *testing.T) {
	serverMetrics := New(WithPerServiceMethodLimit(1))
	interceptor := serverMetrics.UnaryServerInterceptor()

	call := func(method string) {
		_, err := interceptor(t.Context(), nil, fakeInfo(method), okHandler)
		require.NoError(t, err)
	}

	call("/svcA/One")
	call("/svcB/One")

	out := serverMetrics.Collect()

	for _, service := range []string{"svcA", "svcB"} {
		started := findMetric(out, "grpc_server_started_total", map[string]string{
			"grpc_type":    "unary",
			"grpc_service": service,
			"grpc_method":  "One",
		})
		require.NotNil(t, started, "service %s should admit its first method under its own name", service)
	}
}

// TestServiceFilterRejectsUnary verifies that a rejected service records no
// series and calls the handler directly, while an accepted service is
// unaffected.
func TestServiceFilterRejectsUnary(t *testing.T) {
	serverMetrics := New(WithServiceFilter(func(service string) bool {
		return service == "allowed"
	}))
	interceptor := serverMetrics.UnaryServerInterceptor()

	resp, err := interceptor(t.Context(), "req", fakeInfo("/rejected/Method"), okHandler)
	require.NoError(t, err)
	assert.Equal(t, "req", resp, "handler should still run for a rejected service")

	resp, err = interceptor(t.Context(), "req", fakeInfo("/allowed/Method"), okHandler)
	require.NoError(t, err)
	assert.Equal(t, "req", resp)

	out := serverMetrics.Collect()

	rejected := findMetric(out, "grpc_server_started_total", map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "rejected",
		"grpc_method":  "Method",
	})
	assert.Nil(t, rejected, "a rejected service must not record a series")

	allowed := findMetric(out, "grpc_server_started_total", map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "allowed",
		"grpc_method":  "Method",
	})
	require.NotNil(t, allowed, "an accepted service should record normally")
	assert.Equal(t, uint64(1), counterValue(allowed))
}

// TestServiceFilterRejectsStream verifies that a rejected service records no
// series on the stream path either, while an accepted service is unaffected.
func TestServiceFilterRejectsStream(t *testing.T) {
	serverMetrics := New(WithServiceFilter(func(service string) bool {
		return service == "allowed"
	}))
	interceptor := serverMetrics.StreamServerInterceptor()

	info := func(method string) *grpc.StreamServerInfo {
		return &grpc.StreamServerInfo{FullMethod: method, IsClientStream: true, IsServerStream: true}
	}

	require.NoError(t, interceptor(nil, &fakeServerStream{ctx: t.Context()}, info("/rejected/Method"), okStreamHandler))
	require.NoError(t, interceptor(nil, &fakeServerStream{ctx: t.Context()}, info("/allowed/Method"), okStreamHandler))

	out := serverMetrics.Collect()

	rejected := findMetric(out, "grpc_server_started_total", map[string]string{
		"grpc_type":    "bidi_stream",
		"grpc_service": "rejected",
		"grpc_method":  "Method",
	})
	assert.Nil(t, rejected, "a rejected service must not record a series")

	allowed := findMetric(out, "grpc_server_started_total", map[string]string{
		"grpc_type":    "bidi_stream",
		"grpc_service": "allowed",
		"grpc_method":  "Method",
	})
	require.NotNil(t, allowed, "an accepted service should record normally")
	assert.Equal(t, uint64(1), counterValue(allowed))
}

// TestServiceFilterRejectsMethodBookkeeping verifies that a rejected service
// never populates the per-service method map, even under a method limit.
func TestServiceFilterRejectsMethodBookkeeping(t *testing.T) {
	serverMetrics := New(
		WithServiceFilter(func(service string) bool {
			return service == "allowed"
		}),
		WithPerServiceMethodLimit(64),
	)
	interceptor := serverMetrics.UnaryServerInterceptor()

	for _, method := range []string{"/rejected/One", "/rejected/Two", "/rejected/Three"} {
		_, err := interceptor(t.Context(), nil, fakeInfo(method), okHandler)
		require.NoError(t, err)
	}

	serverMetrics.methodsMu.Lock()
	_, ok := serverMetrics.methods["rejected"]
	serverMetrics.methodsMu.Unlock()
	assert.False(t, ok, "a rejected service must not allocate a method bookkeeping entry")
}

// TestServiceFilterDefaultAcceptsAll verifies that a ServerMetrics built
// without WithServiceFilter records every service unchanged.
func TestServiceFilterDefaultAcceptsAll(t *testing.T) {
	serverMetrics := New()
	interceptor := serverMetrics.UnaryServerInterceptor()

	_, err := interceptor(t.Context(), nil, fakeInfo("/anything/Method"), okHandler)
	require.NoError(t, err)

	out := serverMetrics.Collect()
	started := findMetric(out, "grpc_server_started_total", map[string]string{
		"grpc_type":    "unary",
		"grpc_service": "anything",
		"grpc_method":  "Method",
	})
	require.NotNil(t, started, "the default filter should accept every service")
}
