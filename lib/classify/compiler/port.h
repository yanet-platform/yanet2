#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "lib/classify/classifiers/port.h"
#include "lib/classify/rule.h"

#include "declare.h"
#include "u16_ranges.h"

#include <stdint.h>

FILTER_U16_RANGES_DECLARE(
	port, struct classify_query_attr_port, classify_query_attr_port_free
)

static inline void
filter_rule_get_u16_ranges_port_src(
	const struct filter_rule *rule, struct filter_u16_ranges *ranges
) {
	ranges->count = rule->transport.src_count;
	ranges->items = (const struct filter_u16_span *)rule->transport.srcs;
}

static inline void
filter_rule_get_u16_ranges_port_dst(
	const struct filter_rule *rule, struct filter_u16_ranges *ranges
) {
	ranges->count = rule->transport.dst_count;
	ranges->items = (const struct filter_u16_span *)rule->transport.dsts;
}

static const struct filter_compile_u16_handlers classify_attr_port_src = {
	.attr_handlers = classify_attr_port_handlers,
	.get_ranges = filter_rule_get_u16_ranges_port_src,
};

static const struct filter_compile_u16_handlers classify_attr_port_dst = {
	.attr_handlers = classify_attr_port_handlers,
	.get_ranges = filter_rule_get_u16_ranges_port_dst,
};
