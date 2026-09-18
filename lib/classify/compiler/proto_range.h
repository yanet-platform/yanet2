#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "lib/classify/classifiers/proto_range.h"
#include "lib/classify/rule.h"

#include "declare.h"
#include "u16_ranges.h"

#include <stdint.h>

FILTER_U16_RANGES_DECLARE(
	proto,
	struct classify_query_attr_proto_range,
	classify_query_attr_proto_range_free
)

static inline void
filter_rule_get_u16_ranges_proto(
	const struct filter_rule *rule, struct filter_u16_ranges *ranges
) {
	ranges->count = rule->transport.proto_count;
	ranges->items = (const struct filter_u16_span *)rule->transport.protos;
}

static const struct filter_compile_u16_handlers classify_attr_proto_range = {
	.attr_handlers = classify_attr_proto_handlers,
	.get_ranges = filter_rule_get_u16_ranges_proto,
};
