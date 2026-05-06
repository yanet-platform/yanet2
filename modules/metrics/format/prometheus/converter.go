package prometheus

import (
	"context"
	"strings"

	"github.com/yanet-platform/yanet2/common/commonpb"
	metric "github.com/yanet-platform/yanet2/modules/metrics/domain"
	"go.uber.org/zap"
)

type Converter struct {
	log    *zap.Logger
	result string
}

func NewConverter(log *zap.Logger) *Converter {
	if log == nil {
		log = zap.NewNop()
	}
	return &Converter{log: log}
}

func (m *Converter) Write(ctx context.Context, metrics []metric.Metric) error {
	type group struct {
		name    string
		kind    metric.MetricType
		members []metric.Metric
	}
	order := make([]string, 0)
	groups := make(map[string]*group)

	for _, mt := range metrics {
		if mt.Value == nil {
			m.log.Debug("skipping metric with nil value", zap.String("name", mt.Name))
			continue
		}
		g, ok := groups[mt.Name]
		if !ok {
			g = &group{name: mt.Name, kind: mt.Value.Type()}
			groups[mt.Name] = g
			order = append(order, mt.Name)
		} else if g.kind != mt.Value.Type() {
			m.log.Warn("metric family has mixed value types, dropping sample",
				zap.String("name", mt.Name),
				zap.Int("expected", int(g.kind)),
				zap.Int("got", int(mt.Value.Type())),
			)
			continue
		}
		g.members = append(g.members, mt)
	}

	var b strings.Builder
	for _, name := range order {
		g := groups[name]
		writeTypeHeader(&b, g.name, g.members[0].Value.TypeName())
		for _, mt := range g.members {
			mt.Write(&b)
		}
	}
	m.result = b.String()
	return nil
}

func (m *Converter) Metrics() string {
	return m.result
}

func (m *Converter) MetricValue(metrics *commonpb.Metric) metric.Value {
	switch metrics.GetValue().(type) {
	case *commonpb.Metric_Counter:
		return Counter(metrics.GetCounter())
	case *commonpb.Metric_Gauge:
		return Gauge(metrics.GetGauge())
	case *commonpb.Metric_Histogram:
		h := metrics.GetHistogram()
		buckets := make([]Bucket, len(h.Buckets))
		for i, b := range h.Buckets {
			buckets[i] = Bucket{
				UpperBound: b.UpperBound,
				Count:      b.Count,
			}
		}
		return &Histogram{
			Buckets:    buckets,
			TotalCount: h.TotalCount,
		}
	default:
		m.log.Debug("unrecognised metric value type", zap.String("name", metrics.GetName()))
		return &Undefine{}
	}
}
