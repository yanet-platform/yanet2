//go:build sdk_rust_fixtures && linux && (amd64 || arm64)

package sdkfixture

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/agent"
	"github.com/yanet-platform/yanet2/controlplane/native/cpconfig"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// ErrInvalidArgument marks a request no memory state satisfies: a part
// larger than the largest block, a range that ends before it starts, an
// index past its list, a key given twice.
var ErrInvalidArgument = errors.New("invalid argument")

// countValues is the count of values of a counter of one value.
const countValues = 1

// moduleBlock is the block of a module configuration: the module header,
// then the body.
type moduleBlock[B any] struct {
	Header layout.CpModule
	Body   B
}

// newModule builds a module configuration of the type named name in the
// agent's memory, as the SDK's build of a module does: the block, its
// header, then its body through build.
//
// A name its field cannot hold is refused before anything is allocated.
// A failure frees what the build took through destroy, which frees the
// body's fields, each holding nothing or what it owns.
func newModule[B any](
	owner *agent.Agent,
	moduleType, name string,
	build func(config *moduleBlock[B]) error,
	destroy func(config *moduleBlock[B]),
) (*layout.CpModule, error) {
	if err := cpconfig.CheckModuleNames(moduleType, name); err != nil {
		return nil, err
	}
	context := &owner.Agent.MemoryContext
	config := shmalloc.AllocNode[moduleBlock[B]](context, 0)
	if config == nil {
		return nil, fmt.Errorf("allocate %s module %q: %w", moduleType, name, shmalloc.ErrNoMemory)
	}
	config.Body = *new(B)
	if err := cpconfig.InitModule(&config.Header, owner, moduleType, name); err != nil {
		shmalloc.FreeNode(context, config, 0)
		return nil, err
	}
	if err := build(config); err != nil {
		destroy(config)
		return nil, fmt.Errorf("build %s module %q: %w", moduleType, name, err)
	}
	return &config.Header, nil
}

// freeModule frees a module configuration no generation holds through
// destroy.
//
// One a generation holds is refused as still referenced, and nothing is
// freed.
func freeModule[B any](module *layout.CpModule, destroy func(config *moduleBlock[B])) error {
	if err := cpconfig.TryDestroyModule(module); err != nil {
		return err
	}
	owner := module.Entity.Agent.Get()
	destroy((*moduleBlock[B])(unsafe.Pointer(module)))
	cpconfig.DestroyReleased(owner)
	return nil
}

// finiModule frees the header and the block of a configuration whose
// body holds nothing any more.
func finiModule[B any](config *moduleBlock[B]) {
	owner := config.Header.Entity.Agent.Get()
	cpconfig.FiniModule(&config.Header)
	shmalloc.FreeNode(&owner.MemoryContext, config, 0)
}

// newSlice allocates count elements for the slice field, which owns them
// from then on, and returns them to be written; no elements take no block.
func newSlice[T any](context *layout.MemoryContext, field *Slice[T], count uint64) ([]T, error) {
	elements, ok := shmalloc.AllocArray[T](context, count)
	if !ok {
		return nil, shmalloc.ErrNoMemory
	}
	field.Ptr.Set(unsafe.SliceData(elements))
	field.Len = count
	return elements, nil
}

// freeSlice frees the elements a slice field holds, which own nothing.
func freeSlice[T any](context *layout.MemoryContext, field *Slice[T]) {
	shmalloc.FreeArray(context, unsafe.Slice(field.Ptr.Get(), field.Len))
}
