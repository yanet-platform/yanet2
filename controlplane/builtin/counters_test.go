package builtin_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/builtin"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// An oversized query is refused before any shared memory is touched.
func TestCountersByTagsRejectsOversizedQuery(t *testing.T) {
	svc := builtin.NewCounters(0, nil)

	query := make([]string, 65)
	for idx := range query {
		query[idx] = fmt.Sprintf("counter_%d", idx)
	}

	response, err := svc.ByTags(t.Context(), &ynpb.CountersByTagsRequest{
		Query: query,
	})

	require.Nil(t, response)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
