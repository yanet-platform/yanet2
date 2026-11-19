//go:build linux && (amd64 || arm64)

package shm_test

import (
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
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

// waiters is how many goroutines wait for the configuration lock in each
// phase.
const waiters = 1000

// foreignPid stands for a writer in another process holding the lock.
const foreignPid = 1 << 30

// threadsCreated returns how many OS threads the Go runtime has created,
// less the ones that exited with a goroutine locked to them; no thread
// of this test exits.
func threadsCreated() int {
	return pprof.Lookup("threadcreate").Count()
}

// awaitThreads waits until more threads than wanted are created past a
// baseline count, or the timeout passes, and returns how many were.
//
// The runtime hands the processor of a goroutine blocked in a cgo call
// to a new thread only as often as its monitor gets the CPU, so on a
// loaded host the threads come slower than on an idle one.
func awaitThreads(baseline, wanted int, timeout time.Duration) int {
	for deadline := time.Now().Add(timeout); threadsCreated()-baseline <= wanted && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	return threadsCreated() - baseline
}

// rawSharedMemory mirrors the C handle struct yanet_shm.
type rawSharedMemory struct {
	base unsafe.Pointer
	size uintptr
}

// Test_LockPid_WaitersHoldNoThreads verifies that goroutines waiting for
// the configuration lock through the native lock park without OS threads,
// while as many waiting for it inside cgo calls hold an OS thread each.
//
// A writer holds the lock, as one did for tens of milliseconds per update
// in incident GH-1913, where readers blocked in cgo calls grew the
// control plane to thousands of threads. The native phase runs first:
// threads created for cgo calls outlive them.
func Test_LockPid_WaitersHoldNoThreads(t *testing.T) {
	harness, err := dataplaneut.NewHarness(dataplaneut.Config{
		CPMemory:    uint64(256 * datasize.MB),
		DPMemory:    uint64(4 * datasize.MB),
		WorkerCount: 1,
		Modules:     []string{"blackhole"},
	})
	require.NoError(t, err)
	t.Cleanup(harness.Free)
	agent, err := harness.SharedMemory().AgentAttach("lock-demo", 0, 128*datasize.MB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = agent.CleanUp() })

	configs := make([]*cblackhole.ModuleConfig, waiters)
	for idx := range configs {
		configs[idx], err = cblackhole.NewModuleConfig(agent, "waiter"+strconv.Itoa(idx))
		require.NoError(t, err)
	}

	raw := (*rawSharedMemory)(harness.SharedMemory().AsRawPtr())
	storage, err := zone.FromMemory(unsafe.Slice((*byte)(raw.base), raw.size))
	require.NoError(t, err)
	lockWord := &storage.Instances[0].Cp.ConfigLock
	pid := int32(os.Getpid())
	baseline := threadsCreated()

	holder := shm.LockPid(lockWord, foreignPid)
	var native errgroup.Group
	started := time.Now()
	for range waiters {
		native.Go(func() error {
			shm.LockPid(lockWord, pid).Unlock()
			return nil
		})
	}
	time.Sleep(500 * time.Millisecond)
	nativeCreated := threadsCreated() - baseline
	holder.Unlock()
	require.NoError(t, native.Wait())
	nativeDuration := time.Since(started)

	holder = shm.LockPid(lockWord, foreignPid)
	var cgo errgroup.Group
	started = time.Now()
	baseline = threadsCreated()
	for idx := range configs {
		cgo.Go(configs[idx].Free)
	}
	cgoCreated := awaitThreads(baseline, waiters/2, 30*time.Second)
	holder.Unlock()
	require.NoError(t, cgo.Wait())
	cgoDuration := time.Since(started)

	t.Logf("%d waiters, GOMAXPROCS %d", waiters, runtime.GOMAXPROCS(0))
	t.Logf("native: %d threads created, done %v after start", nativeCreated, nativeDuration)
	t.Logf("cgo:    %d threads created, done %v after start", cgoCreated, cgoDuration)

	require.Less(t, nativeCreated, runtime.GOMAXPROCS(0)+16,
		"native waiters must not take an OS thread each")
	require.Greater(t, cgoCreated, waiters/2,
		"cgo waiters are expected to hold an OS thread each")
}
