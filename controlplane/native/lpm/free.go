//go:build linux && (amd64 || arm64)

package lpm

import (
	"github.com/yanet-platform/yanet2/controlplane/native/internal/registry"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
	"github.com/yanet-platform/yanet2/controlplane/native/shmalloc"
)

// Free returns the pages of a tree and its chunk directory and unlinks
// its memory context, as lpm_free does.
//
// The page count and the directory pointer are left as they are; only
// the memory context is zeroed.
func Free(tree *layout.Lpm) {
	context := &tree.MemoryContext
	if pages := tree.Pages.Get(); pages != nil {
		chunkCount := (tree.PageCount + chunkSize - 1) / chunkSize
		directory := registry.Slice(pages, chunkCapacity(chunkCount))
		for idx := range chunkCount {
			registry.FreeArray(context, registry.Slice(directory[idx].Get(), chunkSize))
		}
		registry.FreeArray(context, directory)
	}
	shmalloc.ContextFini(context)
}
