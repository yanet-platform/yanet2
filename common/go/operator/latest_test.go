package operator_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/operator"
)

// Test_Latest_Publish_WakesReaders verifies that a publication closes the
// channel of the version readers held before it.
func Test_Latest_Publish_WakesReaders(t *testing.T) {
	latest := operator.NewLatest[string]()
	before := latest.Load()

	latest.Publish("first")

	select {
	case <-before.Replaced:
	default:
		t.Fatal("the version held before the publication is not replaced")
	}
}

// Test_Latest_Load_SkipsIntermediateValues verifies that a reader loading
// after several publications gets only the newest one, still not replaced.
func Test_Latest_Load_SkipsIntermediateValues(t *testing.T) {
	latest := operator.NewLatest[string]()
	latest.Publish("first")
	latest.Publish("second")

	version := latest.Load()

	require.Equal(t, "second", version.Value)
	require.Equal(t, uint64(2), version.Number)
	select {
	case <-version.Replaced:
		t.Fatal("the newest version is reported as replaced")
	default:
	}
}
