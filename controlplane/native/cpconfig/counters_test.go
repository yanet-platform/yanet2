//go:build linux && (amd64 || arm64)

package cpconfig_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// Test_RegisterCounter_RefusesNameItsFieldCannotHold verifies that a
// counter name C would cut short is refused before the registry is
// touched.
//
// The registry has no memory context, so any allocation from it would
// fault.
func Test_RegisterCounter_RefusesNameItsFieldCannotHold(t *testing.T) {
	for _, tc := range invalidNames(len(layout.Counter{}.Name)) {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cpconfig.RegisterCounter(&layout.CounterRegistry{}, tc.value, 1)
			require.ErrorIs(t, err, cpconfig.ErrInvalidName)
		})
	}
}
