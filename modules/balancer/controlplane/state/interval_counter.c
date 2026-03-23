#include "interval_counter.h"
#include "modules/balancer/dataplane/interval_counter.h"
#include <string.h>

void
rt_interval_counter_init(struct rt_interval_counter *counter, uint32_t now) {
	memset(counter->diff, 0, sizeof(counter->diff));
	counter->last_timestamp = now;
}
