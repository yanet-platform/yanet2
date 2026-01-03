#include "api/counter.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/network.h"

#include "lib/controlplane/diag/diag.h"

#include "selector.h"
#include "vs.h"

#include "state/state.h"
#include "state/vs.h"

#include <string.h>
#include <sys/types.h>

static int
setup_reals(
	struct vs *vs, struct vs_config *config, struct real *reals
) {
	vs->real_count = config->real_count;
	SET_OFFSET_OF(&vs->reals, reals);
	return 0;
}

static int
setup_selector(
	struct vs *vs,
	struct memory_context *mctx,
	struct vs_config *config
) {
	struct real *reals = ADDR_OF(&vs->reals);
	if (selector_init(&vs->selector, mctx, config->scheduler) != 0) {
		PUSH_ERROR("failed to setup selector");
		return -1;
	}
	if (selector_update(&vs->selector, vs->real_count, reals) != 0) {
		selector_free(&vs->selector);
		PUSH_ERROR("failed to setup selector reals");
		return -1;
	}
	return 0;
}

static int
setup_src_filter(
	struct vs *vs,
	struct memory_context *mctx,
	struct vs_config *config
) {
	if (lpm_init(&vs->src_filter, mctx) != 0) {
		NEW_ERROR("failed to initialize container for source addresses"
		);
		return -1;
	}

	const uint8_t key_size =
		vs->identifier.ip_proto == IPPROTO_IP ? NET4_LEN : NET6_LEN;
	for (size_t i = 0; i < config->allowed_src_count; ++i) {
		struct net_addr_range *range = &config->allowed_src[i];
		const uint8_t *from = (const uint8_t *)&range->from;
		const uint8_t *to = (const uint8_t *)&range->to;
		if (lpm_insert(&vs->src_filter, key_size, from, to, 1) != 0) {
			NEW_ERROR(
				"failed to insert allowed sources range at "
				"index %zu",
				i
			);
			lpm_free(&vs->src_filter);
			return -1;
		}
	}

	return 0;
}

static int
register_counter(struct vs *vs, struct counter_registry *registry) {
	char name[60];
	sprintf(name, "vs_%zu", vs->registry_idx);
	vs->counter_id = counter_registry_register(
		registry, name, sizeof(struct vs_stats) / sizeof(uint64_t)
	);
	if (vs->counter_id == (size_t)-1) {
		PUSH_ERROR("failed to register counter in the counter registry"
		);
		return -1;
	}
	return 0;
}

static int
setup_peers(
	struct vs *vs,
	struct memory_context *mctx,
	struct vs_config *config
) {
	vs->peers_v4_count = config->peers_v4_count;
	vs->peers_v6_count = config->peers_v6_count;
	vs->peers_v4 = memory_balloc(
		mctx, sizeof(struct net4_addr) * vs->peers_v4_count
	);
	if (vs->peers_v4 == NULL) {
		NEW_ERROR("failed to allocate memory for IPv4 peers");
		return -1;
	}
	vs->peers_v6 = memory_balloc(
		mctx, sizeof(struct net6_addr) * vs->peers_v6_count
	);
	if (vs->peers_v6 == NULL) {
		NEW_ERROR("failed to allocate memory for IPv6 peers");
		memory_bfree(
			mctx,
			vs->peers_v4,
			sizeof(struct net4_addr) * vs->peers_v4_count
		);
		return -1;
	}
	return 0;
}

static int
setup_state(
	struct vs *vs,
	struct balancer_state *balancer_state,
	struct named_vs_config *config
) {
	struct vs_state *vs_state = balancer_state_find_or_insert_vs(
		balancer_state, &config->identifier
	);
	if (!vs_state) {
		NEW_ERROR(
			"failed to find or insert virtual service into registry"
		);
		return -1;
	}
	vs->registry_idx = vs_state->registry_idx;
	vs->identifier = vs_state->identifier;
	return 0;
}

static int
setup_flags(struct vs *vs, struct vs_config *config) {
	vs->flags = config->flags;
	return 0;
}

int
vs_view_init(
	struct vs *vs,
	struct real *reals,
	struct balancer_state *balancer_state,
	struct named_vs_config *config,
	struct counter_registry *registry,
	struct memory_context *mctx
) {
	if (setup_state(vs, balancer_state, config) != 0) {
		PUSH_ERROR("failed to setup state");
		return -1;
	}

	if (setup_flags(vs, &config->config) != 0) {
		PUSH_ERROR("failed to setup flags");
		return -1;
	}

	if (setup_peers(vs, mctx, &config->config) != 0) {
		PUSH_ERROR("failed to setup peers");
		return -1;
	}

	if (setup_src_filter(vs, mctx, &config->config) != 0) {
		PUSH_ERROR("failed to setup filter for source addresses");
		goto free_peers;
	}

	if (setup_reals(vs, &config->config, reals) != 0) {
		PUSH_ERROR("failed to setup reals");
		goto free_src_filter;
	}

	if (setup_selector(vs, mctx, &config->config) != 0) {
		PUSH_ERROR("failed to setup selector");
		goto free_src_filter;
	}

	if (register_counter(vs, registry) != 0) {
		PUSH_ERROR("failed to register counter");
		goto free_selector;
	}

	return 0;

free_selector:
	selector_free(&vs->selector);

free_src_filter:
	lpm_free(&vs->src_filter);

free_peers:
	memory_bfree(
		mctx,
		ADDR_OF(&vs->peers_v4),
		sizeof(struct net4_addr) * vs->peers_v4_count
	);
	memory_bfree(
		mctx,
		ADDR_OF(&vs->peers_v6),
		sizeof(struct net6_addr) * vs->peers_v6_count
	);

	return -1;
}

void
vs_view_free(struct vs *vs, struct memory_context *mctx) {
	memory_bfree(
		mctx,
		ADDR_OF(&vs->peers_v4),
		sizeof(struct net4_addr) * vs->peers_v4_count
	);
	memory_bfree(
		mctx,
		ADDR_OF(&vs->peers_v6),
		sizeof(struct net6_addr) * vs->peers_v6_count
	);
	lpm_free(&vs->src_filter);
	selector_free(&vs->selector);
}

int
vs_view_update_reals(struct vs *vs) {
	if (selector_update(
		    &vs->selector, vs->real_count, ADDR_OF(&vs->reals)
	    ) != 0) {
		PUSH_ERROR("failed to update real selector");
		return -1;
	}
	return 0;
}

ssize_t
counter_to_vs_registry_idx(struct counter_handle *counter) {
	if (strncmp(counter->name, "vs_", 3) == 0) { // vs counter
		return atoi(counter->name + 3);
	} else {
		return -1;
	}
}