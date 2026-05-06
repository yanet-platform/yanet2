package format

import (
	"context"

	"github.com/yanet-platform/yanet2/common/commonpb"
	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
	"github.com/yanet-platform/yanet2/modules/metrics/format/prometheues"
)

type MetricFormatter int

const (
	ConverterUndefined = iota
	ConverterPrometheus
)

type Formatter interface {
	Write(ctx context.Context, metrics []metric.Metric) error
	Metrics() string
	MetricValue(metrics *commonpb.Metric) metric.Value
}

func NewFormatter(w prometheues.FormatBuilder, format MetricFormatter) Formatter {
	switch format {
	case ConverterPrometheus:
		return prometheues.NewConverter(w)
	default:
		return nil
	}
}

type ConfigFormat string

func Format(format ConfigFormat) MetricFormatter {
	switch format {
	case "prometheus":
		return ConverterPrometheus
	default:
		return ConverterUndefined
	}
}
