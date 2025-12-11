#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "filter.h"
#include "helper.h"

static int
filter_build(
	struct filter *filter,
	const struct filter_rule *rules,
	uint32_t rule_count
) {
	// build leaves
	for (size_t i = 0; i < filter->n; ++i) {
		struct filter_attribute *attr = filter->attr[i];
		struct filter_vertex *v = &filter->v[filter->n + i];

		int res = value_registry_init(
			&v->registry, &filter->memory_context
		);
		if (res < 0) {
			return res;
		}

		res = attr->init_func(
			&v->registry,
			&v->data,
			rules,
			rule_count,
			&filter->memory_context
		);
		if (res < 0) {
			return res;
		}
	}

	// n=1 is corner case because
	// leaf for attribute 0 is vertex 1,
	// but vertex 1 is root for cases n>1.
	if (filter->n == 1) {
		// in this case,
		// root is vertex 0, 1 is leaf, and also there is
		// dummy registry used to build root.
		// dummy registry contains classifier 0 for every action.
		struct value_registry dummy;
		int res = init_dummy_registry(
			&filter->memory_context, rule_count, &dummy
		);
		if (res < 0) {
			value_registry_free(&dummy);
			return res;
		}
		res = merge_and_set_registry_values(
			&filter->memory_context,
			rules,
			&dummy,
			&filter->v[1].registry,
			&filter->v[0].table,
			&filter->v[0].registry
		);
		if (res < 0) {
			value_registry_free(&dummy);
			return res;
		}

		// Free the dummy registry after successful merge
		value_registry_free(&dummy);

		// dummy classifier is always 0
		filter->v[0].slots[0] = 0;

		return 0;
	}

	// build the rest vertices except root
	for (size_t idx = filter->n - 1; idx >= 2; --idx) {
		int res = merge_and_collect_registry(
			&filter->memory_context,
			&filter->v[2 * idx].registry,
			&filter->v[2 * idx + 1].registry,
			&filter->v[idx].table,
			&filter->v[idx].registry
		);
		if (res < 0) {
			return res;
		}
	}

	// build root
	return merge_and_set_registry_values(
		&filter->memory_context,
		rules,
		&filter->v[2 * 1].registry,
		&filter->v[2 * 1 + 1].registry,
		&filter->v[1].table,
		&filter->v[1].registry
	);
}

int
filter_init(
	struct filter *filter,
	const struct filter_attribute **attributes,
	uint32_t attributes_count,
	const struct filter_rule *rules,
	uint32_t rule_count,
	struct memory_context *memory_context
) {
	filter->n = attributes_count;
	if (attributes_count == 0) {
		return -1;
	}

	int res = memory_context_init_from(
		&filter->memory_context, memory_context, "filter"
	);
	if (res < 0) {
		return res;
	}

	memcpy(filter->attr,
	       attributes,
	       attributes_count * sizeof(struct filter_attribute *));

	return filter_build(filter, rules, rule_count);
}

int
filter_query(
	struct filter *filter,
	struct packet *packet,
	const uint32_t **actions,
	uint32_t *count
) {
	// calculate classifiers for attributes
	for (size_t attr_idx = 0; attr_idx < filter->n; ++attr_idx) {
		size_t vertex = filter->n + attr_idx;

		struct filter_attribute *attr = filter->attr[attr_idx];
		struct filter_vertex *v = &filter->v[vertex];

		// store calculated classifier in the parent vertex
		filter->v[vertex / 2].slots[vertex & 1] =
			attr->query_func(packet, ADDR_OF(&v->data));
	}

	// calculate classifiers for the rest vertices except root
	for (size_t vertex = filter->n - 1; vertex >= 2; --vertex) {
		// here both slots must be calculated already
		struct filter_vertex *v = &filter->v[vertex];

		// store calculated classifier in the parent vertex
		filter->v[vertex / 2].slots[vertex & 1] =
			value_table_get(&v->table, v->slots[0], v->slots[1]);
	}

	// get result from root

	// root is 1 when n>1 and 0 else.
	size_t root = filter->n > 1;
	struct filter_vertex *r = &filter->v[root];

	uint32_t result = value_table_get(&r->table, r->slots[0], r->slots[1]);

	struct value_range *range = ADDR_OF(&r->registry.ranges) + result;
	*actions = ADDR_OF(&range->values);
	*count = range->count;

	return 0;
}

void
filter_free(struct filter *filter) {
	if (filter->n == 0) {
		// do nothing
		return;
	}

	for (size_t i = 0; i < filter->n; ++i) {
		struct filter_attribute *attr = filter->attr[i];
		struct filter_vertex *v = &filter->v[filter->n + i];
		attr->free_func(ADDR_OF(&v->data), &filter->memory_context);
	}
	for (size_t i = 1; i < 2 * filter->n; ++i) {
		value_registry_free(&filter->v[i].registry);
	}
	for (size_t i = 1; i < filter->n; ++i) {
		value_table_free(&filter->v[i].table);
	}

	if (filter->n == 1) {
		struct filter_vertex *v = &filter->v[0];
		value_registry_free(&v->registry);
		value_table_free(&v->table);
	}
}
