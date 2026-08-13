#include "cp_module.h"

#include "api/counter.h"
#include "common/container_of.h"

#include "counters/counters.h"
#include "counters/histogram.h"
#include "dataplane/config/zone.h"

#include "controlplane/agent/agent.h"
#include "controlplane/config/zone.h"

#include <stdio.h>

// Destroy every parked module of one type, using the caller's destructor
// for that type.
//
// A different type sharing the same parked list is left in place for its
// own next call. A parked entry already sits at reference count zero, so
// the destructor runs directly with no further release and nothing here
// outlives this call. Matching entries are detached under the
// configuration lock and destroyed outside it, because a type's teardown
// can run tens of milliseconds and must not hold a lock every update
// needs. While outside the lock, a teardown is marked in flight so the
// arena cannot be reclaimed out from under an entry still being destroyed.
static void
cp_module_drain_parked(
	struct agent *agent,
	const char *module_type,
	cp_module_free_handler destroy
);

static int
cp_module_build_perf_counters(struct cp_module *cp_module, yanet_error **err) {
	for (size_t counter_idx = 0; counter_idx < MODULE_ECTX_PERF_COUNTERS;
	     ++counter_idx) {
		char name[16];
		sprintf(name, "hist_%zu", counter_idx);
		cp_module->perf_counters_indices[counter_idx] =
			counter_registry_register(
				&cp_module->counter_registry,
				name,
				MODULE_ECTX_PERF_COUNTER_SIZE,
				err
			);
		if (cp_module->perf_counters_indices[counter_idx] ==
		    COUNTER_INVALID) {
			yanet_error_add(
				err,
				"failed to register histogram counter at index "
				"%zu for module '%s:%s'",
				counter_idx,
				cp_module->type,
				cp_module->name
			);
			return -1;
		}
	}

	return 0;
}

int
cp_module_init(
	struct cp_module *cp_module,
	struct agent *agent,
	const char *module_type,
	const char *module_name,
	cp_module_free_handler destroy,
	yanet_error **err
) {
	memset(cp_module, 0, sizeof(struct cp_module));

	// Reclaim this type's parked entries before anything else here
	// allocates.
	//
	// The caller's own wrapper allocation already happened, but a parked
	// instance can be most of the arena for a large type, so reclaiming
	// it first can be the difference between success and failure under
	// pressure. Construction has not registered or parked this instance
	// anywhere yet, so the walk below cannot observe it — only earlier
	// instances left behind by a previous release.
	cp_module_drain_parked(agent, module_type, destroy);

	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	if (dp_config_lookup_module(
		    dp_config, module_type, &cp_module->dp_module_idx
	    )) {
		yanet_error_add(
			err,
			"module type '%s' not found in dataplane config",
			module_type
		);
		return -1;
	}

	strtcpy(cp_module->type, module_type, sizeof(cp_module->type));
	strtcpy(cp_module->name, module_name, sizeof(cp_module->name));
	memory_context_init_from(
		&cp_module->memory_context, &agent->memory_context, module_name
	);

	SET_OFFSET_OF(&cp_module->agent, agent);

	registry_item_init(&cp_module->config_item);

	if (counter_registry_init(
		    &cp_module->counter_registry, &cp_module->memory_context, 0
	    )) {
		yanet_error_add(
			err,
			"failed to initialize counter registry for module "
			"'%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}

	if (counter_registry_init(
		    &cp_module->config_counter_registry,
		    &cp_module->memory_context,
		    0
	    )) {
		yanet_error_add(
			err,
			"failed to initialize config counter registry for "
			"module '%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}

	cp_module->rx_counter_id = counter_registry_register(
		&cp_module->counter_registry, "rx", 2, err
	);
	if (cp_module->rx_counter_id == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'rx' counter for module '%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}
	cp_module->tx_counter_id = counter_registry_register(
		&cp_module->counter_registry, "tx", 2, err
	);
	if (cp_module->tx_counter_id == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'tx' counter for module '%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}
	cp_module->drop_counter_id = counter_registry_register(
		&cp_module->counter_registry, "drop", 2, err
	);
	if (cp_module->drop_counter_id == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'drop' counter for module '%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}
	cp_module->pending_input_counter_id = counter_registry_register(
		&cp_module->counter_registry, "pending_input", 2, err
	);
	if (cp_module->pending_input_counter_id == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'pending_input' counter for module "
			"'%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}
	cp_module->pending_output_counter_id = counter_registry_register(
		&cp_module->counter_registry, "pending_output", 2, err
	);
	if (cp_module->pending_output_counter_id == COUNTER_INVALID) {
		yanet_error_add(
			err,
			"failed to register 'pending_output' counter for "
			"module "
			"'%s:%s'",
			module_type,
			module_name
		);
		goto fail;
	}

	if (cp_module_build_perf_counters(cp_module, err)) {
		goto fail;
	}

	uint64_t any_idx;
	if (cp_module_link_device(cp_module, "", &any_idx, err)) {
		goto fail;
	}

	// Take the creator's reference only after construction succeeds.
	//
	// A failed construction leaves nothing to unbalance. This reference
	// is never mirrored into the live-reference count: a dead creator
	// must not hold it above zero.
	registry_item_ref(&cp_module->config_item);

	return 0;

fail:
	cp_module_fini(cp_module);
	return -1;
}

void
cp_module_fini(struct cp_module *cp_module) {
	counter_registry_fini(&cp_module->counter_registry);
	counter_registry_fini(&cp_module->config_counter_registry);

	struct cp_module_device *devices = ADDR_OF(&cp_module->devices);
	if (devices != NULL) {
		memory_bfree(
			&cp_module->memory_context,
			devices,
			sizeof(struct cp_module_device) *
				cp_module->device_count
		);
	}
	SET_OFFSET_OF(&cp_module->devices, NULL);
	cp_module->device_count = 0;

	struct cp_module_object *objects = ADDR_OF(&cp_module->objects);
	if (objects != NULL) {
		memory_bfree(
			&cp_module->memory_context,
			objects,
			sizeof(struct cp_module_object) *
				cp_module->object_count
		);
	}
	SET_OFFSET_OF(&cp_module->objects, NULL);
	cp_module->object_count = 0;

	SET_OFFSET_OF(&cp_module->agent, NULL);

	memory_context_fini(&cp_module->memory_context);
}

int
cp_module_link_device(
	struct cp_module *cp_module,
	const char *name,
	uint64_t *index,
	yanet_error **err
) {
	struct cp_module_device *devices = ADDR_OF(&cp_module->devices);
	for (uint64_t idx = 0; idx < cp_module->device_count; ++idx) {
		if (!strncmp(devices[idx].name, name, CP_DEVICE_NAME_LEN)) {
			*index = idx;
			return 0;
		}
	}

	devices = (struct cp_module_device *)memory_brealloc(
		&cp_module->memory_context,
		devices,
		sizeof(struct cp_module_device) * cp_module->device_count,
		sizeof(struct cp_module_device) * (cp_module->device_count + 1)
	);
	if (devices == NULL) {
		yanet_error_add(
			err,
			"failed to reallocate devices array for module '%s:%s'",
			cp_module->type,
			cp_module->name
		);
		return -1;
	}

	strtcpy(devices[cp_module->device_count].name, name, CP_DEVICE_NAME_LEN
	);
	SET_OFFSET_OF(&cp_module->devices, devices);
	*index = cp_module->device_count;
	++cp_module->device_count;

	return 0;
}

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
	struct agent *agent = ADDR_OF(&cp_module->agent);
	struct cp_config *cp_config =
		(agent != NULL) ? ADDR_OF(&agent->cp_config) : NULL;

	if (cp_config != NULL) {
		cp_config_lock(cp_config);
	}
	registry_item_unref(
		&cp_module->config_item, cp_module_registry_item_free_cb, NULL
	);
	if (cp_config != NULL) {
		cp_config_unlock(cp_config);
	}
}

static void
cp_module_drain_parked(
	struct agent *agent,
	const char *module_type,
	cp_module_free_handler destroy
) {
	struct cp_config *cp_config = ADDR_OF(&agent->cp_config);

	cp_config_lock(cp_config);

	// Splice the target type's entries into a local list, leaving the
	// rest linked in place for their own type's next call.
	//
	// A spliced-out tail leaves its former predecessor pointing at itself
	// as the new end of the list, so a parked entry's link is never null.
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

	if (owned == NULL) {
		cp_config_unlock(cp_config);
		return;
	}

	// Pin the agent's arena for the destroy loop below, still under the
	// same lock the splice above ran under, so the reclaim guard can never
	// observe the entries detached but the pin not yet set.
	agent->parked_teardown_count += 1;

	cp_config_unlock(cp_config);

	while (owned != NULL) {
		struct cp_module *next = ADDR_OF(&owned->parked_next);
		destroy(owned);
		owned = next;
	}

	cp_config_lock(cp_config);
	agent->parked_teardown_count -= 1;
	cp_config_unlock(cp_config);
}

int
cp_module_registry_init(
	struct memory_context *memory_context,
	struct cp_module_registry *new_module_registry,
	yanet_error **err
) {
	if (registry_init(memory_context, &new_module_registry->registry, 8)) {
		yanet_error_add(err, "failed to initialize module registry");
		return -1;
	}

	SET_OFFSET_OF(&new_module_registry->memory_context, memory_context);
	return 0;
}

int
cp_module_registry_copy(
	struct memory_context *memory_context,
	struct cp_module_registry *new_module_registry,
	struct cp_module_registry *old_module_registry,
	yanet_error **err
) {
	if (registry_copy(
		    memory_context,
		    &new_module_registry->registry,
		    &old_module_registry->registry
	    )) {
		yanet_error_add(err, "failed to copy module registry");
		return -1;
	};

	SET_OFFSET_OF(&new_module_registry->memory_context, memory_context);

	// Mirror each copied item's new generation reference into its own
	// agent.
	//
	// This keeps the per-agent live count tracking references from a
	// live configuration generation rather than the creator's own hold,
	// which a dead creator would otherwise never release.
	for (uint64_t idx = 0; idx < new_module_registry->registry.capacity;
	     ++idx) {
		struct cp_module *module =
			cp_module_registry_get(new_module_registry, idx);
		if (module == NULL) {
			continue;
		}
		struct agent *agent = ADDR_OF(&module->agent);
		if (agent != NULL) {
			agent->loaded_module_count += 1;
		}
	}

	return 0;
}

void
cp_module_registry_fini(struct cp_module_registry *module_registry) {
	struct memory_context *memory_context =
		ADDR_OF(&module_registry->memory_context);

	// Mirror each remaining item's dropped generation reference into its
	// own agent before releasing it.
	//
	// The callback this teardown invokes now only parks each item at
	// zero references instead of adjusting the live count itself, so
	// this loop mirrors the drop first. The same allocation-failure
	// guard applies as elsewhere: a registry whose backing storage never
	// allocated reports a nonzero capacity with nothing to walk.
	if (ADDR_OF(&module_registry->registry.items) != NULL) {
		for (uint64_t idx = 0; idx < module_registry->registry.capacity;
		     ++idx) {
			struct cp_module *module =
				cp_module_registry_get(module_registry, idx);
			if (module == NULL) {
				continue;
			}
			struct agent *agent = ADDR_OF(&module->agent);
			if (agent != NULL) {
				agent->loaded_module_count -= 1;
			}
		}
	}

	registry_fini(
		&module_registry->registry,
		cp_module_registry_item_free_cb,
		memory_context
	);
}

struct cp_module *
cp_module_registry_get(
	struct cp_module_registry *module_registry, uint64_t index
) {
	struct registry_item *item =
		registry_get(&module_registry->registry, index);
	if (item == NULL) {
		return NULL;
	}
	return container_of(item, struct cp_module, config_item);
}

struct cp_module_cmp_data {
	const char *type;
	const char *name;
};

static int
cp_module_registry_item_cmp(
	const struct registry_item *item, const void *data
) {
	const struct cp_module *module =
		container_of(item, struct cp_module, config_item);

	const struct cp_module_cmp_data *cmp_data =
		(const struct cp_module_cmp_data *)data;

	int cmp = strncmp(module->name, cmp_data->name, sizeof(module->name));

	if (cmp) {
		return cmp;
	}

	return strncmp(module->type, cmp_data->type, sizeof(module->type));
}

int
cp_module_registry_lookup_index(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name,
	uint64_t *index
) {
	struct cp_module_cmp_data cmp_data = {
		.type = type,
		.name = name,
	};

	return registry_lookup(
		&module_registry->registry,
		cp_module_registry_item_cmp,
		&cmp_data,
		index
	);
}

struct cp_module *
cp_module_registry_lookup(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name
) {
	uint64_t index;

	if (cp_module_registry_lookup_index(
		    module_registry, type, name, &index
	    )) {
		return NULL;
	}

	return container_of(
		registry_get(&module_registry->registry, index),
		struct cp_module,
		config_item
	);
}

int
cp_module_registry_upsert(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name,
	struct cp_module *new_module,
	yanet_error **err
) {
	struct cp_module_cmp_data cmp_data = {
		.type = type,
		.name = name,
	};

	struct cp_module *old_module =
		cp_module_registry_lookup(module_registry, type, name);

	if (counter_registry_link(
		    &new_module->counter_registry,
		    (old_module != NULL) ? &old_module->counter_registry : NULL,
		    err
	    )) {
		yanet_error_add(err, "failed to link counter registry");
		return -1;
	}

	if (counter_registry_link(
		    &new_module->config_counter_registry,
		    (old_module != NULL) ? &old_module->config_counter_registry
					 : NULL,
		    err
	    )) {
		yanet_error_add(err, "failed to link config counter registry");
		return -1;
	}

	if (registry_replace(
		    &module_registry->registry,
		    cp_module_registry_item_cmp,
		    &cmp_data,
		    &new_module->config_item,
		    cp_module_registry_item_free_cb,
		    ADDR_OF(&module_registry->memory_context)
	    )) {
		yanet_error_add(err, "failed to replace module in registry");
		return -1;
	}

	// Mirror this upsert's generation-reference changes into each
	// affected module's own agent.
	//
	// Gaining a reference through upsert only ever happens here, and
	// losing one to a displacing upsert only ever happens here too, so
	// the per-agent live count must track both in this one place.
	struct agent *new_agent = ADDR_OF(&new_module->agent);
	if (new_agent != NULL) {
		new_agent->loaded_module_count += 1;
	}
	if (old_module != NULL) {
		struct agent *old_agent = ADDR_OF(&old_module->agent);
		if (old_agent != NULL) {
			old_agent->loaded_module_count -= 1;
		}
	}

	return 0;
}

int
cp_module_registry_delete(
	struct cp_module_registry *module_registry,
	const char *type,
	const char *name
) {
	struct cp_module_cmp_data cmp_data = {
		.type = type,
		.name = name,
	};

	struct cp_module *old_module =
		cp_module_registry_lookup(module_registry, type, name);

	if (registry_replace(
		    &module_registry->registry,
		    cp_module_registry_item_cmp,
		    &cmp_data,
		    NULL,
		    cp_module_registry_item_free_cb,
		    ADDR_OF(&module_registry->memory_context)
	    )) {
		return -1;
	}

	// Mirror the generation reference this delete drops into the removed
	// module's own agent, matching the decrement upsert performs when it
	// displaces an entry.
	if (old_module != NULL) {
		struct agent *old_agent = ADDR_OF(&old_module->agent);
		if (old_agent != NULL) {
			old_agent->loaded_module_count -= 1;
		}
	}

	return 0;
}

size_t
cp_module_registry_size(struct cp_module_registry *module_registry) {
	return module_registry->registry.capacity;
}

int
cp_module_parse_performance_counter(
	struct counter_handle *counter_handle,
	size_t workers,
	size_t *idx,
	struct module_performance_counter *counter
) {
	// Validate inputs
	if (counter_handle == NULL || idx == NULL || counter == NULL) {
		errno = EINVAL;
		return -1;
	}

	if (workers == 0) {
		errno = EINVAL;
		return -1;
	}

	// Parse counter name to extract index (expecting "hist_N" format)
	size_t counter_idx;
	if (sscanf(counter_handle->name, "hist_%zu", &counter_idx) != 1) {
		errno = EINVAL;
		return -1;
	}

	// Validate counter index is in valid range [0, 5]
	if (counter_idx >= MODULE_ECTX_PERF_COUNTERS) {
		errno = EINVAL;
		return -1;
	}

	// Calculate total number of histogram buckets
	const size_t hist_buckets =
		counters_hybrid_histogram_batches(&module_ectx_perf_counter);

	// Determine minimum batch size based on counter index
	// Batch sizes: 1, 2-3, 4-7, 8-15, 16-31, 32+
	const uint64_t batch_sizes[MODULE_ECTX_PERF_COUNTERS] = {
		1, 2, 4, 8, 16, 32
	};

	// Allocate memory for latency ranges
	counter->latency_ranges =
		(struct module_performance_counter_latency_range *)malloc(
			sizeof(struct module_performance_counter_latency_range
			) *
			hist_buckets
		);

	if (counter->latency_ranges == NULL) {
		errno = ENOMEM;
		return -1;
	}

	// Set counter metadata
	counter->min_batch_size = batch_sizes[counter_idx];
	counter->latency_ranges_count = hist_buckets;

	// Salc summary tx and summary latency
	counter->summary_latency = 0;
	counter->packets = 0;
	counter->bytes = 0;
	for (size_t instance_idx = 0; instance_idx < workers; ++instance_idx) {
		struct module_ectx_perf_counter_layout *perf_counter =
			(struct module_ectx_perf_counter_layout *)
				counter_handle->values[instance_idx];
		counter->summary_latency += perf_counter->summary_latency;
		counter->packets += perf_counter->packets;
		counter->bytes += perf_counter->bytes;
	}

	// Fill in latency ranges and accumulate counter values across all
	// workers
	for (size_t range_idx = 0; range_idx < hist_buckets; ++range_idx) {
		struct module_performance_counter_latency_range *latency_range =
			&counter->latency_ranges[range_idx];
		// Calculate minimum latency for this bucket
		latency_range->min_latency =
			counters_hybrid_histogram_batch_first_elem(
				&module_ectx_perf_counter, range_idx
			);

		// Accumulate counter values across all worker instances
		latency_range->batches = 0;
		for (size_t worker_idx = 0; worker_idx < workers;
		     ++worker_idx) {
			struct module_ectx_perf_counter_layout *perf_counter =
				(struct module_ectx_perf_counter_layout *)
					counter_handle->values[worker_idx];
			latency_range->batches +=
				perf_counter->batch_count[range_idx];
		}
	}

	// Set output index
	*idx = counter_idx;

	return 0;
}

int
cp_module_parse_tx_rx(
	struct counter_handle *counter_handle,
	size_t workers,
	uint64_t *tx,
	uint64_t *rx,
	uint64_t *tx_bytes,
	uint64_t *rx_bytes
) {
	if (counter_handle == NULL || workers == 0) {
		errno = EINVAL;
		return -1;
	}

	const char *name = counter_handle->name;

	uint64_t *packets_target = NULL;
	uint64_t *bytes_target = NULL;

	if (strcmp(name, "tx") == 0) {
		packets_target = tx;
		bytes_target = tx_bytes;
	} else if (strcmp(name, "rx") == 0) {
		packets_target = rx;
		bytes_target = rx_bytes;
	} else {
		return 1;
	}

	if (packets_target == NULL || bytes_target == NULL) {
		errno = EINVAL;
		return -1;
	}

	uint64_t total_packets = 0;
	uint64_t total_bytes = 0;
	for (size_t worker_idx = 0; worker_idx < workers; ++worker_idx) {
		uint64_t *counter_values = counter_handle->values[worker_idx];
		// size-2 counter: [0] = packets, [1] = bytes
		total_packets += counter_values[0];
		total_bytes += counter_values[1];
	}

	*packets_target = total_packets;
	*bytes_target = total_bytes;
	return 0;
}
