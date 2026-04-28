package forward

import (
	"github.com/yanet-platform/yanet2/common/commonpb"
)

func (m *ForwardService) Metrics() ([]*commonpb.Metric, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.collectMetrics()
}

func makeGauge(name string, value float64, labels ...*commonpb.Label) *commonpb.Metric {
	return &commonpb.Metric{
		Name:   name,
		Labels: labels,
		Value:  &commonpb.Metric_Gauge{Gauge: value},
	}
}

func makeCounter(name string, value uint64, labels ...*commonpb.Label) *commonpb.Metric {
	return &commonpb.Metric{
		Name:   name,
		Labels: labels,
		Value:  &commonpb.Metric_Counter{Counter: value},
	}
}

func (m *ForwardService) collectMetrics() ([]*commonpb.Metric, error) {
	dpConfig := m.backend.Agent().DPConfig()
	positions := dpConfig.AllModulePositions("acl")

	setCountersNames := make(map[string]struct{}, 0)
	for _, config := range m.configs {
		for _, rule := range config.rules {
			setCountersNames[rule.Action.Counter] = struct{}{}
		}
	}

	result := make([]*commonpb.Metric, 0)
	for _, pos := range positions {
		configName := pos.ModuleName

		baseLabels := []*commonpb.Label{
			{Name: "config", Value: configName},
			{Name: "device", Value: pos.Device},
			{Name: "pipeline", Value: pos.Pipeline},
			{Name: "function", Value: pos.Function},
			{Name: "chain", Value: pos.Chain},
		}

		counters := dpConfig.ModuleCounters(
			pos.Device,
			pos.Pipeline,
			pos.Function,
			pos.Chain,
			"acl",
			configName,
			nil,
		)

		for _, counter := range counters {
			// Sum values across all workers
			var cntPackets, bytes uint64
			for _, workerVals := range counter.Values {
				if len(workerVals) > 0 {
					cntPackets += workerVals[0]
				}
				if len(workerVals) > 1 {
					bytes += workerVals[1]
				}
			}

			if cntPackets == 0 && bytes == 0 {
				continue
			}

			result = append(result,
				makeCounter(counter.Name+"_packets", cntPackets, baseLabels...),
				makeCounter(counter.Name+"_bytes", bytes, baseLabels...),
			)

		}
	}

	return result, nil
}
