#include "agent.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "modules/balancer/controlplane/api/balancer.h"
#include <assert.h>
#include <stdlib.h>
#include <string.h>

/**
 * Clone a named_real_config array from normal pointers to relative pointers.
 * 
 * @param dst Destination pointer (will be set to allocated memory with relative pointers)
 * @param src Source array with normal pointers
 * @param count Number of elements in the array
 * @param mctx Memory context for allocation
 * @return 0 on success, -1 on error
 */
static int
clone_reals_to_relative(
	struct named_real_config **dst,
	struct named_real_config *src,
	size_t count,
	struct memory_context *mctx
) {
	if (count == 0) {
		SET_OFFSET_OF(dst, NULL);
		return 0;
	}

	struct named_real_config *reals = 
		memory_balloc(mctx, sizeof(struct named_real_config) * count);
	if (reals == NULL) {
		return -1;
	}

	// Copy all real configs (they contain no pointers, just embedded structs)
	memcpy(reals, src, sizeof(struct named_real_config) * count);

	SET_OFFSET_OF(dst, reals);
	return 0;
}

/**
 * Clone a net_addr_range array from normal pointers to relative pointers.
 */
static int
clone_addr_ranges_to_relative(
	struct net_addr_range **dst,
	struct net_addr_range *src,
	size_t count,
	struct memory_context *mctx
) {
	if (count == 0) {
		SET_OFFSET_OF(dst, NULL);
		return 0;
	}

	struct net_addr_range *ranges = 
		memory_balloc(mctx, sizeof(struct net_addr_range) * count);
	if (ranges == NULL) {
		return -1;
	}

	memcpy(ranges, src, sizeof(struct net_addr_range) * count);
	SET_OFFSET_OF(dst, ranges);
	return 0;
}

/**
 * Clone a net4_addr array from normal pointers to relative pointers.
 */
static int
clone_net4_addrs_to_relative(
	struct net4_addr **dst,
	struct net4_addr *src,
	size_t count,
	struct memory_context *mctx
) {
	if (count == 0) {
		SET_OFFSET_OF(dst, NULL);
		return 0;
	}

	struct net4_addr *addrs = 
		memory_balloc(mctx, sizeof(struct net4_addr) * count);
	if (addrs == NULL) {
		return -1;
	}

	memcpy(addrs, src, sizeof(struct net4_addr) * count);
	SET_OFFSET_OF(dst, addrs);
	return 0;
}

/**
 * Clone a net6_addr array from normal pointers to relative pointers.
 */
static int
clone_net6_addrs_to_relative(
	struct net6_addr **dst,
	struct net6_addr *src,
	size_t count,
	struct memory_context *mctx
) {
	if (count == 0) {
		SET_OFFSET_OF(dst, NULL);
		return 0;
	}

	struct net6_addr *addrs = 
		memory_balloc(mctx, sizeof(struct net6_addr) * count);
	if (addrs == NULL) {
		return -1;
	}

	memcpy(addrs, src, sizeof(struct net6_addr) * count);
	SET_OFFSET_OF(dst, addrs);
	return 0;
}

/**
 * Clone a vs_config from normal pointers to relative pointers.
 */
static int
clone_vs_config_to_relative(
	struct vs_config *dst,
	struct vs_config *src,
	struct memory_context *mctx
) {
	// Copy scalar fields
	dst->flags = src->flags;
	dst->scheduler = src->scheduler;
	dst->real_count = src->real_count;
	dst->allowed_src_count = src->allowed_src_count;
	dst->peers_v4_count = src->peers_v4_count;
	dst->peers_v6_count = src->peers_v6_count;

	// Clone reals array
	if (clone_reals_to_relative(&dst->reals, src->reals, src->real_count, mctx) != 0) {
		return -1;
	}

	// Clone allowed_src array
	if (clone_addr_ranges_to_relative(&dst->allowed_src, src->allowed_src, 
	                                   src->allowed_src_count, mctx) != 0) {
		return -1;
	}

	// Clone peers_v4 array
	if (clone_net4_addrs_to_relative(&dst->peers_v4, src->peers_v4, 
	                                  src->peers_v4_count, mctx) != 0) {
		return -1;
	}

	// Clone peers_v6 array
	if (clone_net6_addrs_to_relative(&dst->peers_v6, src->peers_v6, 
	                                  src->peers_v6_count, mctx) != 0) {
		return -1;
	}

	return 0;
}

/**
 * Clone a named_vs_config array from normal pointers to relative pointers.
 */
static int
clone_vs_array_to_relative(
	struct named_vs_config **dst,
	struct named_vs_config *src,
	size_t count,
	struct memory_context *mctx
) {
	if (count == 0) {
		SET_OFFSET_OF(dst, NULL);
		return 0;
	}

	struct named_vs_config *vs_array = 
		memory_balloc(mctx, sizeof(struct named_vs_config) * count);
	if (vs_array == NULL) {
		return -1;
	}

	for (size_t i = 0; i < count; i++) {
		// Copy identifier (no pointers)
		vs_array[i].identifier = src[i].identifier;

		// Clone config with nested pointers
		if (clone_vs_config_to_relative(&vs_array[i].config, &src[i].config, mctx) != 0) {
			return -1;
		}
	}

	SET_OFFSET_OF(dst, vs_array);
	return 0;
}

/**
 * Clone packet_handler_config from normal pointers to relative pointers.
 */
static int
clone_handler_config_to_relative(
	struct packet_handler_config *dst,
	struct packet_handler_config *src,
	struct memory_context *mctx
) {
	// Copy scalar fields and embedded structs
	dst->sessions_timeouts = src->sessions_timeouts;
	dst->vs_count = src->vs_count;
	dst->source_v4 = src->source_v4;
	dst->source_v6 = src->source_v6;
	dst->decap_v4_count = src->decap_v4_count;
	dst->decap_v6_count = src->decap_v6_count;

	// Clone vs array
	if (clone_vs_array_to_relative(&dst->vs, src->vs, src->vs_count, mctx) != 0) {
		return -1;
	}

	// Clone decap_v4 array
	if (clone_net4_addrs_to_relative(&dst->decap_v4, src->decap_v4, 
	                                  src->decap_v4_count, mctx) != 0) {
		return -1;
	}

	// Clone decap_v6 array
	if (clone_net6_addrs_to_relative(&dst->decap_v6, src->decap_v6, 
	                                  src->decap_v6_count, mctx) != 0) {
		return -1;
	}

	return 0;
}

/**
 * Clone balancer_config from normal pointers to relative pointers.
 */
static int
clone_balancer_config_to_relative(
	struct balancer_config *dst,
	struct balancer_config *src,
	struct memory_context *mctx
) {
	// Clone handler config
	if (clone_handler_config_to_relative(&dst->handler, &src->handler, mctx) != 0) {
		return -1;
	}

	// Copy state config (no pointers)
	dst->state = src->state;

	return 0;
}

/**
 * Clone balancer_agent_balancer_config from normal pointers to relative pointers.
 * This is the main entry point for converting a config to use relative pointers.
 */
int
clone_into_balancer_cfg_with_relative_pointers(
	struct balancer_agent_balancer_config *dst,
	struct balancer_agent_balancer_config *src,
	struct memory_context *mctx
) {
	// Copy balancer name
	memcpy(dst->balancer_name, src->balancer_name, sizeof(dst->balancer_name));

	// Clone balancer config
	if (clone_balancer_config_to_relative(&dst->balancer_config, &src->balancer_config, mctx) != 0) {
		return -1;
	}

	// Copy adjust_weights_config (no pointers)
	dst->adjust_weights_config = src->adjust_weights_config;

	// Copy refresh_period
	dst->refresh_period = src->refresh_period;

	// Copy adjust_weights_vs_count
	dst->adjust_weights_vs_count = src->adjust_weights_vs_count;

	// Clone adjust_weights_vs array (flexible array member)
	// Note: The flexible array member is allocated as part of the parent structure,
	// so we just copy the data
	if (src->adjust_weights_vs_count > 0) {
		memcpy(dst->adjust_weights_vs, src->adjust_weights_vs, 
		       sizeof(uint32_t) * src->adjust_weights_vs_count);
	}

	return 0;
}

/* ========================================================================
 * Functions for cloning FROM relative pointers TO normal pointers
 * ======================================================================== */

/**
 * Clone a named_real_config array from relative pointers to normal pointers.
 */
static int
clone_reals_from_relative(
	struct named_real_config **dst,
	struct named_real_config **src_offset,
	size_t count
) {
	if (count == 0) {
		*dst = NULL;
		return 0;
	}

	struct named_real_config *src = ADDR_OF(src_offset);
	struct named_real_config *reals = calloc(count, sizeof(struct named_real_config));
	if (reals == NULL) {
		return -1;
	}

	memcpy(reals, src, sizeof(struct named_real_config) * count);
	*dst = reals;
	return 0;
}

/**
 * Clone a net_addr_range array from relative pointers to normal pointers.
 */
static int
clone_addr_ranges_from_relative(
	struct net_addr_range **dst,
	struct net_addr_range **src_offset,
	size_t count
) {
	if (count == 0) {
		*dst = NULL;
		return 0;
	}

	struct net_addr_range *src = ADDR_OF(src_offset);
	struct net_addr_range *ranges = calloc(count, sizeof(struct net_addr_range));
	if (ranges == NULL) {
		return -1;
	}

	memcpy(ranges, src, sizeof(struct net_addr_range) * count);
	*dst = ranges;
	return 0;
}

/**
 * Clone a net4_addr array from relative pointers to normal pointers.
 */
static int
clone_net4_addrs_from_relative(
	struct net4_addr **dst,
	struct net4_addr **src_offset,
	size_t count
) {
	if (count == 0) {
		*dst = NULL;
		return 0;
	}

	struct net4_addr *src = ADDR_OF(src_offset);
	struct net4_addr *addrs = calloc(count, sizeof(struct net4_addr));
	if (addrs == NULL) {
		return -1;
	}

	memcpy(addrs, src, sizeof(struct net4_addr) * count);
	*dst = addrs;
	return 0;
}

/**
 * Clone a net6_addr array from relative pointers to normal pointers.
 */
static int
clone_net6_addrs_from_relative(
	struct net6_addr **dst,
	struct net6_addr **src_offset,
	size_t count
) {
	if (count == 0) {
		*dst = NULL;
		return 0;
	}

	struct net6_addr *src = ADDR_OF(src_offset);
	struct net6_addr *addrs = calloc(count, sizeof(struct net6_addr));
	if (addrs == NULL) {
		return -1;
	}

	memcpy(addrs, src, sizeof(struct net6_addr) * count);
	*dst = addrs;
	return 0;
}

/**
 * Clone a vs_config from relative pointers to normal pointers.
 */
static int
clone_vs_config_from_relative(
	struct vs_config *dst,
	struct vs_config *src
) {
	// Copy scalar fields
	dst->flags = src->flags;
	dst->scheduler = src->scheduler;
	dst->real_count = src->real_count;
	dst->allowed_src_count = src->allowed_src_count;
	dst->peers_v4_count = src->peers_v4_count;
	dst->peers_v6_count = src->peers_v6_count;

	// Clone reals array
	if (clone_reals_from_relative(&dst->reals, &src->reals, src->real_count) != 0) {
		return -1;
	}

	// Clone allowed_src array
	if (clone_addr_ranges_from_relative(&dst->allowed_src, &src->allowed_src, 
	                                     src->allowed_src_count) != 0) {
		free(dst->reals);
		return -1;
	}

	// Clone peers_v4 array
	if (clone_net4_addrs_from_relative(&dst->peers_v4, &src->peers_v4, 
	                                    src->peers_v4_count) != 0) {
		free(dst->reals);
		free(dst->allowed_src);
		return -1;
	}

	// Clone peers_v6 array
	if (clone_net6_addrs_from_relative(&dst->peers_v6, &src->peers_v6, 
	                                    src->peers_v6_count) != 0) {
		free(dst->reals);
		free(dst->allowed_src);
		free(dst->peers_v4);
		return -1;
	}

	return 0;
}

/**
 * Clone a named_vs_config array from relative pointers to normal pointers.
 */
static int
clone_vs_array_from_relative(
	struct named_vs_config **dst,
	struct named_vs_config **src_offset,
	size_t count
) {
	if (count == 0) {
		*dst = NULL;
		return 0;
	}

	struct named_vs_config *src = ADDR_OF(src_offset);
	struct named_vs_config *vs_array = calloc(count, sizeof(struct named_vs_config));
	if (vs_array == NULL) {
		return -1;
	}

	for (size_t i = 0; i < count; i++) {
		// Copy identifier (no pointers)
		vs_array[i].identifier = src[i].identifier;

		// Clone config with nested pointers
		if (clone_vs_config_from_relative(&vs_array[i].config, &src[i].config) != 0) {
			// Cleanup previously allocated vs configs
			for (size_t j = 0; j < i; j++) {
				free(vs_array[j].config.reals);
				free(vs_array[j].config.allowed_src);
				free(vs_array[j].config.peers_v4);
				free(vs_array[j].config.peers_v6);
			}
			free(vs_array);
			return -1;
		}
	}

	*dst = vs_array;
	return 0;
}

/**
 * Clone packet_handler_config from relative pointers to normal pointers.
 */
static int
clone_handler_config_from_relative(
	struct packet_handler_config *dst,
	struct packet_handler_config *src
) {
	// Copy scalar fields and embedded structs
	dst->sessions_timeouts = src->sessions_timeouts;
	dst->vs_count = src->vs_count;
	dst->source_v4 = src->source_v4;
	dst->source_v6 = src->source_v6;
	dst->decap_v4_count = src->decap_v4_count;
	dst->decap_v6_count = src->decap_v6_count;

	// Clone vs array
	if (clone_vs_array_from_relative(&dst->vs, &src->vs, src->vs_count) != 0) {
		return -1;
	}

	// Clone decap_v4 array
	if (clone_net4_addrs_from_relative(&dst->decap_v4, &src->decap_v4, 
	                                    src->decap_v4_count) != 0) {
		// Cleanup vs array
		if (dst->vs) {
			for (size_t i = 0; i < dst->vs_count; i++) {
				free(dst->vs[i].config.reals);
				free(dst->vs[i].config.allowed_src);
				free(dst->vs[i].config.peers_v4);
				free(dst->vs[i].config.peers_v6);
			}
			free(dst->vs);
		}
		return -1;
	}

	// Clone decap_v6 array
	if (clone_net6_addrs_from_relative(&dst->decap_v6, &src->decap_v6, 
	                                    src->decap_v6_count) != 0) {
		// Cleanup
		if (dst->vs) {
			for (size_t i = 0; i < dst->vs_count; i++) {
				free(dst->vs[i].config.reals);
				free(dst->vs[i].config.allowed_src);
				free(dst->vs[i].config.peers_v4);
				free(dst->vs[i].config.peers_v6);
			}
			free(dst->vs);
		}
		free(dst->decap_v4);
		return -1;
	}

	return 0;
}

/**
 * Clone balancer_config from relative pointers to normal pointers.
 */
static int
clone_balancer_config_from_relative(
	struct balancer_config *dst,
	struct balancer_config *src
) {
	// Clone handler config
	if (clone_handler_config_from_relative(&dst->handler, &src->handler) != 0) {
		return -1;
	}

	// Copy state config (no pointers)
	dst->state = src->state;

	return 0;
}

/**
 * Clone balancer_agent_balancer from relative pointers to normal pointers.
 * This is the main entry point for converting from relative pointers to normal pointers.
 */
void
clone_balancer_with_relative_pointers(
	struct balancer_agent_balancer *dst,
	struct balancer_agent_balancer *src
) {
	// Copy handle pointer (it's already a normal pointer, not relative)
	dst->handle = src->handle;

	// Copy balancer name
	memcpy(dst->config.balancer_name, src->config.balancer_name, 
	       sizeof(dst->config.balancer_name));

	// Clone balancer config from relative to normal pointers
	clone_balancer_config_from_relative(&dst->config.balancer_config, 
	                                     &src->config.balancer_config);

	// Copy adjust_weights_config (no pointers)
	dst->config.adjust_weights_config = src->config.adjust_weights_config;

	// Copy refresh_period
	dst->config.refresh_period = src->config.refresh_period;

	// Copy adjust_weights_vs_count
	dst->config.adjust_weights_vs_count = src->config.adjust_weights_vs_count;

	// Clone adjust_weights_vs array
	if (src->config.adjust_weights_vs_count > 0) {
		memcpy(dst->config.adjust_weights_vs, src->config.adjust_weights_vs, 
		       sizeof(uint32_t) * src->config.adjust_weights_vs_count);
	}
}

/**
 * Free a vs_config with relative pointers (allocated in agent memory).
 */
static void
free_vs_config_with_relative_pointers(
	struct vs_config *cfg,
	struct memory_context *mctx
) {
	// Free reals array
	if (cfg->real_count > 0 && cfg->reals != NULL) {
		struct named_real_config *reals = ADDR_OF(&cfg->reals);
		memory_bfree(mctx, reals, sizeof(struct named_real_config) * cfg->real_count);
		cfg->reals = NULL;
		cfg->real_count = 0;
	}

	// Free allowed_src array
	if (cfg->allowed_src_count > 0 && cfg->allowed_src != NULL) {
		struct net_addr_range *ranges = ADDR_OF(&cfg->allowed_src);
		memory_bfree(mctx, ranges, sizeof(struct net_addr_range) * cfg->allowed_src_count);
		cfg->allowed_src = NULL;
		cfg->allowed_src_count = 0;
	}

	// Free peers_v4 array
	if (cfg->peers_v4_count > 0 && cfg->peers_v4 != NULL) {
		struct net4_addr *addrs = ADDR_OF(&cfg->peers_v4);
		memory_bfree(mctx, addrs, sizeof(struct net4_addr) * cfg->peers_v4_count);
		cfg->peers_v4 = NULL;
		cfg->peers_v4_count = 0;
	}

	// Free peers_v6 array
	if (cfg->peers_v6_count > 0 && cfg->peers_v6 != NULL) {
		struct net6_addr *addrs = ADDR_OF(&cfg->peers_v6);
		memory_bfree(mctx, addrs, sizeof(struct net6_addr) * cfg->peers_v6_count);
		cfg->peers_v6 = NULL;
		cfg->peers_v6_count = 0;
	}
}

/**
 * Free a packet_handler_config with relative pointers (allocated in agent memory).
 */
static void
free_handler_config_with_relative_pointers(
	struct packet_handler_config *cfg,
	struct memory_context *mctx
) {
	// Free VS array and nested structures
	if (cfg->vs_count > 0 && cfg->vs != NULL) {
		struct named_vs_config *vs_array = ADDR_OF(&cfg->vs);
		for (size_t i = 0; i < cfg->vs_count; i++) {
			free_vs_config_with_relative_pointers(&vs_array[i].config, mctx);
		}
		memory_bfree(mctx, vs_array, sizeof(struct named_vs_config) * cfg->vs_count);
		cfg->vs = NULL;
		cfg->vs_count = 0;
	}

	// Free decap_v4 array
	if (cfg->decap_v4_count > 0 && cfg->decap_v4 != NULL) {
		struct net4_addr *addrs = ADDR_OF(&cfg->decap_v4);
		memory_bfree(mctx, addrs, sizeof(struct net4_addr) * cfg->decap_v4_count);
		cfg->decap_v4 = NULL;
		cfg->decap_v4_count = 0;
	}

	// Free decap_v6 array
	if (cfg->decap_v6_count > 0 && cfg->decap_v6 != NULL) {
		struct net6_addr *addrs = ADDR_OF(&cfg->decap_v6);
		memory_bfree(mctx, addrs, sizeof(struct net6_addr) * cfg->decap_v6_count);
		cfg->decap_v6 = NULL;
		cfg->decap_v6_count = 0;
	}
}

/**
 * Free balancer_agent_balancer_config with relative pointers.
 * This frees all nested structures allocated in agent memory.
 */
void
free_balancer_cfg_with_relative_pointers(
	struct balancer_agent_balancer_config *cfg,
	struct memory_context *mctx
) {
	// Free handler config (includes VS array with reals, allowed_src, peers, and decap addresses)
	free_handler_config_with_relative_pointers(&cfg->balancer_config.handler, mctx);

	// Note: adjust_weights_vs is a flexible array member at the end of the struct,
	// so it's freed when the parent structure is freed
}

/**
 * Free a vs_config with normal pointers (allocated with calloc).
 */
static void
free_vs_config_normal(struct vs_config *cfg) {
	// Free reals array
	if (cfg->reals != NULL) {
		free(cfg->reals);
		cfg->reals = NULL;
		cfg->real_count = 0;
	}

	// Free allowed_src array
	if (cfg->allowed_src != NULL) {
		free(cfg->allowed_src);
		cfg->allowed_src = NULL;
		cfg->allowed_src_count = 0;
	}

	// Free peers_v4 array
	if (cfg->peers_v4 != NULL) {
		free(cfg->peers_v4);
		cfg->peers_v4 = NULL;
		cfg->peers_v4_count = 0;
	}

	// Free peers_v6 array
	if (cfg->peers_v6 != NULL) {
		free(cfg->peers_v6);
		cfg->peers_v6 = NULL;
		cfg->peers_v6_count = 0;
	}
}

/**
 * Free a packet_handler_config with normal pointers (allocated with calloc).
 */
static void
free_handler_config_normal(struct packet_handler_config *cfg) {
	// Free VS array and nested structures
	if (cfg->vs != NULL) {
		for (size_t i = 0; i < cfg->vs_count; i++) {
			free_vs_config_normal(&cfg->vs[i].config);
		}
		free(cfg->vs);
		cfg->vs = NULL;
		cfg->vs_count = 0;
	}

	// Free decap_v4 array
	if (cfg->decap_v4 != NULL) {
		free(cfg->decap_v4);
		cfg->decap_v4 = NULL;
		cfg->decap_v4_count = 0;
	}

	// Free decap_v6 array
	if (cfg->decap_v6 != NULL) {
		free(cfg->decap_v6);
		cfg->decap_v6 = NULL;
		cfg->decap_v6_count = 0;
	}
}

/**
 * Free a single balancer_agent_balancer with normal pointers.
 * This frees the config structures but NOT the handle pointer (agent-managed).
 */
void
balancer_agent_balancer_free(struct balancer_agent_balancer *balancer) {
	if (balancer == NULL) {
		return;
	}

	// Free handler config (includes VS array with reals, allowed_src, peers, and decap addresses)
	free_handler_config_normal(&balancer->config.balancer_config.handler);

	// Note: We do NOT free balancer->handle as it's managed by the agent
	// Note: adjust_weights_vs is part of the flexible array member,
	// so it will be freed when the parent structure is freed
}

/**
 * Free balancer_agent_balancers structure with normal pointers.
 * This frees all balancer configs but NOT the handle pointers (agent-managed).
 */
void
balancer_agent_balancers_free(struct balancer_agent_balancers *balancers) {
	if (balancers == NULL || balancers->balancers == NULL) {
		return;
	}

	// Free each balancer's config
	for (size_t i = 0; i < balancers->count; i++) {
		balancer_agent_balancer_free(&balancers->balancers[i]);
	}

	// Free the balancers array itself
	free(balancers->balancers);
	balancers->balancers = NULL;
	balancers->count = 0;
}