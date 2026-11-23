//go:build linux && (amd64 || arm64)

package shm

import (
	"runtime"
	"sync/atomic"
	"time"
	"unsafe"
)

// LockByte takes a one-byte spinlock that C declares as an atomic bool.
//
// Go has no byte-wide atomics, so the byte is set through an atomic OR on
// the aligned word that holds it: a neighbouring lock in the same word
// sees its own byte unchanged. Waiting backs off into sleeps, so a long
// hold by another process parks the goroutine instead of an OS thread.
func LockByte(lock *bool) {
	word, shift := wordOf(unsafe.Pointer(lock))
	for attempt := 0; ; attempt++ {
		if atomic.LoadUint32(word)>>shift&0xff == 0 &&
			atomic.OrUint32(word, 1<<shift)>>shift&0xff == 0 {
			return
		}
		backoff(attempt)
	}
}

// UnlockByte releases a lock taken by LockByte.
func UnlockByte(lock *bool) {
	word, shift := wordOf(unsafe.Pointer(lock))
	atomic.AndUint32(word, ^(uint32(0xff) << shift))
}

// wordOf returns the aligned 32-bit word holding the byte at address and
// the byte's bit shift in it.
//
// The shift assumes a little-endian target, as amd64 and arm64 are: the
// byte at the lowest address is the word's lowest byte.
func wordOf(address unsafe.Pointer) (*uint32, uint) {
	misalignment := uintptr(address) & 3
	return (*uint32)(unsafe.Add(address, -int(misalignment))), uint(misalignment) * 8
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
