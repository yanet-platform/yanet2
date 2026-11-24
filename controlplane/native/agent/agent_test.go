//go:build linux && (amd64 || arm64) && cgo

package agent_test

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	dataplaneut "github.com/yanet-platform/yanet2/bindings/go/dataplane_ut"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/nativetest"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
	"github.com/yanet-platform/yanet2/modules/blackhole/bindings/go/cblackhole"
)

// newFixture returns a one-worker dataplane with blackhole and the
// default zones.
func newFixture(t *testing.T) *nativetest.Fixture {
	t.Helper()
	return nativetest.NewFixture(t, dataplaneut.Config{WorkerCount: 1, Modules: []string{"blackhole"}})
}

// attachC attaches an agent through the cgo control plane.
func attachC(t *testing.T, memory *nativetest.Fixture, name string, limit datasize.ByteSize) *ffi.Agent {
	t.Helper()
	attached, err := memory.Harness.SharedMemory().AgentAttach(name, 0, limit)
	require.NoError(t, err)
	return attached
}

// agentOf returns the shared-memory agent behind a cgo handle.
func agentOf(attached *ffi.Agent) *layout.Agent {
	return (*layout.Agent)(attached.AsRawPtr())
}

// supersededChain attaches two agents of one name through C, the first
// shown as holding a configuration so the second does not sweep it, and
// returns both, oldest first.
//
// The holds are only counts written into the shared agents: no
// configuration refers to either agent, so a test may drop a count to
// make its agent reclaimable without leaving a configuration dangling.
func supersededChain(t *testing.T, memory *nativetest.Fixture, name string) (*layout.Agent, *layout.Agent) {
	t.Helper()
	oldest := agentOf(attachC(t, memory, name, 2*datasize.MB))
	atomic.StoreUint64(&oldest.LoadedEntityCount, 1)
	newer := agentOf(attachC(t, memory, name, 2*datasize.MB))
	require.Same(t, oldest, newer.Prev.Get())
	return oldest, newer
}

// registered returns the agent the instance's registry holds under that
// name, or nil.
func registered(instance *zone.Instance, name string) *layout.Agent {
	registry := instance.Cp.AgentRegistry.Get()
	slots := registry.Agents(int(registry.Count))
	for idx := range slots {
		if found := slots[idx].Get(); shm.String(found.Name[:]) == name {
			return found
		}
	}
	return nil
}

// leaveOnly takes every free block of the allocator but one block of each
// of the sizes, so a later request for anything else fails.
//
// The kept blocks are taken first, so each one sits in the pool its size
// selects; the pools are then emptied from the largest down, so no larger
// block is left to split.
func leaveOnly(t *testing.T, allocator *layout.BlockAllocator, sizes ...uintptr) {
	t.Helper()
	var kept []unsafe.Pointer
	for _, size := range sizes {
		block := shmalloc.Alloc(allocator, size)
		require.NotNil(t, block)
		kept = append(kept, block)
	}
	for pool := shmalloc.PoolCount - 1; pool >= 0; pool-- {
		size := uintptr(1)<<(shmalloc.MinBits+pool) - 2*shmalloc.RedZone
		if int(size) <= 0 {
			continue
		}
		for shmalloc.Alloc(allocator, size) != nil {
		}
	}
	for idx, block := range kept {
		shmalloc.Free(allocator, block, sizes[idx])
	}
}

// Test_Attach_MatchesC verifies that attaching an agent from Go leaves the
// storage byte for byte as attaching it from C does, and that the
// registry then holds the new agent with the predecessor C keeps.
//
// A predecessor that holds a configuration stays chained behind the new
// agent; one that holds nothing is swept by the attach itself, also from
// below a predecessor that stays.
func Test_Attach_MatchesC(t *testing.T) {
	cases := []struct {
		name  string
		agent string
		// setup prepares the storage and returns the predecessor the new
		// agent must keep, or nil.
		setup func(t *testing.T, memory *nativetest.Fixture) *layout.Agent
	}{
		{
			name:  "new agent",
			agent: "probe",
			setup: func(*testing.T, *nativetest.Fixture) *layout.Agent { return nil },
		},
		{
			name:  "new agent with the longest name",
			agent: strings.Repeat("n", agent.NameSize-1),
			setup: func(*testing.T, *nativetest.Fixture) *layout.Agent { return nil },
		},
		{
			name:  "superseding an agent that holds a published configuration",
			agent: "probe",
			setup: func(t *testing.T, memory *nativetest.Fixture) *layout.Agent {
				previous := attachC(t, memory, "probe", 2*datasize.MB)
				config, err := cblackhole.NewModuleConfig(previous, "held")
				require.NoError(t, err)
				require.NoError(t, previous.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}))
				return agentOf(previous)
			},
		},
		{
			name:  "sweeping a superseded agent that holds nothing",
			agent: "probe",
			setup: func(t *testing.T, memory *nativetest.Fixture) *layout.Agent {
				attachC(t, memory, "probe", 2*datasize.MB)
				return nil
			},
		},
		{
			name:  "sweeping a reclaimable agent below one that holds a configuration",
			agent: "probe",
			setup: func(t *testing.T, memory *nativetest.Fixture) *layout.Agent {
				oldest, newer := supersededChain(t, memory, "probe")
				atomic.StoreUint64(&newer.LoadedEntityCount, 1)
				atomic.StoreUint64(&oldest.LoadedEntityCount, 0)
				return newer
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newFixture(t)
			previous := tc.setup(t, memory)
			var attached *agent.Agent
			memory.CompareWithC(t,
				func() { attachC(t, memory, tc.agent, 8*datasize.MB) },
				func() {
					var err error
					attached, err = agent.Attach(memory.Instance(), tc.agent, uint64(8*datasize.MB))
					require.NoError(t, err)
				},
			)
			require.Same(t, attached.Agent, registered(memory.Instance(), tc.agent))
			require.Same(t, previous, attached.Agent.Prev.Get())
		})
	}
}

// Test_Attach_FailureMatchesC verifies that an attach the zone cannot
// serve fails with the shared exhaustion error and leaves the storage as
// C's failed attach leaves it: what it took returned, the generation pin
// dropped and the superseded agents swept.
func Test_Attach_FailureMatchesC(t *testing.T) {
	cases := []struct {
		name     string
		cpMemory datasize.ByteSize
		limit    datasize.ByteSize
		setup    func(t *testing.T, memory *nativetest.Fixture)
	}{
		{
			name:  "zero memory limit",
			limit: 0,
			setup: func(*testing.T, *nativetest.Fixture) {},
		},
		{
			// The zone holds one block of the largest size, so the first
			// arena fits and the second does not.
			name:     "zone exhausted partway through the arenas",
			cpMemory: 96 * datasize.MB,
			limit:    2 * shmalloc.MaxSize,
			setup:    func(*testing.T, *nativetest.Fixture) {},
		},
		{
			name:  "zone exhausted by the registry growth after the arenas fit",
			limit: datasize.MB,
			setup: func(t *testing.T, memory *nativetest.Fixture) {
				leaveOnly(t, &memory.Instance().Cp.BlockAllocator,
					unsafe.Sizeof(layout.Agent{}), unsafe.Sizeof(layout.AgentArena{}), uintptr(datasize.MB),
				)
			},
		},
		{
			name:  "failed attach sweeping a reclaimable agent",
			limit: 0,
			setup: func(t *testing.T, memory *nativetest.Fixture) {
				oldest, _ := supersededChain(t, memory, "chain")
				atomic.StoreUint64(&oldest.LoadedEntityCount, 0)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := nativetest.NewFixture(t, dataplaneut.Config{
				CPMemory:    uint64(tc.cpMemory),
				WorkerCount: 1,
				Modules:     []string{"blackhole"},
			})
			tc.setup(t, memory)
			memory.CompareWithC(t,
				func() {
					_, err := memory.Harness.SharedMemory().AgentAttach("probe", 0, tc.limit)
					require.Error(t, err)
				},
				func() {
					_, err := agent.Attach(memory.Instance(), "probe", uint64(tc.limit))
					require.ErrorIs(t, err, shmalloc.ErrNoMemory)
				},
			)
			require.Nil(t, registered(memory.Instance(), "probe"))
		})
	}
}

// Test_Attach_SharesRegistryWithC verifies that an agent Go attached is
// listed by the cgo control plane with its name, process and memory limit.
func Test_Attach_SharesRegistryWithC(t *testing.T) {
	memory := newFixture(t)
	_, err := agent.Attach(memory.Instance(), "native", uint64(8*datasize.MB))
	require.NoError(t, err)
	var found bool
	for _, info := range memory.Harness.SharedMemory().DPConfig(0).Agents() {
		if info.Name != "native" {
			continue
		}
		found = true
		require.NotEmpty(t, info.Instances)
		require.Equal(t, uint32(os.Getpid()), info.Instances[0].PID)
		require.Equal(t, uint64(8*datasize.MB), info.Instances[0].MemoryLimit)
	}
	require.True(t, found, "the cgo control plane does not list the native agent")
}

// Test_Attach_RefusesBadName verifies that a name the registry cannot hold
// as given is refused before anything is allocated.
func Test_Attach_RefusesBadName(t *testing.T) {
	cases := []struct {
		name  string
		agent string
	}{
		{"name without room for its terminator", strings.Repeat("n", agent.NameSize)},
		{"name with an inner NUL", "probe\x00other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			memory := newFixture(t)
			memory.CompareWithC(t,
				func() {},
				func() {
					_, err := agent.Attach(memory.Instance(), tc.agent, uint64(8*datasize.MB))
					require.Error(t, err)
				},
			)
		})
	}
}

// Test_Attach_RefusesUnreadyInstance verifies that an instance whose ready
// magic no longer matches this build is refused with the zone's not-ready
// error before anything is allocated.
func Test_Attach_RefusesUnreadyInstance(t *testing.T) {
	memory := newFixture(t)
	instance := memory.Instance()
	atomic.StoreUint64(&instance.Dp.ReadyMagic, 0)
	t.Cleanup(func() { atomic.StoreUint64(&instance.Dp.ReadyMagic, layout.DpConfigReadyMagic) })

	memory.CompareWithC(t,
		func() {},
		func() {
			_, err := agent.Attach(instance, "probe", uint64(8*datasize.MB))
			require.ErrorIs(t, err, zone.ErrNotReady)
		},
	)
}

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

// threadsCreated returns how many OS threads the Go runtime has created,
// less the ones that exited with a goroutine locked to them, as the cgo
// waiters of this test exit.
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

// awaitArrival waits until count reaches want, failing once the wait
// bound passes.
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
// the configuration lock through the native lock park without OS threads,
// while as many waiting for it inside cgo calls hold an OS thread each.
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
	// Once every waiter has arrived at the held lock, the threads they
	// would need get time to appear before the count: blocked cgo waiters
	// create hundreds within tens of milliseconds.
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

	require.Less(t, nativeCreated, runtime.GOMAXPROCS(0)+16,
		"native waiters must not take an OS thread each")
	require.Greater(t, cgoCreated, lockWaiters/2,
		"cgo waiters are expected to hold an OS thread each")
}
