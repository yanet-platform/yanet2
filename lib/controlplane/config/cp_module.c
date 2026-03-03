#include "cp_module.h"

#include "common/container_of.h"

#include "counters/counters.h"
#include "dataplane/config/zone.h"

#include "controlplane/agent/agent.h"

#include "controlplane/config/zone.h"
#include "lib/controlplane/diag/diag.h"
#include <stdio.h>

static int
cp_module_build_perf_counters(struct cp_module *cp_module) {
	for (size_t counter_idx = 0; counter_idx < MODULE_ECTX_PERF_COUNTERS;
	     ++counter_idx) {
		char name[16];
		sprintf(name, "hist_%zu", counter_idx);
		cp_module->perf_counters_indices[counter_idx] =
			counter_registry_register(
				&cp_module->counter_registry,
				name,
				MODULE_ECTX_PERF_COUNTER_SIZE
			);
		if (cp_module->perf_counters_indices[counter_idx] ==
		    COUNTER_INVALID) {
			NEW_ERROR(
				"failed to register histogram counter at index "
				"%zu for module "
				"'%s:%s'",
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
	const char *module_name
) {
	memset(cp_module, 0, sizeof(struct cp_module));

	struct dp_config *dp_config = ADDR_OF(&agent->dp_config);

	if (dp_config_lookup_module(
		    dp_config, module_type, &cp_module->dp_module_idx
	    )) {
		NEW_ERROR(
			"module type '%s' not found in dataplane config",
			module_type
		);
		errno = ENXIO;
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
		NEW_ERROR(
			"failed to initialize counter registry for module "
			"'%s:%s'",
			module_type,
			module_name
		);
		return -1;
	}

	cp_module->rx_counter_id = counter_registry_register(
		&cp_module->counter_registry, "rx", 1
	);
	if (cp_module->rx_counter_id == COUNTER_INVALID) {
		NEW_ERROR(
			"failed to register 'rx' counter for module '%s:%s'",
			module_type,
			module_name
		);
		return -1;
	}
	cp_module->tx_counter_id = counter_registry_register(
		&cp_module->counter_registry, "tx", 1
	);
	if (cp_module->tx_counter_id == COUNTER_INVALID) {
		NEW_ERROR(
			"failed to register 'tx' counter for module '%s:%s'",
			module_type,
			module_name
		);
		return -1;
	}
	cp_module->rx_bytes_counter_id = counter_registry_register(
		&cp_module->counter_registry, "rx_bytes", 1
	);
	if (cp_module->rx_bytes_counter_id == COUNTER_INVALID) {
		NEW_ERROR(
			"failed to register 'rx_bytes' counter for module "
			"'%s:%s'",
			module_type,
			module_name
		);
		return -1;
	}
	cp_module->tx_bytes_counter_id = counter_registry_register(
		&cp_module->counter_registry, "tx_bytes", 1
	);
	if (cp_module->tx_bytes_counter_id == COUNTER_INVALID) {
		NEW_ERROR(
			"failed to register 'tx_bytes' counter for module "
			"'%s:%s'",
			module_type,
			module_name
		);
		return -1;
	}

	if (cp_module_build_perf_counters(cp_module)) {
		return -1;
	}

	uint64_t any_idx;
	if (cp_module_link_device(cp_module, "", &any_idx)) {
		PUSH_ERROR(
			"in cp_module_init for module '%s:%s'",
			module_type,
			module_name
		);
		return -1;
	}

	return 0;
}

int
cp_module_link_device(
	struct cp_module *cp_module, const char *name, uint64_t *index
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
		NEW_ERROR("failed to reallocate devices array for module");
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
cp_module_registry_init(
	struct memory_context *memory_context,
	struct cp_module_registry *new_module_registry
) {
	if (registry_init(memory_context, &new_module_registry->registry, 8)) {
		NEW_ERROR("failed to initialize module registry");
		return -1;
	}

	SET_OFFSET_OF(&new_module_registry->memory_context, memory_context);
	return 0;
}

int
cp_module_registry_copy(
	struct memory_context *memory_context,
	struct cp_module_registry *new_module_registry,
	struct cp_module_registry *old_module_registry
) {
	if (registry_copy(
		    memory_context,
		    &new_module_registry->registry,
		    &old_module_registry->registry
	    )) {
		NEW_ERROR("failed to copy module registry");
		return -1;
	};

	SET_OFFSET_OF(&new_module_registry->memory_context, memory_context);
	return 0;
}

static void
cp_module_registry_item_free_cb(struct registry_item *item, void *data) {
	(void)data;

	struct cp_module *module =
		container_of(item, struct cp_module, config_item);

	struct agent *agent = ADDR_OF(&module->agent);
	SET_OFFSET_OF(&module->prev, agent->unused_module);
	SET_OFFSET_OF(&agent->unused_module, module);
}

void
cp_module_registry_destroy(struct cp_module_registry *module_registry) {
	struct memory_context *memory_context =
		ADDR_OF(&module_registry->memory_context);
	registry_destroy(
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
	if (item == NULL)
		return NULL;
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

	if (cmp)
		return cmp;

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
	struct cp_module *new_module
) {
	struct cp_module_cmp_data cmp_data = {
		.type = type,
		.name = name,
	};

	struct cp_module *old_module =
		cp_module_registry_lookup(module_registry, type, name);

	counter_registry_link(
		&new_module->counter_registry,
		(old_module != NULL) ? &old_module->counter_registry : NULL
	);

	return registry_replace(
		&module_registry->registry,
		cp_module_registry_item_cmp,
		&cmp_data,
		&new_module->config_item,
		cp_module_registry_item_free_cb,
		ADDR_OF(&module_registry->memory_context)
	);
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

	return registry_replace(
		&module_registry->registry,
		cp_module_registry_item_cmp,
		&cmp_data,
		NULL,
		cp_module_registry_item_free_cb,
		ADDR_OF(&module_registry->memory_context)
	);
}

size_t
cp_module_registry_size(struct cp_module_registry *module_registry) {
	return module_registry->registry.capacity;
}

////////////////////////////////////////////////////////////////////////////////

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
				counter_handle_get_value(
					counter_handle->value_handle,
					instance_idx
				);
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
					counter_handle_get_value(
						counter_handle->value_handle,
						worker_idx
					);
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
	// Validate inputs
	if (counter_handle == NULL || workers == 0) {
		errno = EINVAL;
		return -1;
	}

	// Determine which counter this is by checking the name
	const char *name = counter_handle->name;
	uint64_t *target = NULL;

	if (strcmp(name, "tx") == 0) {
		target = tx;
	} else if (strcmp(name, "rx") == 0) {
		target = rx;
	} else if (strcmp(name, "tx_bytes") == 0) {
		target = tx_bytes;
	} else if (strcmp(name, "rx_bytes") == 0) {
		target = rx_bytes;
	} else {
		// Not a tx/rx counter, return 1 to indicate no match
		return 1;
	}

	// Validate target pointer
	if (target == NULL) {
		errno = EINVAL;
		return -1;
	}

	// Aggregate counter values across all workers
	uint64_t total = 0;
	for (size_t worker_idx = 0; worker_idx < workers; ++worker_idx) {
		uint64_t *counter_values = counter_handle_get_value(
			counter_handle->value_handle, worker_idx
		);
		// Simple counters have size=1, so we access index 0
		total += counter_values[0];
	}

	*target = total;
	return 0;
}
