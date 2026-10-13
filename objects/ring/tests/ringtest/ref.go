package ringtest

//#include <stdlib.h>
//
//#include "objects/ring/tests/ringtest_writer.h"
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/bindings/go/cerrors"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// Reference is an extra reference to a published ring.
//
// It plays the role of a live generation that has not retired yet.
type Reference struct {
	agent    *C.struct_agent
	registry *C.struct_cp_object_registry
}

// Hold adds an extra reference to the named ring in the agent's published
// generation.
//
// Until Release, every attempt to free the ring fails.
func Hold(agent *ffi.Agent, name string) (*Reference, error) {
	cAgent := (*C.struct_agent)(agent.AsRawPtr())
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	var cErr *C.yanet_error
	registry := C.ringtest_hold(cAgent, cName, &cErr)
	if registry == nil {
		return nil, fmt.Errorf("failed to hold ring %q: %w", name, cerrors.FromC(unsafe.Pointer(cErr)))
	}
	return &Reference{agent: cAgent, registry: registry}, nil
}

// Release drops the extra reference.
//
// Later calls do nothing.
func (m *Reference) Release() {
	if m.registry == nil {
		return
	}
	C.ringtest_release(m.agent, m.registry)
	m.registry = nil
}
