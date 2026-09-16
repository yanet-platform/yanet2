package commonpb_test

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_Device_Validate verifies that each direction has an independent budget
// and validation preserves the supplied message, including absent entries.
func Test_Device_Validate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		device  *commonpb.Device
		message string
	}{
		{name: "nil device"},
		{name: "empty device", device: &commonpb.Device{}},
		{name: "nil pipeline", device: &commonpb.Device{Input: []*commonpb.DevicePipeline{nil}}},
		{name: "disabled pipeline", device: &commonpb.Device{Input: []*commonpb.DevicePipeline{{Weight: 0}}}},
		{
			name: "independent limits",
			device: &commonpb.Device{
				Input:  []*commonpb.DevicePipeline{{Weight: 65535}},
				Output: []*commonpb.DevicePipeline{{Weight: 65535}},
			},
		},
		{
			name: "sum at limit with disabled entry",
			device: &commonpb.Device{
				Input: []*commonpb.DevicePipeline{{Weight: 0}, {Weight: 65534}, {Weight: 1}},
			},
		},
		{
			name:    "input above limit",
			device:  &commonpb.Device{Input: []*commonpb.DevicePipeline{{Weight: 65536}}},
			message: "input[0].weight 65536 must be in range 0..65535",
		},
		{
			name:    "input sum above limit",
			device:  &commonpb.Device{Input: []*commonpb.DevicePipeline{{Weight: 65535}, {Weight: 1}}},
			message: "input weight sum 65536 must be in range 0..65535",
		},
		{
			name:    "output sum above limit",
			device:  &commonpb.Device{Output: []*commonpb.DevicePipeline{{Weight: 65535}, {Weight: 1}}},
			message: "output weight sum 65536 must be in range 0..65535",
		},
		{
			name:    "overflowing input sum",
			device:  &commonpb.Device{Input: []*commonpb.DevicePipeline{{Weight: 1}, {Weight: math.MaxUint64}}},
			message: "input[1].weight 18446744073709551615 must be in range 0..65535",
		},
		{
			name: "longest pipeline name",
			device: &commonpb.Device{
				Input: []*commonpb.DevicePipeline{{Name: strings.Repeat("p", commonpb.MaxPipelineNameLen-1), Weight: 1}},
			},
		},
		{
			name:    "input pipeline name with NUL",
			device:  &commonpb.Device{Input: []*commonpb.DevicePipeline{{Name: "p0\x00p1", Weight: 1}}},
			message: "input[0].name must not contain NUL",
		},
		{
			name: "output pipeline name of the buffer size",
			device: &commonpb.Device{
				Output: []*commonpb.DevicePipeline{{Name: "p0"}, {Name: strings.Repeat("p", commonpb.MaxPipelineNameLen)}},
			},
			message: "output[1].name must be shorter than 80 bytes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := proto.Clone(tc.device)
			err := tc.device.Validate()
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
			require.True(t, proto.Equal(before, tc.device))
		})
	}
}
