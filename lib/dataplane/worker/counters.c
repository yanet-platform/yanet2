#include "counters.h"

#include "common/memory_address.h"
#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

static int
register_one(
	struct counter_registry *registry,
	const char *name,
	uint64_t size,
	uint64_t *id
) {
	yanet_error *err = NULL;
	uint64_t rc = counter_registry_register(registry, name, size, &err);
	if (rc == COUNTER_INVALID) {
		LOG(ERROR,
		    "failed to register '%s' counter: %s",
		    name,
		    yanet_error_message(err));
		yanet_error_free(err);
		return -1;
	}
	*id = rc;
	return 0;
}

int
worker_counters_register(
	struct counter_registry *registry, struct worker_counter_ids *ids
) {
	if (register_one(registry, "iterations", 1, &ids->iterations)) {
		return -1;
	}
	if (register_one(registry, "rx", 2, &ids->rx)) {
		return -1;
	}
	if (register_one(registry, "tx", 2, &ids->tx)) {
		return -1;
	}
	if (register_one(registry, "remote_rx", 2, &ids->remote_rx)) {
		return -1;
	}
	if (register_one(registry, "remote_tx", 2, &ids->remote_tx)) {
		return -1;
	}
	if (register_one(
		    registry,
		    "rx_bursts",
		    WORKER_RX_BURST_SIZE + 1,
		    &ids->rx_bursts
	    )) {
		return -1;
	}
	if (register_one(registry, "local_tx_drops", 1, &ids->local_tx_drops)) {
		return -1;
	}
	if (register_one(
		    registry, "remote_tx_drops", 1, &ids->remote_tx_drops
	    )) {
		return -1;
	}
	if (register_one(registry, "drops", 1, &ids->drops)) {
		return -1;
	}
	return 0;
}

static void
bind_one(
	struct dp_worker *dp_worker,
	const struct worker_counter_ids *ids,
	struct counter_storage *storage
) {
	dp_worker->iterations = counter_get_address(ids->iterations, storage);

	dp_worker->rx_count = counter_get_address(ids->rx, storage) + 0;
	dp_worker->rx_size = counter_get_address(ids->rx, storage) + 1;

	dp_worker->tx_count = counter_get_address(ids->tx, storage) + 0;
	dp_worker->tx_size = counter_get_address(ids->tx, storage) + 1;

	dp_worker->remote_rx_count =
		counter_get_address(ids->remote_rx, storage) + 0;

	dp_worker->remote_tx_count =
		counter_get_address(ids->remote_tx, storage) + 0;

	dp_worker->rx_bursts = counter_get_address(ids->rx_bursts, storage);

	dp_worker->local_tx_drops =
		counter_get_address(ids->local_tx_drops, storage);
	dp_worker->remote_tx_drops =
		counter_get_address(ids->remote_tx_drops, storage);
	dp_worker->drop_count = counter_get_address(ids->drops, storage);
}

void
worker_counters_bind(
	struct dp_config *dp_config, const struct worker_counter_ids *ids
) {
	uint64_t count = dp_config->worker_count;
	if (dp_config->worker_counter_storage_count < count) {
		LOG(ERROR, "some workers have no counter storage");
		count = dp_config->worker_counter_storage_count;
	}

	struct dp_worker **workers = ADDR_OF(&dp_config->workers);
	struct counter_storage **storages =
		ADDR_OF(&dp_config->worker_counter_storages);

	for (uint64_t idx = 0; idx < count; ++idx) {
		bind_one(ADDR_OF(workers + idx), ids, ADDR_OF(storages + idx));
	}
}
