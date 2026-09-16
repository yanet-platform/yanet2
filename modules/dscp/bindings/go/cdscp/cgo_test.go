package cdscp_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/modules/dscp/bindings/go/cdscp"
	"github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
)

// Test_MarkLimits_MatchC verifies that the pure-Go flag and mark bounds match
// the C marking constants.
func Test_MarkLimits_MatchC(t *testing.T) {
	require.Equal(t, dscppb.MaxFlag, cdscp.MaxMarkFlag)
	require.Equal(t, dscppb.MaxMark, cdscp.MaxMark)
}
