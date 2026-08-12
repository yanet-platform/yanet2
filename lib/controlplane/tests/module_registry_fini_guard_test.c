/*
 * Regression test for a null items array reaching the live-reference
 * mirror loop in the module registry's teardown.
 *
 * Registry setup records a capacity before it allocates the backing array,
 * so a generation copy that fails partway through, for example on arena
 * exhaustion, can leave a nonzero capacity paired with a null array.
 * Teardown's mirror loop must skip that state exactly as the underlying
 * registry teardown already does for the same never-allocated array.
 */

#include "common/test_assert.h"

#include "controlplane/config/cp_module.h"

#include <string.h>

static int
run_null_items_test(void) {
	struct cp_module_registry registry;
	memset(&registry, 0, sizeof(registry));

	// Mirrors a capacity recorded before the backing array allocation
	// failed: a nonzero capacity paired with a still-null array.
	registry.registry.capacity = 8;

	// Walks all eight recorded slots against a still-null backing array.
	cp_module_registry_fini(&registry);

	TEST_ASSERT_EQUAL(
		registry.registry.capacity,
		8,
		"a registry with no items array must be left untouched, "
		"exactly like registry_fini's own guard"
	);

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("debug");

	return (run_null_items_test() == TEST_SUCCESS) ? 0 : 1;
}
