//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// Test_UpdatePipelines_RefusesNameItsFieldCannotHold verifies that a
// pipeline or pipeline function name C would cut short is refused before
// the instance is touched.
//
// The instance has no zones, so taking its lock would fault.
func Test_UpdatePipelines_RefusesNameItsFieldCannotHold(t *testing.T) {
	type invalidPipeline struct {
		name     string
		pipeline cpconfig.PipelineConfig
	}
	var cases []invalidPipeline
	for _, invalid := range invalidNames(len(layout.CpPipeline{}.Name)) {
		cases = append(cases, invalidPipeline{"pipeline: " + invalid.name, cpconfig.PipelineConfig{Name: invalid.value}})
	}
	for _, invalid := range invalidNames(len(layout.CpPipelineFunction{}.Name)) {
		pipeline := cpconfig.PipelineConfig{Name: "drop", Functions: []string{"drop", invalid.value}}
		cases = append(cases, invalidPipeline{"function: " + invalid.name, pipeline})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cpconfig.UpdatePipelines(&zone.Instance{}, []cpconfig.PipelineConfig{tc.pipeline})
			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}
