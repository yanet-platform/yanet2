#include "globalstat.h"

#include "dataplane.h"

#include <stdint.h>

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

	return 0;
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

uint64_t**
get_worker_field_ptr(struct dataplane *dataplane, size_t info_index, size_t offset_index) {
	return (uint64_t**)((char*)dataplane + global_counter_info[info_index].offset[offset_index]);
}

void thread_unload_nic_stats(struct dataplane *dataplane) {
	struct nic_stats *nic_stats = &dataplane->global_stats.nic_stats;
	struct rte_eth_stats stats0[dataplane->device_count];

	for (uint16_t idx = 0; idx < dataplane->device_count; ++idx) {
		rte_eth_stats_get(
			dataplane->devices[idx].port_id, &stats0[idx]
		);
	}
	for (uint16_t idx = 0; idx < dataplane->device_count; ++idx) {
		*nic_stats->rx_size += stats0[idx].ibytes;
		*nic_stats->tx_size = stats0[idx].obytes;
		*nic_stats->rx_count = stats0[idx].ipackets;
		*nic_stats->tx_count = stats0[idx].opackets;
		*nic_stats->remote_rx_count = stats0[idx].ierrors;
		*nic_stats->remote_tx_count = stats0[idx].oerrors;
		*nic_stats->rx_nombuf_count = stats0[idx].rx_nombuf;
	}
}

void *
stat_thread(void *arg) {
	static uint64_t global_counter_ids[ARRAY_SIZE(global_counter_info)];
	
	struct dataplane *dataplane = (struct dataplane *)arg;

	struct dp_config *dp_config = dataplane->global_dp_config;

	for (size_t i = 0; i < ARRAY_SIZE(global_counter_info); ++i) {
		global_counter_ids[i] = counter_register_counter(
			dp_config,
			global_counter_info[i].name,
			global_counter_info[i].size
		);
	}

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

	struct rte_eth_stats stats0[dataplane->device_count];
	struct rte_eth_xstat_name names[4096];
	struct rte_eth_xstat xstats0[dataplane->device_count][4096];

	for (uint16_t idx = 0; idx < dataplane->device_count; ++idx) {
		rte_eth_stats_get(
			dataplane->devices[idx].port_id, &stats0[idx]
		);
		rte_eth_xstats_get(
			dataplane->devices[idx].port_id, xstats0[idx], 4096
		);
	}

	while (1) {
		sleep(1);

		thread_unload_nic_stats(dataplane);

		for (uint16_t idx = 0; idx < dataplane->device_count; ++idx) {
			struct rte_eth_stats stats1;
			rte_eth_stats_get(
				dataplane->devices[idx].port_id, &stats1
			);
			
			memcpy(&stats0[idx], &stats1, sizeof(stats1));

			struct rte_eth_xstat xstats1[4096];
			rte_eth_xstats_get_names(
				dataplane->devices[idx].port_id, names, 4096
			);
			int cnt = rte_eth_xstats_get(
				dataplane->devices[idx].port_id, xstats1, 4096
			);

			memcpy(&xstats0[idx],
			       xstats1,
			       sizeof(struct rte_eth_xstat) * cnt);
		}

	}

	return NULL;
}