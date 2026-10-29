//go:build sdk_rust_fixtures

package cfixture_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/lib/rust/yanet-sdk-fixture-api/bindings/go/cfixture"
)

// refusedNames are the names that do not fit a name field of a FIB record
// whole.
var refusedNames = []struct {
	name string
	text string
}{
	{name: "name with a NUL", text: "eth\x000"},
	{name: "name without room for the terminator", text: strings.Repeat("n", cfixture.FibNameLen)},
}

// Test_NewFibDevice_RefusedName_ReturnsAnError verifies that device names
// containing NUL or lacking terminator room are refused whole.
func Test_NewFibDevice_RefusedName_ReturnsAnError(t *testing.T) {
	for _, tc := range refusedNames {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cfixture.NewFibDevice(tc.text)

			require.Error(t, err)
		})
	}
}

// Test_NewFibRoute_RefusedCounterName_ReturnsAnError verifies that counter
// names containing NUL or lacking terminator room are refused whole.
func Test_NewFibRoute_RefusedCounterName_ReturnsAnError(t *testing.T) {
	for _, tc := range refusedNames {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cfixture.NewFibRoute([6]byte{}, 0, tc.text)

			require.Error(t, err)
		})
	}
}

// Test_NewFibDevice_LongestName_IsAccepted verifies that a name which leaves
// room for the terminator fits whole.
func Test_NewFibDevice_LongestName_IsAccepted(t *testing.T) {
	_, err := cfixture.NewFibDevice(strings.Repeat("n", cfixture.FibNameLen-1))

	require.NoError(t, err)
}
