//go:build linux && (amd64 || arm64)

package classify

import (
	"errors"

	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// errNoMemory reports that shared memory ran out.
var errNoMemory = errors.New("shared memory exhausted")

// nameSize is the size of a memory context's name, terminator included
// (MEMORY_CONTEXT_NAME_SIZE).
const nameSize = len(layout.MemoryContext{}.Name)

// leafName titles one artifact of a named compile as classify_leaf_name
// does: the name, a colon and the leaf, the name cut with a dot marker
// so that the leaf always fits.
func leafName(name, leaf string) string {
	nameMax := max(nameSize-len(leaf)-2, 1)
	cut := name
	if len(cut) > nameMax {
		cut = cut[:nameMax]
	}
	title := []byte(cut + ":" + leaf)
	if len(title) > nameSize-1 {
		title = title[:nameSize-1]
	}
	if nameMax > 3 && len(name) > nameMax {
		copy(title[nameMax-3:], "...")
	}
	return string(title)
}
