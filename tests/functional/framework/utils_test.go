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

// TestPrefixRange verifies that PrefixRange masks host bits before deriving
// a prefix's start/end endpoints, for both an already-masked and an
// unmasked input, across IPv4 and IPv6.
func TestPrefixRange(t *testing.T) {
	testCases := []struct {
		name          string
		prefix        string
		expectedStart string
		expectedEnd   string
	}{
		{
			name:          "IPv4 masked",
			prefix:        "10.0.0.0/24",
			expectedStart: "10.0.0.0",
			expectedEnd:   "10.0.0.255",
		},
		{
			name:          "IPv4 unmasked host bits",
			prefix:        "10.0.0.5/24",
			expectedStart: "10.0.0.0",
			expectedEnd:   "10.0.0.255",
		},
		{
			name:          "IPv4 default route",
			prefix:        "0.0.0.0/0",
			expectedStart: "0.0.0.0",
			expectedEnd:   "255.255.255.255",
		},
		{
			name:          "IPv6 default route",
			prefix:        "::/0",
			expectedStart: "::",
			expectedEnd:   "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff",
		},
		{
			name:          "IPv6 unmasked host bits",
			prefix:        "2001:db8::5/32",
			expectedStart: "2001:db8::",
			expectedEnd:   "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			start, end, err := framework.PrefixRange(testCase.prefix)

			require.NoError(t, err)
			require.Equal(t, testCase.expectedStart, start)
			require.Equal(t, testCase.expectedEnd, end)
		})
	}
}

// TestPrefixRange_MalformedPrefix verifies that malformed network text is
// rejected rather than producing a zero-value range.
func TestPrefixRange_MalformedPrefix(t *testing.T) {
	_, _, err := framework.PrefixRange("not-a-prefix")

	require.Error(t, err)
}
