#include <stdatomic.h>

#include "lib/statemap/fwmap.h"

#include "ops.h"

// Install fwstate's domain-specific fwmap callbacks under their registry ids.
// The ids are part of the shared-memory contract and must stay stable; only
// the process-local function pointers are filled here. Idempotent: the first
// caller installs, later callers observe the installed state.
void
fwstate_fwmap_registry_ensure(void) {
	static atomic_flag installed = ATOMIC_FLAG_INIT;
	if (atomic_flag_test_and_set(&installed)) {
		return;
	}

	fwmap_func_registry_set(FWMAP_COPY_KEY_FW4, (void *)fwmap_copy_key_fw4);
	fwmap_func_registry_set(FWMAP_COPY_KEY_FW6, (void *)fwmap_copy_key_fw6);
	fwmap_func_registry_set(
		FWMAP_KEY_EQUAL_FW4, (void *)fwmap_fw4_key_equal
	);
	fwmap_func_registry_set(
		FWMAP_KEY_EQUAL_FW6, (void *)fwmap_fw6_key_equal
	);
	fwmap_func_registry_set(
		FWMAP_UPDATE_VALUE_FWSTATE, (void *)fwmap_update_value_fwstate
	);
	fwmap_func_registry_set(
		FWMAP_PROMOTE_VALUE_FWSTATE, (void *)fwmap_promote_value_fwstate
	);
}

// Binaries linked through meson register at startup; processes that reach
// fwstate tables only through their creators additionally rely on the
// ensure() call there.
__attribute__((constructor)) static void
fwstate_register_fwmap_funcs(void) {
	fwstate_fwmap_registry_ensure();
}
