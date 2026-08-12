#include "harness.h"

#include <string.h>
#include <time.h>

// Allocate and initialize a stand-in agent for a test that needs the real
// structure behind it, not just a bare memory context, such as one that
// constructs a module through its own control-plane API.
//
// The allocator does not zero what it hands out, and the parked-module
// reclaim this harness reproduces reads an uninitialized agent's list
// head as a live pointer. Zeroing the whole structure, not just that one
// field, keeps a field a later change adds starting at zero too,
// matching what a freshly attached production agent gets.
struct agent *
fwstate_test_agent_new(struct memory_context *parent, const char *name) {
	struct agent *agent =
		(struct agent *)memory_balloc(parent, sizeof(struct agent));
	if (agent == NULL) {
		return NULL;
	}

	memset(agent, 0, sizeof(struct agent));
	memory_context_init_from(&agent->memory_context, parent, name);

	return agent;
}

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

// Test doubles for the module zero-transition handler, the release entry
// point, and the parked-list reclaim.
//
// The stand-in agent above has no real configuration context, so these
// reproduce the production algorithm without the lock the real versions
// take around it.
void
cp_module_registry_item_free_cb(struct registry_item *item, void *data) {
	struct cp_module *module =
		container_of(item, struct cp_module, config_item);
	struct agent *agent = ADDR_OF(&module->agent);
	if (agent == NULL) {
		return;
	}

	if (ADDR_OF(&module->parked_next) != NULL) {
		return;
	}

	struct cp_module *head = ADDR_OF(&agent->parked_modules);
	SET_OFFSET_OF(&module->parked_next, (head != NULL) ? head : module);
	SET_OFFSET_OF(&agent->parked_modules, module);
	(void)data;
}

void
cp_module_release(struct cp_module *cp_module) {
	registry_item_unref(
		&cp_module->config_item, cp_module_registry_item_free_cb, NULL
	);
}

// Reproduces the production parked-entry reclaim, folded into
// construction below the same way the real implementation folds it in.
static void
cp_module_drain_parked(
	struct agent *agent,
	const char *module_type,
	cp_module_free_handler destroy
) {
	struct cp_module *owned = NULL;
	struct cp_module *prev = NULL;
	struct cp_module *cur = ADDR_OF(&agent->parked_modules);

	while (cur != NULL) {
		struct cp_module *raw_next = ADDR_OF(&cur->parked_next);
		struct cp_module *next = (raw_next == cur) ? NULL : raw_next;

		if (!strncmp(cur->type, module_type, sizeof(cur->type))) {
			if (prev == NULL) {
				SET_OFFSET_OF(&agent->parked_modules, next);
			} else {
				SET_OFFSET_OF(
					&prev->parked_next,
					(next != NULL) ? next : prev
				);
			}
			SET_OFFSET_OF(&cur->parked_next, owned);
			owned = cur;
		} else {
			prev = cur;
		}

		cur = next;
	}

	while (owned != NULL) {
		struct cp_module *next = ADDR_OF(&owned->parked_next);
		destroy(owned);
		owned = next;
	}
}

// Mock implementation of cp_module_init for tests.
// Provides minimal initialization without requiring full dp_config.
int
cp_module_init(
	struct cp_module *cp_module,
	struct agent *agent,
	const char *module_type,
	const char *module_name,
	cp_module_free_handler destroy,
	yanet_error **err
) {
	// Minimal initialization for tests (based on
	// lib/controlplane/config/cp_module.c:13-74)
	memset(cp_module, 0, sizeof(struct cp_module));

	// Reclaim this type's parked entries first, the same as production
	// does, before this construction allocates anything of its own.
	cp_module_drain_parked(agent, module_type, destroy);

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
