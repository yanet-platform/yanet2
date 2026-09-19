#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "lib/classify/classifiers/vlan.h"
#include "lib/classify/rule.h"

#include <stdint.h>

typedef void (*filter_rule_get_vlan_ranges_func)(
	const struct filter_rule *filter_rule,
	struct filter_vlan_ranges *vlan_ranges
);

struct classify_attr_vlan {
	struct classify_attr attr;
	struct classify_query_attr_vlan *query_attr;
};

struct classify_attr_vlan_handlers {
	struct classify_attr_handlers attr_handlers;
	filter_rule_get_vlan_ranges_func get_vlan_ranges;
};

static inline struct classify_attr *
classify_attr_vlan_create(
	struct memory_context *memory_context,
	const struct classify_attr_handlers *handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
) {
	(void)handlers;
	(void)rules;
	(void)rule_count;

	struct classify_attr_vlan *attr =
		(struct classify_attr_vlan *)memory_balloc(
			memory_context, sizeof(struct classify_attr_vlan)
		);
	if (attr == NULL) {
		return NULL;
	}

	attr->query_attr = (struct classify_query_attr_vlan *)memory_balloc(
		memory_context, sizeof(struct classify_query_attr_vlan)
	);
	if (attr->query_attr == NULL) {
		goto error_free;
	}

	if (value_table_init(
		    &attr->query_attr->value_table,
		    memory_context,
		    "filter:vlan",
		    1,
		    4096
	    )) {
		goto error_free_attr;
	}

	return &attr->attr;

error_free_attr:
	memory_bfree(
		memory_context,
		attr->query_attr,
		sizeof(struct classify_query_attr_vlan)
	);

error_free:
	memory_bfree(memory_context, attr, sizeof(struct classify_attr_vlan));

	return NULL;
}

static inline uint32_t
classify_attr_vlan_size(const struct classify_attr *attr) {
	(void)attr;
	return 4096;
}

static inline int
classify_attr_vlan_iter(
	struct classify_attr *attr,
	const struct classify_attr_handlers *attr_handlers,
	classify_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	(void)attr_handlers;

	struct classify_attr_vlan *vlan_ranges_attr =
		container_of(attr, struct classify_attr_vlan, attr);

	struct classify_query_attr_vlan *query_attr =
		vlan_ranges_attr->query_attr;

	for (uint32_t idx = 0; idx < 4096; ++idx) {
		if (iter_cb_func(
			    value_table_get_ptr(
				    &query_attr->value_table, 0, idx
			    ),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
	}
	return 0;
}

static inline int
classify_attr_vlan_rule_is_any(
	const struct classify_attr *attr,
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {
	struct classify_attr_vlan_handlers *vlan_ranges_handlers = container_of(
		attr_handlers, struct classify_attr_vlan_handlers, attr_handlers
	);
	(void)attr;

	struct filter_vlan_ranges ranges;
	vlan_ranges_handlers->get_vlan_ranges(rule, &ranges);

	return ranges.count == 0 ||
	       ranges.items[0].to - ranges.items[0].from == 65535;
}

static inline int
classify_attr_vlan_rule_iter(
	struct classify_attr *attr,
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule *rule,
	classify_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	struct classify_attr_vlan_handlers *vlan_ranges_handlers = container_of(
		attr_handlers, struct classify_attr_vlan_handlers, attr_handlers
	);

	struct classify_attr_vlan *vlan_ranges_attr =
		container_of(attr, struct classify_attr_vlan, attr);

	struct classify_query_attr_vlan *query_attr =
		vlan_ranges_attr->query_attr;

	struct filter_vlan_ranges ranges;
	vlan_ranges_handlers->get_vlan_ranges(rule, &ranges);
	const struct filter_vlan_ranges *vlan_ranges = &ranges;

	for (uint32_t range_idx = 0; range_idx < vlan_ranges->count;
	     ++range_idx) {
		const struct filter_vlan_range *vlan_range =
			vlan_ranges->items + range_idx;
		for (uint32_t vlan = vlan_range->from; vlan <= vlan_range->to;
		     ++vlan) {
			if (iter_cb_func(
				    value_table_get_ptr(
					    &query_attr->value_table, 0, vlan
				    ),
				    cb_func_data
			    ) < 0) {
				return -1;
			}
		}
	}

	return 0;
}

static inline void
classify_attr_vlan_free(
	struct memory_context *memory_context, struct classify_attr *attr
) {
	struct classify_attr_vlan *vlan_ranges_attr =
		container_of(attr, struct classify_attr_vlan, attr);

	if (vlan_ranges_attr->query_attr != NULL) {
		value_table_free(&vlan_ranges_attr->query_attr->value_table);

		memory_bfree(
			memory_context,
			vlan_ranges_attr->query_attr,
			sizeof(struct classify_query_attr_vlan)
		);
	}

	memory_bfree(
		memory_context,
		vlan_ranges_attr,
		sizeof(struct classify_attr_vlan)
	);
}

static inline struct classify_query_attr *
classify_attr_vlan_commit(
	struct memory_context *memory_context, struct classify_attr *attr
) {
	struct classify_attr_vlan *vlan_ranges_attr =
		container_of(attr, struct classify_attr_vlan, attr);

	struct classify_query_attr_vlan *query_attr =
		vlan_ranges_attr->query_attr;
	vlan_ranges_attr->query_attr = NULL;

	classify_attr_vlan_free(memory_context, attr);

	return &query_attr->attr;
}

static inline uint32_t
classify_attr_vlan_hash(
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {
	const struct classify_attr_vlan_handlers *vlan_handlers = container_of(
		attr_handlers, struct classify_attr_vlan_handlers, attr_handlers
	);

	struct filter_vlan_ranges ranges;
	vlan_handlers->get_vlan_ranges(rule, &ranges);

	uint32_t hash = ranges.count;
	for (uint32_t idx = 0; idx < ranges.count; ++idx) {
		hash = hash * 31 + ranges.items[idx].from;
		hash = hash * 31 + ranges.items[idx].to;
	}
	return hash;
}

static inline int
classify_attr_vlan_compare(
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule *first,
	const struct filter_rule *second
) {
	const struct classify_attr_vlan_handlers *vlan_handlers = container_of(
		attr_handlers, struct classify_attr_vlan_handlers, attr_handlers
	);

	struct filter_vlan_ranges first_ranges;
	struct filter_vlan_ranges second_ranges;
	vlan_handlers->get_vlan_ranges(first, &first_ranges);
	vlan_handlers->get_vlan_ranges(second, &second_ranges);

	if (first_ranges.count != second_ranges.count) {
		return 1;
	}
	if (first_ranges.count == 0) {
		return 0;
	}

	return memcmp(first_ranges.items,
		      second_ranges.items,
		      first_ranges.count * sizeof(*first_ranges.items)) != 0;
}

static const struct classify_attr_handlers classify_attr_vlan_handlers = {
	.create = classify_attr_vlan_create,
	.size = classify_attr_vlan_size,
	.iter = classify_attr_vlan_iter,
	.rule_is_any = classify_attr_vlan_rule_is_any,
	.hash = classify_attr_vlan_hash,
	.compare = classify_attr_vlan_compare,
	.rule_iter = classify_attr_vlan_rule_iter,
	.commit = classify_attr_vlan_commit,
	.free_compile = classify_attr_vlan_free,
	.free_query = classify_query_attr_vlan_free,
};

static inline void
filter_rule_get_vlan_ranges(
	const struct filter_rule *rule, struct filter_vlan_ranges *vlan_ranges
) {
	vlan_ranges->count = rule->vlan_range_count;
	vlan_ranges->items = rule->vlan_ranges;
}

static const struct classify_attr_vlan_handlers classify_attr_vlan = {
	.attr_handlers = classify_attr_vlan_handlers,
	.get_vlan_ranges = filter_rule_get_vlan_ranges,
};
