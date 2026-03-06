#include "classifiers/proto_range.h"
#include "common/memory.h"
#include "common/memory_address.h"
#include "common/registry.h"
#include "compiler/segments.h"
#include "declare.h"
#include "filter/rule.h"

#include <stdint.h>
#include <stdlib.h>

////////////////////////////////////////////////////////////////////////////////

static int
validate_proto_ranges(struct filter_proto_ranges ranges) {
	for (size_t i = 0; i < ranges.count; ++i) {
		struct filter_proto_range *range = ranges.items + i;
		if (range->from > range->to) {
			return 0;
		}
	}
	return 1;
}

static int
validate_and_count(const struct filter_rule *rules, size_t rules_count) {
	int cnt = 0;
	for (size_t i = 0; i < rules_count; ++i) {
		struct filter_proto_ranges ranges;
		ranges.count = rules[i].transport.proto_count;
		ranges.items = rules[i].transport.protos;
		if (!validate_proto_ranges(ranges)) {
			return -1;
		}
		cnt += ranges.count;
	}
	return cnt;
}

////////////////////////////////////////////////////////////////////////////////

int
FILTER_ATTR_COMPILER_INIT_FUNC(proto_range_fast)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rule_count,
	struct memory_context *mctx
) {
	struct proto_range_fast_classifier *classifier =
		memory_balloc(mctx, sizeof(struct proto_range_fast_classifier));
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);

	ssize_t count = validate_and_count(rules, rule_count);
	if (count < 0) {
		return -2;
	}

	struct segment_u16 *segments =
		malloc(sizeof(struct segment_u16) * count);
	if (segments == NULL && count > 0) {
		return -1;
	}

	size_t segment_idx = 0;
	for (size_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = &rules[rule_idx];
		struct filter_proto_range *proto_ranges =
			rule->transport.protos;
		size_t proto_count = rule->transport.proto_count;

		for (size_t range_idx = 0; range_idx < proto_count;
		     ++range_idx) {
			struct filter_proto_range *range =
				&proto_ranges[range_idx];
			segments[segment_idx++] =
				(struct segment_u16){.from = range->from,
						     .to = range->to,
						     .label = rule_idx};
		}
	}

	int res = segments_classifier_u16_init(
		&classifier->classifier, mctx, registry, count, segments
	);
	free(segments);
	return res;
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(proto_range_fast)(
	void *data, struct memory_context *memory_context
) {
	if (data == NULL)
		return;
	struct proto_range_fast_classifier *c =
		(struct proto_range_fast_classifier *)data;
	segments_classifier_u16_free(&c->classifier, memory_context);
	memory_bfree(memory_context, c, sizeof(*c));
}

////////////////////////////////////////////////////////////////////////////////