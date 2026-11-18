//go:build linux && amd64

package nativetest

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// RunAssigner plays the dataplane's assigner thread for the in-process
// dataplane until the test ends: each newly published generation's
// contexts are delivered to the workers.
//
// It spins rather than sleeps, so a publish it serves is timed without
// a sleep the in-process C publish, which delivers by itself, never pays.
func (m *Fixture) RunAssigner(t testing.TB) {
	t.Helper()
	instance := m.Instance()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		var assigned *layout.CpConfigGen
		for {
			select {
			case <-stop:
				return
			default:
			}
			if gen := instance.Cp.CpConfigGen.Load(); gen != assigned {
				coracle.AssignWorkerContexts(unsafe.Pointer(instance.Dp), unsafe.Pointer(gen))
				assigned = gen
			}
			runtime.Gosched()
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
	})
}

// Deliver hands a published generation's contexts to the workers of the
// in-process dataplane under its round lock, as the C publish does for it
// on its own thread.
func Deliver(dp *layout.DpConfig, gen *layout.CpConfigGen) {
	coracle.AssignWorkerContexts(unsafe.Pointer(dp), unsafe.Pointer(gen))
}
