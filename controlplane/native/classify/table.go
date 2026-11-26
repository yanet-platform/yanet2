//go:build linux && (amd64 || arm64)

package classify

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shm"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

var (
	// errLineMemory reports that the memory of a value line ran out.
	errLineMemory = fmt.Errorf("value line memory: %w", shmalloc.ErrNoMemory)
	// errTableMemory reports that the memory of a value table ran out.
	errTableMemory = fmt.Errorf("value table memory: %w", shmalloc.ErrNoMemory)
)

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

// initValueTable allocates a table of vDim rows of hDim values in a child
// memory context of the parent named name, as value_table_init does, and
// returns its rows for the caller to write every value of.
//
// A failure frees what the call allocated.
func initValueTable(table *layout.ValueTable, parent *layout.MemoryContext, name string, vDim, hDim uint32) ([][]uint32, error) {
	context := registry.AllocNode[layout.MemoryContext](parent, 0)
	if context == nil {
		return nil, errTableMemory
	}
	shmalloc.ContextInitFrom(context, parent, name)
	table.MemoryContext.Set(context)
	table.VDim = vDim
	table.HDim = hDim
	table.Values.Set(nil)

	chunks, ok := registry.AllocArray[shm.Ptr[uint32]](context, uint64(vDim))
	if !ok {
		freeValueTable(table)
		return nil, errTableMemory
	}
	// The free reads the chunks set so far: the rest stay nil.
	clear(chunks)
	table.Values.Set(unsafe.SliceData(chunks))
	rows := make([][]uint32, vDim)
	for row := range rows {
		values, ok := registry.AllocArray[uint32](context, uint64(hDim))
		if !ok {
			freeValueTable(table)
			return nil, errTableMemory
		}
		chunks[row].Set(unsafe.SliceData(values))
		rows[row] = values
	}
	return rows, nil
}

// freeValueTable releases a table's rows, its row array and its memory
// context, as value_table_free does; it is safe on a zeroed table.
func freeValueTable(table *layout.ValueTable) {
	context := table.MemoryContext.Get()
	if context == nil {
		return
	}
	chunks := registry.Slice(table.Values.Get(), uint64(table.VDim))
	for row := range chunks {
		registry.FreeArray(context, registry.Slice(chunks[row].Get(), uint64(table.HDim)))
		chunks[row].Set(nil)
	}
	registry.FreeArray(context, chunks)
	table.Values.Set(nil)
	parent := context.Parent.Get()
	shmalloc.ContextFini(context)
	registry.FreeNode(parent, context, 0)
	table.MemoryContext.Set(nil)
}
