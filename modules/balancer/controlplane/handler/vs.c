#include "api/vs.h"
#include "api/balancer.h"
#include "api/counter.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/network.h"

#include "compiler/declare.h"
#include "counters/counters.h"
#include "lib/controlplane/diag/diag.h"

#include "rules.h"
#include "selector.h"
#include "vs.h"

#include "state/state.h"
#include "state/vs.h"

#include <assert.h>
#include <netinet/in.h>
#include <stddef.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/types.h>

#include "filter/compiler.h"

#define MAX_TAG_LENGTH 240

static int
validate_tag(const char *tag) {
	if (tag == NULL) {
		return 0; // NULL is valid (means no tracking)
	}
	size_t len = strnlen(tag, MAX_TAG_LENGTH + 1);
	if (len == 0) {
		NEW_ERROR("tag must be at least 1 character long");
		return -1;
	}
	if (len > MAX_TAG_LENGTH) {
		NEW_ERROR(
			"tag length %zu exceeds maximum %d", len, MAX_TAG_LENGTH
		);
		return -1;
	}
	return 0;
}

static int
setup_reals(
	struct vs *vs,
	struct vs_config *config,
	size_t first_real_idx,
	struct real *reals
) {
	vs->reals_count = config->real_count;
	vs->first_real_idx = first_real_idx;
	SET_OFFSET_OF(&vs->reals, reals);
	return 0;
}

static int
setup_selector(
	struct vs *vs,
	struct balancer_state *state,
	struct memory_context *mctx,
	struct vs_config *config
) {
	const struct real *reals = ADDR_OF(&vs->reals);
	if (selector_init(&vs->selector, state, mctx, config->scheduler) != 0) {
		PUSH_ERROR("failed to setup selector");
		return -1;
	}
	if (selector_update(&vs->selector, vs->reals_count, reals) != 0) {
		selector_free(&vs->selector);
		PUSH_ERROR("failed to setup selector reals");
		return -1;
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
	struct vs *vs, struct memory_context *mctx, struct vs_config *config
) {
	vs->peers_v4_count = config->peers_v4_count;
	vs->peers_v6_count = config->peers_v6_count;

	void *peers_v4_ptr = memory_balloc(
		mctx, sizeof(struct net4_addr) * vs->peers_v4_count
	);
	if (peers_v4_ptr == NULL && vs->peers_v4_count > 0) {
		NEW_ERROR("failed to allocate memory for IPv4 peers");
		return -1;
	}
	SET_OFFSET_OF(&vs->peers_v4, peers_v4_ptr);

	// Copy IPv4 peer addresses from config (config uses normal pointers)
	if (vs->peers_v4_count > 0) {
		memcpy(peers_v4_ptr,
		       config->peers_v4,
		       sizeof(struct net4_addr) * vs->peers_v4_count);
	}

	void *peers_v6_ptr = memory_balloc(
		mctx, sizeof(struct net6_addr) * vs->peers_v6_count
	);
	if (peers_v6_ptr == NULL && vs->peers_v6_count > 0) {
		NEW_ERROR("failed to allocate memory for IPv6 peers");
		memory_bfree(
			mctx,
			peers_v4_ptr,
			sizeof(struct net4_addr) * vs->peers_v4_count
		);
		return -1;
	}
	SET_OFFSET_OF(&vs->peers_v6, peers_v6_ptr);

	// Copy IPv6 peer addresses from config (config uses normal pointers)
	if (vs->peers_v6_count > 0) {
		memcpy(peers_v6_ptr,
		       config->peers_v6,
		       sizeof(struct net6_addr) * vs->peers_v6_count);
	}

	return 0;
}

int
vs_state_setup(
	struct vs *vs,
	struct balancer_state *balancer_state,
	struct named_vs_config *config
) {
	struct vs_state *vs_state = balancer_state_find_or_insert_vs(
		balancer_state, &config->identifier
	);
	if (!vs_state) {
		PUSH_ERROR(
			"failed to find or insert virtual service into registry"
		);
		return -1;
	}
	vs->registry_idx = vs_state->registry_idx;
	vs->identifier = config->identifier;
	return 0;
}

static int
setup_flags(struct vs *vs, struct named_vs_config *config) {
	if ((config->config.flags & VS_PURE_L3_FLAG) &&
	    config->identifier.port != 0) {
		NEW_ERROR(
			"PureL3 mode "
			"requires port=0, but port=%u was specified",
			config->identifier.port
		);
		return -1;
	}
	vs->flags = config->config.flags;
	return 0;
}

static int
validate_net4(struct net4 *net4) {
	int prev = 1;
	for (int bit = 31; bit >= 0; --bit) {
		int byte = (31 - bit) / 8;
		int inner_bit = bit % 8;
		int cur = net4->mask[byte] & (1 << inner_bit);
		if (cur && !prev) {
			NEW_ERROR("mask bits must be consecutive");
			return -1;
		}
		prev = cur != 0;
	}

	return 0;
}

static int
validate_net6_half(const uint8_t *mask) { // bytes are in big-endian
	int prev = 1;
	for (int bit = 63; bit >= 0; --bit) {
		int byte = (63 - bit) / 8;
		int inner_bit = bit % 8;
		int cur = mask[byte] & (1 << inner_bit);
		if (cur && !prev) {
			NEW_ERROR("mask bits must be consecutive");
			return -1;
		}
		prev = cur != 0;
	}
	return 0;
}

static int
validate_net6(struct net6 *net6) {
	if (validate_net6_half(net6->mask) != 0) {
		PUSH_ERROR("high mask bits are invalid");
		return -1;
	}
	if (validate_net6_half(net6->mask + 8) != 0) {
		PUSH_ERROR("low mask bits are invalid");
		return -1;
	}
	return 0;
}

// fill_rule creates a filter rule with ABSOLUTE pointers (not relative).
// This allows safe sorting and comparison. Pointers are converted to relative
// offsets later in setup_acl_rules after sorting and deduplication.
static int
fill_rule(
	struct vs *vs,
	struct filter_rule *rule,
	size_t src_idx,
	struct allowed_sources *src,
	struct memory_context *mctx
) {
	rule->action = (uint32_t)src_idx;

	if (vs->identifier.ip_proto == IPPROTO_IP) {
		rule->net4.dst_count = 0;
		rule->net4.dsts = NULL;

		for (size_t net_idx = 0; net_idx < src->nets_count; ++net_idx) {
			if (validate_net4(&src->nets[net_idx].v4) != 0) {
				PUSH_ERROR(
					"IPv4 network at index %zu is invalid",
					net_idx
				);
				return -1;
			}
		}

		rule->net4.src_count = src->nets_count;
		struct net4 *net4_srcs = memory_balloc(
			mctx, sizeof(struct net4) * src->nets_count
		);
		if (net4_srcs == NULL) {
			NEW_ERROR("failed to allocate net4 srcs");
			return -1;
		}

		for (size_t net_idx = 0; net_idx < src->nets_count; ++net_idx) {
			net4_srcs[net_idx] = src->nets[net_idx].v4;
		}

		rule->net4.srcs = net4_srcs; // Store absolute pointer
	} else if (vs->identifier.ip_proto == IPPROTO_IPV6) {
		rule->net6.dst_count = 0;
		rule->net6.dsts = NULL;

		for (size_t net_idx = 0; net_idx < src->nets_count; ++net_idx) {
			if (validate_net6(&src->nets[net_idx].v6) != 0) {
				PUSH_ERROR(
					"IPv6 network at index %zu is invalid",
					net_idx
				);
				return -1;
			}
		}

		rule->net6.src_count = src->nets_count;
		struct net6 *net6_srcs = memory_balloc(
			mctx, sizeof(struct net6) * src->nets_count
		);
		if (net6_srcs == NULL) {
			NEW_ERROR("failed to allocate net6 srcs");
			return -1;
		}

		for (size_t net_idx = 0; net_idx < src->nets_count; ++net_idx) {
			net6_srcs[net_idx] = src->nets[net_idx].v6;
		}

		rule->net6.srcs = net6_srcs; // Store absolute pointer
	}

	// Handle port ranges: if none specified, use default [0, 65535]
	if (src->port_ranges_count == 0) {
		rule->transport.src_count = 1;
		struct filter_port_range *port_srcs =
			memory_balloc(mctx, sizeof(struct filter_port_range));
		if (port_srcs == NULL) {
			NEW_ERROR("failed to allocate port srcs");
			return -1;
		}
		port_srcs[0].from = 0;
		port_srcs[0].to = 65535;
		rule->transport.srcs = port_srcs; // Store absolute pointer
	} else {
		rule->transport.src_count = src->port_ranges_count;
		struct filter_port_range *port_srcs = memory_balloc(
			mctx,
			sizeof(struct filter_port_range) *
				src->port_ranges_count
		);
		if (port_srcs == NULL) {
			NEW_ERROR("failed to allocate port srcs");
			return -1;
		}
		for (size_t port_range_idx = 0;
		     port_range_idx < src->port_ranges_count;
		     ++port_range_idx) {
			struct filter_port_range *filter_port_range =
				&port_srcs[port_range_idx];
			struct ports_range *port_range =
				&src->port_ranges[port_range_idx];
			filter_port_range->from = port_range->from;
			filter_port_range->to = port_range->to;
			if (filter_port_range->from > filter_port_range->to) {
				PUSH_ERROR("port range is invalid");
				return -1;
			}
		}
		rule->transport.srcs = port_srcs; // Store absolute pointer
	}
	return 0;
}

// src_filter_rules creates filter rules with ABSOLUTE pointers.
// The rules can be safely sorted and compared. Conversion to relative
// pointers happens later in setup_acl_rules.
static int
src_filter_rules(
	struct vs *vs,
	struct vs_config *config,
	struct filter_rule **rules,
	size_t *rule_count,
	struct memory_context *mctx
) {
	if (vs->identifier.ip_proto != IPPROTO_IP &&
	    vs->identifier.ip_proto != IPPROTO_IPV6) {
		NEW_ERROR(
			"virtual service IP protocol is incorrect: %u "
			"(expected IPv4 %u or IPv6 %u)",
			vs->identifier.ip_proto,
			IPPROTO_IP,
			IPPROTO_IPV6
		);
		return -1;
	}

	size_t count = config->allowed_src_count;
	if (count == 0) {
		*rule_count = 0;
		*rules = NULL;
		return 0;
	}

	struct filter_rule *r =
		memory_balloc(mctx, sizeof(struct filter_rule) * count);
	if (r == NULL) {
		NEW_ERROR("failed to allocate rules");
		return -1;
	}
	memset(r, 0, sizeof(struct filter_rule) * count);
	for (size_t rule_idx = 0; rule_idx < config->allowed_src_count;
	     ++rule_idx) {
		if (fill_rule(
			    vs,
			    &r[rule_idx],
			    rule_idx,
			    &config->allowed_src[rule_idx],
			    mctx
		    ) != 0) {
			PUSH_ERROR("rule at index %zu is invalid", rule_idx);
			// Free already allocated rules (using absolute
			// pointers)
			for (size_t j = 0; j < rule_idx; ++j) {
				struct filter_rule *rule = &r[j];
				if (rule->net4.src_count > 0) {
					memory_bfree(
						mctx,
						rule->net4.srcs,
						sizeof(struct net4) *
							rule->net4.src_count
					);
				}
				if (rule->net6.src_count > 0) {
					memory_bfree(
						mctx,
						rule->net6.srcs,
						sizeof(struct net6) *
							rule->net6.src_count
					);
				}
				if (rule->transport.src_count > 0) {
					memory_bfree(
						mctx,
						rule->transport.srcs,
						sizeof(struct filter_port_range
						) * rule->transport.src_count
					);
				}
			}
			memory_bfree(
				mctx, r, sizeof(struct filter_rule) * count
			);
			return -1;
		}
	}

	*rule_count = count;
	*rules = r;
	return 0;
}

////////////////////////////////////////////////////////////////////////////////

FILTER_COMPILER_DECLARE(vs_acl_ipv4, net4_fast_src, port_src);
FILTER_COMPILER_DECLARE(vs_acl_ipv6, net6_fast_src, port_src);

////////////////////////////////////////////////////////////////////////////////
// Helper functions for rule comparison and sorting
////////////////////////////////////////////////////////////////////////////////

static int
compare_net4(const void *va, const void *vb) {
	const struct net4 *a = (const struct net4 *)va;
	const struct net4 *b = (const struct net4 *)vb;
	// Compare address first
	int addr_cmp = memcmp(a->addr, b->addr, NET4_LEN);
	if (addr_cmp != 0) {
		return addr_cmp;
	}
	// Then compare mask
	return memcmp(a->mask, b->mask, NET4_LEN);
}

static int
compare_net6(const void *va, const void *vb) {
	const struct net6 *a = (const struct net6 *)va;
	const struct net6 *b = (const struct net6 *)vb;
	// Compare address first
	int addr_cmp = memcmp(a->addr, b->addr, NET6_LEN);
	if (addr_cmp != 0) {
		return addr_cmp;
	}
	// Then compare mask
	return memcmp(a->mask, b->mask, NET6_LEN);
}

static int
compare_port_range(const void *va, const void *vb) {
	const struct filter_port_range *a =
		(const struct filter_port_range *)va;
	const struct filter_port_range *b =
		(const struct filter_port_range *)vb;
	if (a->from != b->from) {
		return (a->from < b->from) ? -1 : 1;
	}
	if (a->to != b->to) {
		return (a->to < b->to) ? -1 : 1;
	}
	return 0;
}

// Normalize a single rule by sorting its internal arrays.
// This function expects ABSOLUTE pointers in the rule.
static void
normalize_rule(struct filter_rule *rule) {
	// Sort net4 sources (already absolute pointer)
	if (rule->net4.src_count > 1 && rule->net4.srcs != NULL) {
		qsort(rule->net4.srcs,
		      rule->net4.src_count,
		      sizeof(struct net4),
		      compare_net4);
	}

	// Sort net6 sources (already absolute pointer)
	if (rule->net6.src_count > 1 && rule->net6.srcs != NULL) {
		qsort(rule->net6.srcs,
		      rule->net6.src_count,
		      sizeof(struct net6),
		      compare_net6);
	}

	// Sort transport source ports (already absolute pointer)
	if (rule->transport.src_count > 1 && rule->transport.srcs != NULL) {
		qsort(rule->transport.srcs,
		      rule->transport.src_count,
		      sizeof(struct filter_port_range),
		      compare_port_range);
	}
}

// compare_filter_rules compares two filter rules with ABSOLUTE pointers.
// Used for sorting rules during setup.
static int
compare_filter_rules(const void *va, const void *vb) {
	const struct filter_rule *a = (const struct filter_rule *)va;
	const struct filter_rule *b = (const struct filter_rule *)vb;

	// Compare IPv4 source networks
	if (a->net4.src_count != b->net4.src_count) {
		return (a->net4.src_count < b->net4.src_count) ? -1 : 1;
	}
	if (a->net4.src_count > 0) {
		// Use absolute pointers directly
		for (size_t i = 0; i < a->net4.src_count; ++i) {
			int cmp = compare_net4(
				&a->net4.srcs[i], &b->net4.srcs[i]
			);
			if (cmp != 0) {
				return cmp;
			}
		}
	}

	// Compare IPv6 source networks
	if (a->net6.src_count != b->net6.src_count) {
		return (a->net6.src_count < b->net6.src_count) ? -1 : 1;
	}
	if (a->net6.src_count > 0) {
		// Use absolute pointers directly
		for (size_t i = 0; i < a->net6.src_count; ++i) {
			int cmp = compare_net6(
				&a->net6.srcs[i], &b->net6.srcs[i]
			);
			if (cmp != 0) {
				return cmp;
			}
		}
	}

	// Compare transport source port ranges
	if (a->transport.src_count != b->transport.src_count) {
		return (a->transport.src_count < b->transport.src_count) ? -1
									 : 1;
	}
	if (a->transport.src_count > 0) {
		// Use absolute pointers directly
		for (size_t i = 0; i < a->transport.src_count; ++i) {
			int cmp = compare_port_range(
				&a->transport.srcs[i], &b->transport.srcs[i]
			);
			if (cmp != 0) {
				return cmp;
			}
		}
	}
	return 0;
}

// compare_filter_rules_relative compares two filter rules with RELATIVE
// pointers. Used for comparing rules from previous VS (which are already stored
// with relative pointers).
static int
compare_filter_rules_relative(
	const struct filter_rule *a, const struct filter_rule *b
) {
	// Compare IPv4 source networks
	if (a->net4.src_count != b->net4.src_count) {
		return (a->net4.src_count < b->net4.src_count) ? -1 : 1;
	}
	if (a->net4.src_count > 0) {
		// Convert relative pointers to absolute for comparison
		const struct net4 *a_srcs = ADDR_OF(&a->net4.srcs);
		const struct net4 *b_srcs = ADDR_OF(&b->net4.srcs);
		for (size_t i = 0; i < a->net4.src_count; ++i) {
			int cmp = compare_net4(&a_srcs[i], &b_srcs[i]);
			if (cmp != 0) {
				return cmp;
			}
		}
	}

	// Compare IPv6 source networks
	if (a->net6.src_count != b->net6.src_count) {
		return (a->net6.src_count < b->net6.src_count) ? -1 : 1;
	}
	if (a->net6.src_count > 0) {
		// Convert relative pointers to absolute for comparison
		const struct net6 *a_srcs = ADDR_OF(&a->net6.srcs);
		const struct net6 *b_srcs = ADDR_OF(&b->net6.srcs);
		for (size_t i = 0; i < a->net6.src_count; ++i) {
			int cmp = compare_net6(&a_srcs[i], &b_srcs[i]);
			if (cmp != 0) {
				return cmp;
			}
		}
	}

	// Compare transport source port ranges
	if (a->transport.src_count != b->transport.src_count) {
		return (a->transport.src_count < b->transport.src_count) ? -1
									 : 1;
	}
	if (a->transport.src_count > 0) {
		// Convert relative pointers to absolute for comparison
		const struct filter_port_range *a_srcs =
			ADDR_OF(&a->transport.srcs);
		const struct filter_port_range *b_srcs =
			ADDR_OF(&b->transport.srcs);
		for (size_t i = 0; i < a->transport.src_count; ++i) {
			int cmp = compare_port_range(&a_srcs[i], &b_srcs[i]);
			if (cmp != 0) {
				return cmp;
			}
		}
	}
	return 0;
}

// rules_equal_relative compares two rule arrays where both have RELATIVE
// pointers. Used for comparing current VS rules with previous VS rules.
static bool
rules_equal_relative(
	const struct filter_rule *rules1,
	size_t count1,
	const struct filter_rule *rules2,
	size_t count2
) {
	if (count1 != count2) {
		return false;
	}

	for (size_t i = 0; i < count1; ++i) {
		if (compare_filter_rules_relative(&rules1[i], &rules2[i]) !=
		    0) {
			return false;
		}
	}

	return true;
}

////////////////////////////////////////////////////////////////////////////////

static int
setup_acl(
	struct vs *vs,
	struct vs *prev_vs,
	struct memory_context *mctx,
	struct balancer_update_info *update_info
) {
	// Check if we can reuse ACL from previous VS
	// Both current and previous VS rules have relative pointers at this
	// point
	if (prev_vs != NULL) {
		const struct filter_rule *prev_rules = ADDR_OF(&prev_vs->rules);
		const struct filter_rule *curr_rules = ADDR_OF(&vs->rules);

		if (rules_equal_relative(
			    curr_rules,
			    vs->rules_count,
			    prev_rules,
			    prev_vs->rules_count
		    )) {
			// Reuse ACL
			EQUATE_OFFSET(&vs->acl, &prev_vs->acl);
			prev_vs->acl_reused = 1;

			// Track reuse in update_info
			if (update_info != NULL) {
				size_t idx = update_info->vs_acl_reused_count++;
				update_info->vs_acl_reused[idx] =
					vs->identifier;
			}

			return 0;
		}
	}

	// Need to create new ACL
	vs->acl = memory_balloc(mctx, sizeof(struct filter));
	if (vs->acl == NULL) {
		PUSH_ERROR("no memory");
		return -1;
	}
	vs->acl_reused = 0;

	// Get rules and convert relative pointers to absolute for FILTER_INIT
	struct filter_rule *rules = ADDR_OF(&vs->rules);
	size_t rule_count = vs->rules_count;

	for (size_t i = 0; i < rule_count; ++i) {
		struct filter_rule *rule = &rules[i];
		rule->net4.srcs = ADDR_OF(&rule->net4.srcs);
		rule->net6.srcs = ADDR_OF(&rule->net6.srcs);
		rule->transport.srcs = ADDR_OF(&rule->transport.srcs);
	}

	// Initialize filter with absolute pointers
	int res;
	if (vs->identifier.ip_proto == IPPROTO_IP) {
		res = FILTER_INIT(
			vs->acl, vs_acl_ipv4, rules, rule_count, mctx
		);
	} else { // IPPROTO_IPV6
		res = FILTER_INIT(
			vs->acl, vs_acl_ipv6, rules, rule_count, mctx
		);
	}

	// Restore relative pointers
	for (size_t i = 0; i < rule_count; ++i) {
		struct filter_rule *rule = &rules[i];
		SET_OFFSET_OF(&rule->net4.srcs, rule->net4.srcs);
		SET_OFFSET_OF(&rule->net6.srcs, rule->net6.srcs);
		SET_OFFSET_OF(&rule->transport.srcs, rule->transport.srcs);
	}

	if (res != 0) {
		NEW_ERROR("no memory");
		return -1;
	}

	SET_OFFSET_OF(&vs->acl, vs->acl);

	return 0;
}

static void
vs_free_acl_rules(struct vs *vs, struct memory_context *mctx) {
	if (vs->rules_count == 0) {
		return;
	}

	struct filter_rule *rules = ADDR_OF(&vs->rules);
	if (rules == NULL) {
		return;
	}

	// Free nested arrays in each rule (only sources matter)
	for (size_t i = 0; i < vs->rules_count; ++i) {
		struct filter_rule *rule = &rules[i];

		// Free net4 source arrays
		if (rule->net4.src_count > 0) {
			memory_bfree(
				mctx,
				ADDR_OF(&rule->net4.srcs),
				sizeof(struct net4) * rule->net4.src_count
			);
		}

		// Free net6 source arrays
		if (rule->net6.src_count > 0) {
			memory_bfree(
				mctx,
				ADDR_OF(&rule->net6.srcs),
				sizeof(struct net6) * rule->net6.src_count
			);
		}

		// Free transport source port arrays
		if (rule->transport.src_count > 0) {
			memory_bfree(
				mctx,
				ADDR_OF(&rule->transport.srcs),
				sizeof(struct filter_port_range) *
					rule->transport.src_count
			);
		}
	}

	// Free the rules array itself
	memory_bfree(mctx, rules, sizeof(struct filter_rule) * vs->rules_count);
	vs->rules_count = 0;
	SET_OFFSET_OF(&vs->rules, NULL);
}

static void
rule_to_relative_addresses(struct filter_rule *rule) {
	// net4 src
	SET_OFFSET_OF(&rule->net4.srcs, rule->net4.srcs);

	// net6 src
	SET_OFFSET_OF(&rule->net6.srcs, rule->net6.srcs);

	// transport src
	SET_OFFSET_OF(&rule->transport.srcs, rule->transport.srcs);
}

static int
setup_acl_rules(
	struct vs *vs,
	struct counter_registry *counters,
	struct vs_config *config,
	struct memory_context *mctx
) {
	// Create filter rules from config (already uses memory_balloc and
	// relative pointers)
	struct filter_rule *rules = NULL;
	size_t rules_count = 0;
	if (src_filter_rules(vs, config, &rules, &rules_count, mctx) != 0) {
		PUSH_ERROR("failed to create filter rules");
		return -1;
	}

	// Normalize each rule (sort internal arrays)
	for (size_t i = 0; i < rules_count; ++i) {
		normalize_rule(&rules[i]);
	}

	// Sort the rules array
	if (rules_count > 1) {
		qsort(rules,
		      rules_count,
		      sizeof(struct filter_rule),
		      compare_filter_rules);
	}

	// Remove duplicates
	int last_rule_idx = -1;
	for (size_t rule_idx = 0; rule_idx < rules_count; ++rule_idx) {
		if (last_rule_idx != -1 &&
		    compare_filter_rules(
			    &rules[rule_idx], &rules[last_rule_idx]
		    ) == 0) {
			continue;
		}
		rules[++last_rule_idx] = rules[rule_idx];
	}

	char counter_name[256];
	uint64_t *rule_counters =
		memory_balloc(mctx, sizeof(uint64_t) * rules_count);
	if (rule_counters == NULL && rules_count > 0) {
		NEW_ERROR("failed to allocate rule counters: no memory");
		return -1;
	}

	// Change to relative offsets
	rules_count = last_rule_idx + 1;
	for (size_t i = 0; i < rules_count; ++i) {
		rule_to_relative_addresses(&rules[i]);

		uint32_t allowed_src_idx = rules[i].action;
		const char *rule_tag = config->allowed_src[allowed_src_idx].tag;

		// Validate tag before using it
		if (validate_tag(rule_tag) != 0) {
			PUSH_ERROR(
				"invalid tag at allowed_src index %u",
				allowed_src_idx
			);
			return -1;
		}

		if (rule_tag != NULL) {
			// register counter
			sprintf(counter_name,
				"acl_%zu_%s",
				vs->registry_idx,
				rule_tag);
			uint64_t counter_id = counter_registry_register(
				counters, counter_name, 1
			);
			if (counter_id == (uint64_t)-1) {
				NEW_ERROR("failed to register counter for "
					  "rule: no memory");
				return -1;
			}

			rule_counters[i] = counter_id;
		} else {
			// counter is undefined, because tag not specified
			rule_counters[i] = (uint64_t)-1;
		}

		// store actions equal rule stable index
		rules[i].action = i;
	}

	// Store using relative pointers
	SET_OFFSET_OF(&vs->rules, rules);
	SET_OFFSET_OF(&vs->rule_counters, rule_counters);
	vs->rules_count = rules_count;

	return 0;
}

int
vs_with_identifier_and_registry_idx_init(
	struct vs *vs,
	struct vs *prev_vs,
	size_t first_real_idx,
	struct real *reals,
	struct balancer_state *balancer_state,
	struct named_vs_config *config,
	struct counter_registry *counters,
	struct memory_context *mctx,
	struct balancer_update_info *update_info
) {
	if (setup_flags(vs, config) != 0) {
		PUSH_ERROR("failed to setup flags");
		return -1;
	}

	if (setup_peers(vs, mctx, &config->config) != 0) {
		PUSH_ERROR("failed to setup peers");
		return -1;
	}

	if (setup_reals(vs, &config->config, first_real_idx, reals) != 0) {
		PUSH_ERROR("failed to setup reals");
		goto free_peers;
	}

	if (setup_selector(vs, balancer_state, mctx, &config->config) != 0) {
		PUSH_ERROR("failed to setup selector");
		goto free_peers;
	}

	if (register_counter(vs, counters) != 0) {
		PUSH_ERROR("failed to register counter");
		goto free_selector;
	}

	if (setup_acl_rules(vs, counters, &config->config, mctx) != 0) {
		PUSH_ERROR("failed to store acl rules");
		goto free_selector;
	}

	if (setup_acl(vs, prev_vs, mctx, update_info) != 0) {
		PUSH_ERROR("failed to setup acl");
		goto free_acl_rules;
	}

	return 0;

free_acl_rules:
	vs_free_acl_rules(vs, mctx);

free_selector:
	selector_free(&vs->selector);

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
vs_free(struct vs *vs, struct memory_context *mctx) {
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
	selector_free(&vs->selector);
}

int
vs_update_reals(struct vs *vs) {
	if (selector_update(
		    &vs->selector, vs->reals_count, ADDR_OF(&vs->reals)
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

////////////////////////////////////////////////////////////////////////////////

static void
setup_reals_usage(
	struct reals_usage *reals_usage, size_t workers, size_t reals_count
) {
	reals_usage->counters_usage =
		sizeof(struct real_stats) * workers * reals_count;
	reals_usage->data_usage = sizeof(struct real) * reals_count;
	reals_usage->total_usage =
		reals_usage->counters_usage + reals_usage->data_usage;
}

void
vs_fill_inspect(struct vs *vs, struct vs_inspect *inspect, size_t workers) {
	inspect->acl_usage = filter_memory_usage(ADDR_OF(&vs->acl));
	inspect->ring_usage = selector_memory_usage(&vs->selector);
	inspect->counters_usage = sizeof(struct vs_stats) * workers;
	setup_reals_usage(&inspect->reals_usage, workers, vs->reals_count);
	inspect->other_usage =
		rules_memory_usage(vs->rules_count, ADDR_OF(&vs->rules)) +
		sizeof(struct filter_rule) * vs->rules_count;
	inspect->other_usage += vs->peers_v4_count * sizeof(struct net4_addr);
	inspect->other_usage += vs->peers_v6_count * sizeof(struct net6_addr);
	inspect->total_usage = inspect->acl_usage + inspect->ring_usage +
			       inspect->counters_usage +
			       inspect->reals_usage.total_usage +
			       inspect->other_usage;
}

ssize_t
parse_vs_acl_counter(struct counter_handle *counter, const char **tag) {
	// in format acl_<vs_registry_idx>_<tag>
	if (strncmp(counter->name, "acl_", 4) == 0) { // vs acl counter
		char *end_ptr = NULL;
		size_t vs_registry_idx =
			strtoull(counter->name + 4, &end_ptr, 10);
		*tag = end_ptr + 1; // Point to the tag string in counter name
		return vs_registry_idx;
	} else {
		return -1;
	}
}