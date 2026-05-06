package prometheues

import (
	"io"
	"strconv"

	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
)

type Counter uint64

func (Counter) IsValue() {}

func (m Counter) Write(w io.Writer, name string, labels []metric.Label) {
	writeTypeHeader(w, name, "counter")
	_, _ = io.WriteString(w, name)
	_, _ = io.WriteString(w, formatLabels(labels))
	_, _ = io.WriteString(w, " ")
	_, _ = io.WriteString(w, strconv.FormatUint(uint64(m), 10))
	_, _ = io.WriteString(w, "\n")
}
