#include "counters.h"

#include <stddef.h>

#include "common/memory_address.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

const struct worker_counter_spec worker_counter_specs[] = {
	{"iterations", 1},
	{"rx", 2},
	{"tx", 2},
	{"remote_rx", 2},
	{"remote_tx", 2},
	{"rx_bursts", WORKER_RX_BURST_SIZE + 1},
	{"local_tx_drops", 1},
	{"remote_tx_drops", 1},
	{"drops", 1},
};

const size_t worker_counter_spec_count =
	sizeof(worker_counter_specs) / sizeof(worker_counter_specs[0]);

const struct worker_counter_field worker_counter_fields[] = {
	{offsetof(struct dp_worker, iterations), "iterations", 0},
	{offsetof(struct dp_worker, rx_count), "rx", 0},
	{offsetof(struct dp_worker, rx_size), "rx", 1},
	{offsetof(struct dp_worker, tx_count), "tx", 0},
	{offsetof(struct dp_worker, tx_size), "tx", 1},
	{offsetof(struct dp_worker, remote_rx_count), "remote_rx", 0},
	{offsetof(struct dp_worker, remote_tx_count), "remote_tx", 0},
	{offsetof(struct dp_worker, rx_bursts), "rx_bursts", 0},
	{offsetof(struct dp_worker, local_tx_drops), "local_tx_drops", 0},
	{offsetof(struct dp_worker, remote_tx_drops), "remote_tx_drops", 0},
	{offsetof(struct dp_worker, drop_count), "drops", 0},
};

const size_t worker_counter_field_count =
	sizeof(worker_counter_fields) / sizeof(worker_counter_fields[0]);

static uint64_t **
field_pointer(
	struct dp_worker *dp_worker, const struct worker_counter_field *field
) {
	return (uint64_t **)((char *)dp_worker + field->offset);
}

static int
register_one(
	struct dp_config *dp_config, const struct worker_counter_spec *spec
) {
	yanet_error *err = NULL;
	uint64_t rc = counter_registry_register(
		&dp_config->worker_counters, spec->name, spec->size, &err
	);
	if (rc == COUNTER_INVALID) {
		LOG(ERROR,
		    "failed to register counter '%s' of size %lu: %s",
		    spec->name,
		    spec->size,
		    yanet_error_message(err));
		yanet_error_free(err);
		return -1;
	}
	return 0;
}

int
worker_counters_register(struct dp_config *dp_config) {
	for (size_t idx = 0; idx < worker_counter_spec_count; ++idx) {
		if (register_one(dp_config, worker_counter_specs + idx)) {
			return -1;
		}
	}
	return 0;
}

// Resolve the address of the counter value a worker field is bound to, or NULL
// if the counter is missing or too small to hold that value.
static uint64_t *
counter_value_address(
	struct dp_worker *dp_worker,
	struct dp_config *dp_config,
	const struct worker_counter_field *field
) {
	struct counter_registry *registry = &dp_config->worker_counters;
	uint64_t idx = counter_registry_lookup_index(registry, field->counter);
	if (idx == COUNTER_INVALID) {
		LOG(ERROR, "counter '%s' not found", field->counter);
		return NULL;
	}

	uint64_t value_count = counter_registry_lookup_size(registry, idx);
	if (field->value_idx >= value_count) {
		LOG(ERROR,
		    "counter '%s': value %lu is out of range, size is %lu",
		    field->counter,
		    field->value_idx,
		    value_count);
		return NULL;
	}

	struct counter_storage **storages =
		ADDR_OF_NONNULL(&dp_config->worker_counter_storages);
	struct counter_storage *storage =
		ADDR_OF_NONNULL(storages + dp_worker->idx);

	return counter_get_address(idx, storage) + field->value_idx;
}

int
worker_counters_bind(struct dp_worker *dp_worker, struct dp_config *dp_config) {
	uint64_t *original[worker_counter_field_count];
	int failed = 0;

	for (size_t idx = 0; idx < worker_counter_field_count; ++idx) {
		const struct worker_counter_field *field =
			worker_counter_fields + idx;
		uint64_t **pointer = field_pointer(dp_worker, field);
		original[idx] = *pointer;

		uint64_t *address =
			counter_value_address(dp_worker, dp_config, field);
		if (address == NULL) {
			failed = 1;
		} else {
			*pointer = address;
		}
	}

	if (failed) {
		for (size_t idx = 0; idx < worker_counter_field_count; ++idx) {
			*field_pointer(dp_worker, worker_counter_fields + idx) =
				original[idx];
		}
		return -1;
	}

	return 0;
}
