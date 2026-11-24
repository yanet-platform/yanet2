//go:build linux && (amd64 || arm64)

package cpconfig

import (
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// build is the state of building a generation from the one an instance
// publishes, as C passes its configuration and generations along.
type build struct {
	cp *layout.CpConfig
	dp *layout.DpConfig
	// gen is the generation being built, old the one it replaces.
	gen *layout.CpConfigGen
	old *layout.CpConfigGen
	// worker is the worker whose contexts are being built, tree their
	// root.
	worker uint64
	tree   *layout.ConfigGenEctx
}

// newBuild starts a build from the generation the instance publishes.
func newBuild(cp *layout.CpConfig, old *layout.CpConfigGen) *build {
	return &build{cp: cp, dp: cp.DpConfig.Get(), old: old}
}

// fill is what a new array holds before the build writes it.
type fill bool

const (
	// zeroed arrays start at zero, as C clears them.
	zeroed fill = true
	// stale arrays keep the bytes the allocator had, as C leaves them.
	stale fill = false
)

// allocArray allocates count elements of T from a context and reports
// whether it got them; no elements take no block.
func allocArray[T any](context *layout.MemoryContext, count uint64, contents fill) ([]T, bool) {
	array, ok := registry.AllocArray[T](context, count)
	if ok && contents == zeroed {
		clear(array)
	}
	return array, ok
}

// newNode allocates a zeroed T followed by tail bytes from a context, or
// returns nil.
//
// A zeroed node is safe to free part-built: its arrays read as absent
// until they are set.
func newNode[T any](context *layout.MemoryContext, tail uintptr) *T {
	node := registry.AllocNode[T](context, tail)
	if node != nil {
		clear(unsafe.Slice((*byte)(unsafe.Pointer(node)), unsafe.Sizeof(*node)+tail))
	}
	return node
}
