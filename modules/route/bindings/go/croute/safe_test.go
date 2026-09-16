package croute_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/modules/route/bindings/go/croute"
	routepb "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// Test_MaxCounterNameLen_MatchesC verifies that the pure-Go usable bound
// matches the C counter-name buffer limit.
func Test_MaxCounterNameLen_MatchesC(t *testing.T) {
	require.Equal(t, routepb.MaxCounterNameLen, croute.CounterNameMaxLen)
}
