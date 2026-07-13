#include "harness.h"

#include <string.h>
#include <time.h>

struct counter_storage *
fwstate_test_counter_storage_setup(struct cp_module *cp_module) {
	struct counter_registry *registry = &cp_module->counter_registry;

	yanet_error *err = NULL;
	if (counter_registry_link(registry, NULL, &err)) {
		// Setup failed: do not run the handler with counter_storage
		// still zero — ADDR_OF_NONNULL on a zero offset yields the
		// address of the field itself, so counter_get_address would
		// read and write through a bogus stack pointer. Free the error
		// chain and bail out.
		yanet_error_free(err);
		return NULL;
	}

	struct counter_storage *storage = counter_storage_spawn(
		&cp_module->memory_context, NULL, registry
	);
	if (storage == NULL) {
		// Allocation failed: same bogus-pointer hazard as above if the
		// zero offset were fed to SET_OFFSET_OF.
		// counter_storage_free(NULL) is safe, but the handler must not
		// run.
		return NULL;
	}

	return storage;
}

void
fwstate_test_counter_storage_free(struct counter_storage *storage) {
	counter_storage_free(storage);
}

void
test_fwstate_handle_packets(
	struct dp_worker *dp_worker,
	struct cp_module *cp_module,
	struct counter_storage *counter_storage,
	struct packet_front *packet_front
) {
	struct module_ectx module_ectx = {};
	SET_OFFSET_OF(&module_ectx.cp_module, cp_module);
	SET_OFFSET_OF(&module_ectx.counter_storage, counter_storage);

	fwstate_handle_packets(dp_worker, &module_ectx, packet_front);
}

void *
addr_of(void **field) {
	return ADDR_OF(field);
}

// Mock implementation of clock_get_time_ns for tests.
// Returns current monotonic time in nanoseconds.
uint64_t
clock_get_time_ns(struct tsc_clock *clock) {
	(void)clock;
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return ts.tv_sec * (uint64_t)1e9 + ts.tv_nsec;
}

// Mock implementation of cp_module_init for tests.
// Provides minimal initialization without requiring full dp_config.
int
cp_module_init(
	struct cp_module *cp_module,
	struct agent *agent,
	const char *module_type,
	const char *module_name,
	yanet_error **err
) {
	// Minimal initialization for tests (based on
	// lib/controlplane/config/cp_module.c:13-74)
	memset(cp_module, 0, sizeof(struct cp_module));

	// We don't have dp_config in tests, so skip dp_module_idx lookup
	cp_module->dp_module_idx = 0;

	// Copy module type and name
	strncpy(cp_module->type, module_type, sizeof(cp_module->type) - 1);
	strncpy(cp_module->name, module_name, sizeof(cp_module->name) - 1);

	// Initialize memory context from agent
	memory_context_init_from(
		&cp_module->memory_context, &agent->memory_context, module_name
	);

	// Set agent offset
	SET_OFFSET_OF(&cp_module->agent, agent);

	// Initialize the counter registry. fwstate_module_config_new registers
	// module-level counters right after cp_module_init, and
	// counter_registry_register dereferences registry->memory_context (an
	// offset pointer).
	if (counter_registry_init(
		    &cp_module->counter_registry, &cp_module->memory_context, 0
	    )) {
		yanet_error_add(err, "failed to init counter registry");
		return -1;
	}

	return 0;
}

void
cp_module_fini(struct cp_module *cp_module) {
	counter_registry_fini(&cp_module->counter_registry);
}
