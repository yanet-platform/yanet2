//go:build linux && (amd64 || arm64)

package shm

import (
	"runtime"
	"sync/atomic"
	"time"
)

// LockSpin takes a spinlock that C keeps as a 32-bit word, one while
// held.
//
// Waiting backs off into sleeps, so a long hold by another process parks
// the goroutine instead of an OS thread.
func LockSpin(lock *uint32) {
	for attempt := 0; ; attempt++ {
		if atomic.LoadUint32(lock) == 0 && atomic.CompareAndSwapUint32(lock, 0, 1) {
			return
		}
		backoff(attempt)
	}
}

// UnlockSpin releases a lock taken by LockSpin.
func UnlockSpin(lock *uint32) {
	atomic.StoreUint32(lock, 0)
}

// PidLock is a lock C keeps as the process id of its holder.
type PidLock struct {
	word *int32
	pid  int32
}

// LockPid takes the lock for this process and returns the token that
// releases it.
//
// The word only names a process, so two goroutines of one process exclude
// each other only through the word being nonzero: the token, not the
// pid, is what proves a goroutine holds the lock. A zero pid would leave
// the lock looking free, so it panics.
func LockPid(word *int32, pid int32) *PidLock {
	if pid == 0 {
		panic("shm: a process-id lock cannot be taken for pid 0")
	}
	for attempt := 0; ; attempt++ {
		if atomic.LoadInt32(word) == 0 && atomic.CompareAndSwapInt32(word, 0, pid) {
			return &PidLock{word: word, pid: pid}
		}
		backoff(attempt)
	}
}

// Unlock releases the lock; a second call does nothing, so it cannot
// release the lock another goroutine of this process took since.
func (m *PidLock) Unlock() {
	if m.word == nil {
		return
	}
	atomic.CompareAndSwapInt32(m.word, m.pid, 0)
	m.word = nil
}

// backoff waits before the next attempt on a contended lock: yields
// first, as C spins, then sleeps of growing length up to a millisecond.
func backoff(attempt int) {
	switch {
	case attempt < 64:
		runtime.Gosched()
	default:
		delay := time.Microsecond << min(attempt-64, 10)
		time.Sleep(min(delay, time.Millisecond))
	}
}
