package cacl_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
)

// Test_MaxActions_MatchesC verifies that the pure-Go action bound matches
// the C per-rule storage capacity.
func Test_MaxActions_MatchesC(t *testing.T) {
	require.Equal(t, aclpb.MaxActions, cacl.MaxActions)
}
