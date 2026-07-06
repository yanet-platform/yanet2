#include "lib/filter/classifiers/proto_range.h"
#include "common/memory.h"
#include "common/radix.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "lib/filter/rule.h"

#include <stdint.h>

////////////////////////////////////////////////////////////////////////////////

#define PROTO_RANGE_CLASSIFIER_MAX_VALUE ((1 << 16))

static inline int
build_proto_range_info(
	struct memory_context *memory_context,
	const struct filter_rule **rules,
	uint32_t rule_count,

	struct radix *proto_range_radix,

	struct filter_proto_range **proto_ranges,
	uint64_t *proto_range_count,

	struct value_range **proto_range_groups,
	uint32_t *proto_range_group_count
) {
	*proto_ranges = NULL;
	*proto_range_count = 0;

	*proto_range_groups = NULL;
	*proto_range_group_count = 0;

	radix_init(proto_range_radix, memory_context);

	for (const struct filter_rule **rule_ptr = rules;
	     rule_ptr < rules + rule_count;
	     ++rule_ptr) {

		if (*rule_ptr == NULL)
			continue;
		const struct filter_rule *rule = *rule_ptr;

		struct filter_proto_range *rule_proto_ranges =
			rule->transport.protos;
		uint32_t rule_proto_range_count = rule->transport.proto_count;

		for (struct filter_proto_range *rule_proto_range =
			     rule_proto_ranges;
		     rule_proto_range <
		     rule_proto_ranges + rule_proto_range_count;
		     ++rule_proto_range) {
			struct filter_proto_range proto_range =
				*rule_proto_range;

			if (radix_lookup(
				    proto_range_radix,
				    4,
				    (uint8_t *)&proto_range
			    ) != RADIX_VALUE_INVALID)
				continue;

			radix_insert(
				proto_range_radix,
				4,
				(uint8_t *)&proto_range,
				*proto_range_count
			);
			if (mem_array_expand_exp(
				    memory_context,
				    (void **)proto_ranges,
				    sizeof(**proto_ranges),
				    proto_range_count
			    )) {
				goto error_proto_ranges;
			}
			(*proto_ranges)[*proto_range_count - 1] = proto_range;
		}
	}

	if (*proto_range_count == 0) {
		return 0;
	}

	struct value_table proto_range_table;
	if (value_table_init(
		    &proto_range_table, memory_context, 1, *proto_range_count
	    )) {
		goto error_proto_ranges;
	}

	struct remap_table proto_range_remap;
	if (remap_table_init(
		    &proto_range_remap, memory_context, *proto_range_count
	    )) {
		goto error_table;
	}

	for (const struct filter_rule **rule_ptr = rules;
	     rule_ptr < rules + rule_count;
	     ++rule_ptr) {

		if (*rule_ptr == NULL)
			continue;
		const struct filter_rule *rule = *rule_ptr;

		remap_table_new_gen(&proto_range_remap);

		struct filter_proto_range *rule_proto_ranges =
			rule->transport.protos;
		uint32_t rule_proto_range_count = rule->transport.proto_count;

		for (struct filter_proto_range *rule_proto_range =
			     rule_proto_ranges;
		     rule_proto_range <
		     rule_proto_ranges + rule_proto_range_count;
		     ++rule_proto_range) {
			struct filter_proto_range proto_range =
				*rule_proto_range;

			uint32_t proto_range_idx = radix_lookup(
				proto_range_radix, 4, (uint8_t *)&proto_range
			);
			uint32_t *v = value_table_get_ptr(
				&proto_range_table, 0, proto_range_idx
			);
			if (remap_table_touch(&proto_range_remap, *v, v) < 0) {
				goto error_touch;
			}
		}
	}

	remap_table_compact(&proto_range_remap);
	value_table_compact(&proto_range_table, &proto_range_remap);
	remap_table_free(&proto_range_remap);

	for (uint32_t idx = 0; idx < *proto_range_count; ++idx)
		if (value_table_get(&proto_range_table, 0, idx) >=
		    *proto_range_group_count)
			*proto_range_group_count =
				value_table_get(&proto_range_table, 0, idx) + 1;
	*proto_range_groups = (struct value_range *)memory_balloc(
		memory_context,
		sizeof(struct value_range) * *proto_range_group_count
	);
	if (*proto_range_groups == NULL)
		goto error_table;

	memset(*proto_range_groups,
	       0,
	       sizeof(struct value_range) * *proto_range_group_count);
	for (uint32_t proto_range_idx = 0; proto_range_idx < *proto_range_count;
	     ++proto_range_idx) {
		if (value_range_append(
			    memory_context,
			    *proto_range_groups + value_table_get(
							  &proto_range_table,
							  0,
							  proto_range_idx
						  ),
			    proto_range_idx
		    )) {
			goto error_append;
		}
	}

	value_table_free(&proto_range_table);

	return 0;

error_append:
	for (uint32_t idx = 0; idx < *proto_range_group_count; ++idx) {
		struct value_range *range = *proto_range_groups + idx;
		mem_array_free_exp(
			memory_context,
			ADDR_OF(&range->values),
			sizeof(uint32_t),
			range->count
		);
	}

	memory_bfree(
		memory_context,
		*proto_range_groups,
		sizeof(struct value_range) * *proto_range_group_count
	);

	*proto_range_groups = NULL;
	*proto_range_group_count = 0;

	goto error_table;

error_touch:
	remap_table_free(&proto_range_remap);

error_table:
	value_table_free(&proto_range_table);

error_proto_ranges:
	mem_array_free_exp(
		memory_context,
		*proto_ranges,
		sizeof(**proto_ranges),
		*proto_range_count
	);
	*proto_ranges = NULL;
	*proto_range_count = 0;

	radix_free(proto_range_radix);

	return -1;
}

static inline int
touch_proto_range_groups(
	struct memory_context *memory_context,
	struct filter_proto_range *all_proto_ranges,
	struct value_range *proto_range_groups,
	uint32_t proto_range_group_count,
	struct value_table *value_table
) {
	struct remap_table remap_table;
	if (remap_table_init(
		    &remap_table,
		    memory_context,
		    PROTO_RANGE_CLASSIFIER_MAX_VALUE
	    )) {
		return -1;
	}

	for (uint32_t proto_range_group_idx = 0;
	     proto_range_group_idx < proto_range_group_count;
	     ++proto_range_group_idx) {
		remap_table_new_gen(&remap_table);

		uint32_t *values = ADDR_OF(
			&proto_range_groups[proto_range_group_idx].values
		);
		for (uint32_t idx = 0;
		     idx < proto_range_groups[proto_range_group_idx].count;
		     ++idx) {
			struct filter_proto_range proto_range =
				all_proto_ranges[values[idx]];

			for (uint32_t idx = proto_range.from;
			     idx <= proto_range.to;
			     ++idx) {
				uint32_t *value = value_table_get_ptr(
					value_table, 0, idx
				);
				if (remap_table_touch(
					    &remap_table, *value, value
				    ) < 0) {
					goto error;
				}
			}
		}
	}

	remap_table_compact(&remap_table);
	value_table_compact(value_table, &remap_table);
	remap_table_free(&remap_table);

	return 0;

error:
	remap_table_free(&remap_table);
	return -1;
}

static inline int
collect_proto_range_values(
	struct filter_proto_range *all_proto_ranges,
	uint32_t all_proto_range_count,
	struct value_table *value_table,
	struct value_registry *registry
) {
	for (uint32_t proto_range_idx = 0;
	     proto_range_idx < all_proto_range_count;
	     ++proto_range_idx) {
		struct filter_proto_range proto_range =
			all_proto_ranges[proto_range_idx];

		value_registry_start(registry);

		for (uint32_t idx = proto_range.from; idx <= proto_range.to;
		     ++idx) {
			if (value_registry_collect(
				    registry,
				    value_table_get(value_table, 0, idx)
			    )) {
				return -1;
			}
		}
	}

	return 0;
}

static int
collect_proto_values(
	struct memory_context *memory_context,
	const struct filter_rule **rules,
	uint32_t count,
	struct value_table *table,
	struct value_registry *registry
) {
	struct filter_proto_range *proto_ranges;
	uint64_t proto_range_count;
	struct radix proto_range_radix;

	struct value_range *proto_range_groups;
	uint32_t proto_range_group_count;

	if (build_proto_range_info(
		    memory_context,
		    rules,
		    count,
		    &proto_range_radix,
		    &proto_ranges,
		    &proto_range_count,
		    &proto_range_groups,
		    &proto_range_group_count

	    )) {
		return -1;
	}

	if (value_table_init(
		    table, memory_context, 1, PROTO_RANGE_CLASSIFIER_MAX_VALUE
	    )) {
		goto error_info;
	}

	if (touch_proto_range_groups(
		    memory_context,
		    proto_ranges,
		    proto_range_groups,
		    proto_range_group_count,
		    table
	    )) {
		goto error_table;
	}

	struct value_registry proto_range_registry;
	value_registry_init(&proto_range_registry, memory_context);

	if (collect_proto_range_values(
		    proto_ranges,
		    proto_range_count,
		    table,
		    &proto_range_registry
	    )) {
		goto error_proto_range_registry;
	}

	for (const struct filter_rule **rule_ptr = rules;
	     rule_ptr < rules + count;
	     ++rule_ptr) {
		// A value range should be created even for empty rules
		value_registry_start(registry);
		if (*rule_ptr == NULL)
			continue;
		const struct filter_rule *rule = *rule_ptr;

		struct filter_proto_range *proto_ranges =
			rule->transport.protos;
		uint32_t proto_count = rule->transport.proto_count;

		for (struct filter_proto_range *proto_range = proto_ranges;
		     proto_range < proto_ranges + proto_count;
		     ++proto_range) {
			uint32_t proto_range_idx = radix_lookup(
				&proto_range_radix, 4, (uint8_t *)proto_range
			);

			struct value_range *rng =
				ADDR_OF(&proto_range_registry.ranges) +
				proto_range_idx;
			uint32_t *vls = ADDR_OF(&rng->values);
			for (uint32_t idx = 0; idx < rng->count; ++idx) {
				if (value_registry_collect(
					    registry, vls[idx]
				    )) {
					goto error_registry;
				}
			}
		}
	}

	radix_free(&proto_range_radix);
	value_registry_fini(&proto_range_registry);
	for (uint32_t idx = 0; idx < proto_range_group_count; ++idx) {
		struct value_range *range = proto_range_groups + idx;
		mem_array_free_exp(
			memory_context,
			ADDR_OF(&range->values),
			sizeof(uint32_t),
			range->count
		);
	}

	memory_bfree(
		memory_context,
		proto_range_groups,
		sizeof(struct value_range) * proto_range_group_count
	);

	mem_array_free_exp(
		memory_context,
		proto_ranges,
		sizeof(*proto_ranges),
		proto_range_count
	);

	return 0;

error_registry:
	value_registry_fini(registry);

error_proto_range_registry:
	value_registry_fini(&proto_range_registry);

error_table:
	value_table_free(table);

error_info:
	for (uint32_t idx = 0; idx < proto_range_group_count; ++idx) {
		struct value_range *range = proto_range_groups + idx;
		mem_array_free_exp(
			memory_context,
			ADDR_OF(&range->values),
			sizeof(uint32_t),
			range->count
		);
	}

	memory_bfree(
		memory_context,
		proto_range_groups,
		sizeof(struct value_range) * proto_range_group_count
	);

	mem_array_free_exp(
		memory_context,
		proto_ranges,
		sizeof(*proto_ranges),
		proto_range_count
	);

	radix_free(&proto_range_radix);
	return -1;
}

////////////////////////////////////////////////////////////////////////////////

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
	SET_OFFSET_OF(data, classifier);
	if (collect_proto_values(
		    mctx, rules, rule_count, &classifier->table, registry
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
	if (data == NULL)
		return;
	struct proto_range_classifier *c =
		(struct proto_range_classifier *)data;
	value_table_free(&c->table);
	memory_bfree(memory_context, c, sizeof(*c));
}

////////////////////////////////////////////////////////////////////////////////

#undef PROTO_RANGE_CLASSIFIER_MAX_VALUE
