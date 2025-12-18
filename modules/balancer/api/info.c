#include "info.h"
#include "common/memory.h"
#include "state.h"

#include "../state/registry.h"
#include "../state/state.h"

////////////////////////////////////////////////////////////////////////////////

int
balancer_fill_virtual_services_info(
	struct balancer_state *state,
	struct balancer_virtual_services_info *info
) {
	size_t count = state->vs_registry.array.size;
	struct balancer_virtual_service_info *vs_info = memory_balloc(
		state->mctx,
		count * sizeof(struct balancer_virtual_service_info)
	);
	if (vs_info == NULL) {
		return -1;
	}

	for (size_t i = 0; i < count; ++i) {
		balancer_fill_virtual_service_info(state, i, &vs_info[i]);
	}

	info->info = vs_info;
	info->count = count;

	return 0;
}

/// Fills virtual service info.
/// @returns -1 on error.
int
balancer_fill_virtual_service_info(
	struct balancer_state *state,
	size_t virtual_service_idx,
	struct balancer_virtual_service_info *info
) {
	if (virtual_service_idx >= state->vs_registry.array.size) {
		return -1;
	}
	memset(info, 0, sizeof(struct balancer_virtual_service_info));
	service_info_accumulate_into_vs_info(
		service_registry_lookup(
			&state->vs_registry, virtual_service_idx
		),
		info,
		state->workers
	);

	return 0;
}

void
balancer_free_virtual_services_info(
	struct balancer_state *state,
	struct balancer_virtual_services_info *info
) {
	memory_bfree(
		state->mctx,
		info->info,
		info->count * sizeof(struct balancer_virtual_service_info)
	);
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_fill_reals_info(
	struct balancer_state *state, struct balancer_reals_info *info
) {
	size_t count = state->real_registry.array.size;
	struct balancer_real_info *real_info = memory_balloc(
		state->mctx, count * sizeof(struct balancer_real_info)
	);
	if (real_info == NULL) {
		return -1;
	}
	for (size_t i = 0; i < count; ++i) {
		balancer_fill_real_info(state, i, &real_info[i]);
	}
	info->info = real_info;
	info->count = count;
	return 0;
}

void
balancer_free_reals_info(
	struct balancer_state *state, struct balancer_reals_info *info
) {
	memory_bfree(
		state->mctx,
		info->info,
		info->count * sizeof(struct balancer_real_info)
	);
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_fill_real_info(
	struct balancer_state *state,
	size_t real_idx,
	struct balancer_real_info *info
) {
	if (real_idx >= state->real_registry.array.size) {
		return -1;
	}
	service_info_accumulate_into_real_info(
		service_registry_lookup(&state->real_registry, real_idx),
		info,
		state->workers
	);
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

// Helper function to add one uint64 array to another.
static inline void
add(uint64_t *dst, uint64_t *src, size_t size) {
	for (size_t i = 0; i < size; ++i) {
		dst[i] += src[i];
	}
}

int
balancer_fill_info(struct balancer_state *state, struct balancer_info *info) {
	// Fill virtual services stats
	if (balancer_fill_virtual_services_info(
		    state, &info->virtual_services
	    ) != 0) {
		return -1;
	}

	// Fill real stats
	if (balancer_fill_reals_info(state, &info->reals) != 0) {
		balancer_free_virtual_services_info(
			state, &info->virtual_services
		);
		return -1;
	}

	// Fill stats

	// Aggregate stats from multiple workers
	memset(&info->stats, 0, sizeof(info->stats));
	for (size_t worker = 0; worker < state->workers; ++worker) {
		struct balancer_stats *current_worker_stats =
			&state->stats[worker];
		add((uint64_t *)&info->stats,
		    (uint64_t *)current_worker_stats,
		    sizeof(info->stats) / sizeof(uint64_t));
	}

	return 0;
}

void
balancer_free_info(struct balancer_state *state, struct balancer_info *info) {
	balancer_free_virtual_services_info(state, &info->virtual_services);
	balancer_free_reals_info(state, &info->reals);
}

////////////////////////////////////////////////////////////////////////////////

int
balancer_fill_sessions_info(
	struct balancer_state *state,
	struct balancer_sessions_info *info,
	uint32_t now,
	bool count_only
) {
	return session_table_fill_sessions_info(
		&state->session_table, info, state->mctx, now, count_only
	);
}

void
balancer_free_sessions_info(
	struct balancer_state *state, struct balancer_sessions_info *info
) {
	session_table_free_sessions_info(info, state->mctx);
}