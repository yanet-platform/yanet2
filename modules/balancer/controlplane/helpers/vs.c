#include <assert.h>
#include <netinet/in.h>
#include <stdalign.h>
#include <stdlib.h>
#include <string.h>

#include "api/agent.h"

#include "common/big_array.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/rcu.h"
#include "common/rng.h"

#include "filter/compiler.h"
#include "filter/rule.h"

#include "lib/controlplane/agent/agent.h"
#include "lib/dataplane/config/zone.h"

#include "modules/balancer/dataplane/dataplane.h"
#include "modules/balancer/dataplane/types/real.h"
#include "modules/balancer/dataplane/types/selector.h"
#include "modules/balancer/dataplane/types/sessions_tracker.h"
#include "modules/balancer/dataplane/types/vs.h"

FILTER_COMPILER_DECLARE(ipv4_vs_acl, net4_fast_src, port_fast_src);
FILTER_COMPILER_DECLARE(ipv6_vs_acl, net6_fast_src, port_fast_src);

static void
free_rules(size_t count, struct filter_rule *rules) {
	for (size_t i = 0; i < count; ++i) {
		free(rules[i].net4.dsts);
		free(rules[i].net6.dsts);
		free(rules[i].net4.srcs);
		free(rules[i].net6.srcs);
		free(rules[i].transport.dsts);
		free(rules[i].transport.srcs);
		free(rules[i].transport.protos);
	}
	free(rules);
}

static int
compile_acl(
	struct filter *filter,
	struct filter_rule *rules,
	size_t count,
	struct memory_context *mctx,
	int ipv6
) {
	int res;
	if (ipv6) {
		res = FILTER_INIT(filter, ipv6_vs_acl, rules, count, mctx);
	} else {
		res = FILTER_INIT(filter, ipv4_vs_acl, rules, count, mctx);
	}

	return res;
}

static int
make_acl_net_rule(
	struct filter_rule *rule, struct net *nets, uint32_t count, int ipv6
) {
	if (count == 0) {
		return 0;
	}

	if (ipv6) {
		rule->net6.src_count = count;
		rule->net6.srcs = calloc(count, sizeof(struct net6));
		if (rule->net6.srcs == NULL) {
			return -1;
		}
		for (uint32_t j = 0; j < count; ++j) {
			memcpy(&rule->net6.srcs[j],
			       &nets[j].v6,
			       sizeof(struct net6));
		}
	} else {
		rule->net4.src_count = count;
		rule->net4.srcs = calloc(count, sizeof(struct net4));
		if (rule->net4.srcs == NULL) {
			return -1;
		}
		for (uint32_t j = 0; j < count; ++j) {
			memcpy(&rule->net4.srcs[j],
			       &nets[j].v4,
			       sizeof(struct net4));
		}
	}

	return 0;
}

static int
make_acl_port_rule(
	struct filter_rule *rule, struct filter_port_range *prs, uint32_t count
) {
	if (count > 0) {
		rule->transport.src_count = count;
		rule->transport.srcs =
			calloc(count, sizeof(struct filter_port_range));
		if (rule->transport.srcs == NULL) {
			return -1;
		}
		memcpy(rule->transport.srcs,
		       prs,
		       count * sizeof(struct filter_port_range));
	} else {
		rule->transport.src_count = 1;
		rule->transport.srcs =
			calloc(1, sizeof(struct filter_port_range));
		if (rule->transport.srcs == NULL) {
			return -1;
		}
		rule->transport.srcs[0].from = 0;
		rule->transport.srcs[0].to = 65535;
	}

	return 0;
}

static int
make_acl_rules(struct filter_rule **out, struct balancer_vs *vs) {
	uint32_t allowed_src_count = vs->allowed_sources_count;
	struct balancer_vs_allowed_source *allowed_srcs =
		ADDR_OF(&vs->allowed_sources);

	struct filter_rule *rules =
		calloc(allowed_src_count, sizeof(struct filter_rule));
	if (rules == NULL && allowed_src_count > 0) {
		return -1;
	}

	int ipv6 = vs->ip_proto == IPPROTO_IPV6;

	for (uint32_t i = 0; i < allowed_src_count; ++i) {
		struct balancer_vs_allowed_source *src = &allowed_srcs[i];
		struct filter_rule *rule = &rules[i];

		if (make_acl_net_rule(
			    rule, ADDR_OF(&src->nets), src->nets_count, ipv6
		    ) != 0) {
			free_rules(allowed_src_count, rules);
			return -1;
		}

		if (make_acl_port_rule(
			    rule,
			    ADDR_OF(&src->port_ranges),
			    src->port_ranges_count
		    ) != 0) {
			free_rules(allowed_src_count, rules);
			return -1;
		}

		rule->action = i;
	}

	*out = rules;
	return 0;
}

int
balancer_vs_set_acl(struct balancer_vs *vs, struct agent *agent) {
	struct memory_context *mctx = &agent->memory_context;

	uint32_t src_count = vs->allowed_sources_count;
	if (src_count == 0) {
		SET_OFFSET_OF(&vs->acl, NULL);
		return 0;
	}

	struct filter_rule *rules = NULL;
	if (make_acl_rules(&rules, vs) != 0) {
		return -2;
	}

	struct filter *filter = memory_balloc(mctx, sizeof(struct filter));
	if (filter == NULL) {
		free_rules(src_count, rules);
		return -1;
	}

	int ipv6 = vs->ip_proto == IPPROTO_IPV6;
	if (compile_acl(filter, rules, src_count, mctx, ipv6) != 0) {
		memory_bfree(mctx, filter, sizeof(struct filter));
		free_rules(src_count, rules);
		return -1;
	}

	free_rules(src_count, rules);
	SET_OFFSET_OF(&vs->acl, filter);

	return 0;
}

static int
selector_ensure(struct balancer_vs *vs, struct memory_context *mctx) {
	if (ADDR_OF(&vs->selector) != NULL) {
		return 0;
	}

	struct balancer_real_selector *sel =
		memory_balloc(mctx, sizeof(struct balancer_real_selector));
	if (sel == NULL) {
		return -1;
	}

	memset(sel, 0, sizeof(*sel));
	SET_OFFSET_OF(&vs->selector, sel);

	return 0;
}

static int
ring_fill(
	struct balancer_ring *ring,
	struct balancer_real *reals,
	uint32_t reals_count,
	uint64_t total_weight,
	struct memory_context *mctx,
	size_t seed
) {
	size_t ring_bytes = total_weight * sizeof(uint32_t);
	if (big_array_init(&ring->real_ids, ring_bytes, mctx) != 0) {
		return -1;
	}

	/* Fill: each enabled real gets weight copies. */
	size_t pos = 0;
	for (uint32_t i = 0; i < reals_count; ++i) {
		if (!(reals[i].flags & balancer_real_enabled) ||
		    (reals[i].flags & balancer_real_removed))
			continue;

		uint32_t w = reals[i].effective_weight;

		for (uint32_t j = 0; j < w; ++j) {
			uint32_t *slot = big_array_get(
				&ring->real_ids, pos * sizeof(uint32_t)
			);
			*slot = i;
			pos++;
		}
	}

	/* Shuffle the ring. */
	uint64_t rng = 0xdeadbeef ^ seed;
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

void
balancer_vs_free_acl(struct balancer_vs *vs, struct agent *agent) {
	struct memory_context *mctx = &agent->memory_context;

	struct filter *filter = ADDR_OF(&vs->acl);
	if (filter == NULL) {
		return;
	}

	int ipv6 = vs->ip_proto == IPPROTO_IPV6;
	if (ipv6) {
		FILTER_FREE(filter, ipv6_vs_acl);
	} else {
		FILTER_FREE(filter, ipv4_vs_acl);
	}
	memory_bfree(mctx, filter, sizeof(struct filter));
	SET_OFFSET_OF(&vs->acl, NULL);
}

int
balancer_vs_update_real_selector(
	struct balancer_vs *vs, rcu_t *rcu, struct agent *agent
) {
	struct memory_context *mctx = &agent->memory_context;

	if (selector_ensure(vs, mctx) != 0) {
		return -1;
	}

	struct balancer_real_selector *sel = ADDR_OF(&vs->selector);
	struct balancer_real *reals = ADDR_OF(&vs->reals);
	uint32_t reals_count = vs->reals_count;

	sel->use_rr = (vs->flags & balancer_vs_round_robin) ? 1 : 0;

	/* Total weight of enabled reals. */
	uint64_t total_weight = 0;
	for (uint32_t i = 0; i < reals_count; ++i) {
		if ((reals[i].flags & balancer_real_enabled) &&
		    !(reals[i].flags & balancer_real_removed)) {
			total_weight += reals[i].effective_weight;
		}
	}

	/* Prepare new ring in the inactive slot. */
	size_t cur_ring =
		atomic_load_explicit(&sel->ring_id, memory_order_acquire);
	size_t new_ring = cur_ring ^ 1;
	big_array_free(&sel->rings[new_ring].real_ids);

	if (total_weight == 0) {
		memset(&sel->rings[new_ring], 0, sizeof(struct balancer_ring));
	} else if (ring_fill(
			   &sel->rings[new_ring],
			   reals,
			   reals_count,
			   total_weight,
			   mctx,
			   vs->stable_idx
		   ) != 0) {
		return -1;
	}

	/* Swap using packet handler's RCU. */
	rcu_update(rcu, (atomic_ulong *)&sel->ring_id, new_ring);

	/* Free old ring. */
	big_array_free(&sel->rings[cur_ring].real_ids);
	return 0;
}

void
balancer_vs_free_real_selector(struct balancer_vs *vs, struct agent *agent) {
	struct memory_context *mctx = &agent->memory_context;

	struct balancer_real_selector *sel = ADDR_OF(&vs->selector);
	if (sel == NULL) {
		return;
	}

	big_array_free(&sel->rings[0].real_ids);
	big_array_free(&sel->rings[1].real_ids);
	memory_bfree(mctx, sel, sizeof(struct balancer_real_selector));
	SET_OFFSET_OF(&vs->selector, NULL);
}

int
balancer_vs_set_session_trackers(struct balancer_vs *vs, struct agent *agent) {
	struct memory_context *mctx = &agent->memory_context;
	struct balancer_real *reals = ADDR_OF(&vs->reals);

	const size_t workers = ADDR_OF(&agent->dp_config)->worker_count;

	for (size_t real_idx = 0; real_idx < vs->reals_count; ++real_idx) {
		struct balancer_real *real = &reals[real_idx];
		if (real->tracker_shards != NULL) {
			continue;
		}
		struct balancer_sessions_tracker_shard *shards = memory_balloc(
			mctx,
			sizeof(struct balancer_sessions_tracker_shard) * workers
		);
		if (shards == NULL) {
			return -1;
		}
		memset(shards,
		       0,
		       sizeof(struct balancer_sessions_tracker_shard) * workers
		);
		SET_OFFSET_OF(&real->tracker_shards, shards);
	}

	return 0;
}

void
balancer_vs_free_session_trackers(struct balancer_vs *vs, struct agent *agent) {
	struct memory_context *mctx = &agent->memory_context;
	struct balancer_real *reals = ADDR_OF(&vs->reals);

	const size_t workers = ADDR_OF(&agent->dp_config)->worker_count;

	for (size_t real_idx = 0; real_idx < vs->reals_count; ++real_idx) {
		struct balancer_real *real = &reals[real_idx];
		struct balancer_sessions_tracker_shard *shards =
			ADDR_OF(&real->tracker_shards);
		if (shards != NULL) {
			memory_bfree(
				mctx,
				shards,
				sizeof(struct balancer_sessions_tracker_shard) *
					workers
			);
			SET_OFFSET_OF(&real->tracker_shards, NULL);
		}
	}
}