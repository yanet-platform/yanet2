package format

import (
	"context"
	"fmt"

	"github.com/yanet-platform/yanet2/common/commonpb"
	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
	"github.com/yanet-platform/yanet2/modules/metrics/format/prometheus"
	"go.uber.org/zap"
)

type MetricFormatter int

const (
	ConverterUndefined MetricFormatter = iota
	ConverterPrometheus
)

type Formatter interface {
	Write(ctx context.Context, metrics []metric.Metric) error
	Metrics() string
	MetricValue(m *commonpb.Metric) metric.Value
}

func NewFormatter(kind MetricFormatter, log *zap.Logger) (Formatter, error) {
	switch kind {
	case ConverterPrometheus:
		return prometheus.NewConverter(log), nil
	case ConverterUndefined:
		return nil, fmt.Errorf("formatter is undefined")
	default:
		return nil, fmt.Errorf("unsupported formatter kind: %d", kind)
	}
}

func ParseFormat(format string) (MetricFormatter, error) {
	switch format {
	case "prometheus":
		return ConverterPrometheus, nil
	default:
		return ConverterUndefined, fmt.Errorf("unsupported format: %q", format)
	}
}
