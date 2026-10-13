// Package testshm provides file-backed storage for nontraffic admission tests.
package testshm

/*
#cgo CFLAGS: -I../../../..
#cgo LDFLAGS: -Wl,--start-group
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

static int load_types(
	struct dp_config *config,
	void *handle,
	const char *const *module_names,
	size_t module_count,
	const char *const *object_names,
	size_t object_count
) {
	for (size_t idx = 0; idx < module_count; ++idx) {
		if (dp_load_module(config, handle, NULL, module_names[idx]) != 0) {
			return -1;
		}
	}
	for (size_t idx = 0; idx < object_count; ++idx) {
		if (dp_load_object(config, handle, object_names[idx]) != 0) {
			return -2;
		}
	}
	return 0;
}

static int load_type(
	struct yanet_shm *shm,
	const char *name,
	int object
) {
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

static int bootstrap(
	struct yanet_shm *shm,
	const char *const *module_names,
	size_t module_count,
	const char *const *object_names,
	size_t object_count,
	size_t instance_count,
	size_t ready_instance_count
) {
	if (instance_count == 0 || ready_instance_count > instance_count) {
		return -1;
	}
	void *handle = NULL;
	if (module_count != 0 || object_count != 0) {
		handle = dlopen(NULL, RTLD_NOW);
		if (handle == NULL) {
			return -2;
		}
	}
	const size_t instance_size = dp_bytes + cp_bytes;
	for (size_t idx = 0; idx < instance_count; ++idx) {
		void *instance_storage = (char *)shm->base + idx * instance_size;
		struct dp_config *dp_config = NULL;
		struct cp_config *cp_config = NULL;
		if (dp_storage_init(0, (uint32_t)idx, instance_storage,
			    dp_bytes, cp_bytes, &dp_config, &cp_config) != 0) {
			if (handle != NULL) {
				dlclose(handle);
			}
			return -6;
		}
		if (handle != NULL) {
			int result = load_types(dp_config, handle, module_names,
				module_count, object_names, object_count);
			if (result != 0) {
				dlclose(handle);
				return result - 1;
			}
		}
		cp_config_lock(cp_config);
		struct agent *agent =
			dp_system_agent_new(cp_config, dp_config, "dataplane");
		if (agent == NULL) {
			cp_config_unlock(cp_config);
			if (handle != NULL) {
				dlclose(handle);
			}
			return -4;
		}
		yanet_error *err = NULL;
		struct cp_config_gen *generation = cp_config_gen_new(agent, &err);
		if (generation == NULL) {
			yanet_error_free(err);
			cp_config_unlock(cp_config);
			if (handle != NULL) {
				dlclose(handle);
			}
			return -5;
		}
		SET_OFFSET_OF(&cp_config->cp_config_gen, generation);
		cp_config_unlock(cp_config);
		dp_config->instance_count = (uint32_t)instance_count;
	}
	if (handle != NULL) {
		dlclose(handle);
	}
	for (size_t idx = 0; idx < ready_instance_count; ++idx) {
		struct dp_config *dp_config = dp_config_nextk(
			(struct dp_config *)shm->base, (uint32_t)idx);
		dp_config_mark_ready(dp_config);
	}
	return 0;
}

static const char *object_name(struct yanet_shm *shm, uint64_t index) {
	struct dp_config *config = shm->base;
	struct dp_object *objects = ADDR_OF(&config->dp_objects);
	return objects[index].name;
}

static int mark_instance_ready(
	struct yanet_shm *shm,
	uint32_t instance_idx,
	uint32_t *instance_count
) {
	struct dp_config *first = shm->base;
	*instance_count = first->instance_count;
	if (instance_idx >= *instance_count) {
		return -1;
	}
	dp_config_mark_ready(dp_config_nextk(first, instance_idx));
	return 0;
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

// StorageTypes selects type constructors and readiness publication for a fixture.
//
// A nil ready count publishes all instances; an explicit zero leaves them unready.
type StorageTypes struct {
	Modules            []string
	Objects            []string
	InstanceCount      uint32
	ReadyInstanceCount *uint32
}

// NewStorage creates a file-backed fixture with the requested ready instances.
//
// A zero instance count selects one instance; a nil ready count publishes all.
// Each instance has the same inert type inventory and no active configuration.
func NewStorage(t *testing.T, types StorageTypes) string {
	t.Helper()
	moduleNames, freeModuleNames := cStringArray(t, types.Modules)
	defer freeModuleNames()
	objectNames, freeObjectNames := cStringArray(t, types.Objects)
	defer freeObjectNames()

	instanceCount := types.InstanceCount
	if instanceCount == 0 {
		instanceCount = 1
	}
	readyInstanceCount := instanceCount
	if types.ReadyInstanceCount != nil {
		readyInstanceCount = *types.ReadyInstanceCount
	}
	require.LessOrEqual(t, readyInstanceCount, instanceCount)
	path := filepath.Join(t.TempDir(), "yanet")
	file, err := os.Create(path)
	require.NoError(t, err)
	storageBytes := int64(C.dp_bytes+C.cp_bytes) * int64(instanceCount)
	err = file.Truncate(storageBytes)
	require.NoError(t, file.Close())
	require.NoError(t, err)
	t.Cleanup(func() {
		mappings, err := os.ReadFile("/proc/self/maps")
		require.NoError(t, err)
		var leaked []string
		mappingPath := strings.ReplaceAll(path, "\n", `\012`)
		for mapping := range strings.SplitSeq(string(mappings), "\n") {
			if strings.Contains(mapping, mappingPath) {
				leaked = append(leaked, mapping)
			}
		}
		require.Empty(t, leaked, "consumer leaked shared memory")
	})
	memory, err := ffi.AttachSharedMemory(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, memory.Detach()) }()
	require.Zero(t, int(C.bootstrap((*C.struct_yanet_shm)(memory.AsRawPtr()),
		moduleNames, C.size_t(len(types.Modules)), objectNames, C.size_t(len(types.Objects)),
		C.size_t(instanceCount), C.size_t(readyInstanceCount))))
	for idx := range instanceCount {
		require.Equal(t, idx < readyInstanceCount, memory.DataplaneReady(idx))
	}
	return path
}

// MarkInstanceReady publishes an initialized fixture instance or reports an
// invalid index.
func MarkInstanceReady(
	memory *ffi.SharedMemory,
	instanceIdx uint32,
) error {
	var instanceCount C.uint32_t
	if C.mark_instance_ready(
		(*C.struct_yanet_shm)(memory.AsRawPtr()),
		C.uint32_t(instanceIdx),
		&instanceCount,
	) != 0 {
		return fmt.Errorf(
			"fixture instance index %d out of range [0, %d)",
			instanceIdx,
			uint32(instanceCount),
		)
	}
	return nil
}

// cStringArray creates C-owned names and returns their cleanup function.
func cStringArray(t *testing.T, values []string) (**C.char, func()) {
	if len(values) == 0 {
		return nil, func() {}
	}
	array := C.malloc(C.size_t(len(values)) * C.size_t(unsafe.Sizeof(uintptr(0))))
	require.NotNil(t, array)
	pointers := unsafe.Slice((**C.char)(array), len(values))
	for idx, value := range values {
		if strings.IndexByte(value, 0) != -1 {
			for _, pointer := range pointers[:idx] {
				C.free(unsafe.Pointer(pointer))
			}
			C.free(array)
			t.Fatalf("type name contains NUL byte")
		}
		pointers[idx] = C.CString(value)
		if pointers[idx] == nil {
			for _, pointer := range pointers[:idx] {
				C.free(unsafe.Pointer(pointer))
			}
			C.free(array)
			t.Fatalf("failed to allocate C string for type name")
		}
	}
	return (**C.char)(array), func() {
		for _, pointer := range pointers {
			C.free(unsafe.Pointer(pointer))
		}
		C.free(array)
	}
}

// LoadedObjectTypes copies inert object type names from an attached fixture.
func LoadedObjectTypes(memory *ffi.SharedMemory) []string {
	handle := (*C.struct_yanet_shm)(memory.AsRawPtr())
	config := (*C.struct_dp_config)(handle.base)
	names := make([]string, int(config.object_count))
	for idx := range names {
		names[idx] = C.GoString(C.object_name(handle, C.uint64_t(idx)))
	}
	return names
}

// LoadModule invokes the production module loader against an attached,
// quiescent fixture.
//
// Callers must not load types concurrently with another consumer of the segment.
func LoadModule(memory *ffi.SharedMemory, name string) error {
	if strings.IndexByte(name, 0) >= 0 {
		return fmt.Errorf("module type name contains NUL byte")
	}
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	if C.load_type((*C.struct_yanet_shm)(memory.AsRawPtr()), cName, 0) != 0 {
		return fmt.Errorf("failed to load module type %q", name)
	}
	return nil
}

// LoadObject invokes the production object loader against an attached,
// quiescent fixture.
//
// Callers must not load types concurrently with another consumer of the segment.
func LoadObject(memory *ffi.SharedMemory, name string) error {
	if strings.IndexByte(name, 0) >= 0 {
		return fmt.Errorf("object type name contains NUL byte")
	}
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	if C.load_type((*C.struct_yanet_shm)(memory.AsRawPtr()), cName, 1) != 0 {
		return fmt.Errorf("failed to load object type %q", name)
	}
	return nil
}
