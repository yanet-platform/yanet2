//go:build linux && amd64

package zone

import (
	"sync/atomic"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// Generation is a published configuration generation pinned by this
// process: its registries and the configurations they hold stay valid
// until Release.
type Generation struct {
	Gen *layout.CpConfigGen
}

// Pin returns the generation the instance publishes now, with a reference
// held. The publication lock is held for the load and the increment only,
// as C's own readers do.
func (m *Instance) Pin() *Generation {
	shm.LockByte(&m.Cp.GenLock.Locked)
	gen := m.Cp.CpConfigGen.Load()
	if gen != nil {
		atomic.AddUint64(&gen.Refcnt, 1)
	}
	shm.UnlockByte(&m.Cp.GenLock.Locked)
	return &Generation{Gen: gen}
}

// Release drops the reference Pin took.
//
// The last reference is not dropped: dropping it obliges the dropper to
// free the generation, which this package does not do yet, so a
// generation replaced while pinned stays allocated.
func (m *Generation) Release() {
	if m.Gen == nil {
		return
	}
	for {
		count := atomic.LoadUint64(&m.Gen.Refcnt)
		if count <= 1 {
			return
		}
		if atomic.CompareAndSwapUint64(&m.Gen.Refcnt, count, count-1) {
			m.Gen = nil
			return
		}
	}
}
