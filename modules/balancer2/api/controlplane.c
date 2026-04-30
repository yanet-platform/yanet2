#include "controlplane.h"

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "common/rcu.h"
#include "common/rng.h"
#include "common/ttlmap/ttlmap.h"

#include "errors.h"
#include "filter/compiler.h"
#include "filter/rule.h"

#include "modules/balancer2/dataplane/config.h"
#include "modules/balancer2/dataplane/real.h"
#include "modules/balancer2/dataplane/selector.h"
#include "modules/balancer2/dataplane/session.h"
#include "modules/balancer2/dataplane/types/stats.h"
#include "modules/balancer2/dataplane/vs.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/controlplane/config/cp_module.h"
#include "lib/dataplane/config/zone.h"

#include <arpa/inet.h>
#include <assert.h>
#include <netinet/in.h>
#include <stdalign.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const char *agent_alloc_failed = "agent: allocation failed";
static const char *heap_alloc_failed = "allocation failed";

#define VS_PREFIX_LEN 64

const char *const balancer_vs_counter_prefix = "vs";
const char *const balancer_vs_acl_counter_prefix = "vs_acl";
const char *const balancer_real_counter_prefix = "real";
const char *const balancer_common_counter_name = "common";
const char *const balancer_l4_counter_name = "l4";

FILTER_COMPILER_DECLARE(vs_acl_ip4, net4_fast_src, port_fast_src);
FILTER_COMPILER_DECLARE(vs_acl_ip6, net6_fast_src, port_fast_src);

FILTER_COMPILER_DECLARE(
	vs_matcher_ip4, net4_fast_dst, port_fast_dst, proto_range
);
FILTER_COMPILER_DECLARE(
	vs_matcher_ip6, net6_fast_dst, port_fast_dst, proto_range
);

struct balancer_handle {
	struct balancer_module_config module_config;
};

static size_t
real_selector_size(size_t workers) {
	return sizeof(struct real_selector) +
	       sizeof(struct rr_counter) * workers;
}

static void
vs_prefix(const struct balancer_vs_config *config, char *buf, size_t buf_size) {
	char addr_str[INET6_ADDRSTRLEN];
	if (config->ip_family == ip_family_ip4) {
		inet_ntop(
			AF_INET,
			config->dst.v4.bytes,
			addr_str,
			sizeof(addr_str)
		);
	} else {
		inet_ntop(
			AF_INET6,
			config->dst.v6.bytes,
			addr_str,
			sizeof(addr_str)
		);
	}
	snprintf(
		buf,
		buf_size,
		"%s:%u/%s",
		addr_str,
		config->port,
		config->transport == transport_proto_tcp ? "tcp" : "udp"
	);
}

static void
real_addr_str(
	const struct balancer_real_config *config, char *buf, size_t buf_size
) {
	if (config->ip_family == ip_family_ip4) {
		inet_ntop(AF_INET, config->dst.v4.bytes, buf, buf_size);
	} else {
		inet_ntop(AF_INET6, config->dst.v6.bytes, buf, buf_size);
	}
}

static bool
mask_is_prefix(const uint8_t *mask, size_t len) {
	bool seen_zero = false;
	for (size_t byte_idx = 0; byte_idx < len; ++byte_idx) {
		for (int bit_pos = 7; bit_pos >= 0; --bit_pos) {
			bool bit = (mask[byte_idx] >> bit_pos) & 1;
			if (bit && seen_zero) {
				return false;
			}
			if (!bit) {
				seen_zero = true;
			}
		}
	}
	return true;
}

static int
validate_port_ranges(
	const struct filter_port_ranges *ranges, yanet_error **error
) {
	for (uint32_t idx = 0; idx < ranges->count; ++idx) {
		const struct filter_port_range *pr = &ranges->items[idx];
		if (pr->from > pr->to) {
			yanet_error_add(
				error,
				"port_ranges[%u]: from %u exceeds to %u",
				idx,
				pr->from,
				pr->to
			);
			return -1;
		}
	}
	return 0;
}

static int
validate_net4s(const struct filter_net4s *nets, yanet_error **error) {
	for (uint32_t idx = 0; idx < nets->count; ++idx) {
		if (!mask_is_prefix(nets->items[idx].mask, NET4_LEN)) {
			yanet_error_add(
				error, "net4s[%u]: non-prefix mask", idx
			);
			return -1;
		}
	}
	return 0;
}

static int
validate_net6s(const struct filter_net6s *nets, yanet_error **error) {
	for (uint32_t idx = 0; idx < nets->count; ++idx) {
		const uint8_t *mask = nets->items[idx].mask;
		if (!mask_is_prefix(mask, NET6_LEN / 2)) {
			yanet_error_add(
				error, "net6s[%u]: non-prefix high mask", idx
			);
			return -1;
		}
		if (!mask_is_prefix(mask + NET6_LEN / 2, NET6_LEN / 2)) {
			yanet_error_add(
				error, "net6s[%u]: non-prefix low mask", idx
			);
			return -1;
		}
	}
	return 0;
}

static int
validate_allowed_sources(
	const struct balancer_allowed_sources *src,
	enum ip_family ip_family,
	yanet_error **error
) {
	if (validate_port_ranges(&src->port_ranges, error) != 0) {
		return -1;
	}

	if (ip_family == ip_family_ip4) {
		if (src->net6s.count != 0) {
			yanet_error_add(
				error, "ipv4 family with non-empty net6s"
			);
			return -1;
		}
		return validate_net4s(&src->net4s, error);
	}

	if (ip_family == ip_family_ip6) {
		if (src->net4s.count != 0) {
			yanet_error_add(
				error, "ipv6 family with non-empty net4s"
			);
			return -1;
		}
		return validate_net6s(&src->net6s, error);
	}

	yanet_error_add(error, "unexpected ip family: %d", ip_family);
	return -1;
}

static void
fill_acl_rule(
	struct filter_rule *rule,
	uint32_t action,
	const struct balancer_allowed_sources *src,
	enum ip_family family
) {
	rule->action = action;

	if (family == ip_family_ip4) {
		rule->net4.src_count = src->net4s.count;
		rule->net4.srcs = src->net4s.items;
	} else {
		rule->net6.src_count = src->net6s.count;
		rule->net6.srcs = src->net6s.items;
	}

	rule->transport.src_count = src->port_ranges.count;
	rule->transport.srcs = src->port_ranges.items;
}

static int
build_vs_acl(
	struct memory_context *mctx,
	struct virtual_service *vs,
	const struct balancer_vs_config *config,
	yanet_error **error
) {
	const struct filter_compiler *compiler =
		config->ip_family == ip_family_ip4 ? vs_acl_ip4 : vs_acl_ip6;

	size_t rule_count = config->allowed_sources_count;
	if (rule_count == 0) {
		if (filter_init(&vs->acl, compiler, NULL, 0, mctx) != 0) {
			yanet_error_add(error, "compilation failed");
			return -1;
		}
		return 0;
	}

	int res = -1;

	struct filter_rule *rules = calloc(rule_count, sizeof(*rules));
	const struct filter_rule **rule_ptrs =
		malloc(rule_count * sizeof(*rule_ptrs));
	if (rules == NULL || rule_ptrs == NULL) {
		yanet_error_add(error, "%s", heap_alloc_failed);
		goto cleanup;
	}

	for (size_t idx = 0; idx < rule_count; ++idx) {
		if (validate_allowed_sources(
			    &config->allowed_sources[idx],
			    config->ip_family,
			    error
		    ) != 0) {
			yanet_error_add(
				error, "allowed sources at index %zu", idx
			);
			goto cleanup;
		}
		fill_acl_rule(
			&rules[idx],
			(uint32_t)idx,
			&config->allowed_sources[idx],
			config->ip_family
		);
		rule_ptrs[idx] = &rules[idx];
	}

	res = filter_init(
		&vs->acl, compiler, rule_ptrs, (uint32_t)rule_count, mctx
	);
	if (res != 0) {
		yanet_error_add(error, "compilation failed");
	}

cleanup:
	free(rules);
	free(rule_ptrs);

	return res;
}

static void
build_real(struct real *real, const struct balancer_real_config *config) {
	memset(real, 0, sizeof(*real));

	switch (config->ip_family) {
	case ip_family_ip4:
		real->addr.v4 = config->dst.v4;
		real->src.v4 = config->src.v4;
		/*
		 * The dataplane requires the source network to
		 * have host bits cleared (addr & mask == addr) so that client
		 * source bits can be embedded directly into the unmasked
		 * positions. Mask here so callers do not need to pre-mask.
		 */
		for (size_t i = 0; i < NET4_LEN; ++i) {
			real->src.v4.addr[i] &= real->src.v4.mask[i];
		}
		break;
	case ip_family_ip6:
		real->addr.v6 = config->dst.v6;
		real->src.v6 = config->src.v6;
		for (size_t i = 0; i < NET6_LEN; ++i) {
			real->src.v6.addr[i] &= real->src.v6.mask[i];
		}
		real->flags |= real_ip6;
		break;
	}

	/*
	 * Reals start disabled. The controlplane must call
	 * balancer_vs_update_real_states with state=true before traffic
	 * is forwarded to a given real.
	 */

	real->counter_id = COUNTER_INVALID;
}

static int
build_vs_reals(
	struct memory_context *mctx,
	struct virtual_service *vs,
	const struct balancer_vs_config *config,
	yanet_error **error
) {
	struct real *reals =
		memory_balloc(mctx, sizeof(struct real) * config->real_count);
	if (reals == NULL && config->real_count > 0) {
		yanet_error_add(error, "%s", agent_alloc_failed);
		return -1;
	}
	for (size_t idx = 0; idx < config->real_count; ++idx) {
		build_real(reals + idx, config->reals + idx);
	}

	vs->reals_count = config->real_count;
	SET_OFFSET_OF(&vs->reals, reals);
	return 0;
}

static uint8_t
build_vs_flags(const struct balancer_vs_config *config) {
	uint8_t flags = 0;
	if (config->scheduler == balancer_vs_sched_op) {
		flags |= vs_ops;
	}
	if (config->fix_mss) {
		flags |= vs_fix_mss;
	}
	if (config->tunnel == balancer_tunnel_kind_gre) {
		flags |= vs_gre;
	}
	if (config->ip_family == ip_family_ip6) {
		flags |= vs_ip6;
	}
	return flags;
}

static int
build_real_selector(
	struct memory_context *mctx,
	struct virtual_service *vs,
	size_t workers,
	const struct balancer_vs_config *config,
	yanet_error **error
) {
	const size_t size = real_selector_size(workers);
	struct real_selector *selector = memory_balloc(mctx, size);
	if (selector == NULL) {
		yanet_error_add(error, "%s", agent_alloc_failed);
		return -1;
	}
	memset(selector, 0, size);
	if (config->scheduler == balancer_vs_sched_sh) {
		selector->packet_hash_mask = (uint64_t)-1;
	}

	/* Rings are built lazily on the first weight update. */
	SET_OFFSET_OF(&vs->selector, selector);
	return 0;
}

static int
register_acl_counters(
	struct memory_context *mctx,
	struct counter_registry *registry,
	struct virtual_service *vs,
	const char *prefix,
	const struct balancer_allowed_sources *sources,
	size_t source_count,
	yanet_error **error
) {
	uint64_t *ids = memory_balloc(mctx, sizeof(uint64_t) * source_count);
	if (ids == NULL && source_count > 0) {
		yanet_error_add(error, "%s", agent_alloc_failed);
		return -1;
	}
	for (size_t idx = 0; idx < source_count; ++idx) {
		if (sources[idx].tag == NULL) {
			ids[idx] = COUNTER_INVALID;
			continue;
		}
		char name[COUNTER_NAME_LEN];
		int written = snprintf(
			name,
			sizeof(name),
			"%s_%s_%s",
			balancer_vs_acl_counter_prefix,
			prefix,
			sources[idx].tag
		);
		if (written < 0 || (size_t)written >= sizeof(name)) {
			yanet_error_add(
				error, "rule[%zu]: tag is too long", idx
			);
			memory_bfree(
				mctx, ids, sizeof(uint64_t) * source_count
			);
			return -1;
		}
		ids[idx] = counter_registry_register(registry, name, 1, error);
		if (ids[idx] == COUNTER_INVALID) {
			yanet_error_add(error, "rule[%zu]", idx);
			memory_bfree(
				mctx, ids, sizeof(uint64_t) * source_count
			);
			return -1;
		}
	}
	vs->rule_count = source_count;
	SET_OFFSET_OF(&vs->rule_counter_ids, ids);
	return 0;
}

static int
register_real_counters(
	struct counter_registry *registry,
	struct virtual_service *vs,
	const char *prefix,
	const struct balancer_real_config *real_configs,
	yanet_error **error
) {
	struct real *reals = ADDR_OF(&vs->reals);
	for (size_t idx = 0; idx < vs->reals_count; ++idx) {
		char dst_str[INET6_ADDRSTRLEN];
		real_addr_str(&real_configs[idx], dst_str, sizeof(dst_str));

		char name[COUNTER_NAME_LEN];
		snprintf(
			name,
			sizeof(name),
			"%s_%s_%s",
			balancer_real_counter_prefix,
			prefix,
			dst_str
		);
		reals[idx].counter_id = counter_registry_register(
			registry,
			name,
			sizeof(struct balancer_real_stats) / sizeof(uint64_t),
			error
		);
		if (reals[idx].counter_id == COUNTER_INVALID) {
			yanet_error_add(error, "real[%zu]", idx);
			return -1;
		}
	}
	return 0;
}

static int
register_vs_counters(
	struct memory_context *mctx,
	struct counter_registry *registry,
	struct virtual_service *vs,
	const struct balancer_vs_config *config,
	yanet_error **error
) {
	char prefix[VS_PREFIX_LEN];
	vs_prefix(config, prefix, sizeof(prefix));

	char name[COUNTER_NAME_LEN];
	snprintf(
		name, sizeof(name), "%s_%s", balancer_vs_counter_prefix, prefix
	);
	vs->counter_id = counter_registry_register(
		registry,
		name,
		sizeof(struct balancer_vs_stats) / sizeof(uint64_t),
		error
	);
	if (vs->counter_id == COUNTER_INVALID) {
		return -1;
	}

	if (register_acl_counters(
		    mctx,
		    registry,
		    vs,
		    prefix,
		    config->allowed_sources,
		    config->allowed_sources_count,
		    error
	    ) != 0) {
		return -1;
	}

	if (register_real_counters(
		    registry, vs, prefix, config->reals, error
	    ) != 0) {
		return -1;
	}

	return 0;
}

static void
free_vs_reals(struct memory_context *mctx, struct virtual_service *vs) {
	struct real *reals = ADDR_OF(&vs->reals);
	if (reals == NULL) {
		return;
	}
	memory_bfree(mctx, reals, sizeof(struct real) * vs->reals_count);
	SET_OFFSET_OF(&vs->reals, NULL);
	vs->reals_count = 0;
}

static void
free_vs_acl(struct memory_context *mctx, struct virtual_service *vs) {
	if (vs->flags & vs_ip6) {
		filter_free(&vs->acl, vs_acl_ip6);
	} else {
		filter_free(&vs->acl, vs_acl_ip4);
	}

	uint64_t *ids = ADDR_OF(&vs->rule_counter_ids);
	if (ids != NULL) {
		memory_bfree(mctx, ids, sizeof(uint64_t) * vs->rule_count);
		SET_OFFSET_OF(&vs->rule_counter_ids, NULL);
		vs->rule_count = 0;
	}
}

static void
free_real_selector(
	struct memory_context *mctx, struct virtual_service *vs, size_t workers
) {
	struct real_selector *selector = ADDR_OF(&vs->selector);
	if (selector == NULL) {
		return;
	}
	memory_bfree(mctx, selector, real_selector_size(workers));
	SET_OFFSET_OF(&vs->selector, NULL);
}

static int
init_vs(struct memory_context *mctx,
	struct counter_registry *registry,
	struct virtual_service *vs,
	const struct balancer_vs_config *config,
	size_t workers,
	yanet_error **error) {
	memset(vs, 0, sizeof(*vs));
	vs->counter_id = COUNTER_INVALID;

	vs->flags = build_vs_flags(config);

	if (build_vs_acl(mctx, vs, config, error) != 0) {
		yanet_error_add(error, "acl");
		return -1;
	}

	if (build_vs_reals(mctx, vs, config, error) != 0) {
		yanet_error_add(error, "real");
		goto err_acl;
	}

	if (build_real_selector(mctx, vs, workers, config, error) != 0) {
		goto err_reals;
	}

	if (register_vs_counters(mctx, registry, vs, config, error) != 0) {
		yanet_error_add(error, "register counters");
		goto err_selector;
	}

	return 0;

err_selector:
	free_real_selector(mctx, vs, workers);
err_reals:
	free_vs_reals(mctx, vs);
err_acl:
	free_vs_acl(mctx, vs);
	return -1;
}

static void
free_vs(struct memory_context *mctx, struct virtual_service *vs, size_t workers
) {
	free_vs_reals(mctx, vs);
	free_vs_acl(mctx, vs);
	free_real_selector(mctx, vs, workers);
}

static int
register_balancer_counters(
	struct balancer_module_config *cfg,
	struct counter_registry *registry,
	yanet_error **error
) {
	cfg->common_counter_id = counter_registry_register(
		registry,
		balancer_common_counter_name,
		sizeof(struct balancer_common_stats) / sizeof(uint64_t),
		error
	);
	if (cfg->common_counter_id == COUNTER_INVALID) {
		return -1;
	}
	cfg->l4_counter_id = counter_registry_register(
		registry,
		balancer_l4_counter_name,
		sizeof(struct balancer_l4_stats) / sizeof(uint64_t),
		error
	);
	if (cfg->l4_counter_id == COUNTER_INVALID) {
		return -1;
	}
	return 0;
}

static int
validate_vs(const struct balancer_vs_config *vs, yanet_error **error) {
	if (vs->ip_family != ip_family_ip4 && vs->ip_family != ip_family_ip6) {
		yanet_error_add(
			error, "unexpected network family: %d", vs->ip_family
		);
		return -1;
	}
	if (vs->transport != transport_proto_udp &&
	    vs->transport != transport_proto_tcp) {
		yanet_error_add(
			error, "unexpected transport proto: %d", vs->transport
		);
		return -1;
	}
	if (vs->scheduler != balancer_vs_sched_wrr &&
	    vs->scheduler != balancer_vs_sched_sh &&
	    vs->scheduler != balancer_vs_sched_op) {
		yanet_error_add(
			error, "unexpected scheduler: %d", vs->scheduler
		);
		return -1;
	}
	if (vs->tunnel != balancer_tunnel_kind_gre &&
	    vs->tunnel != balancer_tunnel_kind_ip) {
		yanet_error_add(
			error, "unexpected tunnel kind: %d", vs->tunnel
		);
		return -1;
	}
	for (size_t idx = 0; idx < vs->real_count; ++idx) {
		enum ip_family family = vs->reals[idx].ip_family;
		if (family != ip_family_ip4 && family != ip_family_ip6) {
			yanet_error_add(
				error,
				"real[%zu]: unexpected network family: %d",
				idx,
				family
			);
			return -1;
		}
	}
	return 0;
}

static struct virtual_service *
build_vs_array(
	struct memory_context *mctx,
	struct counter_registry *registry,
	const struct balancer_vs_config *configs,
	size_t count,
	size_t workers,
	yanet_error **error
) {
	struct virtual_service *vs =
		memory_balloc(mctx, sizeof(struct virtual_service) * count);
	if (vs == NULL && count > 0) {
		yanet_error_add(error, "%s", agent_alloc_failed);
		return NULL;
	}
	for (size_t idx = 0; idx < count; ++idx) {
		const struct balancer_vs_config *vs_config = &configs[idx];
		if (validate_vs(vs_config, error) != 0 ||
		    init_vs(mctx, registry, &vs[idx], vs_config, workers, error
		    ) != 0) {
			yanet_error_add(error, "vs[%zu]", idx);
			for (size_t j = 0; j < idx; ++j) {
				free_vs(mctx, &vs[j], workers);
			}
			memory_bfree(
				mctx, vs, sizeof(struct virtual_service) * count
			);
			return NULL;
		}
	}
	return vs;
}

static void
free_vs_array(
	struct memory_context *mctx,
	struct virtual_service *vs,
	size_t count,
	size_t workers
) {
	for (size_t idx = 0; idx < count; ++idx) {
		free_vs(mctx, &vs[idx], workers);
	}
	memory_bfree(mctx, vs, sizeof(struct virtual_service) * count);
}

static void
free_matcher_rule(struct filter_rule *rule) {
	free(rule->net4.dsts);
	free(rule->net6.dsts);
	free(rule->transport.dsts);
	free(rule->transport.protos);
}

static int
fill_matcher_rule(
	struct filter_rule *rule,
	uint32_t action,
	const struct balancer_vs_config *cfg,
	yanet_error **error
) {
	rule->action = action;

	if (cfg->ip_family == ip_family_ip4) {
		struct net4 *n4 = malloc(sizeof(struct net4));
		if (n4 == NULL) {
			yanet_error_add(error, "%s", heap_alloc_failed);
			goto err;
		}
		memcpy(n4->addr, cfg->dst.v4.bytes, NET4_LEN);
		memset(n4->mask, 0xFF, NET4_LEN);
		rule->net4.dst_count = 1;
		rule->net4.dsts = n4;
	} else {
		struct net6 *n6 = malloc(sizeof(struct net6));
		if (n6 == NULL) {
			yanet_error_add(error, "%s", heap_alloc_failed);
			goto err;
		}
		memcpy(n6->addr, cfg->dst.v6.bytes, NET6_LEN);
		memset(n6->mask, 0xFF, NET6_LEN);
		rule->net6.dst_count = 1;
		rule->net6.dsts = n6;
	}

	struct filter_port_range *port_range =
		malloc(sizeof(struct filter_port_range));
	if (port_range == NULL) {
		yanet_error_add(error, "%s", heap_alloc_failed);
		goto err;
	}
	if (cfg->port != 0) {
		port_range->from = cfg->port;
		port_range->to = cfg->port;
	} else {
		port_range->from = 0;
		port_range->to = 65535;
	}
	rule->transport.dst_count = 1;
	rule->transport.dsts = port_range;

	struct filter_proto_range *proto_range =
		malloc(sizeof(struct filter_proto_range));
	if (proto_range == NULL) {
		yanet_error_add(error, "%s", heap_alloc_failed);
		goto err;
	}

	uint16_t transport = cfg->transport == transport_proto_tcp
				     ? IPPROTO_TCP
				     : IPPROTO_UDP;
	proto_range->from = transport * 256;
	proto_range->to = transport * 256 + 255;
	rule->transport.proto_count = 1;
	rule->transport.protos = proto_range;

	return 0;

err:
	free_matcher_rule(rule);
	return -1;
}

static int
build_vs_matcher(
	struct memory_context *mctx,
	struct filter *matcher,
	enum ip_family family,
	const struct balancer_vs_config *vs,
	size_t vs_count,
	yanet_error **error
) {
	const struct filter_compiler *compiler =
		family == ip_family_ip4 ? vs_matcher_ip4 : vs_matcher_ip6;

	size_t rule_count = 0;
	for (size_t idx = 0; idx < vs_count; ++idx) {
		if (vs[idx].ip_family == family) {
			++rule_count;
		}
	}
	if (rule_count == 0) {
		if (filter_init(matcher, compiler, NULL, 0, mctx) != 0) {
			yanet_error_add(error, "compilation failed");
			return -1;
		}
		return 0;
	}

	int res = -1;
	size_t filled = 0;

	struct filter_rule *rules = calloc(rule_count, sizeof(*rules));
	const struct filter_rule **rule_ptrs =
		malloc(rule_count * sizeof(*rule_ptrs));
	if (rules == NULL || rule_ptrs == NULL) {
		yanet_error_add(error, "%s", heap_alloc_failed);
		goto cleanup;
	}

	for (size_t idx = 0; idx < vs_count; ++idx) {
		if (vs[idx].ip_family != family) {
			continue;
		}
		if (fill_matcher_rule(
			    &rules[filled], (uint32_t)idx, &vs[idx], error
		    ) != 0) {
			yanet_error_add(error, "vs[%zu]", idx);
			goto cleanup;
		}
		rule_ptrs[filled] = &rules[filled];
		++filled;
	}

	res = filter_init(
		matcher, compiler, rule_ptrs, (uint32_t)rule_count, mctx
	);

cleanup:
	for (size_t idx = 0; idx < filled; ++idx) {
		free_matcher_rule(&rules[idx]);
	}
	free(rules);
	free(rule_ptrs);

	return res;
}

static int
build_vs_matchers(
	struct memory_context *mctx,
	struct balancer_module_config *cfg,
	const struct balancer_vs_config *configs,
	size_t count,
	yanet_error **error
) {
	if (build_vs_matcher(
		    mctx,
		    &cfg->vs_matcher_ip4,
		    ip_family_ip4,
		    configs,
		    count,
		    error
	    ) != 0) {
		return -1;
	}

	if (build_vs_matcher(
		    mctx,
		    &cfg->vs_matcher_ip6,
		    ip_family_ip6,
		    configs,
		    count,
		    error
	    ) != 0) {
		filter_free(&cfg->vs_matcher_ip4, vs_matcher_ip4);
		return -1;
	}

	return 0;
}

static void
free_vs_matchers(struct balancer_module_config *cfg) {
	filter_free(&cfg->vs_matcher_ip4, vs_matcher_ip4);
	filter_free(&cfg->vs_matcher_ip6, vs_matcher_ip6);
}

static void
free_module_config(struct agent *agent, struct balancer_module_config *cfg) {
	const size_t workers = ADDR_OF(&agent->dp_config)->worker_count;
	struct memory_context *mctx = &agent->memory_context;
	free_vs_matchers(cfg);
	free_vs_array(mctx, ADDR_OF(&cfg->vs), cfg->vs_count, workers);
	rcu_free(&cfg->rcu, mctx);
	cp_module_fini(&cfg->cp_module);
}

static int
init_module_config(
	struct agent *agent,
	struct balancer_module_config *cfg,
	const char *name,
	struct balancer_session_table_chain *session_table_chain,
	struct balancer_session_timeouts *timeouts,
	const struct balancer_vs_config *vs_configs,
	size_t vs_count,
	yanet_error **error
) {
	struct memory_context *mctx = &agent->memory_context;
	const size_t workers = ADDR_OF(&agent->dp_config)->worker_count;

	if (workers == 0) {
		yanet_error_add(error, "zero workers");
		return -1;
	}

	if (cp_module_init(&cfg->cp_module, agent, "balancer", name, error) !=
	    0) {
		return -1;
	}

	struct counter_registry *registry = &cfg->cp_module.counter_registry;

	if (register_balancer_counters(cfg, registry, error) != 0) {
		yanet_error_add(error, "register counters");
		free_module_config(agent, cfg);
		return -1;
	}

	if (vs_count > 0) {
		struct virtual_service *vs = build_vs_array(
			mctx, registry, vs_configs, vs_count, workers, error
		);
		if (vs == NULL) {
			free_module_config(agent, cfg);
			return -1;
		}
		SET_OFFSET_OF(&cfg->vs, vs);
		cfg->vs_count = (uint32_t)vs_count;
	}

	if (build_vs_matchers(mctx, cfg, vs_configs, vs_count, error) != 0) {
		yanet_error_add(error, "VS matcher");
		free_module_config(agent, cfg);
		return -1;
	}

	if (rcu_init(&cfg->rcu, mctx, workers) != 0) {
		yanet_error_add(error, "%s", agent_alloc_failed);
		free_module_config(agent, cfg);
		return -1;
	}

	SET_OFFSET_OF(&cfg->st_chain, session_table_chain);
	cfg->session_timeouts = *timeouts;

	return 0;
}

struct balancer_handle *
balancer_create(
	struct agent *agent,
	const char *name,
	struct balancer_session_table_chain *session_table_chain,
	struct balancer_session_timeouts *timeouts,
	const struct balancer_vs_config *vs_configs,
	uint32_t vs_count,
	yanet_error **error
) {
	yanet_error_reset(error);

	if (session_table_chain == NULL) {
		yanet_error_add(error, "missing session table chain");
		return NULL;
	}
	if (timeouts == NULL) {
		yanet_error_add(error, "missing session timeouts");
		return NULL;
	}

	struct memory_context *mctx = &agent->memory_context;

	struct balancer_handle *handle = memory_balloc(mctx, sizeof(*handle));
	if (handle == NULL) {
		yanet_error_add(error, "%s", agent_alloc_failed);
		return NULL;
	}
	memset(handle, 0, sizeof(*handle));

	if (init_module_config(
		    agent,
		    &handle->module_config,
		    name,
		    session_table_chain,
		    timeouts,
		    vs_configs,
		    vs_count,
		    error
	    ) != 0) {
		memory_bfree(mctx, handle, sizeof(*handle));
		return NULL;
	}

	return handle;
}

int
balancer_install(
	struct agent *agent, struct balancer_handle *handle, yanet_error **error
) {
	yanet_error_reset(error);

	struct cp_module *module = &handle->module_config.cp_module;
	return agent_update_modules(agent, 1, &module, error);
}

void
balancer_free(struct agent *agent, struct balancer_handle *handle) {
	if (handle == NULL) {
		return;
	}
	struct memory_context *mctx = &agent->memory_context;
	struct balancer_module_config *cfg = &handle->module_config;
	free_module_config(agent, cfg);
	memory_bfree(mctx, handle, sizeof(*handle));
}

/* This procedure does not respect disabled reals.
 * It is expected user manually sets zero weights for disabled reals
 * if needed.
 */
static int
build_ring(
	struct ring *ring,
	const uint32_t *weights,
	uint32_t reals_count,
	struct memory_context *mctx,
	uint64_t shuffle_seed,
	yanet_error **error
) {
	uint64_t total_weight = 0;
	for (uint32_t i = 0; i < reals_count; ++i) {
		total_weight += weights[i];
	}

	if (total_weight == 0) {
		memset(ring, 0, sizeof(*ring));
		return 0;
	}

	size_t bytes = total_weight * sizeof(uint32_t);
	if (big_array_init(&ring->real_ids, bytes, mctx) != 0) {
		yanet_error_add(error, "%s", agent_alloc_failed);
		return -1;
	}

	size_t pos = 0;
	for (uint32_t i = 0; i < reals_count; ++i) {
		for (uint32_t j = 0; j < weights[i]; ++j) {
			uint32_t *slot = big_array_get(
				&ring->real_ids, pos * sizeof(uint32_t)
			);
			*slot = i;
			++pos;
		}
	}

	uint64_t rng = 0xdeadbeef ^ shuffle_seed;
	for (size_t i = pos; i > 1; --i) {
		uint32_t *a = big_array_get(
			&ring->real_ids, (i - 1) * sizeof(uint32_t)
		);
		uint32_t *b = big_array_get(
			&ring->real_ids, (rng % i) * sizeof(uint32_t)
		);
		uint32_t tmp = *a;
		*a = *b;
		*b = tmp;
		rng = rng_next(&rng);
	}

	return 0;
}

int
balancer_vs_update_real_weights(
	struct balancer_handle *balancer,
	uint32_t vs_idx,
	const uint32_t *weights,
	yanet_error **error
) {
	yanet_error_reset(error);

	struct balancer_module_config *cfg = &balancer->module_config;
	if (vs_idx >= cfg->vs_count) {
		yanet_error_add(
			error,
			"index %u exceeds number of virtual services %u",
			vs_idx,
			cfg->vs_count
		);
		return -1;
	}

	struct memory_context *mctx = &cfg->cp_module.agent->memory_context;
	struct virtual_service *vs = ADDR_OF(&cfg->vs) + vs_idx;
	struct real_selector *selector = ADDR_OF(&vs->selector);

	size_t cur_ring =
		atomic_load_explicit(&selector->ring_id, memory_order_relaxed);
	size_t new_ring = cur_ring ^ 1;

	if (build_ring(
		    &selector->rings[new_ring],
		    weights,
		    vs->reals_count,
		    mctx,
		    vs_idx,
		    error
	    ) != 0) {
		yanet_error_add(error, "build ring");
		return -1;
	}

	/* Blocks until all workers see new value (or will see on demand) */
	rcu_update(&cfg->rcu, &selector->ring_id, new_ring);

	big_array_free(&selector->rings[cur_ring].real_ids);

	return 0;
}

int
balancer_vs_update_real_states(
	struct balancer_handle *balancer,
	uint32_t vs_idx,
	const bool *states,
	yanet_error **error
) {
	yanet_error_reset(error);

	struct balancer_module_config *cfg = &balancer->module_config;
	if (vs_idx >= cfg->vs_count) {
		yanet_error_add(
			error,
			"index %u exceeds number of virtual services %u",
			vs_idx,
			cfg->vs_count
		);
		return -1;
	}

	struct virtual_service *vs = ADDR_OF(&cfg->vs) + vs_idx;
	struct real *reals = ADDR_OF(&vs->reals);

	for (uint32_t i = 0; i < vs->reals_count; ++i) {
		uint8_t flags = atomic_load_explicit(
			&reals[i].flags, memory_order_relaxed
		);
		uint8_t new_flags = 0;
		if (states[i]) {
			new_flags = flags | real_enabled;
		} else {
			new_flags = flags & ~real_enabled;
		}
		if (flags != new_flags) {
			atomic_store_explicit(
				&reals[i].flags, new_flags, memory_order_relaxed
			);
		}
	}

	return 0;
}

struct balancer_session_table *
balancer_create_session_table(
	struct agent *agent, size_t capacity, yanet_error **error
) {
	yanet_error_reset(error);

	if (capacity == 0) {
		yanet_error_add(error, "zero capacity");
		return NULL;
	}

	struct memory_context *mctx = &agent->memory_context;

	struct balancer_session_table *table =
		memory_balloc(mctx, sizeof(*table));
	if (table == NULL) {
		yanet_error_add(error, "%s", agent_alloc_failed);
		return NULL;
	}

	if (TTLMAP_INIT(
		    &table->map,
		    mctx,
		    struct balancer_session_id,
		    struct balancer_session_state,
		    capacity
	    ) != 0) {
		yanet_error_add(error, "%s", agent_alloc_failed);
		memory_bfree(mctx, table, sizeof(*table));
		return NULL;
	}

	return table;
}

/*
 * Slot mapping for the chain follows
 *   idx(gen) = ((gen + 1) & 0b11) >> 1
 * so even gens are steady (only the front slot is used; back is NULL)
 * and odd gens are transitions (both slots populated, workers fall
 * back to the back map for sessions that have not migrated yet).
 */

int
balancer_session_table_chain_push_front(
	struct balancer_session_table_chain *chain,
	struct balancer_session_table *front_table,
	yanet_error **error
) {
	yanet_error_reset(error);

	uint64_t gen = rcu_load(&chain->rcu, &chain->gen);
	if (gen & 1) {
		yanet_error_add(error, "the back table still exists");
		return -1;
	}
	uint32_t cur_idx = (uint32_t)(((gen + 1) & 0b11) >> 1);
	uint32_t back_idx = cur_idx ^ 1;

	assert(chain->tables[back_idx] == NULL);

	SET_OFFSET_OF(&chain->tables[back_idx], front_table);
	rcu_update(&chain->rcu, &chain->gen, gen + 1);
	return 0;
}

int
balancer_session_table_chain_pop_back(
	struct balancer_session_table_chain *chain, yanet_error **error
) {
	yanet_error_reset(error);

	uint64_t gen = rcu_load(&chain->rcu, &chain->gen);
	if ((gen & 1) == 0) {
		yanet_error_add(error, "no back table to pop");
		return -1;
	}

	uint32_t cur_idx = (uint32_t)(((gen + 1) & 0b11) >> 1);
	uint32_t back_idx = cur_idx ^ 1;

	assert(chain->tables[back_idx] != NULL);

	rcu_update(&chain->rcu, &chain->gen, gen + 1);
	SET_OFFSET_OF(&chain->tables[back_idx], NULL);
	return 0;
}

void
balancer_free_session_table(
	struct agent *agent, struct balancer_session_table *table
) {
	if (table == NULL) {
		return;
	}
	struct memory_context *mctx = &agent->memory_context;
	TTLMAP_FREE(&table->map);
	memory_bfree(mctx, table, sizeof(*table));
	return;
}

struct balancer_session_table_chain *
balancer_create_session_table_chain(
	struct agent *agent,
	struct balancer_session_table *front_table,
	yanet_error **error
) {
	yanet_error_reset(error);

	struct memory_context *mctx = &agent->memory_context;
	const size_t workers = ADDR_OF(&agent->dp_config)->worker_count;

	if (workers == 0) {
		yanet_error_add(error, "zero workers");
		return NULL;
	}

	struct balancer_session_table_chain *chain =
		memory_balloc(mctx, sizeof(*chain));
	if (chain == NULL) {
		yanet_error_add(error, "%s", agent_alloc_failed);
		return NULL;
	}
	memset(chain, 0, sizeof(*chain));

	if (rcu_init(&chain->rcu, mctx, workers) != 0) {
		yanet_error_add(error, "%s", agent_alloc_failed);
		memory_bfree(mctx, chain, sizeof(*chain));
		return NULL;
	}

	SET_OFFSET_OF(&chain->tables[0], front_table);
	SET_OFFSET_OF(&chain->tables[1], NULL);

	return chain;
}

void
balancer_free_session_table_chain(
	struct agent *agent, struct balancer_session_table_chain *chain
) {
	if (chain == NULL) {
		return;
	}
	struct memory_context *mctx = &agent->memory_context;
	rcu_free(&chain->rcu, mctx);
	memory_bfree(mctx, chain, sizeof(*chain));
}
