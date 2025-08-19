// The purpose of the filter is to classify packets based on its attributes
// (ip addresses, ports, protocols and so on). Classification rules should
// be specified by user. Result of the packet classification is the list of
// rules which packet satisfies to. If there is a terminal rule, then all rules
// with greater number are discarded. Also, every rules is associated with an
// integer number, called action. After the filter is initialized with rules,
// user can query filter with packets. Result of each query is the list of rule
// actions, which the packet satisfies to.
//
// In this terms, after initialization, Filter is a function from Packet to List
// of Actions. We introduce notation: F(P) = {a1, ..., aN}, where F is filter, P
// is packet and {a1, ..., aN} is list of actions of length N.
//
// Consider the following example:
// Suppose rule list is:
//	1. (Src ip from 255.255.240.0/20) and (Src port >= 1000) and (Src port
//<= 2000) |action = 10| <-- terminal
//	2. (Src ip from 255.255.255.0/24) and (Src port >= 1500) |action = 20|
//<-- not terminal
// 	3. (Src ip from 255.255.0.0/16) and (Src port <= 3000) |action = 30| <--
// terminal These rules can be used to initialize filter F. Then packet P1 with
// [Src ip = 255.255.255.1, Src port = 1500] corresponds to rules 1 and 2, and,
// as the first rule is terminal, F(P1) = {10}. Packet P2 with [Src ip =
// 255.255.255.1, Src port = 2500] corresponds to rules 2 and 3, and F(P2) =
// {20, 30} as the second rule is not terminal. Packet P3 with [Src ip =
// 255.240.10.15, Src port = 2500] corresponds to no rules, and F(P3) = {}.
//
// To initialize filter, user specifies list of the packet attributes which
// matters during classification, (see `filter_attribute`) and the list of rules
// (see `filter_rule`). For each attribute, user must specify functions to
// classify corresponding packet attribute. Also, for each attribute classifier,
// this functions must fill list of rules, which corresponds to the classifier.
// Refer to the previous example and consider packet attribute `Src port`. There
// are 3 segments: [1000, 2000] for rule 1, [1500, 65536] for rule 2, [0, 3000]
// for rule 3. Then user function can introduce 5 classifiers:
//	0: classifier 0 corresponds to segment [0, 999] and maps to rule 3.
//	1: classifier 1 corresponds to segment [1000, 1499] and maps to rules 1
// and 3.
// 	2: [1500, 2000] -> rules {1, 2, 3}
// 	3: [2001, 3000] -> rules {2, 3}
// 	4: [3001, 65536] -> rule 2.
//
// During filter initialization, algorithm invokes user-defined function for
// initialize classifiers for every packet attribute. Based on the classifiers
// for every packet attribute, algorithm makes classifiers for the pairs of
// packet attributes, and so on. For example, suppose there are 4 attributes in
// filter: [src ip, dst ip, src port, dst port} and the classifiers for them are
// [{0, 1}, {0, 1}, {0, 1, 2}, {0}]. Then, algorithm merges classifiers [src ip,
// dst ip] and [src port and dst port], resulting in classifiers C1 and C2.
// After that, algorithm merges classifiers C1 and C2 and gets final classifier
// C.
//
// Lets illustrate this process step by step:
// 	1. Get C1: merge `src ip` and `dst ip`: {0, 1} x {0, 1} -> {{0, 0}, {0,
// 1}, {1, 0}, {1, 1}}
// 	2. Get C2: merge `src port` and `dst port`: {0, 1, 2} x {0} -> {{0, 0},
// {1, 0}, {2, 0}}
// 	3. Get C: merge C1 and C2: {{0, 0}, {0, 1}, {1, 0}, {1, 1}} x {{0, 0},
// {1, 0}, {2, 0}} =
//			{{0, 0, 0, 0}, {0, 1, 0, 0}, ...}
//
// Results of merges are stored in the vertices of the classification tree.
// Leaves corresponds to packet attributes.The other vertices corresponds to
// merge between left and right son. The root of the classification tree is the
// final classifier. This final classifier holds list of actions for each result
// classifier. To classify packet, algorithm gets result classifier of the
// packet and lookup list of action for the result classifier in the root vertex
// of the classification tree.
//
// To get the final classifier of the packet, first algorithm classifier every
// packet attribute separately. For this, it calls user-provided classification
// function for every attribute. After that algirthm gets classifiers of the
// higher level and so no, until the result classifier is calculated. For every
// such vertex, it stores mapping table table[c1][c2] -> c`, where c1 is the
// classifier in the left son vertex, c2 is the classifier in the right son
// vertex, and c` is the result classifier. Also, for each classifier c` it
// stores list of rules, which corresponds to the classifier c`.
//
// Also, user specifies function to free data allocated by user in the
// initialization function.

////////////////////////////////////////////////////////////////////////////////

#pragma once

#include "attribute.h"
#include "common/registry.h"
#include "helper.h"

////////////////////////////////////////////////////////////////////////////////

// Represents vertex in the classfication tree.
//
// If vertex is a leaf, it corresponds to the classifier of the single packet
// attribute. If vertex is not a leaf, it corresonds to the combined classifier
// of the left vertex son and the right vertex son.
struct filter_vertex {
	// Corresponds to the mapping from classifier to the list of rules
	// which classifier satisfies to.
	// It vertex is a root, then it maps classifier to the list of
	// rule actions instead of the rule numbers.
	struct value_registry registry;

	// 2-dimentional table
	// [left son classifier][right son classifier]
	// -> combined classifier
	//
	// This table is not filled to leaves.
	struct value_table table;

	// This values are used during packet classification.
	// In slots[0] the calculated classifier for the left son is stored.
	// In slots[1] the calculated classifier for the right son is stored.
	// If slots[0] and slots[1] are calculated for the vertex,
	// the classifier for the current vertex can be calculated in the
	// following way:
	// 	result classifier = table[slots[0]][slots[1]].
	// After that, the calculated classifier must be stored in the slots
	// of the parent vertex.
	uint32_t slots[2];

	// This data is dedicated for user.
	// It is passed in the initialization function for
	// the packet attribute classifier, and can be filled by user
	// in any way. After that, user uses this data to classifiy packet
	// attribute.
	void *data;
};

// Represents packet filter.
struct filter {
	// Vertices in the classification tree.
	//
	// Vertices enumerated in [1..2*n-1].
	// Leaves are in [n..2*n-1].
	// 1 is root.
	// The parent for the vertex v is v/2,
	// the left son is v*2, and the right son is v*2+1.
	struct filter_vertex v[2 * MAX_ATTRIBUTES];

	// Filter attributes.
	struct filter_attribute *attr[MAX_ATTRIBUTES];

	// Attributes count
	uint32_t n;

	struct memory_context memory_context;
};

////////////////////////////////////////////////////////////////////////////////

// Allows to initialize filter with provided attributes and rules.
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
	};

#define FILTER_INIT(filter, tag, rules, rule_count, ctx, res)                  \
	do {                                                                   \
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

#define FILTER_QUERY(filter, tag, packet, actions, actions_count)              \
	do {                                                                   \
		const size_t n = sizeof(__filter_attrs_##tag) /                \
				 sizeof(struct filter_attribute *);            \
		for (size_t attr_idx = 0; attr_idx < n; ++attr_idx) {          \
			size_t vertex = n + attr_idx;                          \
			const struct filter_attribute *attr =                  \
				__filter_attrs_##tag[attr_idx];                \
			struct filter_vertex *v = &((filter)->v)[vertex];      \
			(filter)->v[vertex / 2].slots[vertex & 1] =            \
				attr->query_func(packet, v->data);             \
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

#define FILTER_FREE(filter, tag)                                               \
	do {                                                                   \
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
