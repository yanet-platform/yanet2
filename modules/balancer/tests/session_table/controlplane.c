#include "controlplane.h"

#include "api/session_table.h"

#include <stdatomic.h>

#include <lib/logging/log.h>

////////////////////////////////////////////////////////////////////////////////

void
run_watcher(struct watcher *watcher) {
	LOG(INFO, "watcher start");

	const uint32_t sleep_time_ms = 100;
	const uint32_t sleep_time_us = sleep_time_ms * 1000; // 100 ms
	while (atomic_load(&watcher->stop) == 0) {
		int extend_result = balancer_session_table_extend(
			watcher->session_table, false
		);
		if (extend_result == 1) {
			LOG(INFO, "extended table");
		} else if (extend_result == -1) {
			LOG(WARN, "failed to extend table");
		}
		int free_result = balancer_session_table_free_unused(
			watcher->session_table
		);
		if (free_result == 1) {
			LOG(INFO, "released unused memory");
		}
		rte_delay_us_sleep(sleep_time_us); // 100 ms
	}

	LOG(INFO, "watcher done");
}