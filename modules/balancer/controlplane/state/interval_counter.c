#include "interval_counter.h"
#include <string.h>

int
rt_interval_counter_init(
	struct rt_interval_counter *counter,
	const struct rt_interval_counter_config *config,
	uint32_t now,
	struct memory_context *mctx
) {
	int32_t *diff =
		memory_balloc(mctx, config->time_ring_size * sizeof(int32_t));
	if (diff == NULL) {
		return -1;
	}
	memset(diff, 0, config->time_ring_size * sizeof(int32_t));
	counter->last_timestamp = now;
	SET_OFFSET_OF(&counter->diff, diff);
	return 0;
}

void
rt_interval_counter_free(
	struct rt_interval_counter *counter,
	const struct rt_interval_counter_config *config,
	struct memory_context *mctx
) {
	int32_t *diff = ADDR_OF(&counter->diff);
	memory_bfree(mctx, diff, config->time_ring_size * sizeof(int32_t));
	counter->last_timestamp = 0;
	SET_OFFSET_OF(&counter->diff, NULL);
}
