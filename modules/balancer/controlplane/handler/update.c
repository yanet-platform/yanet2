#include "common/memory_address.h"
#include "lib/controlplane/diag/diag.h"

#include "real.h"
#include "registry.h"
#include "vs.h"

#include "handler.h"

#define VS_COUNT 1024 * 1024
static uint64_t updated_vs[VS_COUNT / 64] = {0};

static inline void
mark_vs_updated(size_t ph_idx) {
	if (ph_idx < VS_COUNT) {
		updated_vs[ph_idx / 64] |= 1ULL << (ph_idx % 64);
	}
}

static inline int
is_vs_updated(size_t ph_idx) {
	if (ph_idx < VS_COUNT) {
		return updated_vs[ph_idx / 64] & (1ULL << (ph_idx % 64));
	} else {
		return 1;
	}
}

static inline void
unmark_vs_updated(size_t ph_idx) {
	if (ph_idx < VS_COUNT) {
		updated_vs[ph_idx / 64] &= ~(1ULL << (ph_idx % 64));
	}
}

static int
validate_update(struct packet_handler *handler, struct real_update *update) {
	// validate real - check if it exists in handler's registry
	ssize_t real_stable_idx = reals_registry_lookup(
		&handler->reals_registry, &update->identifier
	);
	if (real_stable_idx < 0) {
		NEW_ERROR("real not found in registry");
		return -1;
	}

	// check if real is present in current handler config
	size_t real_config_idx;
	if (map_find(
		    &handler->reals_index, real_stable_idx, &real_config_idx
	    ) != 0) {
		NEW_ERROR("real is not present in current handler configuration"
		);
		return -1;
	}

	// validate virtual service
	ssize_t vs_stable_idx = vs_registry_lookup(
		&handler->vs_registry, &update->identifier.vs_identifier
	);
	if (vs_stable_idx < 0) {
		NEW_ERROR("virtual service not found in registry");
		return -1;
	}

	// check if VS is present in current handler config
	size_t vs_config_idx;
	if (map_find(&handler->vs_index, vs_stable_idx, &vs_config_idx) != 0) {
		NEW_ERROR("virtual service is not present in current handler "
			  "configuration");
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
		// update changes nothing, and it is ok
		return 0;
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
	// Find real's stable index
	ssize_t real_stable_idx = reals_registry_lookup(
		&handler->reals_registry, &update->identifier
	);
	assert(real_stable_idx >= 0);

	// Find real's config index
	size_t real_config_idx;
	int res = map_find(
		&handler->reals_index, real_stable_idx, &real_config_idx
	);
	assert(res == 0);

	// Get the real structure
	struct real *reals = ADDR_OF(&handler->reals);
	struct real *real = &reals[real_config_idx];

	// Find VS stable index
	ssize_t vs_stable_idx = vs_registry_lookup(
		&handler->vs_registry, &update->identifier.vs_identifier
	);
	assert(vs_stable_idx >= 0);

	// Find VS config index
	size_t vs_config_idx;
	res = map_find(&handler->vs_index, vs_stable_idx, &vs_config_idx);
	assert(res == 0);

	// Update real fields
	int updated = 0;
	if (update->enabled != DONT_UPDATE_REAL_ENABLED &&
	    real->enabled != update->enabled) {
		real->enabled = update->enabled;
		updated = 1;
	}

	if (update->weight != DONT_UPDATE_REAL_WEIGHT &&
	    real->weight != update->weight) {
		real->weight = update->weight;
		updated = 1;
	}

	if (updated) {
		mark_vs_updated(vs_config_idx);
	}
}

static int
update_vs(struct packet_handler *handler, struct real_update *update) {
	// Find VS stable index
	ssize_t vs_stable_idx = vs_registry_lookup(
		&handler->vs_registry, &update->identifier.vs_identifier
	);
	assert(vs_stable_idx >= 0);

	// Find VS config index
	size_t vs_config_idx;
	int res = map_find(&handler->vs_index, vs_stable_idx, &vs_config_idx);
	assert(res == 0);

	if (!is_vs_updated(vs_config_idx)) {
		return 0;
	}

	struct vs *vss = ADDR_OF(&handler->vs);
	struct vs *vs = &vss[vs_config_idx];
	if (vs_update_reals(vs) != 0) {
		PUSH_ERROR("failed to update reals");
		return -1;
	}

	unmark_vs_updated(vs_config_idx);

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

	// update virtual services that were marked as updated
	for (size_t i = 0; i < count; ++i) {
		struct real_update *update = &updates[i];
		if (update_vs(handler, update) != 0) {
			PUSH_ERROR(
				"failed to update virtual service for update "
				"at "
				"index %lu",
				i
			);
			return -1;
		}
	}

	return 0;
}