package cunrdup_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/modules/unrdup/bindings/go/cunrdup"
	unrduppb "github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)

// Test_MaxModuleNameLen_MatchesC verifies that the pure-Go usable bound
// matches the C module-name buffer limit.
func Test_MaxModuleNameLen_MatchesC(t *testing.T) {
	require.Equal(t, unrduppb.MaxModuleNameLen, cunrdup.ModuleNameMaxLen)
}
