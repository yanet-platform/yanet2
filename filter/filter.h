#pragma once

#include "attribute.h"
#include "common/registry.h"
#include "helper.h"

////////////////////////////////////////////////////////////////////////////////

// Represents vertex in the classfication tree
struct filter_vertex {
	struct value_registry registry;

	// 2-dim table
	// [left_son_classifier][right_son_classifier]
	// -> combined classifier
	//
	// does not fill the table for leaves
	struct value_table table;

	// Calculated classifier
	// from left son and right son.
	//
	// Not relevant for leaves.
	uint32_t slots[2];

	// Data structure which allows to
	// lookup classifier of packet attribute
	// corresponds to leaf.
	void *data;
};

struct filter {
	// Vertices in the classification tree.
	//
	// Vertices enumerated in [1..2*n-1].
	// Leaves are in [n..2*n-1].
	// 1 is root.
	struct filter_vertex v[2 * MAX_ATTRIBUTES];

	// Filter attributes
	struct filter_attribute *attr[MAX_ATTRIBUTES];

	// Attributes count
	uint32_t n;

	struct memory_context memory_context;
};

////////////////////////////////////////////////////////////////////////////////

// Allows to initialize filter with provided attributes and actions.
int
filter_init(
	struct filter *filter,
	const struct filter_attribute **attributes,
	uint32_t attributes_count,
	const struct filter_rule *rules,
	uint32_t rule_count,
	struct memory_context *memory_context
);

// Allows to query actions corresponds to the provided packet.
int
filter_query(
	struct filter *filter,
	struct packet *packet,
	uint32_t **actions,
	uint32_t *count
);

// Allows to free filter memory.
void
filter_free(struct filter *filter);

////////////////////////////////////////////////////////////////////////////////

#define FILTER_DECLARE(tag, ...)                                               \
	static const struct filter_attribute *__filter_attrs_##tag[] = {       \
		__VA_ARGS__                                                    \
	};                                                                     \
	struct filter __filter_##tag;

#define FILTER_INIT(tag, rules, rule_count, ctx, res)                          \
	do {                                                                   \
		struct filter *filter = &(__filter_##tag);                     \
		if (sizeof(__filter_attrs_##tag) == 0) {                       \
			*(res) = -1;                                           \
			goto init_failed;                                      \
		}                                                              \
		*(res) = memory_context_init_from(                             \
			&(filter)->memory_context, ctx, "filter"               \
		);                                                             \
		if (*(res) < 0) {                                              \
			goto init_failed;                                      \
		}                                                              \
		const size_t n = sizeof(__filter_attrs_##tag) /                \
				 sizeof(struct filter_attribute *);            \
		for (size_t i = 0; i < n; ++i) {                               \
			const struct filter_attribute *attr =                  \
				__filter_attrs_##tag[i];                       \
			struct filter_vertex *v = &(filter)->v[n + i];         \
			*(res) = value_registry_init(                          \
				&v->registry, &(filter)->memory_context        \
			);                                                     \
			if (*(res) < 0) {                                      \
				goto init_failed;                              \
			}                                                      \
			*(res) = attr->init_func(                              \
				&v->registry,                                  \
				&v->data,                                      \
				rules,                                         \
				rule_count,                                    \
				&(filter)->memory_context                      \
			);                                                     \
			if (*(res) < 0) {                                      \
				goto init_failed;                              \
			}                                                      \
		}                                                              \
		if (n == 1) {                                                  \
			struct value_registry dummy;                           \
			*(res) = init_dummy_registry(                          \
				&(filter)->memory_context, rule_count, &dummy  \
			);                                                     \
			if (*(res) < 0) {                                      \
				value_registry_free(&dummy);                   \
				goto init_failed;                              \
			}                                                      \
			*(res) = merge_and_set_registry_values(                \
				&(filter)->memory_context,                     \
				rules,                                         \
				&dummy,                                        \
				&(filter)->v[1].registry,                      \
				&(filter)->v[0].table,                         \
				&(filter)->v[0].registry                       \
			);                                                     \
			if (*(res) < 0) {                                      \
				value_registry_free(&dummy);                   \
				goto init_failed;                              \
			}                                                      \
			(filter)->v[0].slots[0] = 0;                           \
			goto init_finish;                                      \
		}                                                              \
		for (size_t idx = n - 1; idx >= 2; --idx) {                    \
			*(res) = merge_and_collect_registry(                   \
				&(filter)->memory_context,                     \
				&(filter)->v[2 * idx].registry,                \
				&(filter)->v[2 * idx + 1].registry,            \
				&(filter)->v[idx].table,                       \
				&(filter)->v[idx].registry                     \
			);                                                     \
			if (*(res) < 0) {                                      \
				goto init_failed;                              \
			}                                                      \
		}                                                              \
		*(res) = merge_and_set_registry_values(                        \
			&(filter)->memory_context,                             \
			rules,                                                 \
			&(filter)->v[2 * 1].registry,                          \
			&(filter)->v[2 * 1 + 1].registry,                      \
			&(filter)->v[1].table,                                 \
			&(filter)->v[1].registry                               \
		);                                                             \
	} while (0);                                                           \
	init_failed:                                                           \
	init_finish:

////////////////////////////////////////////////////////////////////////////////

#define FILTER_QUERY(tag, packet, actions, actions_count)                      \
	do {                                                                   \
		struct filter *filter = &(__filter_##tag);                     \
		const size_t n = sizeof(__filter_attrs_##tag) /                \
				 sizeof(struct filter_attribute *);            \
		for (size_t attr_idx = 0; attr_idx < n; ++attr_idx) {          \
			size_t vertex = n + attr_idx;                          \
			const struct filter_attribute *attr =                  \
				__filter_attrs_##tag[attr_idx];                \
			struct filter_vertex *v = &((filter)->v)[vertex];      \
			(filter)->v[vertex / 2].slots[vertex & 1] =            \
				attr->lookup_func(packet, v->data);            \
		}                                                              \
		for (size_t vertex = n - 1; vertex >= 2; --vertex) {           \
			struct filter_vertex *v = &((filter)->v)[vertex];      \
			(filter)->v[vertex / 2].slots[vertex & 1] =            \
				value_table_get(                               \
					&v->table, v->slots[0], v->slots[1]    \
				);                                             \
		}                                                              \
		const size_t root = n > 1;                                     \
		struct filter_vertex *r = &((filter)->v)[root];                \
		uint32_t result =                                              \
			value_table_get(&r->table, r->slots[0], r->slots[1]);  \
		struct value_range *range =                                    \
			ADDR_OF(&r->registry.ranges) + result;                 \
		*(actions) = ADDR_OF(&r->registry.values) + range->from;       \
		*(actions_count) = range->count;                               \
	} while (0)

////////////////////////////////////////////////////////////////////////////////

#define FILTER_FREE(tag)                                                       \
	do {                                                                   \
		struct filter *filter = &(__filter_##tag);                     \
		const size_t n = sizeof(__filter_attrs_##tag) /                \
				 sizeof(struct filter_attribute *);            \
		if (n == 0) {                                                  \
			goto free_finish;                                      \
		}                                                              \
		for (size_t i = 0; i < n; ++i) {                               \
			const struct filter_attribute *attr =                  \
				__filter_attrs_##tag[i];                       \
			struct filter_vertex *v = &(filter)->v[n + i];         \
			attr->free_func(v->data, &(filter)->memory_context);   \
		}                                                              \
		for (size_t i = 1; i < 2 * n; ++i) {                           \
			value_registry_free(&(filter)->v[i].registry);         \
		}                                                              \
		for (size_t i = 1; i < n; ++i) {                               \
			value_table_free(&(filter)->v[i].table);               \
		}                                                              \
		if (n == 1) {                                                  \
			struct filter_vertex *v = &(filter)->v[0];             \
			value_registry_free(&v->registry);                     \
			value_table_free(&v->table);                           \
		}                                                              \
	} while (0);                                                           \
	free_finish:
