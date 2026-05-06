package prometheus

import (
	"io"
	"math"
	"strconv"

	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
)

type Histogram struct {
	Buckets    []Bucket
	TotalCount uint64
	// Sum        float64
}

type Bucket struct {
	UpperBound float64
	Count      uint64
}

func (*Histogram) IsValue() {}

func (m *Histogram) Write(w io.Writer, name string, labels []metric.Label) {
	if m == nil {
		return
	}
	writeTypeHeader(w, name, "histogram")

	hasInf := false
	for _, b := range m.Buckets {
		le := formatFloat(b.UpperBound)
		if math.IsInf(b.UpperBound, +1) {
			hasInf = true
		}
		_, _ = io.WriteString(w, name)
		_, _ = io.WriteString(w, "_bucket")
		_, _ = io.WriteString(w, formatLabels(labels, metric.Label{Name: "le", Value: le}))
		_, _ = io.WriteString(w, " ")
		_, _ = io.WriteString(w, strconv.FormatUint(b.Count, 10))
		_, _ = io.WriteString(w, "\n")
	}
	if !hasInf {
		_, _ = io.WriteString(w, name)
		_, _ = io.WriteString(w, "_bucket")
		_, _ = io.WriteString(w, formatLabels(labels, metric.Label{Name: "le", Value: "+Inf"}))
		_, _ = io.WriteString(w, " ")
		_, _ = io.WriteString(w, strconv.FormatUint(m.TotalCount, 10))
		_, _ = io.WriteString(w, "\n")
	}

	labelStr := formatLabels(labels)

	// только приближенно если
	// _, _ = io.WriteString(w, name)
	// _, _ = io.WriteString(w, "_sum")
	// _, _ = io.WriteString(w, labelStr)
	// _, _ = io.WriteString(w, " ")
	// _, _ = io.WriteString(w, formatFloat(h.Sum))
	// _, _ = io.WriteString(w, "\n")

	_, _ = io.WriteString(w, name)
	_, _ = io.WriteString(w, "_count")
	_, _ = io.WriteString(w, labelStr)
	_, _ = io.WriteString(w, " ")
	_, _ = io.WriteString(w, strconv.FormatUint(m.TotalCount, 10))
	_, _ = io.WriteString(w, "\n")
}

func formatFloat(v float64) string {
	switch {
	case math.IsInf(v, +1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	case math.IsNaN(v):
		return "NaN"
	default:
		return strconv.FormatFloat(v, 'g', -1, 64)
	}
}
