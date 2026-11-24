//go:build linux && (amd64 || arm64)

package zone_test

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// Test_Generation_ModulesRegistryItemOpensModule verifies that a module's
// registry item sits at its very start, so the item a registry holds is
// the module itself.
func Test_Generation_ModulesRegistryItemOpensModule(t *testing.T) {
	require.Zero(t, unsafe.Offsetof(layout.CpModule{}.Entity))
	require.Zero(t, unsafe.Offsetof(layout.CpEntity{}.ConfigItem))
}
