//go:build linux && (amd64 || arm64)

package zone

import (
	"sync/atomic"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// Generation is a published configuration generation pinned by this
// process: its registries and the configurations they hold stay valid
// until Release.
//
// This package cannot free shared memory yet, so it never drops a
// generation's last reference: a generation a publication retires while
// pinned here is not freed by this reader and stays allocated until native
// generation teardown lands.
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

// Release drops the reference Pin took; a second call does nothing.
//
// The last reference is not dropped: dropping it obliges the dropper to
// free the generation, which this reader cannot yet. A generation a
// publication retires while pinned therefore keeps that reference and
// stays allocated until native generation teardown lands.
func (m *Generation) Release() {
	gen := m.Gen
	m.Gen = nil
	if gen == nil {
		return
	}
	for {
		count := atomic.LoadUint64(&gen.Refcnt)
		if count <= 1 {
			return
		}
		if atomic.CompareAndSwapUint64(&gen.Refcnt, count, count-1) {
			return
		}
	}
}
