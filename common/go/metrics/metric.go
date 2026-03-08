package metrics

import "github.com/yanet-platform/yanet2/common/commonpb"

type Label struct {
	Name  string
	Value string
}

type MetricID struct {
	Name   string
	Labels []Label
}

type MetricValue interface {
	ToProto() *commonpb.MetricType
}
