package commonpb

import (
	"fmt"
	"strings"
)

// MaxWeightSum bounds each chain or pipeline selection table to 512 KiB per
// worker and occurrence in the execution graph.
const MaxWeightSum = 65535

// MaxDeviceNameLen mirrors the C device name buffer size, including the
// terminating NUL.
const MaxDeviceNameLen = 80

// MaxPipelineNameLen mirrors the C pipeline name buffer size, including the
// terminating NUL.
const MaxPipelineNameLen = 80

// Validate checks the input and output weight sums independently.
//
// A nil device is an empty configuration. Zero weights disable entries. Each
// pipeline name must fit the C name buffer.
func (m *Device) Validate() error {
	if err := validateDevicePipelines("input", m.GetInput()); err != nil {
		return err
	}
	return validateDevicePipelines("output", m.GetOutput())
}

func validateDevicePipelines(field string, pipelines []*DevicePipeline) error {
	var sum uint64
	for idx, pipeline := range pipelines {
		name := pipeline.GetName()
		if strings.IndexByte(name, 0) != -1 {
			return fmt.Errorf("%s[%d].name must not contain NUL", field, idx)
		}
		if len(name) >= MaxPipelineNameLen {
			return fmt.Errorf("%s[%d].name must be shorter than %d bytes", field, idx, MaxPipelineNameLen)
		}

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
