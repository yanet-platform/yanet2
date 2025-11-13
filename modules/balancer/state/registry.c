#include "registry.h"
#include "common/interval_counter.h"

void
service_state_copy(struct service_state *dst, struct service_state *src) {
	dst->last_seen = src->last_seen;
	interval_counter_copy(&dst->active_sessions, &src->active_sessions);
}