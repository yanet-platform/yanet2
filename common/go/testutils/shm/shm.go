// Package testshm provides file-backed storage for nontraffic admission tests.
package testshm

/*
#cgo CFLAGS: -I../../../..
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
#include <dlfcn.h>
#include <stdlib.h>
#include "common/memory_address.h"
#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/zone.h"
#include "lib/dataplane/config/agent.h"
#include "lib/dataplane/config/bootstrap.h"
#include "lib/dataplane/config/module_loader.h"
#include "lib/dataplane/config/object_loader.h"
#include "lib/dataplane/config/zone.h"
#include "lib/errors/errors.h"

enum { dp_bytes = 4 << 20, cp_bytes = 128 << 20 };

extern struct module *new_module_l3b(void);
extern struct object *new_object_l3b_virtual_service(void);
extern struct object *new_object_l3b_session_table(void);

static int load_type(struct yanet_shm *shm, const char *name, int object) {
	// Retain the real archive symbols without calling constructors directly.
	void *volatile constructors[] = {
		new_module_l3b,
		new_object_l3b_virtual_service,
		new_object_l3b_session_table,
	};
	(void)constructors;
	void *handle = dlopen(NULL, RTLD_NOW);
	if (handle == NULL) {
		return -1;
	}
	struct dp_config *config = shm->base;
	int result = object ? dp_load_object(config, handle, name) :
		dp_load_module(config, handle, NULL, name);
	dlclose(handle);
	return result;
}

static int bootstrap(struct yanet_shm *shm) {
	struct dp_config *dp_config = NULL;
	struct cp_config *cp_config = NULL;
	if (dp_storage_init(0, 0, shm->base, dp_bytes, cp_bytes,
		&dp_config, &cp_config) != 0) {
		return -1;
	}
	if (load_type(shm, "l3b", 0) != 0 ||
		load_type(shm, "l3b_virtual_service", 1) != 0 ||
		load_type(shm, "l3b_session_table", 1) != 0) {
		return -1;
	}
	cp_config_lock(cp_config);
	struct agent *agent = dp_system_agent_new(cp_config, dp_config, "dataplane");
	if (agent == NULL) {
		cp_config_unlock(cp_config);
		return -1;
	}
	yanet_error *err = NULL;
	struct cp_config_gen *generation = cp_config_gen_new(agent, &err);
	if (generation == NULL) {
		yanet_error_free(err);
		cp_config_unlock(cp_config);
		return -1;
	}
	SET_OFFSET_OF(&cp_config->cp_config_gen, generation);
	cp_config_unlock(cp_config);
	dp_config->instance_count = 1;
	dp_config_mark_ready(dp_config);
	return 0;
}

static const char *object_name(struct yanet_shm *shm, uint64_t index) {
	struct dp_config *config = shm->base;
	struct dp_object *objects = ADDR_OF(&config->dp_objects);
	return objects[index].name;
}
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
)

// NewStorage creates one ready instance with L3B types but no live objects.
//
// Consumers must close before test cleanup; the final check detects leaked
// mappings. Extra control-plane space covers the default arena and bootstrap.
func NewStorage(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "yanet")
	file, err := os.Create(path)
	require.NoError(t, err)
	err = file.Truncate(C.dp_bytes + C.cp_bytes)
	require.NoError(t, file.Close())
	require.NoError(t, err)
	t.Cleanup(func() {
		mappings, err := os.ReadFile("/proc/self/maps")
		require.NoError(t, err)
		var leaked []string
		for mapping := range strings.SplitSeq(string(mappings), "\n") {
			if strings.Contains(mapping, path) {
				leaked = append(leaked, mapping)
			}
		}
		require.Empty(t, leaked, "consumer leaked shared memory")
	})
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, memory.Detach()) }()
	require.Zero(t, int(C.bootstrap((*C.struct_yanet_shm)(memory.AsRawPtr()))))
	require.True(t, memory.DataplaneReady(0))
	return path
}

// ObjectTypes copies loaded inert type names from an attached fixture.
func ObjectTypes(memory *ffi.SharedMemory) []string {
	handle := (*C.struct_yanet_shm)(memory.AsRawPtr())
	config := (*C.struct_dp_config)(handle.base)
	names := make([]string, int(config.object_count))
	for idx := range names {
		names[idx] = C.GoString(C.object_name(handle, C.uint64_t(idx)))
	}
	return names
}

// LoadType invokes a production loader against an attached, quiescent fixture.
//
// The boolean selects an inert object type rather than a packet module. Callers
// must not load types concurrently with another consumer of the segment.
func LoadType(memory *ffi.SharedMemory, name string, object bool) error {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	var kind C.int
	if object {
		kind = 1
	}
	if C.load_type((*C.struct_yanet_shm)(memory.AsRawPtr()), cName, kind) != 0 {
		return fmt.Errorf("failed to load type %q (object=%t)", name, object)
	}
	return nil
}
