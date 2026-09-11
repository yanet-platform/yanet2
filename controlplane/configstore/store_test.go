package configstore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/common/go/testutils"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

var errInjected = errors.New("injected failure")

// publishing returns a callback that stores the given entry without
// looking at the current one.
func publishing(entry *testutils.FreeSequence) func(*testutils.FreeSequence, bool) (*testutils.FreeSequence, error) {
	return func(*testutils.FreeSequence, bool) (*testutils.FreeSequence, error) {
		return entry, nil
	}
}

// unpublishing returns a delete callback that reports the given error.
func unpublishing(err error) func(*testutils.FreeSequence) error {
	return func(*testutils.FreeSequence) error {
		return err
	}
}

// Test_Store_Update_PublishesAndLists verifies that updated entries are
// returned by name and listed in sorted order.
func Test_Store_Update_PublishesAndLists(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()
	first := testutils.NewFreeSequence()
	second := testutils.NewFreeSequence()

	require.NoError(t, store.Update("b", publishing(first)))
	require.NoError(t, store.Update("a", publishing(second)))

	assert.Equal(t, []string{"a", "b"}, store.Names())

	entry, ok := store.Get("b")
	require.True(t, ok)
	assert.Same(t, first, entry)

	_, ok = store.Get("missing")
	assert.False(t, ok)
}

// Test_Store_Update_CallbackSeesCurrent verifies that the publish
// callback is handed the entry currently published under the name, and
// nothing on a first publish.
func Test_Store_Update_CallbackSeesCurrent(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()
	first := testutils.NewFreeSequence()

	err := store.Update("a", func(current *testutils.FreeSequence, ok bool) (*testutils.FreeSequence, error) {
		assert.False(t, ok)
		assert.Nil(t, current)
		return first, nil
	})
	require.NoError(t, err)

	err = store.Update("a", func(current *testutils.FreeSequence, ok bool) (*testutils.FreeSequence, error) {
		assert.True(t, ok)
		assert.Same(t, first, current)
		return testutils.NewFreeSequence(), nil
	})
	require.NoError(t, err)
}

// Test_Store_Update_PublishFailureLeavesPrevious verifies that a failed
// publish returns its error unchanged and keeps the previous entry
// published and unfreed.
func Test_Store_Update_PublishFailureLeavesPrevious(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()
	first := testutils.NewFreeSequence()
	require.NoError(t, store.Update("a", publishing(first)))

	err := store.Update("a", func(*testutils.FreeSequence, bool) (*testutils.FreeSequence, error) {
		return nil, errInjected
	})
	require.ErrorIs(t, err, errInjected)

	entry, ok := store.Get("a")
	require.True(t, ok)
	assert.Same(t, first, entry)
	assert.Equal(t, int64(0), first.Calls())
}

// Test_Store_Update_FreesSuperseded verifies that a successful publish
// frees the entry it replaced exactly once.
func Test_Store_Update_FreesSuperseded(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()
	first := testutils.NewFreeSequence()
	second := testutils.NewFreeSequence()

	require.NoError(t, store.Update("a", publishing(first)))
	require.NoError(t, store.Update("a", publishing(second)))

	assert.Equal(t, int64(1), first.Freed())
	assert.Equal(t, int64(0), second.Calls())
}

// Test_Store_Update_ParksRefusedThenReclaims verifies that a superseded
// entry whose free is refused is retried by the next mutation of any
// name and released once the refusal ends.
func Test_Store_Update_ParksRefusedThenReclaims(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()
	refusing := testutils.NewFreeSequence(ffi.ErrStillReferenced)

	require.NoError(t, store.Update("a", publishing(refusing)))
	require.NoError(t, store.Update("a", publishing(testutils.NewFreeSequence())))
	assert.Equal(t, int64(1), refusing.Calls())
	assert.Equal(t, int64(0), refusing.Freed())

	require.NoError(t, store.Update("b", publishing(testutils.NewFreeSequence())))
	assert.Equal(t, int64(2), refusing.Calls())
	assert.Equal(t, int64(1), refusing.Freed())

	require.NoError(t, store.Update("c", publishing(testutils.NewFreeSequence())))
	assert.Equal(t, int64(2), refusing.Calls())
}

// Test_Store_Delete_MissingName verifies that deleting an unknown name
// reports not found without running the callback.
func Test_Store_Delete_MissingName(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()

	called := false
	err := store.Delete("a", func(*testutils.FreeSequence) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, configstore.ErrNotFound)
	assert.False(t, called)
}

// Test_Store_Delete_UnpublishFailureKeepsEntry verifies that a refused
// delete returns its error unchanged and leaves the entry published and
// unfreed.
func Test_Store_Delete_UnpublishFailureKeepsEntry(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()
	first := testutils.NewFreeSequence()
	require.NoError(t, store.Update("a", publishing(first)))

	err := store.Delete("a", unpublishing(errInjected))
	require.ErrorIs(t, err, errInjected)

	assert.Equal(t, []string{"a"}, store.Names())
	assert.Equal(t, int64(0), first.Calls())
}

// Test_Store_Delete_FreesEntry verifies that a successful delete forgets
// the name and frees its entry.
func Test_Store_Delete_FreesEntry(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()
	first := testutils.NewFreeSequence()
	require.NoError(t, store.Update("a", publishing(first)))

	require.NoError(t, store.Delete("a", unpublishing(nil)))

	assert.Empty(t, store.Names())
	assert.Equal(t, int64(1), first.Freed())
}

// Test_Store_Delete_ParksRefusedThenReclaims verifies that a deleted
// entry whose free is refused is parked and released by a later delete.
func Test_Store_Delete_ParksRefusedThenReclaims(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()
	refusing := testutils.NewFreeSequence(ffi.ErrStillReferenced)
	require.NoError(t, store.Update("a", publishing(refusing)))
	require.NoError(t, store.Update("b", publishing(testutils.NewFreeSequence())))

	require.NoError(t, store.Delete("a", unpublishing(nil)))
	assert.Equal(t, []string{"b"}, store.Names())
	assert.Equal(t, int64(0), refusing.Freed())

	require.NoError(t, store.Delete("b", unpublishing(nil)))
	assert.Equal(t, int64(1), refusing.Freed())
}

// Test_Store_ReclaimDeferred_RetriesUntilDrained verifies that an
// explicit reclaim retries a parked entry on every call and drops it once
// the free succeeds.
func Test_Store_ReclaimDeferred_RetriesUntilDrained(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()
	refusing := testutils.NewFreeSequence(
		ffi.ErrStillReferenced,
		ffi.ErrStillReferenced,
		ffi.ErrStillReferenced,
	)
	require.NoError(t, store.Update("a", publishing(refusing)))
	require.NoError(t, store.Delete("a", unpublishing(nil)))
	require.Equal(t, int64(1), refusing.Calls())

	store.ReclaimDeferred()
	assert.Equal(t, int64(2), refusing.Calls())
	assert.Equal(t, int64(0), refusing.Freed())

	store.ReclaimDeferred()
	store.ReclaimDeferred()
	assert.Equal(t, int64(4), refusing.Calls())
	assert.Equal(t, int64(1), refusing.Freed())

	store.ReclaimDeferred()
	assert.Equal(t, int64(4), refusing.Calls())
}

// Test_Store_Reads_DoNotWaitForPublish verifies that lookups complete
// while a publish is in flight, so a slow shared-memory write never
// stalls the read path.
func Test_Store_Reads_DoNotWaitForPublish(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()
	first := testutils.NewFreeSequence()
	require.NoError(t, store.Update("a", publishing(first)))

	entered := make(chan struct{})
	release := make(chan struct{})

	var group errgroup.Group
	group.Go(func() error {
		return store.Update("a", func(*testutils.FreeSequence, bool) (*testutils.FreeSequence, error) {
			close(entered)
			<-release
			return testutils.NewFreeSequence(), nil
		})
	})
	<-entered

	type readResult struct {
		entry *testutils.FreeSequence
		ok    bool
		names []string
	}
	reads := make(chan readResult, 1)
	go func() {
		entry, ok := store.Get("a")
		reads <- readResult{entry: entry, ok: ok, names: store.Names()}
	}()

	var result readResult
	select {
	case result = <-reads:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("reads blocked behind an in-flight publish")
	}
	assert.True(t, result.ok)
	assert.Same(t, first, result.entry)
	assert.Equal(t, []string{"a"}, result.names)

	close(release)
	require.NoError(t, group.Wait())
}

// Test_Store_Writers_Serialize verifies that a second mutation does not
// start its callback until the first mutation has finished, whatever the
// names involved.
func Test_Store_Writers_Serialize(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()

	entered := make(chan struct{})
	release := make(chan struct{})
	secondStarted := make(chan struct{})

	var group errgroup.Group
	group.Go(func() error {
		return store.Update("a", func(*testutils.FreeSequence, bool) (*testutils.FreeSequence, error) {
			close(entered)
			<-release
			return testutils.NewFreeSequence(), nil
		})
	})
	<-entered

	secondLaunched := make(chan struct{})
	group.Go(func() error {
		close(secondLaunched)
		return store.Update("b", func(*testutils.FreeSequence, bool) (*testutils.FreeSequence, error) {
			close(secondStarted)
			return testutils.NewFreeSequence(), nil
		})
	})
	<-secondLaunched

	select {
	case <-secondStarted:
		close(release)
		t.Fatal("second publish started while the first was in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	require.NoError(t, group.Wait())
	<-secondStarted
	assert.Equal(t, []string{"a", "b"}, store.Names())
}

// Test_Store_ConcurrentMutations verifies that interleaved updates,
// deletes and reads over shared names leave every published entry
// consistent and free each retired entry exactly once.
func Test_Store_ConcurrentMutations(t *testing.T) {
	store := configstore.NewStore[*testutils.FreeSequence]()
	names := []string{"a", "b", "c"}

	const workers = 8
	const iterations = 200

	var minted []*testutils.FreeSequence
	var group errgroup.Group
	handles := make(chan *testutils.FreeSequence, workers*iterations)
	for idx := range workers {
		group.Go(func() error {
			for jdx := range iterations {
				name := names[(idx+jdx)%len(names)]
				if jdx%5 == 4 {
					err := store.Delete(name, unpublishing(nil))
					if err != nil && !errors.Is(err, configstore.ErrNotFound) {
						return err
					}
					continue
				}
				handle := testutils.NewFreeSequence()
				handles <- handle
				if err := store.Update(name, publishing(handle)); err != nil {
					return err
				}
			}
			return nil
		})
	}
	for range workers {
		group.Go(func() error {
			for range iterations {
				for _, name := range store.Names() {
					store.Get(name)
				}
			}
			return nil
		})
	}
	require.NoError(t, group.Wait())
	close(handles)
	for handle := range handles {
		minted = append(minted, handle)
	}

	live := map[*testutils.FreeSequence]bool{}
	for _, name := range store.Names() {
		entry, ok := store.Get(name)
		require.True(t, ok)
		live[entry] = true
	}
	for _, handle := range minted {
		if live[handle] {
			assert.Equal(t, int64(0), handle.Calls())
			continue
		}
		assert.Equal(t, int64(1), handle.Freed())
	}
}
