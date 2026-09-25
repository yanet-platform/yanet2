#include "lib/filter/classifiers/proto_range.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "lib/filter/rule.h"

#include <stdbool.h>
#include <stdint.h>
#include <string.h>

#define PROTO_RANGE_CLASSIFIER_MAX_VALUE ((1 << 16))

// The protocols whose classification reads a subtype byte and therefore
// need a dedicated class for packets with an unavailable transport header.
static const uint8_t unavailable_class_protos[] = {
	IPPROTO_TCP,
	IPPROTO_ICMP,
	IPPROTO_ICMPV6,
};

// Whether the rule's protocol ranges collectively cover every subtype of
// one protocol block, independent of range order, overlap or boundary
// crossings.
static bool
rule_covers_proto_block(const struct filter_rule *rule, uint8_t proto) {
	bool covered[256];
	uint32_t base = (uint32_t)proto * 256;

	memset(covered, 0, sizeof(covered));
	for (const struct filter_proto_range *range = rule->transport.protos;
	     range < rule->transport.protos + rule->transport.proto_count;
	     ++range) {
		uint32_t from = range->from;
		uint32_t to = range->to;
		if (from < base) {
			from = base;
		}
		if (to > base + 255) {
			to = base + 255;
		}
		for (uint32_t value = from; value <= to; ++value) {
			covered[value - base] = true;
		}
	}
	for (uint32_t idx = 0; idx < 256; ++idx) {
		if (!covered[idx]) {
			return false;
		}
	}
	return true;
}

static int
collect_proto_values(
	struct memory_context *memory_context,
	const struct filter_rule **rules,
	uint32_t count,
	struct proto_range_classifier *classifier,
	struct value_registry *registry
) {
	struct vline *line = &classifier->line;
	if (vline_init(
		    line,
		    memory_context,
		    "proto-range",
		    PROTO_RANGE_CLASSIFIER_MAX_VALUE
	    )) {
		return -1;
	}

	struct remap_table remap_table;
	if (remap_table_init(
		    &remap_table,
		    memory_context,
		    PROTO_RANGE_CLASSIFIER_MAX_VALUE +
			    PROTO_UNAVAILABLE_CLASS_COUNT
	    )) {
		goto error_remap_table;
	}

	for (const struct filter_rule **rule_ptr = rules;
	     rule_ptr < rules + count;
	     ++rule_ptr) {
		if (*rule_ptr == NULL) {
			continue;
		}
		const struct filter_rule *rule = *rule_ptr;

		remap_table_new_gen(&remap_table);

		struct filter_proto_range *proto_ranges =
			rule->transport.protos;
		size_t proto_count = rule->transport.proto_count;

		for (struct filter_proto_range *proto_range = proto_ranges;
		     proto_range < proto_ranges + proto_count;
		     ++proto_range) {
			for (uint32_t proto = proto_range->from;
			     proto <= proto_range->to;
			     ++proto) {
				uint32_t *value = vline_get_ptr(line, proto);
				if (remap_table_touch(
					    &remap_table, *value, value
				    ) < 0) {
					goto error_touch;
				}
			}
		}

		for (uint32_t class_idx = 0;
		     class_idx < PROTO_UNAVAILABLE_CLASS_COUNT;
		     ++class_idx) {
			if (rule_covers_proto_block(
				    rule, unavailable_class_protos[class_idx]
			    )) {
				uint32_t *value =
					&classifier->unavailable_classes
						 [class_idx];
				if (remap_table_touch(
					    &remap_table, *value, value
				    ) < 0) {
					goto error_touch;
				}
			}
		}
	}

	remap_table_compact(&remap_table);
	vline_compact(line, &remap_table);
	for (uint32_t class_idx = 0; class_idx < PROTO_UNAVAILABLE_CLASS_COUNT;
	     ++class_idx) {
		classifier->unavailable_classes[class_idx] =
			remap_table_compacted(
				&remap_table,
				classifier->unavailable_classes[class_idx]
			);
	}
	remap_table_free(&remap_table);

	for (const struct filter_rule **rule_ptr = rules;
	     rule_ptr < rules + count;
	     ++rule_ptr) {
		// A value range should be created even for empty rules
		if (value_registry_start(registry)) {
			goto error_collect;
		}
		if (*rule_ptr == NULL) {
			continue;
		}

		const struct filter_rule *rule = *rule_ptr;

		struct filter_proto_range *proto_ranges =
			rule->transport.protos;
		size_t proto_count = rule->transport.proto_count;

		for (struct filter_proto_range *proto_range = proto_ranges;
		     proto_range < proto_ranges + proto_count;
		     ++proto_range) {
			for (uint32_t proto = proto_range->from;
			     proto <= proto_range->to;
			     ++proto) {
				if (value_registry_collect(
					    registry, vline_get(line, proto)
				    )) {
					goto error_collect;
				}
			}
		}

		for (uint32_t class_idx = 0;
		     class_idx < PROTO_UNAVAILABLE_CLASS_COUNT;
		     ++class_idx) {
			if (rule_covers_proto_block(
				    rule, unavailable_class_protos[class_idx]
			    )) {
				if (value_registry_collect(
					    registry,
					    classifier->unavailable_classes
						    [class_idx]
				    )) {
					goto error_collect;
				}
			}
		}
	}

	return 0;

error_touch:
	remap_table_free(&remap_table);

error_collect:
error_remap_table:

	vline_free(line);
	return -1;
}

int
FILTER_ATTR_COMPILER_INIT_FUNC(proto_range)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *mctx
) {
	struct proto_range_classifier *classifier =
		memory_balloc(mctx, sizeof(struct proto_range_classifier));
	if (classifier == NULL) {
		return -1;
	}
	for (uint32_t class_idx = 0; class_idx < PROTO_UNAVAILABLE_CLASS_COUNT;
	     ++class_idx) {
		classifier->unavailable_classes[class_idx] = 0;
	}
	SET_OFFSET_OF(data, classifier);
	if (collect_proto_values(
		    mctx, rules, rule_count, classifier, registry
	    )) {
		SET_OFFSET_OF(data, NULL);
		memory_bfree(mctx, classifier, sizeof(*classifier));
		return -1;
	}

	return 0;
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(proto_range)(
	void *data, struct memory_context *memory_context
) {
	if (data == NULL) {
		return;
	}
	struct proto_range_classifier *c =
		(struct proto_range_classifier *)data;
	vline_free(&c->line);
	memory_bfree(memory_context, c, sizeof(*c));
}

#undef PROTO_RANGE_CLASSIFIER_MAX_VALUE
