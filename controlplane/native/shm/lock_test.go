//go:build linux && amd64

package shm_test

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
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

// threadCount returns the number of OS threads of this process.
func threadCount(t *testing.T) int {
	t.Helper()
	status, err := os.Open("/proc/self/status")
	require.NoError(t, err)
	defer status.Close()
	scanner := bufio.NewScanner(status)
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(scanner.Text(), "Threads:"); ok {
			count, err := strconv.Atoi(strings.TrimSpace(value))
			require.NoError(t, err)
			return count
		}
	}
	t.Fatal("no thread count in /proc/self/status")
	return 0
}

// peakThreads samples the thread count for the duration and returns the
// largest value seen.
func peakThreads(t *testing.T, duration time.Duration) int {
	t.Helper()
	peak := 0
	for deadline := time.Now().Add(duration); time.Now().Before(deadline); {
		peak = max(peak, threadCount(t))
		time.Sleep(10 * time.Millisecond)
	}
	return peak
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
	baseline := threadCount(t)

	holder := shm.LockPid(lockWord, foreignPid)
	var native errgroup.Group
	started := time.Now()
	for range waiters {
		native.Go(func() error {
			shm.LockPid(lockWord, pid).Unlock()
			return nil
		})
	}
	nativePeak := peakThreads(t, 500*time.Millisecond)
	holder.Unlock()
	require.NoError(t, native.Wait())
	nativeDuration := time.Since(started)

	holder = shm.LockPid(lockWord, foreignPid)
	var cgo errgroup.Group
	started = time.Now()
	for idx := range configs {
		cgo.Go(configs[idx].Free)
	}
	cgoPeak := peakThreads(t, 500*time.Millisecond)
	holder.Unlock()
	require.NoError(t, cgo.Wait())
	cgoDuration := time.Since(started)

	t.Logf("%d waiters, GOMAXPROCS %d, %d threads before", waiters, runtime.GOMAXPROCS(0), baseline)
	t.Logf("native: peak %d threads (+%d), done %v after start", nativePeak, nativePeak-baseline, nativeDuration)
	t.Logf("cgo:    peak %d threads (+%d), done %v after start", cgoPeak, cgoPeak-baseline, cgoDuration)

	require.Less(t, nativePeak-baseline, runtime.GOMAXPROCS(0)+16,
		"native waiters must not take an OS thread each")
	require.Greater(t, cgoPeak-baseline, waiters/2,
		"cgo waiters are expected to hold an OS thread each")
}
