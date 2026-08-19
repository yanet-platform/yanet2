package xmath_test

import (
	"testing"

	"github.com/yanet-platform/yanet2/common/go/xmath"
)

// verifies that 32-bit inputs round up to a representable power or report
// overflow.
func Test_NextPowerOfTwo32_RoundsUpAndReportsOverflow(t *testing.T) {
	testCases := []struct {
		name     string
		input    uint32
		expected uint32
	}{
		{name: "zero returns one", input: 0, expected: 1},
		{name: "one returns one", input: 1, expected: 1},
		{name: "exact power is unchanged", input: 8, expected: 8},
		{name: "non-power rounds upward", input: 9, expected: 16},
		{
			name:     "highest representable power is unchanged",
			input:    1 << 31,
			expected: 1 << 31,
		},
		{
			name:     "above maximum power reports overflow",
			input:    (1 << 31) + 1,
			expected: 0,
		},
		{name: "maximum uint32 reports overflow", input: ^uint32(0), expected: 0},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := xmath.NextPowerOfTwo32(testCase.input); got != testCase.expected {
				t.Errorf(
					"NextPowerOfTwo32(%d) = %d, want %d",
					testCase.input,
					got,
					testCase.expected,
				)
			}
		})
	}
}
