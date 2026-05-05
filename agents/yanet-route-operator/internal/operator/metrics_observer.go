package operator

import (
	"time"

	"github.com/yanet-platform/yanet2/common/commonpb"
)

// MetricsCollector renders the current state of the operator metrics
// as a flat slice of commonpb.Metric values.
type MetricsCollector interface {
	Collect() []*commonpb.Metric
}

// noopMetricsCollector is the default MetricsCollector wired into
// service options when the caller does not pass a real one.
type noopMetricsCollector struct{}

func (noopMetricsCollector) Collect() []*commonpb.Metric {
	return nil
}

// ReconcilerState is the current state of the reconcile loop.
type ReconcilerState int

const (
	ReconcilerStateIdle ReconcilerState = iota
	ReconcilerStateApplying
	ReconcilerStateSleeping
)

// ReconcilerMetricsObserver receives semantic events from the reconcile
// loop and translates them into metrics.
type ReconcilerMetricsObserver interface {
	OnReconcileCompleted(err error)
	OnBackoffScheduled(delay time.Duration)
	OnBackoffReset()
	OnStateChanged(state ReconcilerState)
}

// noopReconcilerMetricsObserver is the default
// ReconcilerMetricsObserver wired into reconciler options when no
// metrics sink is attached.
type noopReconcilerMetricsObserver struct{}

func (noopReconcilerMetricsObserver) OnReconcileCompleted(error)       {}
func (noopReconcilerMetricsObserver) OnBackoffScheduled(time.Duration) {}
func (noopReconcilerMetricsObserver) OnBackoffReset()                  {}
func (noopReconcilerMetricsObserver) OnStateChanged(ReconcilerState)   {}
