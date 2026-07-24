package framework_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/tests/functional/framework"
)

// TestVMReadyTimeout verifies that VMReadyTimeout parses a valid override and
// falls back to the default for an empty, unparseable, or non-positive value.
func TestVMReadyTimeout(t *testing.T) {
	testCases := []struct {
		name     string
		value    string
		expected time.Duration
	}{
		{
			name:     "valid override",
			value:    "90s",
			expected: 90 * time.Second,
		},
		{
			name:     "empty falls back to default",
			value:    "",
			expected: 120 * time.Second,
		},
		{
			name:     "unparseable falls back to default",
			value:    "garbage",
			expected: 120 * time.Second,
		},
		{
			name:     "zero falls back to default",
			value:    "0s",
			expected: 120 * time.Second,
		},
		{
			name:     "negative falls back to default",
			value:    "-5s",
			expected: 120 * time.Second,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("YANET_VM_READY_TIMEOUT", testCase.value)

			timeout := framework.VMReadyTimeout()

			require.Equal(t, testCase.expected, timeout)
		})
	}
}
