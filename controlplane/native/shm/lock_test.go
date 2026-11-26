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
