#include "vs.h"
#include "service.h"

void
vs_get_info(struct vs_state *vs, struct named_vs_info *info) {
	// Copy identifier
	info->identifier = vs->identifier;

	// Accumulate per-worker shards into a single snapshot
	union service_info svc_info;
	svc_info.vs = vs->info;

	vs_info_accum(&info->info, &svc_info, MAX_WORKERS_NUM);
}
