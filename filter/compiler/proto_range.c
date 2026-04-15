#include "filter/classifiers/proto_range.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "filter/rule.h"

#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

#define PROTO_RANGE_CLASSIFIER_MAX_VALUE ((1 << 16))

static int
collect_proto_values(
	struct memory_context *memory_context,
	const struct filter_rule *rules,
	uint32_t count,
	struct value_table *table,
	struct value_registry *registry
) {
	if (value_table_init(
		    table, memory_context, 1, PROTO_RANGE_CLASSIFIER_MAX_VALUE
	    ))
		return -1;

	struct remap_table remap_table;
	if (remap_table_init(
		    &remap_table,
		    memory_context,
		    PROTO_RANGE_CLASSIFIER_MAX_VALUE
	    )) {
		goto error_remap_table;
	}

	for (const struct filter_rule *rule = rules; rule < rules + count;
	     ++rule) {

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
				uint32_t *value =
					value_table_get_ptr(table, 0, proto);
				if (remap_table_touch(
					    &remap_table, *value, value
				    ) < 0) {
					goto error_touch;
				}
			}
		}
	}

	remap_table_compact(&remap_table);
	value_table_compact(table, &remap_table);
	remap_table_free(&remap_table);

	for (const struct filter_rule *rule = rules; rule < rules + count;
	     ++rule) {
		value_registry_start(registry);

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
					    registry,
					    value_table_get(table, 0, proto)
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

	value_table_free(table);
	return -1;
}

////////////////////////////////////////////////////////////////////////////////

int
FILTER_ATTR_COMPILER_INIT_FUNC(proto_range)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t rule_count,
	struct memory_context *mctx
) {
	struct proto_range_classifier *classifier =
		memory_balloc(mctx, sizeof(struct proto_range_classifier));
	if (classifier == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, classifier);
	if (collect_proto_values(
		    mctx, rules, rule_count, &classifier->table, registry
	    )) {
		SET_OFFSET_OF(data, NULL);
		return -1;
	}

	return 0;
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(proto_range)(
	void *data, struct memory_context *memory_context
) {
	if (data == NULL)
		return;
	struct proto_range_classifier *c =
		(struct proto_range_classifier *)data;
	value_table_free(&c->table);
	memory_bfree(memory_context, c, sizeof(*c));
}

////////////////////////////////////////////////////////////////////////////////

#undef PROTO_RANGE_CLASSIFIER_MAX_VALUE
