#include "info.h"
#include "common/memory.h"
#include "state.h"

#include "../state/registry.h"
#include "../state/state.h"

////////////////////////////////////////////////////////////////////////////////

int
balancer_fill_vs_info(
	struct balancer_state *state,
	struct balancer_virtual_services_info *info
) {
	size_t count = state->vs_registry.service_count;
	struct balancer_vs_info *vs_info = memory_balloc(
		state->mctx, count * sizeof(struct balancer_vs_info)
	);
	if (vs_info == NULL) {
		return -1;
	}
	for (size_t i = 0; i < count; ++i) {
		service_info_accumulate_into_vs_info(
			&state->vs_registry.services[i],
			&vs_info[i],
			state->workers
		);
	}
	info->info = vs_info;
	info->count = count;
	return 0;
}

void
balancer_free_vs_info(
	struct balancer_state *state,
	struct balancer_virtual_services_info *info
) {
	memory_bfree(
		state->mctx,
		info,
		info->count * sizeof(struct balancer_virtual_services_info)
	);
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_fill_real_info(
	struct balancer_state *state, struct balancer_reals_info *info
) {
	size_t count = state->real_registry.service_count;
	struct balancer_real_info *real_info = memory_balloc(
		state->mctx, count * sizeof(struct balancer_reals_info)
	);
	if (real_info == NULL) {
		return -1;
	}
	for (size_t i = 0; i < count; ++i) {
		service_info_accumulate_into_real_info(
			&state->real_registry.services[i],
			&real_info[i],
			state->workers
		);
	}
	info->info = real_info;
	info->count = count;
	return 0;
}

void
balancer_free_real_info(
	struct balancer_state *state, struct balancer_reals_info *info
) {
	memory_bfree(
		state->mctx,
		info,
		info->count * sizeof(struct balancer_reals_info)
	);
}