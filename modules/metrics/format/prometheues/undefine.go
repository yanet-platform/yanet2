package prometheues

import (
	"io"

	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
)

type Undefine struct {
}

func (m *Undefine) IsValue() {}

func (m *Undefine) Write(w io.Writer, name string, labels []metric.Label) {
	writeTypeHeader(w, "METRIC IS NOT COLLECTED", "counter")
	_, _ = io.WriteString(w, "METRIC IS NOT COLLECTED")
	_, _ = io.WriteString(w, formatLabels(labels))
	_, _ = io.WriteString(w, " ")
	_, _ = io.WriteString(w, "0")
	_, _ = io.WriteString(w, "\n")
}
