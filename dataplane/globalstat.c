#include "globalstat.h"

#include "dataplane.h"

#include <unistd.h>

#include <rte_ethdev.h>
#include <rte_ether.h>

#include "dataplane/device.h"
#include "dataplane/worker.h"

#include "counters/counters.h"
#include "lib/dataplane/config/zone.h"
#include "lib/errors/errors.h"
#include "logging/log.h"

#define ARRAY_SIZE(arr) (size_t)(sizeof(arr) / sizeof((arr)[0]))

static int
counter_register_counter(
	struct dp_config *dp_config, const char *name, uint64_t size
) {
	yanet_error *err = NULL;
	uint64_t rc = counter_registry_register(
		&dp_config->counters, name, size, &err
	);
	if (rc == COUNTER_INVALID) {
		LOG(ERROR,
		    "failed to register '%s' counter: %s",
		    name,
		    yanet_error_message(err));
		yanet_error_free(err);
		return -1;
	}

	return rc;
}

static const struct {
	const char *name;
	uint64_t size;
	const size_t *offset;
} global_counter_info[] = {
	{"nic_rx", 2, (const size_t[]){offsetof(struct dataplane, global_stats.nic_stats.rx_count), offsetof(struct dataplane, global_stats.nic_stats.rx_size)}},
	{"nic_tx", 2, (const size_t[]){offsetof(struct dataplane, global_stats.nic_stats.tx_count), offsetof(struct dataplane, global_stats.nic_stats.tx_size)}},
	{"nic_rx_tx_errors", 2, (const size_t[]){offsetof(struct dataplane, global_stats.nic_stats.remote_rx_count), offsetof(struct dataplane, global_stats.nic_stats.remote_tx_count)}},
	{"nic_rx_nombuf", 1, (const size_t[]){offsetof(struct dataplane, global_stats.nic_stats.rx_nombuf_count)}},
};

static uint64_t global_counter_ids[ARRAY_SIZE(global_counter_info)];

int
dataplane_globalstat_register_counters(struct dp_config *dp_config) {
	counter_registry_init(
		&dp_config->counters, &dp_config->memory_context, 0
	);

	for (size_t i = 0; i < ARRAY_SIZE(global_counter_info); ++i) {
		uint64_t id = counter_register_counter(
			dp_config,
			global_counter_info[i].name,
			global_counter_info[i].size
		);
		if (id == COUNTER_INVALID) {
			return -1;
		}
		global_counter_ids[i] = id;
	}

	return 0;
}

uint64_t**
get_worker_field_ptr(struct dataplane *dataplane, size_t info_index, size_t offset_index) {
	return (uint64_t**)((char*)dataplane + global_counter_info[info_index].offset[offset_index]);
}

static void
calculate_and_update_stats(
	struct dataplane *dataplane,
	struct rte_eth_stats *prev_stats,
	struct rte_eth_stats *cur_stats
) {
	struct nic_stats *nic_stats = &dataplane->global_stats.nic_stats;

	uint64_t rx_count = 0, tx_count = 0;
	uint64_t rx_size = 0, tx_size = 0;
	uint64_t rx_errors = 0, tx_errors = 0;
	uint64_t rx_nombuf = 0;

	for (uint16_t idx = 0; idx < dataplane->device_count; ++idx) {
		rx_count += cur_stats[idx].ipackets - prev_stats[idx].ipackets;
		tx_count += cur_stats[idx].opackets - prev_stats[idx].opackets;
		rx_size += cur_stats[idx].ibytes - prev_stats[idx].ibytes;
		tx_size += cur_stats[idx].obytes - prev_stats[idx].obytes;
		rx_errors += cur_stats[idx].ierrors - prev_stats[idx].ierrors;
		tx_errors += cur_stats[idx].oerrors - prev_stats[idx].oerrors;
		rx_nombuf += cur_stats[idx].rx_nombuf - prev_stats[idx].rx_nombuf;
	}

	*nic_stats->rx_count = rx_count;
	*nic_stats->tx_count = tx_count;
	*nic_stats->rx_size = rx_size;
	*nic_stats->tx_size = tx_size;
	*nic_stats->remote_rx_count = rx_errors;
	*nic_stats->remote_tx_count = tx_errors;
	*nic_stats->rx_nombuf_count = rx_nombuf;
}

void *
stat_thread(void *arg) {
	struct dataplane *dataplane = (struct dataplane *)arg;
	struct dp_config *dp_config = dataplane->global_dp_config;

	for (size_t i = 0; i < ARRAY_SIZE(global_counter_info); ++i) {
		for (size_t j = 0; j < global_counter_info[i].size; ++j) {
			uint64_t **field_ptr = get_worker_field_ptr(dataplane, i, j);
			*field_ptr = counter_get_address(
				global_counter_ids[i],
				0,
				ADDR_OF(&dp_config->counter_storage)
			) + j;
		}
	}

	struct rte_eth_stats stats_prev[dataplane->device_count];
	struct rte_eth_stats stats_cur[dataplane->device_count];

	memset(stats_prev, 0, sizeof(stats_prev));
	memset(stats_cur, 0, sizeof(stats_cur));

	for (uint16_t idx = 0; idx < dataplane->device_count; ++idx) {
		rte_eth_stats_get(dataplane->devices[idx].port_id, &stats_prev[idx]);
	}

	while (1) {
		sleep(1);

		for (uint16_t idx = 0; idx < dataplane->device_count; ++idx) {
			rte_eth_stats_get(dataplane->devices[idx].port_id, &stats_cur[idx]);
		}

		calculate_and_update_stats(dataplane, stats_prev, stats_cur);

		memcpy(stats_prev, stats_cur, sizeof(stats_prev));
	}

	return NULL;
}