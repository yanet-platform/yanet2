#include "services.h"

#include "api/balancer.h"
#include "api/vs.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/swap.h"
#include "handler.h"
#include "lib/controlplane/diag/diag.h"
#include "rules.h"
#include "state/state.h"
#include "state/vs.h"
#include "vs.h"

#include <assert.h>
#include <netinet/in.h>
#include <stdlib.h>
#include <string.h>

struct vs *
find_vs_in_packet_handler_vs(
	struct packet_handler_vs *packet_handler_vs, struct vs *vs
) {
	if (packet_handler_vs == NULL) {
		return NULL;
	}

	uint32_t *vs_index = ADDR_OF(&packet_handler_vs->vs_index);
	size_t vs_index_count = packet_handler_vs->vs_index_size;

	if (vs->registry_idx >= vs_index_count) {
		return NULL;
	}

	struct vs *services = ADDR_OF(&packet_handler_vs->vs);

	uint32_t vs_idx = vs_index[vs->registry_idx];
	if (vs_idx == INDEX_INVALID) {
		return NULL;
	}

	return vs_idx == INDEX_INVALID ? NULL : &services[vs_idx];
}

struct packet_handler_vs *
get_packet_handler_vs(struct packet_handler *handler, int proto) {
	return handler == NULL ? NULL
			       : (proto == IPPROTO_IP ? &handler->vs_ipv4
						      : &handler->vs_ipv6);
}

int
can_reuse_filter(int current_vs_count, int prev_vs_count, int match_count) {
	// all virtual services are unique, it is validated on packet handler
	// update
	return current_vs_count == prev_vs_count &&
	       current_vs_count == match_count;
}

static int
validate_vs_config(struct named_vs_config *config) {
	int proto = config->identifier.ip_proto;
	if (proto != IPPROTO_IP && proto != IPPROTO_IPV6) {
		NEW_ERROR(
			"network protocol is invalid: got %d, but only IPv4 "
			"(%d) and IPv6 (%d) are supported",
			proto,
			IPPROTO_IP,
			IPPROTO_IPV6
		);
		return -1;
	}

	if (config->identifier.transport_proto != IPPROTO_TCP &&
	    config->identifier.transport_proto != IPPROTO_UDP) {
		NEW_ERROR(
			"transport protocol is invalid: got %d, but only TCP "
			"(%d) and UDP (%d) are supported",
			config->identifier.transport_proto,
			IPPROTO_TCP,
			IPPROTO_UDP
		);
		return -1;
	}

	// TODO: better validation

	return 0;
}

static void
swap_vs_configs(
	size_t *initial_vs_idx,
	struct named_vs_config *configs,
	size_t left_idx,
	size_t right_idx
) {
	SWAP(configs + left_idx, configs + right_idx);
	SWAP(initial_vs_idx + left_idx, initial_vs_idx + right_idx);
}

int
validate_and_reorder_vs_configs(
	size_t *initial_vs_idx,
	size_t count,
	struct named_vs_config *configs,
	size_t *ipv4_count,
	size_t *ipv6_count
) {
	// move ipv4 services first, and ipv6 then.

	ssize_t last_ipv6 = -1;
	for (size_t idx = 0; idx < count; ++idx) {
		struct named_vs_config *current = &configs[idx];

		// validate service
		if (validate_vs_config(current) != 0) {
			PUSH_ERROR("at index %zu", idx);
			return -1;
		}

		int proto = current->identifier.ip_proto;

		if (proto == IPPROTO_IPV6) {
			// IPv6 service
			*ipv6_count += 1;
			if (last_ipv6 == -1) {
				last_ipv6 = idx;
			}
			continue;
		}

		// IPv4 service
		*ipv4_count += 1;
		if (last_ipv6 == -1) {
			continue;
		}

		swap_vs_configs(initial_vs_idx, configs, idx, last_ipv6);

		last_ipv6 += 1;
	}

	return 0;
}

int
register_virtual_services(
	size_t vs_count,
	const size_t *initial_vs_idx,
	struct named_vs_config *configs,
	struct balancer_state *state,
	struct packet_handler *prev_handler,
	size_t *match
) {
	uint32_t *prev_vs_index =
		prev_handler != NULL ? ADDR_OF(&prev_handler->vs_index) : NULL;
	size_t prev_vs_index_count =
		prev_handler != NULL ? prev_handler->vs_index_size : 0;

	for (size_t vs_idx = 0; vs_idx < vs_count; ++vs_idx) {
		struct named_vs_config *vs_config = &configs[vs_idx];
		struct vs_state *vs_state = balancer_state_find_or_insert_vs(
			state, &vs_config->identifier
		);
		if (vs_state == NULL) {
			PUSH_ERROR("at index %zu", initial_vs_idx[vs_idx]);
			return -1;
		}

		size_t vs_registry_idx = vs_state->registry_idx;
		if (vs_registry_idx < prev_vs_index_count &&
		    prev_vs_index[vs_registry_idx] != INDEX_INVALID) {
			*match += 1;
		}
	}

	return 0;
}

int
register_and_prepare_vs(
	struct packet_handler *handler,
	struct packet_handler *prev_handler,
	int proto,
	size_t vs_count,
	struct named_vs_config *vs_configs,
	size_t *initial_vs_idx,
	struct vs *virtual_services,
	struct balancer_state *state,
	struct balancer_update_info *update_info,
	int *reuse_filter
) {
	// only IPv4 and IPv6 are supported
	assert(proto == IPPROTO_IP || proto == IPPROTO_IPV6);

	// first, register virtual services in balancer state registry
	// and get number of services matching with
	// services from the previous config
	size_t match = 0;
	if (register_virtual_services(
		    vs_count,
		    initial_vs_idx,
		    vs_configs,
		    state,
		    prev_handler,
		    &match
	    ) != 0) {
		PUSH_ERROR("registration failed");
		return -1;
	}

	// init some fields of the packet_handler_vs for this protocol:
	// - vs_count
	// - vs
	struct packet_handler_vs *packet_handler_vs =
		get_packet_handler_vs(handler, proto);
	packet_handler_vs->vs_count = vs_count;
	SET_OFFSET_OF(&packet_handler_vs->vs, virtual_services);

	// prev handler is optional
	struct packet_handler_vs *prev_packet_handler_vs =
		get_packet_handler_vs(prev_handler, proto);

	// check if VS filter for this protocol can be reused
	*reuse_filter = prev_packet_handler_vs == NULL
				? 0
				: can_reuse_filter(
					  vs_count,
					  prev_packet_handler_vs->vs_count,
					  match
				  );
	if (update_info != NULL) {
		*(proto == IPPROTO_IPV6 ? &update_info->vs_ipv6_matcher_reused
					: &update_info->vs_ipv4_matcher_reused
		) = *reuse_filter;
	}

	// to reuse filter for network protocol, the VS indices in
	// packet_handler_vs MUST match with the corresponding indices in the
	// previous config. this is because the VS matching mechanism
	if (*reuse_filter) {
		// permute VS configs according to indices in the previous
		// config

		uint32_t *prev_vs_index =
			ADDR_OF(&prev_packet_handler_vs->vs_index);
		size_t prev_vs_index_size =
			prev_packet_handler_vs->vs_index_size;
		(void)prev_vs_index_size;

		for (size_t vs_idx = 0; vs_idx < vs_count; ++vs_idx) {
			struct vs_state *vs_state = balancer_state_find_vs(
				state, &vs_configs[vs_idx].identifier
			);
			assert(vs_state != NULL);

			size_t vs_registry_idx = vs_state->registry_idx;
			assert(vs_registry_idx < prev_vs_index_size);

			uint32_t position = prev_vs_index[vs_registry_idx];

			swap_vs_configs(
				initial_vs_idx, vs_configs, vs_idx, position
			);
		}
	}

	return 0;
}

int
init_packet_handler_vs(
	struct packet_handler *handler,
	int proto,
	struct balancer_state *state,
	struct memory_context *mctx,
	struct named_vs_config *vs_configs,
	struct counter_registry *registry,
	struct packet_handler *prev_handler,
	struct real *reals,
	size_t *reals_counter,
	struct balancer_update_info *update_info,
	size_t *initial_vs_idx
) {
	// only IPv4 and IPv6 are supported
	assert(proto == IPPROTO_IP || proto == IPPROTO_IPV6);

	// prev packet handler is optional
	struct packet_handler_vs *prev_packet_handler_vs =
		get_packet_handler_vs(prev_handler, proto);

	// find packet handler vs for this protocol
	struct packet_handler_vs *packet_handler_vs =
		get_packet_handler_vs(handler, proto);
	size_t vs_count = packet_handler_vs->vs_count;
	struct vs *virtual_services = ADDR_OF(&packet_handler_vs->vs);

	// setup index
	size_t index_size = balancer_state_vs_count(state);
	uint32_t *vs_index = memory_balloc(mctx, index_size * sizeof(uint32_t));
	if (vs_index == NULL) {
		// TODO: free allocated memory
		NEW_ERROR("no memory");
		return -1;
	}
	memset(vs_index, INDEX_INVALID, index_size * sizeof(uint32_t));
	packet_handler_vs->vs_index_size = index_size;
	SET_OFFSET_OF(&packet_handler_vs->vs_index, vs_index);

	// initialize virtual services
	for (size_t vs_idx = 0; vs_idx < vs_count; ++vs_idx) {
		struct vs *current_vs = virtual_services + vs_idx;
		struct named_vs_config *current_vs_config = vs_configs + vs_idx;

		// set identifier
		current_vs->identifier = current_vs_config->identifier;
		struct vs_state *current_vs_state =
			balancer_state_find_vs(state, &current_vs->identifier);
		assert(current_vs_state != NULL);

		// set registry idx
		current_vs->registry_idx = current_vs_state->registry_idx;

		// set index value
		vs_index[current_vs->registry_idx] = vs_idx;

		// try to find this virtual service in previous config, can be
		// null
		struct vs *prev_vs = find_vs_in_packet_handler_vs(
			prev_packet_handler_vs, current_vs
		);

		size_t first_real_idx = *reals_counter;
		if (vs_with_identifier_and_registry_idx_init(
			    current_vs,
			    prev_vs,
			    first_real_idx,
			    reals + first_real_idx,
			    state,
			    current_vs_config,
			    registry,
			    mctx,
			    update_info
		    ) != 0) {
			PUSH_ERROR(
				"service at index %zu", initial_vs_idx[vs_idx]
			);
			// TODO: free allocated memory
			return -1;
		}

		// increase reals counter
		*reals_counter += current_vs->reals_count;
	}

	return 0;
}

int
init_vs_filter(
	struct packet_handler_vs *packet_handler_vs,
	struct packet_handler_vs *prev_packet_handler_vs,
	struct named_vs_config *vs_configs,
	int reuse_filter,
	struct memory_context *mctx,
	size_t *initial_vs_idx,
	int proto
) {
	packet_handler_vs->filter_reused = 0;
	if (reuse_filter) {
		// just reuse filter from the current packet handler
		EQUATE_OFFSET(
			&packet_handler_vs->filter,
			&prev_packet_handler_vs->filter
		);
		prev_packet_handler_vs->filter_reused = 1;
	} else {
		if (build_filter(
			    packet_handler_vs,
			    initial_vs_idx,
			    vs_configs,
			    mctx,
			    proto
		    ) != 0) {
			PUSH_ERROR("build failed");
			return -1;
		}
	}
	return 0;
}

int
init_announce(
	struct packet_handler_vs *handler,
	struct memory_context *mctx,
	struct named_vs_config *vs_configs,
	int proto
) {
	struct lpm *lpm = &handler->announce;
	if (lpm_init(lpm, mctx) != 0) {
		NEW_ERROR("no memory");
		return -1;
	}

	for (size_t vs_idx = 0; vs_idx < handler->vs_count; ++vs_idx) {
		struct named_vs_config *vs_config = vs_configs + vs_idx;
		int res;
		if (proto == IPPROTO_IP) {
			res = lpm4_insert(
				lpm,
				vs_config->identifier.addr.v4.bytes,
				vs_config->identifier.addr.v4.bytes,
				1
			);
		} else {
			res = lpm8_insert(
				lpm,
				vs_config->identifier.addr.v6.bytes,
				vs_config->identifier.addr.v6.bytes,
				1
			);
		}
		if (res != 0) {
			lpm_free(lpm);
			NEW_ERROR("no memory");
			return -1;
		}
	}

	return 0;
}

int
setup_vs_index(
	struct packet_handler *handler,
	struct vs *virtual_services,
	size_t *initial_vs_idx,
	struct balancer_state *state,
	struct memory_context *mctx
) {
	size_t vs_index_size = balancer_state_vs_count(state);
	uint32_t *vs_index =
		memory_balloc(mctx, sizeof(uint32_t) * vs_index_size);
	if (vs_index == NULL && handler->vs_count > 0) {
		PUSH_ERROR("no memory");
		return -1;
	}
	memset(vs_index, INDEX_INVALID, sizeof(uint32_t) * vs_index_size);

	for (size_t vs_idx = 0; vs_idx < handler->vs_count; vs_idx++) {
		struct vs *vs = virtual_services + vs_idx;
		if (vs_index[vs->registry_idx] != INDEX_INVALID) {
			NEW_ERROR(
				"service at index %zu matches with service at "
				"index %zu",
				initial_vs_idx[vs_idx],
				initial_vs_idx[vs_index[vs->registry_idx]]
			);
			return -1;
		}
		vs_index[vs->registry_idx] = vs_idx;
	}

	SET_OFFSET_OF(&handler->vs_index, vs_index);
	handler->vs_index_size = vs_index_size;

	return 0;
}