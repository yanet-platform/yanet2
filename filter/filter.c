#include "attribute.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "filter.h"
#include "helper.h"

static int
filter_build(
	struct filter *filter,
	const struct filter_action *actions,
	uint32_t actions_count
) {
	// build leaves
	for (size_t i = 0; i < filter->n; ++i) {
		struct filter_attribute *attr = &filter->attr[i];
		struct filter_vertex *v = &filter->v[filter->n + i];

		int res = attr->init_func(
			&v->registry,
			&v->data,
			actions,
			actions_count,
			&filter->memory_context
		);
		if (res < 0) {
			return res;
		}
	}

	// n=1 is corner case because root is leaf
	if (filter->n == 1) {
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
	return set_registry_values(
		&filter->memory_context,
		actions,
		&filter->v[2 * 1].registry,
		&filter->v[2 * 1 + 1].registry,
		&filter->v[1].table,
		&filter->v[1].registry
	);
}

int
filter_init(
	struct filter *filter,
	const struct filter_attribute *attributes,
	uint32_t attributes_count,
	const struct filter_action *actions,
	uint32_t actions_count,
	struct memory_context *memory_context
) {
	if (attributes_count == 0) {
		return -1;
	}
	int res = memory_context_init_from(
		&filter->memory_context, memory_context, "filter"
	);
	if (res < 0) {
		return res;
	}
	filter->n = attributes_count;
	memcpy(filter->attr,
	       attributes,
	       attributes_count * sizeof(struct filter_attribute));
	return filter_build(filter, actions, actions_count);
}

int
filter_query(
	struct filter *filter,
	struct packet *packet,
	uint32_t **actions,
	uint32_t *count
) {
	// calculate classifiers for attributes
	for (size_t attr_idx = 0; attr_idx < filter->n; ++attr_idx) {
		size_t vertex = filter->n + attr_idx;

		struct filter_attribute *attr = &filter->attr[attr_idx];
		struct filter_vertex *v = &filter->v[vertex];

		// store calculated classifier in the parent vertex
		filter->v[vertex / 2].slots[vertex & 1] =
			attr->lookup_func(packet, v->data);
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
	struct filter_vertex *r = &filter->v[1];

	uint32_t result;
	if (filter->n == 1) { // n=1 is corner case
		// calculated in the first cycle
		result = filter->v[0].slots[1];
	} else {
		result = value_table_get(&r->table, r->slots[0], r->slots[1]);
	}

	struct value_range *range = ADDR_OF(&r->registry.ranges) + result;
	*actions = ADDR_OF(&r->registry.values) + range->from;
	*count = range->count;

	return 0;
}