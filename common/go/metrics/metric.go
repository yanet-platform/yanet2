package metrics

type Label struct {
	Name  string
	Value string
}

type MetricID struct {
	Name   string
	Labels []Label
}
