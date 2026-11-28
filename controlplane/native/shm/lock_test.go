//go:build linux && (amd64 || arm64)

package shm_test

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// blockedFor is how long a taker must stay blocked to count as waiting
// for a held lock.
//
// The waiting tests run in a synctest bubble, so the time is fake and the
// lock's back-off sleeps pass instantly.
const blockedFor = 50 * time.Millisecond

// requireBlocked fails unless a taker that started before still waits
// after blockedFor, once every goroutine of the bubble is blocked.
func requireBlocked(t *testing.T, acquired *atomic.Bool) {
	t.Helper()
	time.Sleep(blockedFor)
	synctest.Wait()
	require.False(t, acquired.Load(), "the lock was taken while held")
}

// Test_LockSpin_HoldsWordUntilUnlock verifies that a spinlock word reads
// one while held and zero once released, as C's spinlock keeps it.
func Test_LockSpin_HoldsWordUntilUnlock(t *testing.T) {
	var word uint32
	shm.LockSpin(&word)
	require.Equal(t, uint32(1), atomic.LoadUint32(&word))
	shm.UnlockSpin(&word)
	require.Zero(t, atomic.LoadUint32(&word))
}

// Test_LockSpin_WaitsForHolder verifies that a spinlock C holds, its word
// set to one, keeps a taker waiting until C clears it.
func Test_LockSpin_WaitsForHolder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var word uint32
		atomic.StoreUint32(&word, 1)

		var taker errgroup.Group
		var acquired atomic.Bool
		taker.Go(func() error {
			shm.LockSpin(&word)
			acquired.Store(true)
			return nil
		})
		requireBlocked(t, &acquired)

		atomic.StoreUint32(&word, 0)
		require.NoError(t, taker.Wait())
		require.Equal(t, uint32(1), atomic.LoadUint32(&word))
		shm.UnlockSpin(&word)
	})
}

// Test_Lock_HoldsWordUntilUnlock verifies that the word of an SDK mutex
// reads one while held and zero once released, as the Rust lock keeps it.
func Test_Lock_HoldsWordUntilUnlock(t *testing.T) {
	var word atomic.Uint32
	shm.Lock(&word)
	require.Equal(t, uint32(1), word.Load())
	shm.Unlock(&word)
	require.Zero(t, word.Load())
}

// Test_TryLock_TakesOnlyAFreeLock verifies that a nonblocking take takes a
// free lock and refuses one of any nonzero word, which the Rust lock counts
// as held, leaving the word as it was.
func Test_TryLock_TakesOnlyAFreeLock(t *testing.T) {
	cases := []struct {
		name  string
		word  uint32
		taken bool
		after uint32
	}{
		{name: "free lock", word: 0, taken: true, after: 1},
		{name: "held lock", word: 1, taken: false, after: 1},
		{name: "other nonzero word", word: 7, taken: false, after: 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var word atomic.Uint32
			word.Store(tc.word)

			taken := shm.TryLock(&word)

			require.Equal(t, tc.taken, taken)
			require.Equal(t, tc.after, word.Load())
		})
	}
}

// Test_Lock_WaitsForHolder verifies that an SDK mutex a worker holds, its
// word set to one, keeps a taker waiting until the worker clears it.
func Test_Lock_WaitsForHolder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var word atomic.Uint32
		word.Store(1)

		var taker errgroup.Group
		var acquired atomic.Bool
		taker.Go(func() error {
			shm.Lock(&word)
			acquired.Store(true)
			return nil
		})
		requireBlocked(t, &acquired)

		word.Store(0)
		require.NoError(t, taker.Wait())
		require.Equal(t, uint32(1), word.Load())
		shm.Unlock(&word)
	})
}

// Test_Lock_ExcludesConcurrentHolders verifies that goroutines adding to a
// plain count under an SDK mutex, half of them taking it without waiting,
// never overlap: no add is lost.
//
// Under the race detector, a missing happens-before edge between one
// holder's release and the next holder's take is a reported race.
func Test_Lock_ExcludesConcurrentHolders(t *testing.T) {
	const goroutines, rounds = 8, 10_000
	var word atomic.Uint32
	var count uint64

	var holders errgroup.Group
	for idx := range goroutines {
		take := shm.Lock
		if idx%2 == 1 {
			take = func(word *atomic.Uint32) {
				for !shm.TryLock(word) {
					runtime.Gosched()
				}
			}
		}
		holders.Go(func() error {
			for range rounds {
				take(&word)
				count++
				shm.Unlock(&word)
			}
			return nil
		})
	}
	require.NoError(t, holders.Wait())

	require.Equal(t, uint64(goroutines*rounds), count)
	require.Zero(t, word.Load())
}

// Test_LockPid_HoldsPidUntilUnlock verifies that a process-id lock stores
// its holder's id while held, clears it on release, and that a second
// release leaves a later holder's lock alone.
func Test_LockPid_HoldsPidUntilUnlock(t *testing.T) {
	var word int32
	lock := shm.LockPid(&word, 42)
	require.Equal(t, int32(42), atomic.LoadInt32(&word))
	lock.Unlock()
	require.Zero(t, atomic.LoadInt32(&word))

	later := shm.LockPid(&word, 42)
	lock.Unlock()
	require.Equal(t, int32(42), atomic.LoadInt32(&word))
	later.Unlock()
	require.Zero(t, atomic.LoadInt32(&word))
}

// Test_LockPid_WaitsForHolder verifies that a taker waits while another
// holds the process-id lock and takes it once released.
func Test_LockPid_WaitsForHolder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var word int32
		holder := shm.LockPid(&word, 7)

		var taker errgroup.Group
		var acquired atomic.Bool
		taker.Go(func() error {
			shm.LockPid(&word, 42)
			acquired.Store(true)
			return nil
		})
		requireBlocked(t, &acquired)

		holder.Unlock()
		require.NoError(t, taker.Wait())
		require.Equal(t, int32(42), atomic.LoadInt32(&word))
	})
}

// Test_LockPid_RefusesPidZero verifies that a lock cannot be taken for pid
// zero, which would leave it looking free.
func Test_LockPid_RefusesPidZero(t *testing.T) {
	var word int32
	require.Panics(t, func() { shm.LockPid(&word, 0) })
	require.Zero(t, word)
}

// lockWaiters is how many goroutines wait for the configuration lock in
// the thread test.
const lockWaiters = 1000

// foreignPid stands for a writer in another process holding the lock.
const foreignPid = 1 << 30

// waitBound caps the wait for the thread test's waiters to park, so a
// waiter that never parks fails the test instead of hanging it.
const waitBound = 10 * time.Second

// threadsCreated returns how many OS threads the Go runtime holds: those
// it created less those that ended with a goroutine locked to them.
func threadsCreated() int {
	return pprof.Lookup("threadcreate").Count()
}

// Test_LockPid_WaitersHoldNoThreads verifies that goroutines waiting for
// a control-plane zone's configuration lock park without OS threads.
//
// A writer holds the lock, as one did for tens of milliseconds per update
// in incident GH-1913, where readers blocked in cgo calls grew the
// control plane to thousands of threads. The waiters run in a bubble,
// whose wait returns only once each of them is parked; one blocked in a
// system call would keep it waiting, so a watchdog bounds the test.
func Test_LockPid_WaitersHoldNoThreads(t *testing.T) {
	lockWord := new(int32)
	pid := int32(os.Getpid())
	watchdog := time.AfterFunc(waitBound, func() {
		panic(fmt.Sprintf("%s: lock waiters did not park within %v", t.Name(), waitBound))
	})
	defer watchdog.Stop()

	synctest.Test(t, func(t *testing.T) {
		baseline := threadsCreated()
		holder := shm.LockPid(lockWord, foreignPid)
		var waiters errgroup.Group
		for range lockWaiters {
			waiters.Go(func() error {
				shm.LockPid(lockWord, pid).Unlock()
				return nil
			})
		}
		time.Sleep(blockedFor)
		synctest.Wait()
		created := threadsCreated() - baseline
		holder.Unlock()
		require.NoError(t, waiters.Wait())

		t.Logf("%d waiters, GOMAXPROCS %d: %d threads created", lockWaiters, runtime.GOMAXPROCS(0), created)
		require.Less(t, created, runtime.GOMAXPROCS(0)+16, "lock waiters must not take an OS thread each")
		require.Zero(t, atomic.LoadInt32(lockWord), "every waiter released the lock")
	})
}
