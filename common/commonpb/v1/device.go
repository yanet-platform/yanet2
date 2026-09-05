package commonpb

import "fmt"

// MaxWeightSum bounds each chain or pipeline selection table to 512 KiB per
// worker and occurrence in the execution graph.
const MaxWeightSum = 65535

// Validate checks the input and output weight sums independently.
//
// A nil device is an empty configuration. Zero weights disable entries.
func (m *Device) Validate() error {
	if err := validateDevicePipelines("device.input", m.GetInput()); err != nil {
		return err
	}
	return validateDevicePipelines("device.output", m.GetOutput())
}

func validateDevicePipelines(field string, pipelines []*DevicePipeline) error {
	var sum uint64
	for idx, pipeline := range pipelines {
		weight := pipeline.GetWeight()
		if weight > MaxWeightSum {
			return fmt.Errorf("%s[%d].weight %d must be in range 0..%d", field, idx, weight, MaxWeightSum)
		}
		if weight > MaxWeightSum-sum {
			return fmt.Errorf("%s weight sum %d must be in range 0..%d", field, sum+weight, MaxWeightSum)
		}
		sum += weight
	}
	return nil
}
