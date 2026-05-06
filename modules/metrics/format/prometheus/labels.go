package prometheus

import (
	"io"
	"strconv"
	"strings"

	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
)

func formatLabels(labels []metric.Label, extra ...metric.Label) string {
	if len(labels) == 0 && len(extra) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteByte('{')

	first := true
	writeLabel := func(l metric.Label) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(l.Name)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(l.Value))
	}

	for _, l := range labels {
		writeLabel(l)
	}

	for _, l := range extra {
		writeLabel(l)
	}

	b.WriteByte('}')
	return b.String()
}

func writeTypeHeader(w io.Writer, name, kind string) {
	_, _ = io.WriteString(w, "# TYPE ")
	_, _ = io.WriteString(w, name)
	_, _ = io.WriteString(w, " ")
	_, _ = io.WriteString(w, kind)
	_, _ = io.WriteString(w, "\n")
}
