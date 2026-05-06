package metric

import "io"

type (
	Label struct {
		Name  string
		Value string
	}

	Metric struct {
		Name   string
		Labels []Label
		Value  Value
	}

	Value interface {
		IsValue()
		Write(w io.Writer, name string, labels []Label)
	}
)

func (m Metric) Write(w io.Writer) {
	if m.Value == nil {
		return
	}
	m.Value.Write(w, m.Name, m.Labels)
}
