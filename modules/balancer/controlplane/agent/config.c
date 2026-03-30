#include "common/memory.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "manager.h"
#include "modules/balancer/controlplane/api/balancer.h"
#include "modules/balancer/controlplane/api/vs.h"
#include <assert.h>
#include <stdlib.h>
#include <string.h>

/**
 * Clone a named_real_config array from normal pointers to relative pointers.
 *
 * @param dst Destination pointer (will be set to allocated memory with relative
 * pointers)
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

	// Copy all real configs (they contain no pointers, just embedded
	// structs)
	memcpy(reals, src, sizeof(struct named_real_config) * count);

	SET_OFFSET_OF(dst, reals);
	return 0;
}

/**
 * Clone an allowed_src array from normal pointers to relative pointers.
 * Each allowed_src contains an address and an array of port ranges.
 */
static int
clone_allowed_src_to_relative(
	struct allowed_sources **dst,
	struct allowed_sources *src,
	size_t count,
	struct memory_context *mctx
) {
	if (count == 0) {
		SET_OFFSET_OF(dst, NULL);
		return 0;
	}

	// Allocate array of allowed_src entries
	struct allowed_sources *entries =
		memory_balloc(mctx, sizeof(struct allowed_sources) * count);
	if (entries == NULL) {
		return -1;
	}

	// For each allowed_src entry, copy addr and clone port ranges
	for (size_t i = 0; i < count; i++) {
		// Copy networks
		entries[i].nets_count = src[i].nets_count;
		if (entries[i].nets_count > 0) {
			entries[i].nets = memory_balloc(
				mctx, sizeof(struct net) * entries[i].nets_count
			);
			if (entries[i].nets == NULL) {
				// todo: proper cleanup
				memory_bfree(
					mctx,
					entries,
					sizeof(struct allowed_sources) * count
				);
				return -1;
			}
			for (size_t net_idx = 0;
			     net_idx < entries[i].nets_count;
			     ++net_idx) {
				entries[i].nets[net_idx] = src[i].nets[net_idx];
			}
			SET_OFFSET_OF(&entries[i].nets, entries[i].nets);
		} else {
			entries[i].nets = NULL;
		}

		// Clone tag string if present
		if (src[i].tag != NULL) {
			size_t tag_len = strlen(src[i].tag) + 1;
			char *tag_copy = memory_balloc(mctx, tag_len);
			if (tag_copy == NULL) {
				// TODO: proper cleanup
				memory_bfree(
					mctx,
					entries,
					sizeof(struct allowed_sources) * count
				);
				return -1;
			}
			memcpy(tag_copy, src[i].tag, tag_len);
			entries[i].tag = tag_copy;
			SET_OFFSET_OF(&entries[i].tag, entries[i].tag);
		} else {
			entries[i].tag = NULL;
		}

		entries[i].port_ranges_count = src[i].port_ranges_count;

		// Clone the ports_range array
		if (src[i].port_ranges_count > 0) {
			struct ports_range *ranges = memory_balloc(
				mctx,
				sizeof(struct ports_range) *
					src[i].port_ranges_count
			);
			if (ranges == NULL) {
				// Cleanup previously allocated ranges
				for (size_t j = 0; j < i; j++) {
					if (entries[j].port_ranges_count > 0) {
						memory_bfree(
							mctx,
							ADDR_OF(&entries[j]
									 .port_ranges
							),
							sizeof(struct
							       ports_range
							) * entries[j].port_ranges_count
						);
					}
				}
				// TODO: proper clean up
				memory_bfree(
					mctx,
					entries,
					sizeof(struct allowed_sources) * count
				);
				return -1;
			}
			memcpy(ranges,
			       src[i].port_ranges,
			       sizeof(struct ports_range) *
				       src[i].port_ranges_count);
			SET_OFFSET_OF(&entries[i].port_ranges, ranges);
		} else {
			SET_OFFSET_OF(&entries[i].port_ranges, NULL);
		}
	}

	SET_OFFSET_OF(dst, entries);
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
	if (clone_reals_to_relative(
		    &dst->reals, src->reals, src->real_count, mctx
	    ) != 0) {
		return -1;
	}

	// Clone allowed_src array
	if (clone_allowed_src_to_relative(
		    &dst->allowed_src,
		    src->allowed_src,
		    src->allowed_src_count,
		    mctx
	    ) != 0) {
		// TODO: free reals
		return -1;
	}

	// Clone peers_v4 array
	if (clone_net4_addrs_to_relative(
		    &dst->peers_v4, src->peers_v4, src->peers_v4_count, mctx
	    ) != 0) {
		// TODO: free reals and addr ranges
		return -1;
	}

	// Clone peers_v6 array
	if (clone_net6_addrs_to_relative(
		    &dst->peers_v6, src->peers_v6, src->peers_v6_count, mctx
	    ) != 0) {
		// TODO: free freals, addr ranges and net4 addrs
		return -1;
	}

	return 0;
}

static void
free_vs_config_with_relative_pointers(
	struct vs_config *vs_config, struct memory_context *mctx
);

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
		if (clone_vs_config_to_relative(
			    &vs_array[i].config, &src[i].config, mctx
		    ) != 0) {
			for (size_t j = 0; j < i; ++j) {
				free_vs_config_with_relative_pointers(
					&vs_array[j].config, mctx
				);
			}
			memory_bfree(
				mctx,
				vs_array,
				sizeof(struct named_vs_config) * count
			);
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
	if (clone_vs_array_to_relative(
		    &dst->vs, src->vs, src->vs_count, mctx
	    ) != 0) {
		return -1;
	}

	// Clone decap_v4 array
	if (clone_net4_addrs_to_relative(
		    &dst->decap_v4, src->decap_v4, src->decap_v4_count, mctx
	    ) != 0) {
		return -1;
	}

	// Clone decap_v6 array
	if (clone_net6_addrs_to_relative(
		    &dst->decap_v6, src->decap_v6, src->decap_v6_count, mctx
	    ) != 0) {
		return -1;
	}

	return 0;
}

/**
 * Clone balancer_config from normal pointers to relative pointers.
 */
int
clone_balancer_config_to_relative(
	struct balancer_config *dst,
	struct balancer_config *src,
	struct memory_context *mctx
) {
	// Clone handler config
	if (clone_handler_config_to_relative(
		    &dst->handler, &src->handler, mctx
	    ) != 0) {
		return -1;
	}

	// Copy state config (no pointers)
	dst->state = src->state;

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
	struct named_real_config *reals =
		calloc(count, sizeof(struct named_real_config));
	if (reals == NULL) {
		return -1;
	}

	memcpy(reals, src, sizeof(struct named_real_config) * count);
	*dst = reals;
	return 0;
}

/**
 * Clone an allowed_src array from relative pointers to normal pointers.
 * Each allowed_src contains an address and an array of port ranges.
 */
static int
clone_allowed_src_from_relative(
	struct allowed_sources **dst,
	struct allowed_sources **src_offset,
	size_t count
) {
	if (count == 0) {
		*dst = NULL;
		return 0;
	}

	struct allowed_sources *src = ADDR_OF(src_offset);
	struct allowed_sources *entries =
		calloc(count, sizeof(struct allowed_sources));
	if (entries == NULL) {
		return -1;
	}

	// For each allowed_src entry, copy addr and clone port ranges
	for (size_t i = 0; i < count; i++) {
		// Copy networks
		entries[i].nets_count = src[i].nets_count;
		if (entries[i].nets_count > 0) {
			entries[i].nets =
				calloc(entries[i].nets_count,
				       sizeof(struct net6));
			struct net *nets = ADDR_OF(&src[i].nets);
			for (size_t net_idx = 0;
			     net_idx < entries[i].nets_count;
			     ++net_idx) {
				entries[i].nets[net_idx] = nets[net_idx];
			}
		}

		entries[i].port_ranges_count = src[i].port_ranges_count;

		// Clone the ports_range array
		if (src[i].port_ranges_count > 0) {
			struct ports_range *src_ranges =
				ADDR_OF(&src[i].port_ranges);
			struct ports_range *ranges =
				calloc(src[i].port_ranges_count,
				       sizeof(struct ports_range));
			if (ranges == NULL) {
				// Cleanup previously allocated ranges
				for (size_t j = 0; j < i; j++) {
					free(entries[j].port_ranges);
				}
				free(entries);
				return -1;
			}
			memcpy(ranges,
			       src_ranges,
			       sizeof(struct ports_range) *
				       src[i].port_ranges_count);
			entries[i].port_ranges = ranges;
		} else {
			entries[i].port_ranges = NULL;
		}

		// Clone tag string if present
		if (src[i].tag != NULL) {
			const char *src_tag = ADDR_OF(&src[i].tag);
			entries[i].tag = strdup(src_tag);
		} else {
			entries[i].tag = NULL;
		}
	}

	*dst = entries;
	return 0;
}

/**
 * Clone a net4_addr array from relative pointers to normal pointers.
 */
static int
clone_net4_addrs_from_relative(
	struct net4_addr **dst, struct net4_addr **src_offset, size_t count
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
	struct net6_addr **dst, struct net6_addr **src_offset, size_t count
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
clone_vs_config_from_relative(struct vs_config *dst, struct vs_config *src) {
	// Copy scalar fields
	dst->flags = src->flags;
	dst->scheduler = src->scheduler;
	dst->real_count = src->real_count;
	dst->allowed_src_count = src->allowed_src_count;
	dst->peers_v4_count = src->peers_v4_count;
	dst->peers_v6_count = src->peers_v6_count;

	// Clone reals array
	if (clone_reals_from_relative(
		    &dst->reals, &src->reals, src->real_count
	    ) != 0) {
		return -1;
	}

	// Clone allowed_src array
	if (clone_allowed_src_from_relative(
		    &dst->allowed_src, &src->allowed_src, src->allowed_src_count
	    ) != 0) {
		free(dst->reals);
		return -1;
	}

	// Clone peers_v4 array
	if (clone_net4_addrs_from_relative(
		    &dst->peers_v4, &src->peers_v4, src->peers_v4_count
	    ) != 0) {
		free(dst->reals);
		free(dst->allowed_src);
		return -1;
	}

	// Clone peers_v6 array
	if (clone_net6_addrs_from_relative(
		    &dst->peers_v6, &src->peers_v6, src->peers_v6_count
	    ) != 0) {
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
	struct named_vs_config *vs_array =
		calloc(count, sizeof(struct named_vs_config));

	for (size_t i = 0; i < count; i++) {
		// Copy identifier (no pointers)
		vs_array[i].identifier = src[i].identifier;

		// Clone config with nested pointers
		clone_vs_config_from_relative(
			&vs_array[i].config, &src[i].config
		);
	}

	*dst = vs_array;
	return 0;
}

/**
 * Clone packet_handler_config from relative pointers to normal pointers.
 */
int
packet_handler_config_from_relative(
	struct packet_handler_config *dst, struct packet_handler_config *src
) {
	// Copy scalar fields and embedded structs
	dst->sessions_timeouts = src->sessions_timeouts;
	dst->vs_count = src->vs_count;
	dst->source_v4 = src->source_v4;
	dst->source_v6 = src->source_v6;
	dst->decap_v4_count = src->decap_v4_count;
	dst->decap_v6_count = src->decap_v6_count;

	// Clone vs array
	if (clone_vs_array_from_relative(&dst->vs, &src->vs, src->vs_count) !=
	    0) {
		return -1;
	}

	// Clone decap_v4 array
	if (clone_net4_addrs_from_relative(
		    &dst->decap_v4, &src->decap_v4, src->decap_v4_count
	    ) != 0) {
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
	if (clone_net6_addrs_from_relative(
		    &dst->decap_v6, &src->decap_v6, src->decap_v6_count
	    ) != 0) {
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
int
clone_balancer_config_from_relative(
	struct balancer_config *dst, struct balancer_config *src
) {
	// Clone handler config
	if (packet_handler_config_from_relative(&dst->handler, &src->handler) !=
	    0) {
		return -1;
	}

	// Copy state config (no pointers)
	dst->state = src->state;

	return 0;
}

/**
 * Free a vs_config with relative pointers (allocated in agent memory).
 */
static void
free_vs_config_with_relative_pointers(
	struct vs_config *cfg, struct memory_context *mctx
) {
	// Free reals array
	if (cfg->real_count > 0 && cfg->reals != NULL) {
		struct named_real_config *reals = ADDR_OF(&cfg->reals);
		memory_bfree(
			mctx,
			reals,
			sizeof(struct named_real_config) * cfg->real_count
		);
		cfg->reals = NULL;
		cfg->real_count = 0;
	}

	// Free allowed_src array (with nested port ranges)
	if (cfg->allowed_src_count > 0 && cfg->allowed_src != NULL) {
		struct allowed_sources *entries = ADDR_OF(&cfg->allowed_src);

		// First, free each nested ports_range array and tag strings
		for (size_t i = 0; i < cfg->allowed_src_count; i++) {
			if (entries[i].port_ranges_count > 0 &&
			    entries[i].port_ranges != NULL) {
				struct ports_range *ranges =
					ADDR_OF(&entries[i].port_ranges);
				memory_bfree(
					mctx,
					ranges,
					sizeof(struct ports_range) *
						entries[i].port_ranges_count
				);
			}
			// Free tag string if present
			if (entries[i].tag != NULL) {
				const char *tag = ADDR_OF(&entries[i].tag);
				size_t tag_len = strlen(tag) + 1;
				memory_bfree(mctx, (void *)tag, tag_len);
			}
		}

		// Then free the allowed_src array itself
		memory_bfree(
			mctx,
			entries,
			sizeof(struct allowed_sources) * cfg->allowed_src_count
		);
		cfg->allowed_src = NULL;
		cfg->allowed_src_count = 0;
	}

	// Free peers_v4 array
	if (cfg->peers_v4_count > 0 && cfg->peers_v4 != NULL) {
		struct net4_addr *addrs = ADDR_OF(&cfg->peers_v4);
		memory_bfree(
			mctx,
			addrs,
			sizeof(struct net4_addr) * cfg->peers_v4_count
		);
		cfg->peers_v4 = NULL;
		cfg->peers_v4_count = 0;
	}

	// Free peers_v6 array
	if (cfg->peers_v6_count > 0 && cfg->peers_v6 != NULL) {
		struct net6_addr *addrs = ADDR_OF(&cfg->peers_v6);
		memory_bfree(
			mctx,
			addrs,
			sizeof(struct net6_addr) * cfg->peers_v6_count
		);
		cfg->peers_v6 = NULL;
		cfg->peers_v6_count = 0;
	}
}
