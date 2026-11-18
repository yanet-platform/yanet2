package nativetest

import (
	"testing"
	"time"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/zone"
)

// RunAssigner plays the dataplane's assigner thread for the in-process
// dataplane until the test ends: each newly published generation's
// contexts are delivered to the workers.
func (m *Fixture) RunAssigner(t *testing.T) {
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
			time.Sleep(time.Millisecond)
		}
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
	})
}

// AssignOnce delivers the next generation the instance publishes after
// current to the workers, once, as the dataplane's assigner would; the
// channel closes once it is delivered.
func AssignOnce(instance *zone.Instance, current *layout.CpConfigGen) chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if gen := instance.Cp.CpConfigGen.Load(); gen != current {
				coracle.AssignWorkerContexts(unsafe.Pointer(instance.Dp), unsafe.Pointer(gen))
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	return done
}
