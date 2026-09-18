package cnat64_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/modules/nat64/bindings/go/cnat64"
	nat64pb "github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb/v1"
)

// Test_MaxMTU_MatchesC verifies that the protobuf validation bound matches the
// uint16_t limit used by the NAT64 binding.
func Test_MaxMTU_MatchesC(t *testing.T) {
	require.Equal(t, nat64pb.MaxMTU, cnat64.MaxMTU)
}
