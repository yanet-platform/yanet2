package operator

import (
	"context"
	"time"
)

// meteredActuator wraps an inner Actuator and records apply duration and
// outcome per gateway.
//
// The inner error is returned unchanged so FanOutActuator error joining
// and reconcile backoff are preserved.
type meteredActuator struct {
	inner   Actuator
	gateway *GatewayMetrics
}

// newMeteredActuator wraps inner so every Apply call is timed and
// reported to gatewayMetrics.
func newMeteredActuator(inner Actuator, gatewayMetrics *GatewayMetrics) *meteredActuator {
	return &meteredActuator{
		inner:   inner,
		gateway: gatewayMetrics,
	}
}

// Apply times the inner actuator's Apply call and reports the outcome
// via ObserveApply, returning the inner error unchanged.
func (m *meteredActuator) Apply(ctx context.Context, snapshot RouteSnapshot) error {
	start := time.Now()
	err := m.inner.Apply(ctx, snapshot)
	m.gateway.ObserveApply(time.Since(start), err)
	return err
}

// Close delegates to the inner actuator.
func (m *meteredActuator) Close() error {
	return m.inner.Close()
}
