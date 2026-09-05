package commonpb_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_Device_Validate verifies that each direction has an independent budget
// and validation preserves the supplied message, including absent entries.
func Test_Device_Validate(t *testing.T) {
	for _, test := range []struct {
		name   string
		device *commonpb.Device
		field  string
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
			name:   "input above limit",
			device: &commonpb.Device{Input: []*commonpb.DevicePipeline{{Weight: 65536}}},
			field:  "device.input[0].weight",
		},
		{
			name:   "input sum above limit",
			device: &commonpb.Device{Input: []*commonpb.DevicePipeline{{Weight: 65535}, {Weight: 1}}},
			field:  "device.input weight sum",
		},
		{
			name:   "output sum above limit",
			device: &commonpb.Device{Output: []*commonpb.DevicePipeline{{Weight: 65535}, {Weight: 1}}},
			field:  "device.output weight sum",
		},
		{
			name:   "overflowing input sum",
			device: &commonpb.Device{Input: []*commonpb.DevicePipeline{{Weight: 1}, {Weight: math.MaxUint64}}},
			field:  "device.input[1].weight",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := proto.Clone(test.device)
			err := test.device.Validate()
			if test.field == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.field)
				require.ErrorContains(t, err, "0..65535")
			}
			require.True(t, proto.Equal(before, test.device))
		})
	}
}
