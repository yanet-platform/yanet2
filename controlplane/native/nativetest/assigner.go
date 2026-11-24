//go:build linux && (amd64 || arm64) && cgo

package nativetest

import (
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/native/internal/coracle"
	"github.com/yanet-platform/yanet2/controlplane/native/layout"
)

// Deliver hands a published generation's contexts to the workers of the
// in-process dataplane under its round lock, as the C publication does.
func Deliver(dp *layout.DpConfig, gen *layout.CpConfigGen) {
	coracle.AssignWorkerContexts(unsafe.Pointer(dp), unsafe.Pointer(gen))
}
