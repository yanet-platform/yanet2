#include "../classifiers/net6_fast.h"
#include "classifiers/segments.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "common/registry.h"
#include "compiler/helper.h"
#include "compiler/segments.h"
#include "declare.h"
#include "rule.h"
#include <assert.h>
#include <stdlib.h>

static int
validate_net6_half(uint8_t *bytes) {
	int prev = 1;
	for (int bit = 63; bit >= 0; --bit) {
		int byte = (63 - bit) / 8;
		int inner_bit = bit % 8;
		int cur = bytes[byte] & (1 << inner_bit);
		if (!prev && cur) {
			return 0;
		}
		prev = cur != 0;
	}
	return 1;
}

struct net6_count {
	size_t count;
	struct net6 *nets;
};

typedef struct net6_count(get_net)(const struct filter_rule *);

static int
validate_net6(struct net6 *net6) {
	return validate_net6_half(net6->mask) &&
	       validate_net6_half(net6->mask + 8);
}

static void
net6_part_from_to(struct net6 *n, int part, uint64_t *from, uint64_t *to) {
	uint64_t addr = 0;
	uint64_t mask = 0;

	for (size_t i = 0; i < 8; ++i) {
		addr |= ((uint64_t)n->addr[i + 8 * part]) << (56 - i * 8);
		mask |= ((uint64_t)n->mask[i + 8 * part]) << (56 - i * 8);
	}

	*from = addr & mask;
	*to = addr | ~mask;
}

static int
validate_and_count(
	const struct filter_rule **rules, size_t rules_count, get_net getter
) {
	int cnt = 0;
	for (size_t i = 0; i < rules_count; ++i) {
		if (rules[i] == NULL)
			continue;
		struct net6_count n6_count = getter(rules[i]);
		for (size_t j = 0; j < n6_count.count; ++j) {
			if (!validate_net6(n6_count.nets + j)) {
				return -1;
			}
		}
		cnt += n6_count.count;
	}
	return cnt;
}

static int
init_classifier_part(
	get_net getter,
	struct memory_context *mctx,
	struct value_registry *registry,
	size_t segments_count,
	struct segments_u64_classifier *classifier,
	int part /* 0/1 */,
	const struct filter_rule **rules,
	size_t rules_count
) {
	struct segment_u64 *segments =
		malloc(sizeof(struct segment_u64) * segments_count);
	if (segments == NULL) {
		return -1;
	}

	size_t segment_idx = 0;
	for (size_t rule_idx = 0; rule_idx < rules_count; ++rule_idx) {
		if (rules[rule_idx] == NULL)
			continue;
		struct net6_count cur = getter(rules[rule_idx]);
		for (size_t j = 0; j < cur.count; ++j) {
			uint64_t from, to;
			net6_part_from_to(cur.nets + j, part, &from, &to);
			segments[segment_idx++] = (struct segment_u64
			){.from = from, .to = to, .label = rule_idx};
		}
	}

	int res = segments_classifier_u64_init(
		classifier, mctx, registry, segments_count, segments
	);
	free(segments);
	return res;
}

static void
free_classifier_part(
	struct segments_u64_classifier *classifier, struct memory_context *mctx
) {
	segments_classifier_u64_free(classifier, mctx);
}

static int
init_classifier(
	get_net getter,
	struct memory_context *mctx,
	struct net6_fast_classifier *classifier,
	const struct filter_rule **rules,
	size_t rules_count,
	struct value_registry *registry
) {
	int segments_count = validate_and_count(rules, rules_count, getter);
	if (segments_count < 0) {
		return -2;
	}

	struct value_registry high_registry;
	if (value_registry_init(&high_registry, mctx) != 0) {
		return -1;
	}

	if (init_classifier_part(
		    getter,
		    mctx,
		    &high_registry,
		    segments_count,
		    &classifier->high,
		    0,
		    rules,
		    rules_count
	    ) != 0) {
		value_registry_free(&high_registry);
		return -1;
	}

	struct value_registry low_registry;
	if (value_registry_init(&low_registry, mctx) != 0) {
		value_registry_free(&high_registry);
		free_classifier_part(&classifier->high, mctx);
		return -1;
	}

	if (init_classifier_part(
		    getter,
		    mctx,
		    &low_registry,
		    segments_count,
		    &classifier->low,
		    1,
		    rules,
		    rules_count
	    ) != 0) {
		value_registry_free(&high_registry);
		value_registry_free(&low_registry);
		free_classifier_part(&classifier->high, mctx);
		return -1;
	}

	if (merge_and_collect_registry(
		    mctx,
		    &high_registry,
		    &low_registry,
		    &classifier->comb,
		    registry
	    ) != 0) {
		value_registry_free(&high_registry);
		value_registry_free(&low_registry);
		free_classifier_part(&classifier->high, mctx);
		free_classifier_part(&classifier->low, mctx);
	}

	value_registry_free(&high_registry);
	value_registry_free(&low_registry);

	return 0;
}

static void
free_classifier(
	struct net6_fast_classifier *classifier, struct memory_context *mctx
) {
	free_classifier_part(&classifier->low, mctx);
	free_classifier_part(&classifier->high, mctx);
	value_table_free(&classifier->comb);
}

static struct net6_count
get_src(const struct filter_rule *rule) {
	struct net6_count res;
	res.count = rule->net6.src_count;
	res.nets = rule->net6.srcs;
	return res;
}

static struct net6_count
get_dst(const struct filter_rule *rule) {
	struct net6_count res;
	res.count = rule->net6.dst_count;
	res.nets = rule->net6.dsts;
	return res;
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(net6_fast_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rules_count,
	struct memory_context *memory_context
) {
	struct net6_fast_classifier *classifier = memory_balloc(
		memory_context, sizeof(struct net6_fast_classifier)
	);
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);
	return init_classifier(
		get_dst,
		memory_context,
		classifier,
		rules,
		rules_count,
		registry
	);
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(net6_fast_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rules_count,
	struct memory_context *memory_context
) {
	struct net6_fast_classifier *classifier = memory_balloc(
		memory_context, sizeof(struct net6_fast_classifier)
	);
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);
	return init_classifier(
		get_src,
		memory_context,
		classifier,
		rules,
		rules_count,
		registry
	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net6_fast_src)(
	void *data, struct memory_context *memory_context
) {
	struct net6_fast_classifier *classifier =
		(struct net6_fast_classifier *)data;
	free_classifier(classifier, memory_context);
	memory_bfree(
		memory_context, classifier, sizeof(struct net6_fast_classifier)
	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(net6_fast_dst)(
	void *data, struct memory_context *memory_context
) {
	struct net6_fast_classifier *classifier =
		(struct net6_fast_classifier *)data;
	free_classifier(classifier, memory_context);
	memory_bfree(
		memory_context, classifier, sizeof(struct net6_fast_classifier)
	);
}
