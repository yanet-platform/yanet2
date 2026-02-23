#include "init.h"

#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "handler.h"
#include "lib/controlplane/diag/diag.h"
#include "real.h"
#include "state/state.h"

#include <string.h>

extern uint64_t
register_common_counter(struct counter_registry *registry);

extern uint64_t
register_icmp_v4_counter(struct counter_registry *registry);

extern uint64_t
register_icmp_v6_counter(struct counter_registry *registry);

extern uint64_t
register_l4_counter(struct counter_registry *registry);

int
init_counters(
	struct packet_handler *handler, struct counter_registry *registry
) {
	if ((handler->counter.common = register_common_counter(registry)) ==
	    (uint64_t)-1) {
		PUSH_ERROR("failed to register common counter");
		return -1;
	}
	if ((handler->counter.icmp_v4 = register_icmp_v4_counter(registry)) ==
	    (uint64_t)-1) {
		PUSH_ERROR("failed to register ICMPv4 counter");
		return -1;
	}
	if ((handler->counter.icmp_v6 = register_icmp_v6_counter(registry)) ==
	    (uint64_t)-1) {
		PUSH_ERROR("failed to register ICMPv6 counter");
		return -1;
	}
	if ((handler->counter.l4 = register_l4_counter(registry)) ==
	    (uint64_t)-1) {
		PUSH_ERROR("failed to register L4 counter");
		return -1;
	}

	return 0;
}

int
init_sources(
	struct packet_handler *handler,
	struct memory_context *mctx,
	struct packet_handler_config *config
) {
	(void)mctx;
	memcpy(&handler->source_ipv4,
	       &config->source_v4,
	       sizeof(struct net4_addr));
	memcpy(&handler->source_ipv6,
	       &config->source_v6,
	       sizeof(struct net6_addr));
	return 0;
}

int
init_decaps(
	struct packet_handler *handler,
	struct memory_context *mctx,
	struct packet_handler_config *config
) {
	// init ipv4 decap addresses
	if (lpm_init(&handler->decap_ipv4, mctx) != 0) {
		NEW_ERROR(
			"failed to allocate container for decap IPv4 addresses"
		);
		return -1;
	}
	for (size_t i = 0; i < config->decap_v4_count; i++) {
		struct net4_addr *addr = &config->decap_v4[i];
		if (lpm4_insert(
			    &handler->decap_ipv4, addr->bytes, addr->bytes, 1
		    ) != 0) {
			lpm_free(&handler->decap_ipv4);
			NEW_ERROR(
				"failed to insert decap IPv4 address at index "
				"%zu",
				i
			);
			return -1;
		}
	}

	// init ipv6 decap addresses
	if (lpm_init(&handler->decap_ipv6, mctx) != 0) {
		NEW_ERROR(
			"failed to allocate container for decap IPv6 addresses"
		);
		return -1;
	}
	for (size_t i = 0; i < config->decap_v6_count; i++) {
		struct net6_addr *addr = &config->decap_v6[i];
		if (lpm8_insert(
			    &handler->decap_ipv6, addr->bytes, addr->bytes, 1
		    ) != 0) {
			lpm_free(&handler->decap_ipv4);
			lpm_free(&handler->decap_ipv6);
			NEW_ERROR(
				"failed to insert decap IPv6 address at index "
				"%zu",
				i
			);
			return -1;
		}
	}

	return 0;
}

int
setup_reals_index(struct packet_handler *handler, struct memory_context *mctx) {
	struct balancer_state *state = ADDR_OF(&handler->state);
	size_t registry_reals_count = balancer_state_reals_count(state);
	uint32_t *reals_index =
		memory_balloc(mctx, sizeof(uint32_t) * registry_reals_count);
	if (reals_index == NULL && registry_reals_count > 0) {
		NEW_ERROR("failed to allocate memory for reals index");
		return -1;
	}

	memset(reals_index,
	       INDEX_INVALID,
	       sizeof(uint32_t) * registry_reals_count);
	SET_OFFSET_OF(&handler->reals_index, reals_index);
	handler->reals_index_size = registry_reals_count;

	return 0;
}

int
init_reals(
	struct packet_handler *handler,
	struct balancer_state *state,
	struct memory_context *mctx,
	struct packet_handler_config *config,
	struct counter_registry *registry,
	size_t *initial_vs_idx
) {
	size_t real_count = 0;
	for (size_t i = 0; i < config->vs_count; ++i) {
		real_count += config->vs[i].config.real_count;
	}
	handler->reals_count = real_count;
	struct real *reals =
		memory_balloc(mctx, sizeof(struct real) * real_count);
	if (reals == NULL && real_count > 0) {
		NEW_ERROR("no memory");
		return -1;
	}
	memset(reals, 0, sizeof(struct real) * real_count);
	SET_OFFSET_OF(&handler->reals, reals);

	size_t real_ph_idx = 0;
	for (size_t i = 0; i < config->vs_count; ++i) {
		struct named_vs_config *vs_config = &config->vs[i];
		for (size_t j = 0; j < vs_config->config.real_count; ++j) {
			struct named_real_config *real_config =
				&vs_config->config.reals[j];
			struct real *real = &reals[real_ph_idx];
			if (real_init(
				    real,
				    state,
				    &vs_config->identifier,
				    real_config,
				    registry
			    ) != 0) {
				// failed to init real
				PUSH_ERROR(
					"service at index %zu: real at index "
					"%zu",
					initial_vs_idx[i],
					j
				);
				memory_bfree(
					mctx,
					reals,
					sizeof(struct real) * real_count
				);
				return -1;
			}
			++real_ph_idx;
		}
	}

	// setup reals index
	if (setup_reals_index(handler, mctx) != 0) {
		PUSH_ERROR("failed to setup reals index");
		memory_bfree(mctx, reals, sizeof(struct real) * real_count);
		return -1;
	}

	uint32_t *reals_index = ADDR_OF(&handler->reals_index);

	real_ph_idx = 0;
	for (size_t i = 0; i < config->vs_count; ++i) {
		struct named_vs_config *vs_config = &config->vs[i];
		for (size_t j = 0; j < vs_config->config.real_count; ++j) {
			struct real *real = &reals[real_ph_idx];
			reals_index[real->registry_idx] = real_ph_idx;
			++real_ph_idx;
		}
	}

	return 0;
}