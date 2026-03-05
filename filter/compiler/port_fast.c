#include "../classifiers/port_fast.h"
#include "common/btree/u32.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/registry.h"
#include "declare.h"
#include "helper.h"
#include "rule.h"
#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct filter_port_ranges(port_ranges_getter)(
	const struct filter_rule *rule
);

static int
compare_segments_u32(const void *left_void, const void *right_void) {
	struct segment_u32 *left = (struct segment_u32 *)left_void;
	struct segment_u32 *right = (struct segment_u32 *)right_void;
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
validate_port_ranges(struct filter_port_ranges ranges) {
	for (size_t i = 0; i < ranges.count; ++i) {
		struct filter_port_range *range = ranges.items + i;
		if (range->from > range->to) {
			return 0;
		}
	}
	return 1;
}

static int
validate_and_count(
	const struct filter_rule *rules,
	size_t rules_count,
	port_ranges_getter getter
) {
	int cnt = 0;
	for (size_t i = 0; i < rules_count; ++i) {
		struct filter_port_ranges ranges = getter(rules + i);
		if (!validate_port_ranges(ranges)) {
			return -1;
		}
		cnt += ranges.count;
	}
	return cnt;
}

static size_t
fill_segments(
	struct segment_u32 *segments,
	const struct filter_rule *rules,
	size_t rules_count,
	port_ranges_getter getter
) {
	size_t cnt = 0;
	for (size_t i = 0; i < rules_count; ++i) {
		struct filter_port_ranges cur = getter(rules + i);
		for (size_t j = 0; j < cur.count; ++j) {
			uint32_t from = cur.items[j].from;
			uint32_t to = cur.items[j].to;
			segments[cnt++] =
				(struct segment_u32){.from = from, .to = to};
		}
	}

	qsort(segments, cnt, sizeof(struct segment_u32), compare_segments_u32);

	// Merge overlapping or adjacent segments
	return merge_segments_u32(segments, cnt);
}

static int
fill_value_registry(
	struct port_fast_classifier *classifier,
	const struct filter_rule *rules,
	size_t rules_count,
	port_ranges_getter getter,
	struct value_registry *registry,
	struct segment_u32 *segments
) {
	(void)segments;
	for (size_t i = 0; i < rules_count; ++i) {
		struct filter_port_ranges current_port_ranges =
			getter(rules + i);
		if (value_registry_start(registry) != 0) {
			return -1;
		}
		for (size_t j = 0; j < current_port_ranges.count; ++j) {
			uint32_t from = current_port_ranges.items[j].from;
			uint32_t to = current_port_ranges.items[j].to;
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
port_fast_classifier_init(
	struct port_fast_classifier *classifier,
	const struct filter_rule *rules,
	size_t rules_count,
	port_ranges_getter getter,
	struct value_registry *registry,
	struct memory_context *mctx
) {
	int validate_res = validate_and_count(rules, rules_count, getter);
	if (validate_res < 0) {
		return -2;
	}

	size_t cnt = validate_res;
	struct segment_u32 *segments = malloc(sizeof(struct segment_u32) * cnt);

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

static struct filter_port_ranges
get_port_dst(const struct filter_rule *rule) {
	struct filter_port_ranges res;
	res.count = rule->transport.dst_count;
	res.items = rule->transport.dsts;
	return res;
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(port_fast_dst)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct port_fast_classifier *classifier = memory_balloc(
		memory_context, sizeof(struct port_fast_classifier)
	);
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);
	return port_fast_classifier_init(
		classifier,
		rules,
		actions_count,
		get_port_dst,
		registry,
		memory_context
	);
}

static struct filter_port_ranges
get_port_src(const struct filter_rule *rule) {
	struct filter_port_ranges res;
	res.count = rule->transport.src_count;
	res.items = rule->transport.srcs;
	return res;
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(port_fast_src)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct port_fast_classifier *classifier = memory_balloc(
		memory_context, sizeof(struct port_fast_classifier)
	);
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);
	return port_fast_classifier_init(
		classifier,
		rules,
		actions_count,
		get_port_src,
		registry,
		memory_context
	);
}

static void
port_fast_classifier_free(
	struct port_fast_classifier *classifier,
	struct memory_context *memory_context
) {
	btree_u32_free(&classifier->btree);
	memory_bfree(
		memory_context,
		ADDR_OF(&classifier->to),
		classifier->btree.n * sizeof(uint32_t)
	);
	memory_bfree(
		memory_context, classifier, sizeof(struct port_fast_classifier)
	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(port_fast_src)(
	void *data, struct memory_context *memory_context
) {
	struct port_fast_classifier *classifier =
		(struct port_fast_classifier *)data;
	port_fast_classifier_free(classifier, memory_context);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(port_fast_dst)(
	void *data, struct memory_context *memory_context
) {
	struct port_fast_classifier *classifier =
		(struct port_fast_classifier *)data;
	port_fast_classifier_free(classifier, memory_context);
}
