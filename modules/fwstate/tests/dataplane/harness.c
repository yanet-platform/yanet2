#include "harness.h"

#include <errno.h>
#include <string.h>
#include <time.h>

#include "common/strutils.h"

// Allocate and initialize a stand-in agent for a test that needs the real
// structure behind it, not just a bare memory context, such as one that
// constructs a module through its own control-plane API.
//
// The allocator does not zero what it hands out, and the parked-module
// reclaim this harness reproduces reads an uninitialized agent's list
// head as a live pointer. Zeroing the whole structure, not just that one
// field, keeps a field a later change adds starting at zero too,
// matching what a freshly attached production agent gets.
//
// The stand-in also wires a minimal dp_config carrying the fwstate-map
// object types and a cp_config holding one generation with an empty
// object registry, so both cp_object_init and the registry lookup in
// fwstate_module_config_update run against the production structures.
struct agent *
fwstate_test_agent_new(struct memory_context *parent, const char *name) {
	struct agent *agent =
		(struct agent *)memory_balloc(parent, sizeof(struct agent));
	if (agent == NULL) {
		return NULL;
	}

	memset(agent, 0, sizeof(struct agent));
	memory_context_init_from(&agent->memory_context, parent, name);

	struct dp_config *dp_config =
		memory_balloc(parent, sizeof(struct dp_config));
	if (dp_config == NULL) {
		return NULL;
	}
	memset(dp_config, 0, sizeof(struct dp_config));

	struct dp_object *dp_objects =
		memory_balloc(parent, 2 * sizeof(struct dp_object));
	if (dp_objects == NULL) {
		return NULL;
	}
	memset(dp_objects, 0, 2 * sizeof(struct dp_object));
	strtcpy(dp_objects[0].name,
		FWSTATE_MAP_V4_OBJECT_TYPE,
		sizeof(dp_objects[0].name));
	strtcpy(dp_objects[1].name,
		FWSTATE_MAP_V6_OBJECT_TYPE,
		sizeof(dp_objects[1].name));
	SET_OFFSET_OF(&dp_config->dp_objects, dp_objects);
	dp_config->object_count = 2;
	SET_OFFSET_OF(&agent->dp_config, dp_config);

	struct cp_config *cp_config =
		memory_balloc(parent, sizeof(struct cp_config));
	if (cp_config == NULL) {
		return NULL;
	}
	memset(cp_config, 0, sizeof(struct cp_config));

	struct cp_config_gen *config_gen =
		memory_balloc(parent, sizeof(struct cp_config_gen));
	if (config_gen == NULL) {
		return NULL;
	}
	memset(config_gen, 0, sizeof(struct cp_config_gen));
	SET_OFFSET_OF(&cp_config->cp_config_gen, config_gen);
	SET_OFFSET_OF(&agent->cp_config, cp_config);

	yanet_error *err = NULL;
	if (cp_object_registry_init(
		    &agent->memory_context, &config_gen->object_registry, &err
	    )) {
		yanet_error_free(err);
		return NULL;
	}

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

// Test double for the object-link declaration, mirroring the production
// cp_module_link_object: return the index of a matching (type, name)
// entry, or append one.
//
// Mocked because the production symbol lives in the same translation
// unit as the cp_module constructors mocked above, and pulling that unit
// in from the archive would collide with them.
int
cp_module_link_object(
	struct cp_module *cp_module,
	const char *object_type,
	const char *object_name,
	uint64_t *index,
	yanet_error **err
) {
	struct cp_module_object *objects = ADDR_OF(&cp_module->objects);
	for (uint64_t idx = 0; idx < cp_module->object_count; ++idx) {
		if (!strncmp(
			    objects[idx].type, object_type, CP_OBJECT_TYPE_LEN
		    ) &&
		    !strncmp(
			    objects[idx].name, object_name, CP_OBJECT_NAME_LEN
		    )) {
			*index = idx;
			return 0;
		}
	}

	objects = (struct cp_module_object *)memory_brealloc(
		&cp_module->memory_context,
		objects,
		sizeof(struct cp_module_object) * cp_module->object_count,
		sizeof(struct cp_module_object) * (cp_module->object_count + 1)
	);
	if (objects == NULL) {
		yanet_error_add(
			err,
			"failed to reallocate objects array for module '%s:%s'",
			cp_module->type,
			cp_module->name
		);
		return -1;
	}

	strtcpy(objects[cp_module->object_count].type,
		object_type,
		CP_OBJECT_TYPE_LEN);
	strtcpy(objects[cp_module->object_count].name,
		object_name,
		CP_OBJECT_NAME_LEN);
	SET_OFFSET_OF(&cp_module->objects, objects);
	*index = cp_module->object_count;
	++cp_module->object_count;

	return 0;
}

// Resolve the module config's linked table for one family.
static fwtable_t *
fwstate_test_table(struct cp_module *cp_module, bool is_ipv6) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	return is_ipv6 ? ADDR_OF(&config->fw6table)
		       : ADDR_OF(&config->fw4table);
}

// Append one layer to a map object's table, picking the family from the
// cp_object type.
static int
fwstate_test_object_insert_layer(struct cp_object *cp_object) {
	if (!strncmp(
		    cp_object->type,
		    FWSTATE_MAP_V6_OBJECT_TYPE,
		    sizeof(cp_object->type)
	    )) {
		struct fwstate_map_v6_object *object = container_of(
			cp_object, struct fwstate_map_v6_object, cp_object
		);
		return fwstate_map_v6_object_insert_layer(object, 1024, 64, 1);
	}

	struct fwstate_map_v4_object *object = container_of(
		cp_object, struct fwstate_map_v4_object, cp_object
	);
	return fwstate_map_v4_object_insert_layer(object, 1024, 64, 1);
}

struct cp_object *
fwstate_test_map_object_new(
	struct agent *agent, bool is_ipv6, const char *name
) {
	yanet_error *err = NULL;
	struct cp_object *cp_object =
		is_ipv6 ? fwstate_map_v6_object_config_new(agent, name, &err)
			: fwstate_map_v4_object_config_new(agent, name, &err);
	if (cp_object == NULL) {
		yanet_error_free(err);
		return NULL;
	}

	if (fwstate_test_object_insert_layer(cp_object)) {
		return NULL;
	}

	return cp_object;
}

int
fwstate_test_register_object(struct agent *agent, struct cp_object *cp_object) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);

	yanet_error *err = NULL;
	if (cp_object_registry_upsert(
		    &config_gen->object_registry,
		    cp_object->type,
		    cp_object->name,
		    cp_object,
		    &err
	    )) {
		yanet_error_free(err);
		return -1;
	}
	return 0;
}

int
fwstate_test_insert_new_layer(struct cp_module *cp_module) {
	fwtable_t *fw4table = fwstate_test_table(cp_module, false);
	fwtable_t *fw6table = fwstate_test_table(cp_module, true);
	if (fw4table == NULL || fw6table == NULL) {
		errno = EINVAL;
		return -1;
	}

	struct fwstate_map_v4_object *object4 =
		container_of(fw4table, struct fwstate_map_v4_object, table);
	struct fwstate_map_v6_object *object6 =
		container_of(fw6table, struct fwstate_map_v6_object, table);

	if (fwstate_map_v4_object_insert_layer(object4, 1024, 64, 1)) {
		return -1;
	}
	return fwstate_map_v6_object_insert_layer(object6, 1024, 64, 1);
}

int
fwstate_test_trim_stale_layers(struct cp_module *cp_module, uint64_t now) {
	fwtable_t *fw4table = fwstate_test_table(cp_module, false);
	fwtable_t *fw6table = fwstate_test_table(cp_module, true);
	if (fw4table == NULL || fw6table == NULL) {
		errno = EINVAL;
		return -1;
	}

	// The harness runs single-threaded between packet rounds, so no
	// reader can be mid-walk when the parked layers are released in the
	// same call — no generation barrier is needed here.
	int rc4 = fwtable_unlink_stale_cp(fw4table, now);
	int rc6 = fwtable_unlink_stale_cp(fw6table, now);
	struct agent *agent = ADDR_OF(&cp_module->agent);
	fwtable_free_stale(fw4table, &agent->memory_context);
	fwtable_free_stale(fw6table, &agent->memory_context);
	return (rc4 || rc6) ? -1 : 0;
}

fwmap_t *
fwstate_test_table_layer(
	struct cp_module *cp_module, bool is_ipv6, uint32_t layer_index
) {
	fwtable_t *table = fwstate_test_table(cp_module, is_ipv6);
	if (table == NULL) {
		return NULL;
	}

	fwmap_t *map = ADDR_OF(&table->head);
	for (uint32_t idx = 0; idx < layer_index; ++idx) {
		if (map == NULL) {
			return NULL;
		}
		map = (fwmap_t *)ADDR_OF(&map->next);
	}
	return map;
}
