package operator_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/common/go/operator"
)

// startRebuilder runs the rebuilder in the test's bubble and returns the
// function that stops it.
func startRebuilder[T any](t *testing.T, rebuilder *operator.Rebuilder[T]) func() {
	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error {
		return rebuilder.Run(ctx)
	})

	return func() {
		cancel()
		require.True(t, errors.Is(group.Wait(), context.Canceled))
	}
}

// Test_Rebuilder_Run_PublishesAtStart verifies that the desired state is
// built and published once when the rebuilder starts, before any change.
func Test_Rebuilder_Run_PublishesAtStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rebuilder := operator.NewRebuilder(func() string { return "state" })
		stop := startRebuilder(t, rebuilder)
		defer stop()

		synctest.Wait()

		require.Equal(t, uint64(1), rebuilder.Latest().Load().Number)
	})
}

// Test_Rebuilder_Invalidate_FoldsBurst verifies that changes reported
// during the window after a build make a single build.
func Test_Rebuilder_Invalidate_FoldsBurst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rebuilder := operator.NewRebuilder(func() string { return "state" })
		stop := startRebuilder(t, rebuilder)
		defer stop()
		synctest.Wait()
		rebuilder.Latest().Load()

		for range 10 {
			rebuilder.Invalidate()
			time.Sleep(operator.RebuildWindow / 20)
		}
		time.Sleep(3 * operator.RebuildWindow)
		synctest.Wait()

		require.Equal(t, uint64(2), rebuilder.Latest().Load().Number)
	})
}

// Test_Rebuilder_Invalidate_BuildsQuietChangeAtOnce verifies that a change
// coming after a quiet spell is built without waiting for a window.
func Test_Rebuilder_Invalidate_BuildsQuietChangeAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rebuilder := operator.NewRebuilder(func() string { return "state" })
		stop := startRebuilder(t, rebuilder)
		defer stop()
		synctest.Wait()
		rebuilder.Latest().Load()
		time.Sleep(2 * operator.RebuildWindow)
		synctest.Wait()

		rebuilder.Invalidate()
		synctest.Wait()

		require.Equal(t, uint64(2), rebuilder.Latest().Load().Number)
	})
}

// Test_Rebuilder_Invalidate_KeepsChangeDuringBuild verifies that a change
// reported while a build runs is built again after the window.
func Test_Rebuilder_Invalidate_KeepsChangeDuringBuild(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		builds := 0
		var rebuilder *operator.Rebuilder[int]
		rebuilder = operator.NewRebuilder(func() int {
			builds++
			if builds == 1 {
				rebuilder.Invalidate()
			}
			return builds
		})
		stop := startRebuilder(t, rebuilder)
		defer stop()
		synctest.Wait()
		rebuilder.Latest().Load()

		time.Sleep(3 * operator.RebuildWindow)
		synctest.Wait()

		require.Equal(t, 2, rebuilder.Latest().Load().Value)
	})
}

// Test_Rebuilder_Invalidate_WaitsForReader verifies that a change is not
// built until a reader loaded the previous build.
func Test_Rebuilder_Invalidate_WaitsForReader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rebuilder := operator.NewRebuilder(func() string { return "state" })
		stop := startRebuilder(t, rebuilder)
		defer stop()
		synctest.Wait()

		rebuilder.Invalidate()
		time.Sleep(3 * operator.RebuildWindow)
		synctest.Wait()

		require.Equal(t, uint64(1), rebuilder.Latest().Load().Number)
	})
}
