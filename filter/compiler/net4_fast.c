#include "classifiers/segments.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "common/registry.h"
#include "compiler/segments.h"
#include "declare.h"
#include "rule.h"
#include <assert.h>
#include <stdlib.h>

struct net4_count {
	struct net4 *net4;
	size_t count;
};

typedef struct net4_count(net_getter)(const struct filter_rule *rule);

// check that net4 has prefix mask
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

static int
classifier_init(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rules_count,
	struct memory_context *mctx,
	net_getter getter
) {
	struct segments_u32_classifier *classifier =
		memory_balloc(mctx, sizeof(struct segments_u32_classifier));
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);

	ssize_t count = validate_and_count(rules, rules_count, getter);
	if (count < 0) {
		return -2;
	}

	struct segment_u32 *segments =
		malloc(sizeof(struct segment_u32) * count);
	if (segments == NULL) {
		return -1;
	}

	size_t segment_idx = 0;
	for (size_t rule_idx = 0; rule_idx < rules_count; ++rule_idx) {
		struct net4_count cur = getter(&rules[rule_idx]);
		for (size_t j = 0; j < cur.count; ++j) {
			uint32_t from, to;
			net4_from_to(cur.net4 + j, &from, &to);
			segments[segment_idx++] = (struct segment_u32
			){.from = from, .to = to, .label = rule_idx};
		}
	}

	int res = segments_classifier_u32_init(
		classifier, mctx, registry, count, segments
	);
	free(segments);
	return res;
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
	return classifier_init(
		registry,
		data,
		rules,
		actions_count,
		memory_context,
		get_net4_dst
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
	return classifier_init(
		registry,
		data,
		rules,
		actions_count,
		memory_context,
		get_net4_src
	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net4_fast_src)(
	void *data, struct memory_context *memory_context
) {
	struct segments_u32_classifier *classifier =
		(struct segments_u32_classifier *)data;
	segments_classifier_u32_free(classifier, memory_context);
	memory_bfree(
		memory_context,
		classifier,
		sizeof(struct segments_u32_classifier)
	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net4_fast_dst)(
	void *data, struct memory_context *memory_context
) {
	struct segments_u32_classifier *classifier =
		(struct segments_u32_classifier *)data;
	segments_classifier_u32_free(classifier, memory_context);
	memory_bfree(
		memory_context,
		classifier,
		sizeof(struct segments_u32_classifier)
	);
}
