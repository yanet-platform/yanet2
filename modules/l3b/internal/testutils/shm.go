// Package testutils links the L3B dataplane type constructors for shared-memory tests.
package testutils

/*
#cgo CFLAGS: -I../../../../
#cgo LDFLAGS: -Wl,-E
#cgo LDFLAGS: -Wl,--start-group
#cgo LDFLAGS: -L../../../../build/modules/l3b/dataplane -ll3b_dp
#cgo LDFLAGS: -L../../../../build/objects/l3b/api -ll3b_objects
#cgo LDFLAGS: -L../../../../build/lib/dataplane/config -lconfig_dp
#cgo LDFLAGS: -L../../../../build/lib/dataplane/module -lmodule
#cgo LDFLAGS: -L../../../../build/lib/dataplane/packet -lpacket
#cgo LDFLAGS: -L../../../../build/lib/controlplane/agent -lagent
#cgo LDFLAGS: -L../../../../build/lib/controlplane/config -lconfig_cp
#cgo LDFLAGS: -L../../../../build/lib/filter -lfilter_compiler
#cgo LDFLAGS: -L../../../../build/lib/statemap -lstatemap
#cgo LDFLAGS: -L../../../../build/lib/l3state -ll3state
#cgo LDFLAGS: -L../../../../build/lib/counters -lcounters
#cgo LDFLAGS: -L../../../../build/lib/errors -lerrors
#cgo LDFLAGS: -L../../../../build/lib/logging -llogging
#cgo LDFLAGS: -Wl,--end-group -ldl

#include <stdlib.h>
#include "lib/dataplane/module/module.h"

extern struct module *new_module_l3b(void);
extern struct object *new_object_l3b_virtual_service(void);
extern struct object *new_object_l3b_session_table(void);

static void retain_l3b_constructors(void) {
	void *volatile constructors[] = {
		new_module_l3b,
		new_object_l3b_virtual_service,
		new_object_l3b_session_table,
	};
	(void)constructors;
}
*/
import "C"

import (
	"testing"

	testshm "github.com/yanet-platform/yanet2/common/go/testutils/shm"
)

// NewStorage loads the production L3B module and inert object types into a
// ready fixture without creating workers or active configuration.
func NewStorage(t *testing.T) string {
	t.Helper()
	C.retain_l3b_constructors()
	return testshm.NewStorage(t, testshm.StorageTypes{
		Modules: []string{"l3b"},
		Objects: []string{"l3b_virtual_service", "l3b_session_table"},
	})
}
