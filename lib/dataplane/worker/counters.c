#include "counters.h"

#include <stdbool.h>

#include "lib/counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/errors/errors.h"
#include "lib/logging/log.h"

// Returns the assigned counter identifier, or the invalid sentinel after
// logging the failure.
static uint64_t
register_one(struct dp_config *dp_config, const char *name, uint64_t size) {
	yanet_error *err = NULL;
	uint64_t rc = counter_registry_register(
		&dp_config->worker_counters, name, size, &err
	);
	if (rc == COUNTER_INVALID) {
		LOG(ERROR,
		    "failed to register '%s' counter: %s",
		    name,
		    yanet_error_message(err));
		yanet_error_free(err);
	}
	return rc;
}

static bool
worker_counters_valid(const struct worker_counters *counters) {
	return counters->iterations != COUNTER_INVALID &&
	       counters->rx != COUNTER_INVALID &&
	       counters->tx != COUNTER_INVALID &&
	       counters->remote_rx != COUNTER_INVALID &&
	       counters->remote_tx != COUNTER_INVALID &&
	       counters->rx_bursts != COUNTER_INVALID &&
	       counters->local_tx_drops != COUNTER_INVALID &&
	       counters->remote_tx_drops != COUNTER_INVALID &&
	       counters->drops != COUNTER_INVALID;
}

int
worker_counters_register(
	struct dp_config *dp_config, struct worker_counters *counters
) {
	counters->iterations = register_one(dp_config, "iterations", 1);
	counters->rx = register_one(dp_config, "rx", 2);
	counters->tx = register_one(dp_config, "tx", 2);
	counters->remote_rx = register_one(dp_config, "remote_rx", 2);
	counters->remote_tx = register_one(dp_config, "remote_tx", 2);
	counters->rx_bursts =
		register_one(dp_config, "rx_bursts", WORKER_RX_BURST_SIZE + 1);
	counters->local_tx_drops = register_one(dp_config, "local_tx_drops", 1);
	counters->remote_tx_drops =
		register_one(dp_config, "remote_tx_drops", 1);
	counters->drops = register_one(dp_config, "drops", 1);

	return worker_counters_valid(counters) ? 0 : -1;
}

void
worker_counters_bind(
	struct dp_worker *dp_worker, struct counter_storage *storage
) {
	const struct worker_counters *counters = &dp_worker->counters;

	dp_worker->iterations =
		counter_get_address(counters->iterations, storage);

	// Packet counters carry a byte sub-slot right after the packet count.
	dp_worker->rx_count = counter_get_address(counters->rx, storage) + 0;
	dp_worker->rx_size = counter_get_address(counters->rx, storage) + 1;

	dp_worker->tx_count = counter_get_address(counters->tx, storage) + 0;
	dp_worker->tx_size = counter_get_address(counters->tx, storage) + 1;

	dp_worker->remote_rx_count =
		counter_get_address(counters->remote_rx, storage) + 0;
	dp_worker->remote_tx_count =
		counter_get_address(counters->remote_tx, storage) + 0;

	dp_worker->rx_bursts =
		counter_get_address(counters->rx_bursts, storage);

	dp_worker->local_tx_drops =
		counter_get_address(counters->local_tx_drops, storage);
	dp_worker->remote_tx_drops =
		counter_get_address(counters->remote_tx_drops, storage);
	dp_worker->drop_count = counter_get_address(counters->drops, storage);
}
