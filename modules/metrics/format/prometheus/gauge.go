package prometheus

import (
	"io"
	"strconv"

	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
)

type Gauge float64

func (Gauge) IsValue() {}

func (Gauge) Type() metric.MetricType {
	return metric.Gauge
}

func (Gauge) TypeName() string {
	return "gauge"
}

func (m Gauge) Write(w io.Writer, name string, labels []metric.Label) {
	_, _ = io.WriteString(w, name)
	_, _ = io.WriteString(w, formatLabels(labels))
	_, _ = io.WriteString(w, " ")
	_, _ = io.WriteString(w, strconv.FormatFloat(float64(m), 'g', -1, 64))
	_, _ = io.WriteString(w, "\n")
}
