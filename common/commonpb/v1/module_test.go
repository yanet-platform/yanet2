package commonpb_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// Test_ValidateModuleName verifies that a module config name is required and
// obeys the fixed-size buffer and NUL rules under the stated field.
func Test_ValidateModuleName(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		message string
	}{
		{name: "empty name", value: "", message: "module_name is required"},
		{name: "name with NUL", value: "acl0\x00acl1", message: "module_name must not contain NUL"},
		{
			name:    "name of the buffer size",
			value:   strings.Repeat("m", commonpb.MaxModuleNameLen),
			message: "module_name must be shorter than 80 bytes",
		},
		{name: "longest name", value: strings.Repeat("m", commonpb.MaxModuleNameLen-1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := commonpb.ValidateModuleName("module_name", tc.value)
			if tc.message == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.message)
			}
		})
	}
}
