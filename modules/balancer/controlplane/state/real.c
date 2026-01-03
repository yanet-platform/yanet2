#include "real.h"
#include "service.h"

void
real_get_info(struct real_state *real, struct named_real_info *info) {
	// Copy identifier and enabled flag
	info->identifier = real->identifier;
	info->enabled = real->enabled;

	// Accumulate per-worker shards into a single snapshot
	union service_info svc_info;
	svc_info.real = real->info;

	real_info_accum(&info->info, &svc_info, MAX_WORKERS_NUM);
}