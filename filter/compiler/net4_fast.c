#include "../classifiers/net4_fast.h"
#include "common/btree/u32.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "common/registry.h"
#include "declare.h"
#include "rule.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

struct net4_count {
	struct net4 *net4;
	size_t count;
};

typedef struct net4_count(net_getter)(const struct filter_rule *rule);

struct segment {
	uint32_t from;
	uint32_t to;
};

static int
compare_segments(const void *left_void, const void *right_void) {
	struct segment *left = (struct segment *)left_void;
	struct segment *right = (struct segment *)right_void;
	if (left->from < right->from) {
		return -1;
	} else if (left->from > right->from) {
		return 1;
	} else if (left->to > right->to) {
		return -1;
	} else if (left->to < right->to) {
		return 1;
	}
	return 0;
}

// check that net4 has prefix mask and
static int
validate_net4(struct net4 *net) {
	int prev = 1;
	for (int bit = 0; bit < 32; ++bit) {
		if (net->mask[bit / 8] & (1 << (7 - bit % 8))) {
			if (!prev) {
				return 0;
			}
		} else {
			prev = 0;
		}
	}
	return 1;
}

static void
net4_from_to(struct net4 *n, uint32_t *from, uint32_t *to) {
	uint32_t addr = 0;
	uint32_t mask = 0;

	for (size_t i = 0; i < 4; ++i) {
		addr |= ((uint32_t)n->addr[i]) << (24 - i * 8);
		mask |= ((uint32_t)n->mask[i]) << (24 - i * 8);
	}

	*from = addr & mask;
	*to = addr | ~mask;
}

static int
validate_and_count(
	const struct filter_rule *rules, size_t rules_count, net_getter getter
) {
	int cnt = 0;
	for (size_t i = 0; i < rules_count; ++i) {
		struct net4_count n4_count = getter(rules + i);
		for (size_t j = 0; j < n4_count.count; ++j) {
			if (!validate_net4(n4_count.net4 + j)) {
				return -1;
			}
		}
		cnt += n4_count.count;
	}
	return cnt;
}

static size_t
fill_segments(
	struct segment *segments,
	const struct filter_rule *rules,
	size_t rules_count,
	net_getter getter
) {
	size_t cnt = 0;
	for (size_t i = 0; i < rules_count; ++i) {
		struct net4_count cur = getter(rules + i);
		for (size_t j = 0; j < cur.count; ++j) {
			uint32_t from, to;
			net4_from_to(cur.net4 + j, &from, &to);
			segments[cnt++] =
				(struct segment){.from = from, .to = to};
		}
	}

	qsort(segments, cnt, sizeof(struct segment), compare_segments);

	uint32_t max_right = 0;
	size_t taken = 0;
	for (size_t i = 0; i < cnt; ++i) {
		if (segments[i].to > max_right || taken == 0) {
			segments[taken++] = segments[i];
			max_right = segments[i].to;
		}
	}

	return taken;
}

int
fill_value_registry(
	struct net4_fast_classifier *classifier,
	const struct filter_rule *rules,
	size_t rules_count,
	net_getter getter,
	struct value_registry *registry,
	struct segment *segments
) {
	(void)segments;
	for (size_t i = 0; i < rules_count; ++i) {
		struct net4_count cur = getter(rules + i);
		if (value_registry_start(registry) != 0) {
			return -1;
		}
		for (size_t j = 0; j < cur.count; ++j) {
			uint32_t from, to;
			net4_from_to(cur.net4 + j, &from, &to);
			size_t idx = btree_u32_lower_bound(
				&classifier->btree, from + 1
			);
			assert(idx > 0);
			--idx;
			uint32_t *to_array = ADDR_OF(&classifier->to);
			assert(to_array[idx] >= to);
			if (value_registry_collect(registry, idx) != 0) {
				return -1;
			}
		}
	}
	return 0;
}

// -1 means no memory
// -2 means incorrect net4
static int
net4_fast_classifier_init(
	struct net4_fast_classifier *classifier,
	const struct filter_rule *rules,
	size_t rules_count,
	net_getter getter,
	struct value_registry *registry,
	struct memory_context *mctx
) {
	int validate_res = validate_and_count(rules, rules_count, getter);
	if (validate_res < 0) {
		return -2;
	}

	size_t cnt = validate_res;
	struct segment *segments = malloc(sizeof(struct segment) * cnt);

	size_t after_collapse_cnt =
		fill_segments(segments, rules, rules_count, getter);

	uint32_t *from = malloc(sizeof(uint32_t) * after_collapse_cnt);
	if (from == NULL && after_collapse_cnt > 0) {
		goto free_segments;
	}
	for (size_t i = 0; i < after_collapse_cnt; ++i) {
		from[i] = segments[i].from;
	}

	classifier->to =
		memory_balloc(mctx, sizeof(uint32_t) * after_collapse_cnt);
	if (classifier->to == NULL && after_collapse_cnt > 0) {
		goto free_from;
	}
	for (size_t i = 0; i < after_collapse_cnt; ++i) {
		classifier->to[i] = segments[i].to;
	}
	SET_OFFSET_OF(&classifier->to, classifier->to);

	if (btree_u32_init(
		    &classifier->btree, from, after_collapse_cnt, mctx
	    ) != 0) {
		goto free_to;
	}

	if (fill_value_registry(
		    classifier, rules, rules_count, getter, registry, segments
	    ) != 0) {
		goto free_to;
	}

	free(segments);
	free(from);

	return 0;

free_to:
	memory_bfree(
		mctx,
		ADDR_OF(&classifier->to),
		sizeof(uint32_t) * after_collapse_cnt
	);

free_from:
	free(from);

free_segments:
	free(segments);

	return -1;
}

static struct net4_count
get_net4_dst(const struct filter_rule *rule) {
	struct net4_count res;
	res.count = rule->net4.dst_count;
	res.net4 = rule->net4.dsts;
	return res;
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(net4_fast_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct net4_fast_classifier *classifier = memory_balloc(
		memory_context, sizeof(struct net4_fast_classifier)
	);
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);
	return net4_fast_classifier_init(
		classifier,
		rules,
		actions_count,
		get_net4_dst,
		registry,
		memory_context
	);
}

static struct net4_count
get_net4_src(const struct filter_rule *rule) {
	struct net4_count res;
	res.count = rule->net4.src_count;
	res.net4 = rule->net4.srcs;
	return res;
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(net4_fast_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct net4_fast_classifier *classifier = memory_balloc(
		memory_context, sizeof(struct net4_fast_classifier)
	);
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);
	return net4_fast_classifier_init(
		classifier,
		rules,
		actions_count,
		get_net4_src,
		registry,
		memory_context
	);
}

static void
net4_fast_classifier_free(
	struct net4_fast_classifier *classifier,
	struct memory_context *memory_context
) {
	btree_u32_free(&classifier->btree);
	memory_bfree(
		memory_context,
		ADDR_OF(&classifier->to),
		classifier->btree.n * sizeof(uint32_t)
	);
	memory_bfree(
		memory_context, classifier, sizeof(struct net4_fast_classifier)
	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net4_fast_src)(
	void *data, struct memory_context *memory_context
) {
	struct net4_fast_classifier *classifier =
		(struct net4_fast_classifier *)data;
	net4_fast_classifier_free(classifier, memory_context);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net4_fast_dst)(
	void *data, struct memory_context *memory_context
) {
	struct net4_fast_classifier *classifier =
		(struct net4_fast_classifier *)data;
	net4_fast_classifier_free(classifier, memory_context);
}
