#include "controlplane.h"

#include <stdatomic.h>

#include <lib/logging/log.h>

////////////////////////////////////////////////////////////////////////////////

void
run_controlplane(struct cp_config *config) {
	LOG(INFO, "Start controlplane");
	const uint32_t sleep_time_ms = 100;
	const uint32_t sleep_time_us = sleep_time_ms * 1000; // 100 ms
	while (__c11_atomic_load(&config->stop, __ATOMIC_SEQ_CST) == 0) {
		LOG(DEBUG, "CP: trying to extend balancer state");
		int extend_result =
			balancer_extend_state_on_demand(config->balancer);
		LOG(DEBUG, "CP: extend_result=%d", extend_result);
		if (!extend_result) {
			LOG(DEBUG, "CP: trying to free unused state");
			int free_result =
				balancer_try_free_unused(config->balancer);
			LOG(DEBUG, "CP: free_result=%d", free_result);
		}
		LOG(DEBUG, "CP: sleeping for %zuus", (size_t)sleep_time_us);
		rte_delay_us_sleep(sleep_time_us); // 100 ms
	}
	LOG(INFO, "Controlplane done");
}