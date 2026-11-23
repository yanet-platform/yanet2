//go:build linux && (amd64 || arm64)

package shm_test

import (
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// blockedFor is how long a taker must stay blocked to count as waiting
// for a held lock.
const blockedFor = 50 * time.Millisecond

// takeAsync starts taking a lock on another goroutine and returns a
// channel closed once it holds it.
func takeAsync(take func()) <-chan struct{} {
	acquired := make(chan struct{})
	go func() {
		take()
		close(acquired)
	}()
	return acquired
}

// requireBlocked fails unless the taker still waits after blockedFor.
func requireBlocked(t *testing.T, acquired <-chan struct{}) {
	t.Helper()
	select {
	case <-acquired:
		t.Fatal("the lock was taken while held")
	case <-time.After(blockedFor):
	}
}

// Test_LockByte_LeavesNeighbours verifies that taking and releasing a byte
// lock changes only its own byte of the word it shares with other locks,
// as C's one-byte spinlocks expect.
func Test_LockByte_LeavesNeighbours(t *testing.T) {
	cases := []struct {
		name   string
		offset int
	}{
		{"lowest byte of the word", 0},
		{"second byte of the word", 1},
		{"third byte of the word", 2},
		{"highest byte of the word", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			words := [2]uint32{0x5a5a5a5a, 0x5a5a5a5a}
			memory := unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), 8)
			memory[tc.offset] = 0
			lock := (*bool)(unsafe.Pointer(&memory[tc.offset]))

			shm.LockByte(lock)
			locked := [8]byte(memory)
			shm.UnlockByte(lock)

			want := [8]byte{0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a, 0x5a}
			want[tc.offset] = 1
			require.Equal(t, want, locked)
			want[tc.offset] = 0
			require.Equal(t, want, [8]byte(memory))
		})
	}
}

// Test_LockByte_WaitsForHolder verifies that a byte lock C holds, its byte
// set to true, keeps a taker waiting until C clears it, while the other
// lock of the same word stays free.
func Test_LockByte_WaitsForHolder(t *testing.T) {
	cases := []struct {
		name      string
		held      int
		neighbour int
	}{
		{"first lock of the word held", 0, 1},
		{"second lock of the word held", 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var word uint32
			memory := unsafe.Slice((*byte)(unsafe.Pointer(&word)), 4)
			held := (*bool)(unsafe.Pointer(&memory[tc.held]))
			neighbour := (*bool)(unsafe.Pointer(&memory[tc.neighbour]))
			atomic.StoreUint32(&word, 1<<(8*tc.held))

			acquired := takeAsync(func() { shm.LockByte(held) })
			shm.LockByte(neighbour)
			shm.UnlockByte(neighbour)
			requireBlocked(t, acquired)

			atomic.AndUint32(&word, ^(uint32(0xff) << (8 * tc.held)))
			<-acquired
			require.Equal(t, uint32(1)<<(8*tc.held), atomic.LoadUint32(&word))
			shm.UnlockByte(held)
		})
	}
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
	var word int32
	holder := shm.LockPid(&word, 7)

	acquired := takeAsync(func() { shm.LockPid(&word, 42) })
	requireBlocked(t, acquired)

	holder.Unlock()
	<-acquired
	require.Equal(t, int32(42), atomic.LoadInt32(&word))
}

// Test_LockPid_RefusesPidZero verifies that a lock cannot be taken for pid
// zero, which would leave it looking free.
func Test_LockPid_RefusesPidZero(t *testing.T) {
	var word int32
	require.Panics(t, func() { shm.LockPid(&word, 0) })
	require.Zero(t, word)
}
