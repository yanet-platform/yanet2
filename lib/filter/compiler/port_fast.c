#include "classifiers/segments.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/registry.h"
#include "compiler/segments.h"
#include "declare.h"
#include "rule.h"
#include <assert.h>
#include <stdlib.h>

typedef struct filter_port_ranges(port_ranges_getter)(
	const struct filter_rule *rule
);

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
	const struct filter_rule **rules,
	size_t rules_count,
	port_ranges_getter getter
) {
	int cnt = 0;
	for (size_t i = 0; i < rules_count; ++i) {
		if (rules[i] == NULL)
			continue;
		struct filter_port_ranges ranges = getter(rules[i]);
		if (!validate_port_ranges(ranges)) {
			return -1;
		}
		cnt += ranges.count;
	}
	return cnt;
}

static int
classifier_init(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rules_count,
	struct memory_context *mctx,
	port_ranges_getter getter
) {
	struct segment_u16_classifier *classifier =
		memory_balloc(mctx, sizeof(struct segment_u16_classifier));
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);
	ssize_t count = validate_and_count(rules, rules_count, getter);
	if (count < 0) {
		return -2;
	}
	struct segment_u16 *segments =
		malloc(sizeof(struct segment_u16) * count);
	size_t segment_idx = 0;
	for (size_t rule_idx = 0; rule_idx < rules_count; ++rule_idx) {
		if (rules[rule_idx] == NULL)
			continue;
		struct filter_port_ranges ranges = getter(rules[rule_idx]);
		for (size_t range_idx = 0; range_idx < ranges.count;
		     ++range_idx) {
			struct filter_port_range range =
				ranges.items[range_idx];
			segments[segment_idx++] = (struct segment_u16
			){.from = range.from, .to = range.to, .label = rule_idx
			};
		}
	}
	int res = segments_classifier_u16_init(
		classifier, mctx, registry, count, segments
	);
	free(segments);
	return res;
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
	const struct filter_rule **rules,
	size_t rules_count,
	struct memory_context *memory_context
) {
	return classifier_init(
		registry, data, rules, rules_count, memory_context, get_port_dst
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
	const struct filter_rule **rules,
	size_t rules_count,
	struct memory_context *memory_context
) {
	return classifier_init(
		registry, data, rules, rules_count, memory_context, get_port_src
	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(port_fast_src)(
	void *data, struct memory_context *memory_context
) {
	struct segment_u16_classifier *classifier =
		(struct segment_u16_classifier *)data;
	segments_classifier_u16_free(classifier, memory_context);
	memory_bfree(
		memory_context,
		classifier,
		sizeof(struct segment_u16_classifier)
	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(port_fast_dst)(
	void *data, struct memory_context *memory_context
) {
	struct segment_u16_classifier *classifier =
		(struct segment_u16_classifier *)data;
	segments_classifier_u16_free(classifier, memory_context);
	memory_bfree(
		memory_context,
		classifier,
		sizeof(struct segment_u16_classifier)
	);
}
