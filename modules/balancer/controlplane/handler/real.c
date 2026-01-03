#include "real.h"

#include "api/counter.h"
#include "api/real.h"

#include "common/network.h"
#include "lib/controlplane/diag/diag.h"
#include "lib/counters/counters.h"

#include "state/real.h"
#include "state/state.h"
#include <string.h>

uint16_t
real_weight(struct real *real) {
	return real->enabled ? real->weight : 0;
}

int
real_init(
	struct real *real,
	struct balancer_state *balancer_state,
	struct named_real_config *config,
	struct counter_registry *registry
) {
	struct real_state *real_state = balancer_state_find_or_insert_real(
		balancer_state, &config->identifier
	);
	if (!real_state) {
		NEW_ERROR("failed to find or insert real into registry");
		return -1;
	}

	// init identifier
	memcpy(&real->identifier, &real_state->identifier, sizeof(struct real_identifier));

	// registry idx
	real->registry_idx = real_state->registry_idx;

	// weight
	real->weight = config->config.weight;

	// enabled
	real->enabled = real_state->enabled;

	// source net
	memcpy(&real->src, &config->config.src, sizeof(struct net));
	uint8_t *src_addr = real->src.v6.addr;
	const uint8_t *src_mask = real->src.v6.mask;
	for (size_t i = 0; i < NET6_LEN; i++) {
		src_addr[i] &= src_mask[i];
	}

	// register counter
	char name[60];
	sprintf(name, "rl_%zu", real_state->registry_idx);
	real->counter_id = counter_registry_register(
		registry, name, sizeof(struct real_stats) / sizeof(uint64_t)
	);
	if (real->counter_id == (size_t)-1) {
		NEW_ERROR("failed to register counter");
		return -1;
	}

	return 0;
}

ssize_t
counter_to_real_registry_idx(struct counter_handle *counter) {
	if (strncmp(counter->name, "rl_", 3) == 0) {
		return atoi(counter->name + 3);
	} else {
		return -1;
	}
}