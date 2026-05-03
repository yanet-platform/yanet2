#include "filter/classifiers/vlan.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "filter/rule.h"

#include <stdint.h>

typedef void (*filter_rule_get_vlan_ranges_func)(
	const struct filter_rule *filter_rule,
	struct filter_vlan_ranges *vlan_ranges
);

struct filter_compile_attr_vlan {
	struct filter_compile_attr attr;
	struct filter_query_attr_vlan *query_attr;
};

struct filter_compile_attr_vlan_handlers {
	struct filter_compile_attr_handlers attr_handlers;
	filter_rule_get_vlan_ranges_func get_vlan_ranges;
};

static inline struct filter_compile_attr *
filter_compile_attr_vlan_create(
	struct memory_context *memory_context,
	const struct filter_compile_attr_handlers *handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
) {
	(void)handlers;
	(void)rules;
	(void)rule_count;

	struct filter_compile_attr_vlan *attr =
		(struct filter_compile_attr_vlan *)memory_balloc(
			memory_context, sizeof(struct filter_compile_attr_vlan)
		);
	if (attr == NULL) {
		return NULL;
	}

	attr->query_attr = (struct filter_query_attr_vlan *)memory_balloc(
		memory_context, sizeof(struct filter_query_attr_vlan)
	);
	if (attr->query_attr == NULL)
		goto error_free;

	if (value_table_init(
		    &attr->query_attr->value_table, memory_context, 1, 4096
	    )) {
		goto error_free_attr;
	}

	return &attr->attr;

error_free_attr:
	memory_bfree(
		memory_context,
		attr->query_attr,
		sizeof(struct filter_query_attr_vlan)
	);

error_free:
	memory_bfree(
		memory_context, attr, sizeof(struct filter_compile_attr_vlan)
	);

	return NULL;
}

static inline uint32_t
filter_compile_attr_vlan_size(const struct filter_compile_attr *attr) {
	(void)attr;
	return 4096;
}

static inline int
filter_compile_attr_vlan_iter(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	(void)attr_handlers;

	struct filter_compile_attr_vlan *vlan_ranges_attr =
		container_of(attr, struct filter_compile_attr_vlan, attr);

	struct filter_query_attr_vlan *query_attr =
		vlan_ranges_attr->query_attr;

	for (uint32_t idx = 0; idx < 4096; ++idx)
		if (iter_cb_func(
			    value_table_get_ptr(
				    &query_attr->value_table, 0, idx
			    ),
			    cb_func_data
		    ))
			return -1;
	return 0;
}

static inline int
filter_compile_attr_vlan_rule_is_any(
	const struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {
	struct filter_compile_attr_vlan_handlers *vlan_ranges_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_vlan_handlers,
			attr_handlers
		);
	(void)attr;

	struct filter_vlan_ranges ranges;
	vlan_ranges_handlers->get_vlan_ranges(rule, &ranges);

	return ranges.count == 0 ||
	       ranges.items[0].to - ranges.items[0].from == 65535;
}

static inline int
filter_compile_attr_vlan_rule_iter(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	struct filter_compile_attr_vlan_handlers *vlan_ranges_handlers =
		container_of(
			attr_handlers,
			struct filter_compile_attr_vlan_handlers,
			attr_handlers
		);

	struct filter_compile_attr_vlan *vlan_ranges_attr =
		container_of(attr, struct filter_compile_attr_vlan, attr);

	struct filter_query_attr_vlan *query_attr =
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
			    )) {
				return -1;
			}
		}
	}

	return 0;
}

static inline void
filter_compile_attr_vlan_free(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_attr_vlan *vlan_ranges_attr =
		container_of(attr, struct filter_compile_attr_vlan, attr);

	if (vlan_ranges_attr->query_attr != NULL) {
		value_table_free(&vlan_ranges_attr->query_attr->value_table);

		memory_bfree(
			memory_context,
			vlan_ranges_attr->query_attr,
			sizeof(struct filter_query_attr_vlan)
		);
	}

	memory_bfree(
		memory_context,
		vlan_ranges_attr,
		sizeof(struct filter_compile_attr_vlan)
	);
}

static inline struct filter_query_attr *
filter_compile_attr_vlan_commit(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_attr_vlan *vlan_ranges_attr =
		container_of(attr, struct filter_compile_attr_vlan, attr);

	struct filter_query_attr_vlan *query_attr =
		vlan_ranges_attr->query_attr;
	vlan_ranges_attr->query_attr = NULL;

	filter_compile_attr_vlan_free(memory_context, attr);

	return &query_attr->attr;
}

static const struct filter_compile_attr_handlers
	filter_compile_attr_vlan_handlers = {
		.create = filter_compile_attr_vlan_create,
		.size = filter_compile_attr_vlan_size,
		.iter = filter_compile_attr_vlan_iter,
		.rule_is_any = filter_compile_attr_vlan_rule_is_any,
		.rule_iter = filter_compile_attr_vlan_rule_iter,
		.commit = filter_compile_attr_vlan_commit,
		.free_compile = filter_compile_attr_vlan_free,
		.free_query = filter_query_attr_vlan_free,
};

static inline void
filter_rule_get_vlan_ranges(
	const struct filter_rule *rule, struct filter_vlan_ranges *vlan_ranges
) {
	vlan_ranges->count = rule->vlan_range_count;
	vlan_ranges->items = rule->vlan_ranges;
}

static const struct filter_compile_attr_vlan_handlers filter_compile_attr_vlan =
	{
		.attr_handlers = filter_compile_attr_vlan_handlers,
		.get_vlan_ranges = filter_rule_get_vlan_ranges,
};

int
FILTER_ATTR_COMPILER_INIT_FUNC(vlan)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	return filter_compile_attr_build(
		&filter_compile_attr_vlan.attr_handlers,
		registry,
		data,
		rules,
		rule_count,
		memory_context

	);
}

void
FILTER_ATTR_COMPILER_FREE_FUNC(vlan)(
	void *data, struct memory_context *memory_context
) {
	struct filter_query_attr_vlan *attr =
		(struct filter_query_attr_vlan *)data;
	if (attr == NULL)
		return;

	filter_query_attr_vlan_free(memory_context, &attr->attr);
}
