package operator

import (
	"time"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/operator"
)

// Metrics is the single observability sink for the route operator.
//
// TODO: collect metrics.
type Metrics struct{}

// NewMetrics constructs an empty Metrics sink.
func NewMetrics() *Metrics {
	return &Metrics{}
}

// Collect renders the current state of every metric as a slice of
// commonpb.Metric values. Empty for now.
func (m *Metrics) Collect() []*commonpb.Metric {
	return nil
}

func (m *Metrics) OnReconcileCompleted(error)              {}
func (m *Metrics) OnBackoffScheduled(time.Duration)        {}
func (m *Metrics) OnBackoffReset()                         {}
func (m *Metrics) OnStateChanged(operator.ReconcilerState) {}
