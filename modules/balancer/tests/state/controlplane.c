#include "controlplane.h"

#include <stdatomic.h>

#include <lib/logging/log.h>

////////////////////////////////////////////////////////////////////////////////

void
run_controlplane(struct cp_config *config) {
	LOG(INFO, "Controlplane start");

	const uint32_t sleep_time_ms = 100;
	const uint32_t sleep_time_us = sleep_time_ms * 1000; // 100 ms
	while (atomic_load(&config->stop) == 0) {
		int extend_result =
			balancer_extend_state_on_demand(config->balancer);
		if (extend_result == 1) {
			LOG(INFO, "extended balancer state");
		} else if (extend_result == -1) {
			LOG(WARN, "failed to extended balancer state");
		}
		if (extend_result != 1) {
			int free_result =
				balancer_try_free_unused(config->balancer);
			if (free_result == 1) {
				LOG(INFO, "destroyed unused balancer state");
			}
		}
		rte_delay_us_sleep(sleep_time_us); // 100 ms
	}

	LOG(INFO, "Controlplane done");
}