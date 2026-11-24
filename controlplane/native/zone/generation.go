//go:build linux && (amd64 || arm64)

package zone

import (
	"fmt"
	"sync/atomic"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/teardown"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
)

// Generation is a published configuration generation this process
// pinned: its registries and configurations stay valid until Release.
type Generation struct {
	Gen *layout.CpConfigGen
	// cp is the control-plane zone of the instance the generation was
	// pinned in.
	cp *layout.CpConfig
}

// Pin returns the generation the instance publishes now, with a reference
// held.
//
// The publication lock is held only while the generation is loaded,
// checked and referenced, as C's own readers hold it. The generation is
// the only shared address this reader writes through that attach did not
// check, since each publication may replace it, so one outside the
// instance is refused before the increment touches it; its alignment is
// not checked (attach: trust).
func (m *Instance) Pin() (*Generation, error) {
	shm.LockSpin(&m.Cp.GenLock.Locked)
	defer shm.UnlockSpin(&m.Cp.GenLock.Locked)
	gen := m.Cp.CpConfigGen.Load()
	if gen == nil {
		return &Generation{}, nil
	}
	if !contains(m.memory, unsafe.Pointer(gen), unsafe.Sizeof(*gen)) {
		return nil, fmt.Errorf("instance %d: published generation lies outside the instance", m.Index)
	}
	atomic.AddUint64(&gen.Refcnt, 1)
	return &Generation{Gen: gen, cp: m.Cp}, nil
}

// Release drops the reference Pin took, as cp_config_gen_release does; a
// second call does nothing.
//
// A publication that retired the generation while it was pinned left it
// to the last reference, so the release that drops it frees the
// generation and then reclaims the superseded agents only it held. It
// takes the agent lock, which the caller must not hold. A generation no
// pin produced is left alone.
func (m *Generation) Release() {
	gen := m.Gen
	m.Gen = nil
	if gen == nil || m.cp == nil {
		return
	}
	teardown.ReleaseGeneration(m.cp, gen)
}
