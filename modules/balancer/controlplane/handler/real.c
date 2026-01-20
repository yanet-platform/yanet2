#include "real.h"

#include "api/counter.h"
#include "api/real.h"

#include "api/vs.h"
#include "common/network.h"
#include "lib/controlplane/diag/diag.h"
#include "lib/counters/counters.h"

#include "state/real.h"
#include "state/state.h"
#include <assert.h>
#include <netinet/in.h>
#include <string.h>

int
real_init(
	struct real *real,
	struct balancer_state *balancer_state,
	struct vs_identifier *vs,
	struct named_real_config *named_config,
	struct counter_registry *registry
) {
	struct real_identifier identifier;
	memset(&identifier, 0, sizeof(identifier));
	identifier.vs_identifier = *vs;
	identifier.relative = named_config->real;
	struct real_state *real_state =
		balancer_state_find_or_insert_real(balancer_state, &identifier);
	if (!real_state) {
		NEW_ERROR("failed to find or insert real into registry");
		return -1;
	}
	real_state->weight = named_config->config.weight;

	// register counter
	char name[60];
	sprintf(name, "rl_%zu", real_state->registry_idx);
	uint64_t counter_id = counter_registry_register(
		registry, name, sizeof(struct real_stats) / sizeof(uint64_t)
	);
	if (counter_id == (size_t)-1) {
		NEW_ERROR("failed to register counter");
		return -1;
	}

	// source net
	struct net src = named_config->config.src;

	// Mask the source address based on IP protocol version
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

	struct real r = {
		.identifier = identifier.relative,
		.registry_idx = real_state->registry_idx,
		.counter_id = counter_id,
		.src = src
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