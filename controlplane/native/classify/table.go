//go:build linux && (amd64 || arm64)

package classify

import (
	"fmt"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// errLineMemory reports that the memory of a value line ran out.
var errLineMemory = fmt.Errorf("value line memory: %w", shmalloc.ErrNoMemory)

// initVline allocates a line of size values in a child memory context of
// the parent named name, as vline_init does, and returns its values for
// the caller to write every one of.
//
// A failure frees what the call allocated.
func initVline(line *layout.Vline, parent *layout.MemoryContext, name string, size uint32) ([]uint32, error) {
	context := registry.AllocNode[layout.MemoryContext](parent, 0)
	if context == nil {
		return nil, errLineMemory
	}
	shmalloc.ContextInitFrom(context, parent, name)
	line.MemoryContext.Set(context)
	line.Size = size
	line.Values.Set(nil)

	values, ok := registry.AllocArray[uint32](context, uint64(size))
	if !ok {
		freeVline(line)
		return nil, errLineMemory
	}
	line.Values.Set(&values[0])
	return values, nil
}

// freeVline releases a line's values and its memory context, as
// vline_free does; it is safe on a zeroed line.
func freeVline(line *layout.Vline) {
	context := line.MemoryContext.Get()
	if context == nil {
		return
	}
	registry.FreeArray(context, registry.Slice(line.Values.Get(), uint64(line.Size)))
	line.Values.Set(nil)
	parent := context.Parent.Get()
	shmalloc.ContextFini(context)
	registry.FreeNode(parent, context, 0)
	line.MemoryContext.Set(nil)
}
