package configstore_test

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

var errInjected = errors.New("injected failure")

// freeSequence is a fake entry whose Free returns the scripted outcomes in
// order and succeeds once they run out, counting every call.
type freeSequence struct {
	outcomes []error
	calls    atomic.Int64
	freed    atomic.Int64
}

func newFreeSequence(outcomes ...error) *freeSequence {
	return &freeSequence{outcomes: outcomes}
}

func (m *freeSequence) Free() error {
	call := int(m.calls.Add(1))
	if call <= len(m.outcomes) && m.outcomes[call-1] != nil {
		return m.outcomes[call-1]
	}
	m.freed.Add(1)
	return nil
}

// publishing returns a callback that stores the given entry without
// looking at the current one.
func publishing(entry *freeSequence) func(*freeSequence, bool) (*freeSequence, error) {
	return func(*freeSequence, bool) (*freeSequence, error) {
		return entry, nil
	}
}

// unpublishing returns a delete callback that reports the given error.
func unpublishing(err error) func(*freeSequence) error {
	return func(*freeSequence) error {
		return err
	}
}

// Test_Store_Update_PublishesAndLists verifies that updated entries are
// returned by name and listed in sorted order.
func Test_Store_Update_PublishesAndLists(t *testing.T) {
	store := configstore.NewStore[*freeSequence]()
	first := newFreeSequence()
	second := newFreeSequence()

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
	store := configstore.NewStore[*freeSequence]()
	first := newFreeSequence()

	err := store.Update("a", func(current *freeSequence, ok bool) (*freeSequence, error) {
		assert.False(t, ok)
		assert.Nil(t, current)
		return first, nil
	})
	require.NoError(t, err)

	err = store.Update("a", func(current *freeSequence, ok bool) (*freeSequence, error) {
		assert.True(t, ok)
		assert.Same(t, first, current)
		return newFreeSequence(), nil
	})
	require.NoError(t, err)
}

// Test_Store_Update_PublishFailureLeavesPrevious verifies that a failed
// publish returns its error unchanged and keeps the previous entry
// published and unfreed.
func Test_Store_Update_PublishFailureLeavesPrevious(t *testing.T) {
	store := configstore.NewStore[*freeSequence]()
	first := newFreeSequence()
	require.NoError(t, store.Update("a", publishing(first)))

	err := store.Update("a", func(*freeSequence, bool) (*freeSequence, error) {
		return nil, errInjected
	})
	require.ErrorIs(t, err, errInjected)

	entry, ok := store.Get("a")
	require.True(t, ok)
	assert.Same(t, first, entry)
	assert.Equal(t, int64(0), first.calls.Load())
}

// Test_Store_Update_FreesSuperseded verifies that a successful publish
// frees the entry it replaced exactly once.
func Test_Store_Update_FreesSuperseded(t *testing.T) {
	store := configstore.NewStore[*freeSequence]()
	first := newFreeSequence()
	second := newFreeSequence()

	require.NoError(t, store.Update("a", publishing(first)))
	require.NoError(t, store.Update("a", publishing(second)))

	assert.Equal(t, int64(1), first.freed.Load())
	assert.Equal(t, int64(0), second.calls.Load())
}

// Test_Store_Update_ParksRefusedThenReclaims verifies that a superseded
// entry whose free is refused is retried by the next mutation of any
// name and released once the refusal ends.
func Test_Store_Update_ParksRefusedThenReclaims(t *testing.T) {
	store := configstore.NewStore[*freeSequence]()
	refusing := newFreeSequence(ffi.ErrStillReferenced)

	require.NoError(t, store.Update("a", publishing(refusing)))
	require.NoError(t, store.Update("a", publishing(newFreeSequence())))
	assert.Equal(t, int64(1), refusing.calls.Load())
	assert.Equal(t, int64(0), refusing.freed.Load())

	require.NoError(t, store.Update("b", publishing(newFreeSequence())))
	assert.Equal(t, int64(2), refusing.calls.Load())
	assert.Equal(t, int64(1), refusing.freed.Load())

	require.NoError(t, store.Update("c", publishing(newFreeSequence())))
	assert.Equal(t, int64(2), refusing.calls.Load())
}

// Test_Store_Delete_MissingName verifies that deleting an unknown name
// reports not found without running the callback.
func Test_Store_Delete_MissingName(t *testing.T) {
	store := configstore.NewStore[*freeSequence]()

	called := false
	err := store.Delete("a", func(*freeSequence) error {
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
	store := configstore.NewStore[*freeSequence]()
	first := newFreeSequence()
	require.NoError(t, store.Update("a", publishing(first)))

	err := store.Delete("a", unpublishing(errInjected))
	require.ErrorIs(t, err, errInjected)

	assert.Equal(t, []string{"a"}, store.Names())
	assert.Equal(t, int64(0), first.calls.Load())
}

// Test_Store_Delete_FreesEntry verifies that a successful delete forgets
// the name and frees its entry.
func Test_Store_Delete_FreesEntry(t *testing.T) {
	store := configstore.NewStore[*freeSequence]()
	first := newFreeSequence()
	require.NoError(t, store.Update("a", publishing(first)))

	require.NoError(t, store.Delete("a", unpublishing(nil)))

	assert.Empty(t, store.Names())
	assert.Equal(t, int64(1), first.freed.Load())
}

// Test_Store_Delete_ParksRefusedThenReclaims verifies that a deleted
// entry whose free is refused is parked and released by a later delete.
func Test_Store_Delete_ParksRefusedThenReclaims(t *testing.T) {
	store := configstore.NewStore[*freeSequence]()
	refusing := newFreeSequence(ffi.ErrStillReferenced)
	require.NoError(t, store.Update("a", publishing(refusing)))
	require.NoError(t, store.Update("b", publishing(newFreeSequence())))

	require.NoError(t, store.Delete("a", unpublishing(nil)))
	assert.Equal(t, []string{"b"}, store.Names())
	assert.Equal(t, int64(0), refusing.freed.Load())

	require.NoError(t, store.Delete("b", unpublishing(nil)))
	assert.Equal(t, int64(1), refusing.freed.Load())
}

// Test_Store_ReclaimDeferred_RetriesUntilDrained verifies that an
// explicit reclaim retries a parked entry on every call and drops it once
// the free succeeds.
func Test_Store_ReclaimDeferred_RetriesUntilDrained(t *testing.T) {
	store := configstore.NewStore[*freeSequence]()
	refusing := newFreeSequence(
		ffi.ErrStillReferenced,
		ffi.ErrStillReferenced,
		ffi.ErrStillReferenced,
	)
	require.NoError(t, store.Update("a", publishing(refusing)))
	require.NoError(t, store.Delete("a", unpublishing(nil)))
	require.Equal(t, int64(1), refusing.calls.Load())

	store.ReclaimDeferred()
	assert.Equal(t, int64(2), refusing.calls.Load())
	assert.Equal(t, int64(0), refusing.freed.Load())

	store.ReclaimDeferred()
	store.ReclaimDeferred()
	assert.Equal(t, int64(4), refusing.calls.Load())
	assert.Equal(t, int64(1), refusing.freed.Load())

	store.ReclaimDeferred()
	assert.Equal(t, int64(4), refusing.calls.Load())
}

// Test_Store_Reads_DoNotWaitForPublish verifies that lookups complete
// while a publish is in flight, so a slow shared-memory write never
// stalls the read path.
func Test_Store_Reads_DoNotWaitForPublish(t *testing.T) {
	store := configstore.NewStore[*freeSequence]()
	first := newFreeSequence()
	require.NoError(t, store.Update("a", publishing(first)))

	entered := make(chan struct{})
	release := make(chan struct{})

	var group errgroup.Group
	group.Go(func() error {
		return store.Update("a", func(*freeSequence, bool) (*freeSequence, error) {
			close(entered)
			<-release
			return newFreeSequence(), nil
		})
	})
	<-entered

	type readResult struct {
		entry *freeSequence
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
	store := configstore.NewStore[*freeSequence]()

	entered := make(chan struct{})
	release := make(chan struct{})
	secondStarted := make(chan struct{})

	var group errgroup.Group
	group.Go(func() error {
		return store.Update("a", func(*freeSequence, bool) (*freeSequence, error) {
			close(entered)
			<-release
			return newFreeSequence(), nil
		})
	})
	<-entered

	secondLaunched := make(chan struct{})
	group.Go(func() error {
		close(secondLaunched)
		return store.Update("b", func(*freeSequence, bool) (*freeSequence, error) {
			close(secondStarted)
			return newFreeSequence(), nil
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
	store := configstore.NewStore[*freeSequence]()
	names := []string{"a", "b", "c"}

	const workers = 8
	const iterations = 200

	var minted []*freeSequence
	var group errgroup.Group
	handles := make(chan *freeSequence, workers*iterations)
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
				handle := newFreeSequence()
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

	live := map[*freeSequence]bool{}
	for _, name := range store.Names() {
		entry, ok := store.Get(name)
		require.True(t, ok)
		live[entry] = true
	}
	for _, handle := range minted {
		if live[handle] {
			assert.Equal(t, int64(0), handle.calls.Load())
			continue
		}
		assert.Equal(t, int64(1), handle.freed.Load())
	}
}
