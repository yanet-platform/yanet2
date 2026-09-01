package pdump_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	"github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
)

// TestShowConfigUnknownConfig verifies that ShowConfig reports NotFound for
// a config name that was never set.
func TestShowConfigUnknownConfig(t *testing.T) {
	service := pdump.NewPdumpService(nil)

	_, err := service.ShowConfig(t.Context(), &pdumppb.ShowConfigRequest{Name: "missing"})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Test_PdumpService_SetConfig_RejectsExplicitZeroSnaplen verifies that an
// explicit snaplen=0 in the update mask is rejected, not defaulted.
func Test_PdumpService_SetConfig_RejectsExplicitZeroSnaplen(t *testing.T) {
	service := pdump.NewPdumpService(nil)

	_, err := service.SetConfig(t.Context(), &pdumppb.SetConfigRequest{
		Name:       "pdump0",
		Config:     &pdumppb.Config{Snaplen: 0},
		UpdateMask: &pdumppb.FieldMask{Paths: []string{"snaplen"}},
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
