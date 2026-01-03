#include "common/memory_address.h"
#include "lib/controlplane/diag/diag.h"

#include "real.h"
#include "state/state.h"
#include "vs.h"

#include "handler.h"

#define VS_COUNT 1024 * 64
static uint64_t updated_vs[VS_COUNT / 64] = {0};

static inline void
mark_vs_updated(size_t idx) {
	idx %= VS_COUNT;
	updated_vs[idx / 64] |= 1ULL << (idx % 64);
}

static inline int
is_vs_updated(size_t idx) {
	idx %= VS_COUNT;
	return updated_vs[idx / 64] & (1ULL << (idx % 64));
}

static inline void
unmark_vs_updated(size_t idx) {
	idx %= VS_COUNT;
	updated_vs[idx / 64] &= ~(1ULL << (idx % 64));
}

static int
validate_update(struct packet_handler *handler, struct real_update *update) {
	struct balancer_state *state = ADDR_OF(&handler->state);

	// validate real
	struct real_state *real =
		balancer_state_find_real(state, &update->identifier);
	if (real == NULL) {
		NEW_ERROR("real not found");
		return -1;
	}
	if (ADDR_OF(&handler->reals_index)[real->registry_idx] == (uint32_t)-1) {
		NEW_ERROR("real is not registered in handler");
		return -1;
	}

	// validate virtual service
	struct vs_state *vs = balancer_state_find_vs(
		state, &update->identifier.vs_identifier
	);
	if (vs == NULL) {
		NEW_ERROR("virtual service not found");
		return -1;
	}
	if (ADDR_OF(&handler->vs_index)[vs->registry_idx] == (uint32_t)-1) {
		NEW_ERROR("virtual service is not registered in handler");
		return -1;
	}

	// check update params
	if (update->enabled != DONT_UPDATE_REAL_ENABLED) {
		if (update->enabled != 0 && update->enabled != 1) {
			NEW_ERROR(
				"incorrect enabled field: %u (0, 1 or -1 "
				"expected)",
				update->enabled
			);
			return -1;
		}
	}
	if (update->weight == DONT_UPDATE_REAL_WEIGHT &&
	    update->enabled == DONT_UPDATE_REAL_ENABLED) {
		NEW_ERROR("update changes nothing");
		return -1;
	}
	if (update->weight != DONT_UPDATE_REAL_WEIGHT &&
	    update->weight > MAX_REAL_WEIGHT) {
		NEW_ERROR(
			"weight %u is too big (max is %u)",
			update->weight,
			MAX_REAL_WEIGHT
		);
		return -1;
	}
	
	return 0;
}

static void
update_real(struct packet_handler *handler, struct real_update *update) {
	struct balancer_state *state = ADDR_OF(&handler->state);
	uint32_t *real_index = ADDR_OF(&handler->reals_index);
	uint32_t *vs_index = ADDR_OF(&handler->vs_index);
	struct real_state *real =
		balancer_state_find_real(state, &update->identifier);
	struct vs_state *vs = balancer_state_find_vs(
		state, &update->identifier.vs_identifier
	);
	assert(real != NULL && vs != NULL);
	size_t vs_ph_idx = vs_index[vs->registry_idx];
	size_t real_ph_idx = real_index[real->registry_idx];
	int updated = 0;
	if (update->enabled != DONT_UPDATE_REAL_ENABLED &&
	    real->enabled != update->enabled) {
		real->enabled = update->enabled;
		updated = 1;
	}
	struct real *views = ADDR_OF(&handler->reals);
	struct real *view = &views[real_ph_idx];
	if (update->weight != DONT_UPDATE_REAL_WEIGHT &&
	    view->weight != update->weight) {
		view->weight = update->weight;
		updated = 1;
	}
	if (updated) {
		mark_vs_updated(vs_ph_idx);
	}
}

static int
update_vs(struct packet_handler *handler, struct real_update *update) {
	struct balancer_state *state = ADDR_OF(&handler->state);
	struct vs_state *vs = balancer_state_find_vs(
		state, &update->identifier.vs_identifier
	);
	assert(vs != NULL);
	uint32_t *vs_index = ADDR_OF(&handler->vs_index);
	size_t vs_ph_idx = vs_index[vs->registry_idx];
	if (!is_vs_updated(vs_ph_idx)) {
		return 0;
	}

	unmark_vs_updated(vs_ph_idx);

	struct vs *vs_views = ADDR_OF(&handler->vs);
	struct vs *vs_view = &vs_views[vs_ph_idx];
	if (vs_view_update_reals(vs_view) != 0) {
		PUSH_ERROR("failed to update reals");
		return -1;
	}

	return 0;
}

int
packet_handler_update_reals(
	struct packet_handler *handler,
	size_t count,
	struct real_update *updates
) {
	// validate
	for (size_t i = 0; i < count; ++i) {
		struct real_update *update = &updates[i];
		if (validate_update(handler, update) != 0) {
			PUSH_ERROR("update at index %lu is invalid", i);
			return -1;
		}
	}

	// update
	for (size_t i = 0; i < count; ++i) {
		struct real_update *update = &updates[i];
		update_real(handler, update);
	}

	// update virtual services
	for (size_t i = 0; i < handler->vs_count; ++i) {
		struct real_update *update = &updates[i];
		if (update_vs(handler, update) != 0) {
			if (i == 0) {
				PUSH_ERROR(
					"services %lu..%lu not updated: update "
					"at index %lu: failed to update "
					"virtual service",
					i,
					handler->vs_count,
					i
				);
			} else {
				PUSH_ERROR(
					"services 0..%lu successfully updated, "
					"services %lu..%lu not updated: "
					"update at index %lu: failed to update "
					"virtual service",
					i - 1,
					i,
					handler->vs_count,
					i
				);
			}
			break;
		}
	}

	return 0;
}