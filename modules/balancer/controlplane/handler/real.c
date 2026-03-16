#include "real.h"

#include "api/counter.h"
#include "api/real.h"

#include "api/vs.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "handler.h"
#include "lib/controlplane/diag/diag.h"
#include "lib/counters/counters.h"
#include "registry.h"

#include <assert.h>
#include <netinet/in.h>
#include <string.h>

int
real_init(
	struct real *real,
	struct packet_handler *handler,
	struct packet_handler *prev_handler,
	struct vs_identifier *vs,
	struct named_real_config *named_config,
	struct counter_registry *registry
) {
	// Build full real identifier
	struct real_identifier identifier;
	memset(&identifier, 0, sizeof(identifier));
	identifier.vs_identifier = *vs;
	identifier.relative = named_config->real;

	// Look up stable index in handler's registry (must already be
	// initialized)
	ssize_t stable_idx =
		reals_registry_lookup(&handler->reals_registry, &identifier);
	if (stable_idx < 0) {
		NEW_ERROR("real not found in registry");
		return -1;
	}

	// Register counter using stable index
	char name[60];
	sprintf(name, "rl_%zu", (size_t)stable_idx);
	uint64_t counter_id = counter_registry_register(
		registry, name, sizeof(struct real_stats) / sizeof(uint64_t)
	);
	if (counter_id == (size_t)-1) {
		NEW_ERROR("failed to register counter");
		return -1;
	}

	// Determine enabled and weight - preserve from previous config if
	// exists
	bool enabled = false; // default
	uint16_t weight = named_config->config.weight;

	if (prev_handler) {
		// Check if this real existed in previous handler
		size_t prev_config_idx;
		if (map_find(
			    &prev_handler->reals_index,
			    stable_idx,
			    &prev_config_idx
		    ) == 0) {
			// Real existed - preserve its mutable state
			struct real *prev_reals = ADDR_OF(&prev_handler->reals);
			enabled = prev_reals[prev_config_idx].enabled;
			weight = prev_reals[prev_config_idx].weight;
		}
	}

	// Mask the source address based on IP protocol version
	struct net src = named_config->config.src;
	if (named_config->real.ip_proto == IPPROTO_IP) { // IPv4
		uint8_t *src_addr = src.v4.addr;
		const uint8_t *src_mask = src.v4.mask;
		for (size_t i = 0; i < NET4_LEN; i++) {
			src_addr[i] &= src_mask[i];
		}
	} else { // IPv6
		uint8_t *src_addr = src.v6.addr;
		const uint8_t *src_mask = src.v6.mask;
		for (size_t i = 0; i < NET6_LEN; i++) {
			src_addr[i] &= src_mask[i];
		}
	}

	// Initialize the real structure
	struct real r = {
		.identifier = identifier, // Full identifier (includes VS)
		.stable_idx = (size_t)stable_idx,
		.counter_id = counter_id,
		.src = src,
		.enabled = enabled,
		.weight = weight
	};
	memcpy(real, &r, sizeof(struct real));

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
