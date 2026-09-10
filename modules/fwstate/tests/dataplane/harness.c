#include "harness.h"

#include <errno.h>
#include <string.h>
#include <time.h>

#include "common/strutils.h"

// Allocate and initialize a stand-in agent for a test that needs the real
// structure behind it, not just a bare memory context, such as one that
// constructs a module through its own control-plane API.
//
// The allocator does not zero what it hands out, and the production
// module lifecycle this harness runs reads agent fields like the loaded
// counts, which stale bytes would corrupt. Zeroing the whole structure,
// not just any one field, keeps a field a later change adds starting at
// zero too, matching what a freshly attached production agent gets.
//
// The stand-in also wires a minimal dp_config carrying the fwstate
// module type and the fwstate-map object types, and a cp_config holding
// one generation with an empty object registry, so module and object
// construction and the harness's link-name lookups all run against the
// production structures and the production module lifecycle code. The
// cp_config lock is a compare-and-swap on a zeroed process id, which is
// a valid uncontended state.
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

	// Register the fwstate module type so a construction resolving its
	// dataplane module index succeeds as it would against a loaded
	// dataplane. The handler rides along for faithfulness; nothing on
	// the control-plane path dispatches through it.
	struct dp_module *dp_modules =
		memory_balloc(parent, sizeof(struct dp_module));
	if (dp_modules == NULL) {
		return NULL;
	}
	memset(dp_modules, 0, sizeof(struct dp_module));
	strtcpy(dp_modules[0].name,
		FWSTATE_MODULE_NAME,
		sizeof(dp_modules[0].name));
	dp_modules[0].handler = fwstate_handle_packets;
	SET_OFFSET_OF(&dp_config->dp_modules, dp_modules);
	dp_config->module_count = 1;
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
		    &agent->memory_context,
		    cp_config,
		    &config_gen->object_registry,
		    &err
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
fwstate_test_mark_internal(struct packet_front *packet_front) {
	for (struct packet *packet = packet_list_first(&packet_front->input);
	     packet != NULL;
	     packet = packet->next) {
		packet->flags |= 1U << PACKET_FLAG_FWSTATE_SYNC_INTERNAL;
	}
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

	// Populate the ectx's object links the way the production build does:
	// one entry per declaration, each pointing at the registered object.
	// A published module's links always resolve, so an unresolvable one
	// leaves every link unwired (the handler then sees NULL tables)
	// instead of a half-indexed array. The handler runs synchronously,
	// so stack arrays outlive the call.
	uint64_t object_count = cp_module->object_count;
	struct cp_module_object *objects = ADDR_OF(&cp_module->objects);

	struct object_ectx object_ectxs[object_count > 0 ? object_count : 1];
	struct module_object_link_ectx
		links[object_count > 0 ? object_count : 1];

	struct agent *agent = ADDR_OF(&cp_module->agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);

	bool all_resolved = true;
	for (uint64_t idx = 0; idx < object_count; ++idx) {
		memset(&object_ectxs[idx], 0, sizeof(object_ectxs[idx]));
		memset(&links[idx], 0, sizeof(links[idx]));

		struct cp_object *cp_object = cp_object_registry_lookup(
			&config_gen->object_registry,
			objects[idx].type,
			objects[idx].name
		);
		if (cp_object == NULL) {
			all_resolved = false;
			break;
		}

		SET_OFFSET_OF(&object_ectxs[idx].cp_object, cp_object);
		SET_OFFSET_OF(&links[idx].object_ectx, &object_ectxs[idx]);
	}

	if (all_resolved) {
		module_ectx.object_link_count = object_count;
		SET_OFFSET_OF(&module_ectx.object_links, &links[0]);
	}

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

// Resolve the module config's linked table for one family: read the
// link declaration at the family's slot and look its (type, name) up in
// the harness agent's object registry.
//
// Returns NULL when the slot holds no link (the sentinel), the index is
// out of range, or the declaration matches no registered object.
static fwtable_t *
fwstate_test_table(struct cp_module *cp_module, bool is_ipv6) {
	struct fwstate_module_config *config = container_of(
		cp_module, struct fwstate_module_config, cp_module
	);

	uint64_t link_idx = is_ipv6 ? config->v6_object_link_idx
				    : config->v4_object_link_idx;
	if (link_idx == FWSTATE_OBJECT_LINK_NONE ||
	    link_idx >= cp_module->object_count) {
		return NULL;
	}

	struct cp_module_object *objects = ADDR_OF(&cp_module->objects);

	struct agent *agent = ADDR_OF(&cp_module->agent);
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);
	struct cp_config_gen *config_gen = ADDR_OF(&cp_config->cp_config_gen);

	struct cp_object *cp_object = cp_object_registry_lookup(
		&config_gen->object_registry,
		objects[link_idx].type,
		objects[link_idx].name
	);
	if (cp_object == NULL) {
		return NULL;
	}

	return is_ipv6 ? fwstate_map_v6_object_table(cp_object)
		       : fwstate_map_v4_object_table(cp_object);
}

fwtable_t *
fwstate_test_linked_table(struct cp_module *cp_module, bool is_ipv6) {
	return fwstate_test_table(cp_module, is_ipv6);
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

	cp_config_lock(cp_config);

	yanet_error *err = NULL;
	if (cp_object_registry_upsert(
		    &config_gen->object_registry,
		    cp_object->type,
		    cp_object->name,
		    cp_object,
		    &err
	    )) {
		yanet_error_free(err);
		cp_config_unlock(cp_config);
		return -1;
	}

	cp_config_unlock(cp_config);
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

	// Layer memory is charged to the owning map object's context, so the
	// release must free through the same context.
	struct fwstate_map_v4_object *object4 =
		container_of(fw4table, struct fwstate_map_v4_object, table);
	struct fwstate_map_v6_object *object6 =
		container_of(fw6table, struct fwstate_map_v6_object, table);
	fwtable_free_stale(fw4table, &object4->cp_object.memory_context);
	fwtable_free_stale(fw6table, &object6->cp_object.memory_context);
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

// Snapshot one context's accounting counters. The harness is
// single-threaded between setup calls, so plain reads race with nothing.
static void
fwstate_test_mem_counters_read(
	const struct memory_context *ctx, struct fwstate_test_mem_counters *out
) {
	out->balloc_count = ctx->balloc_count;
	out->bfree_count = ctx->bfree_count;
	out->balloc_size = ctx->balloc_size;
	out->bfree_size = ctx->bfree_size;
}

void
fwstate_test_agent_mem_counters(
	struct agent *agent, struct fwstate_test_mem_counters *out
) {
	fwstate_test_mem_counters_read(&agent->memory_context, out);
}

void
fwstate_test_object_mem_counters(
	struct cp_object *cp_object, struct fwstate_test_mem_counters *out
) {
	fwstate_test_mem_counters_read(&cp_object->memory_context, out);
}
