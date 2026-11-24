//go:build linux && (amd64 || arm64) && cgo

package shm_test

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// lockWaiters is how many goroutines wait for the configuration lock in
// each phase of the thread test.
const lockWaiters = 1000

// foreignPid stands for a writer in another process holding the lock.
const foreignPid = 1 << 30

// waitBound caps every wait of the thread test, so a lock that never
// releases fails the test instead of hanging it.
//
// An idle run waits tens of milliseconds; a loaded race or sanitizer run
// waits seconds.
const waitBound = 10 * time.Second

// rawSharedMemory mirrors the C handle struct yanet_shm: the base and
// size of the storage in this process.
type rawSharedMemory struct {
	base unsafe.Pointer
	size uintptr
}

// threadsCreated returns how many OS threads the Go runtime holds: those
// it created less those that ended with a goroutine locked to them.
func threadsCreated() int {
	return pprof.Lookup("threadcreate").Count()
}

// awaitThreads waits until more threads than wanted are created past a
// baseline count, or the wait bound passes, and returns how many were.
//
// The runtime hands the processor of a goroutine blocked in a cgo call
// to a new thread only as often as its monitor gets the CPU, so on a
// loaded host the threads come slower than on an idle one.
func awaitThreads(baseline, wanted int) int {
	for deadline := time.Now().Add(waitBound); threadsCreated()-baseline <= wanted && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	return threadsCreated() - baseline
}

// awaitArrival waits until the counter reaches the wanted value, failing
// once the wait bound passes.
func awaitArrival(count *atomic.Int64, want int64) error {
	for deadline := time.Now().Add(waitBound); count.Load() < want; {
		if time.Now().After(deadline) {
			return fmt.Errorf("%d of %d lock waiters arrived after %v", count.Load(), want, waitBound)
		}
		time.Sleep(time.Millisecond)
	}
	return nil
}

// awaitWaiters waits for a group of lock waiters to finish, failing once
// the wait bound passes.
func awaitWaiters(group *errgroup.Group) error {
	done := make(chan error, 1)
	go func() { done <- group.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(waitBound):
		return fmt.Errorf("lock waiters still blocked after %v", waitBound)
	}
}

// Test_LockPid_WaitersHoldNoThreads verifies that goroutines waiting for
// the configuration lock park without OS threads; cgo waiters hold one each.
//
// A writer holds the lock, as one did for tens of milliseconds per update
// in incident GH-1913, where readers blocked in cgo calls grew the
// control plane to thousands of threads. The native phase runs first,
// and cgo waiters end their threads on exit: an idle thread would serve
// a later waiter without being created for it. A waiter that never
// returns still reads the harness, so a failed wait leaks the harness
// instead of freeing it under the waiter.
func Test_LockPid_WaitersHoldNoThreads(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:    uint64(256 * datasize.MB),
		DPMemory:    uint64(4 * datasize.MB),
		WorkerCount: 1,
		Modules:     []string{"blackhole"},
	})
	require.NoError(t, err)
	settled := true
	t.Cleanup(func() {
		if !settled {
			t.Log("lock waiters still run in the harness, so it is not freed")
			return
		}
		harness.Free()
	})
	owner, err := harness.SharedMemory().AgentAttach("lock-waiters", 0, 128*datasize.MB)
	require.NoError(t, err)

	configs := make([]*cblackhole.ModuleConfig, lockWaiters)
	for idx := range configs {
		configs[idx], err = cblackhole.NewModuleConfig(owner, "waiter"+strconv.Itoa(idx))
		require.NoError(t, err)
	}

	raw := (*rawSharedMemory)(harness.SharedMemory().AsRawPtr())
	storage, err := zone.FromMemory(unsafe.Slice((*byte)(raw.base), raw.size))
	require.NoError(t, err)
	lockWord := &storage.Instances[0].Cp.ConfigLock
	pid := int32(os.Getpid())

	settled = false
	baseline := threadsCreated()
	holder := shm.LockPid(lockWord, foreignPid)
	var native errgroup.Group
	var arrived atomic.Int64
	started := time.Now()
	for range lockWaiters {
		native.Go(func() error {
			arrived.Add(1)
			shm.LockPid(lockWord, pid).Unlock()
			return nil
		})
	}
	// The count waits for every waiter to reach the held lock, then as long
	// as blocked cgo waiters take to create hundreds of threads.
	require.NoError(t, awaitArrival(&arrived, lockWaiters))
	time.Sleep(50 * time.Millisecond)
	nativeCreated := threadsCreated() - baseline
	holder.Unlock()
	require.NoError(t, awaitWaiters(&native))
	nativeDuration := time.Since(started)

	// A lock the native phase left held would block the next take for
	// good; refuse it instead.
	require.Zero(t, atomic.LoadInt32(lockWord), "the native phase left the configuration lock held")
	holder = shm.LockPid(lockWord, foreignPid)
	var cgo errgroup.Group
	started = time.Now()
	baseline = threadsCreated()
	for idx := range configs {
		cgo.Go(func() error {
			// A goroutine that exits locked ends its thread, so the
			// threads of this phase do not serve a later run's waiters.
			runtime.LockOSThread()
			return configs[idx].Free()
		})
	}
	cgoCreated := awaitThreads(baseline, lockWaiters/2)
	holder.Unlock()
	require.NoError(t, awaitWaiters(&cgo))
	cgoDuration := time.Since(started)
	settled = true
	require.NoError(t, owner.CleanUp())

	t.Logf("%d waiters, GOMAXPROCS %d", lockWaiters, runtime.GOMAXPROCS(0))
	t.Logf("native: %d threads created, done %v after start", nativeCreated, nativeDuration)
	t.Logf("cgo:    %d threads created, done %v after start", cgoCreated, cgoDuration)

	require.Less(
		t, nativeCreated, runtime.GOMAXPROCS(0)+16,
		"native waiters must not take an OS thread each",
	)
	require.Greater(
		t, cgoCreated, lockWaiters/2,
		"cgo waiters are expected to hold an OS thread each",
	)
}
