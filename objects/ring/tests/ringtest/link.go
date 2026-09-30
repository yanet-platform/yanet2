package ringtest

//#cgo CFLAGS: -I../../../../
//#cgo LDFLAGS: -L../../../../build/lib/controlplane/config -lconfig_cp
//
//#include "lib/controlplane/config/cp_module.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/objects/ring/bindings/go/cring"
)

// LinkRing links a module config, given as a raw pointer, to the named ring
// object.
//
// It calls the C module link function directly.
func LinkRing(moduleConfigPtr unsafe.Pointer, name string) error {
	cpModule := (*C.struct_cp_module)(moduleConfigPtr)

	cType := C.CString(cring.ObjectType)
	defer C.free(unsafe.Pointer(cType))
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var index C.uint64_t
	var cErr *C.yanet_error
	rc := C.cp_module_link_object(cpModule, cType, cName, &index, &cErr)
	if rc != 0 {
		return fmt.Errorf("failed to link ring %q: %w", name, cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return nil
}
