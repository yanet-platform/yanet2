#pragma once

#include "common/lpm.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "lib/classify/classifiers/net4.h"
#include "lib/classify/rule.h"
#include "partition.h"

#include <stdint.h>
#include <string.h>

/*
 * Getter of the IPv4 networks of a module rule - the source or the
 * destination side - filled by the module and adapted to the rule
 * agnostic core through the compile macro below.
 */
typedef void (*classify_net4_get_net4s_func)(
	const struct classifier_rule *rule, struct filter_net4s *net
);

/*
 * Compile scratch of the IPv4 network attribute: the region partition
 * of the address domain with the class table over its regions.
 *
 * The rule field selector of the compiled side - source or destination
 * networks - is stored in the scratch, so one body and one set of
 * callbacks serve both sides.
 */
struct classify_compile_net4 {
	classify_net4_get_net4s_func get_net4s;
	const char *name;

	struct classify_partition partition;
	struct value_table value_table;
};

// The address of an IPv4 network masked to its prefix, as a key.
static inline uint64_t
classify_net4_from(const struct net4 *net4) {
	return classify_key_load(net4->addr, 4) &
	       classify_key_load(net4->mask, 4);
}

static inline uint64_t
classify_net4_len(const struct net4 *net4) {
	return __builtin_popcountll(*(net4_word *)net4->mask);
}

static inline struct classify_compile_net4 *
classify_compile_net4_create(
	struct memory_context *memory_context,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_net4_get_net4s_func get_net4s,
	const char *name
) {
	struct classify_compile_net4 *compile =
		(struct classify_compile_net4 *)memory_balloc(
			memory_context, sizeof(struct classify_compile_net4)
		);
	if (compile == NULL) {
		return NULL;
	}

	compile->get_net4s = get_net4s;
	compile->name = name;

	struct classify_prefixes prefixes = {NULL, 0};

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct classifier_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		struct filter_net4s nets;
		get_net4s(rule, &nets);
		const struct filter_net4s *net = &nets;

		for (struct net4 *net4 = net->items;
		     net4 < net->items + net->count;
		     ++net4) {
			if (classify_prefixes_add(
				    memory_context,
				    &prefixes,
				    classify_net4_from(net4),
				    classify_net4_len(net4)
			    )) {
				goto error_prefixes;
			}
		}
	}

	if (classify_partition_build(
		    memory_context, 32, &prefixes, &compile->partition
	    )) {
		goto error_prefixes;
	}

	char table_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(table_name, sizeof(table_name), name, "table");
	if (value_table_init(
		    &compile->value_table,
		    memory_context,
		    table_name,
		    1,
		    compile->partition.region_count
	    )) {
		goto error_partition;
	}

	classify_prefixes_free(memory_context, &prefixes);

	return compile;

error_partition:
	classify_partition_free(memory_context, &compile->partition);

error_prefixes:
	classify_prefixes_free(memory_context, &prefixes);

	memory_bfree(
		memory_context, compile, sizeof(struct classify_compile_net4)
	);

	return NULL;
}

static inline uint32_t
classify_compile_net4_size(const void *compile) {
	const struct classify_compile_net4 *net4_compile = compile;

	return net4_compile->value_table.h_dim *
	       net4_compile->value_table.v_dim;
}

static inline int
classify_compile_net4_rule_is_any(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_net4 *net4_compile = compile;

	struct filter_net4s nets;
	net4_compile->get_net4s(rule, &nets);

	return nets.count == 0 ||
	       (nets.items[0].mask[0] == 0 && nets.items[0].mask[1] == 0 &&
		nets.items[0].mask[2] == 0 && nets.items[0].mask[3] == 0);
}

static inline int
classify_compile_net4_rule_iter(
	void *compile,
	const struct classifier_rule *rule,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_net4 *net4_compile = compile;

	struct filter_net4s nets;
	net4_compile->get_net4s(rule, &nets);
	const struct filter_net4s *net = &nets;

	const struct classify_partition *partition = &net4_compile->partition;

	for (uint32_t net_idx = 0; net_idx < net->count; ++net_idx) {
		const struct net4 *net4 = net->items + net_idx;

		uint64_t from = classify_net4_from(net4);
		uint32_t start;
		uint32_t stop;
		classify_partition_bounds(
			partition,
			from,
			classify_prefix_to(32, from, classify_net4_len(net4)),
			&start,
			&stop
		);

		for (uint32_t idx = start; idx < stop; ++idx) {
			if (iter_cb_func(
				    value_table_get_ptr(
					    &net4_compile->value_table,
					    0,
					    partition->values[idx]
				    ),
				    cb_func_data
			    ) < 0) {
				return -1;
			}
		}
	}

	return 0;
}

static inline int
classify_compile_net4_iter(
	void *compile,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_net4 *net4_compile = compile;

	for (uint32_t idx = 0; idx < net4_compile->value_table.h_dim; ++idx) {
		if (iter_cb_func(
			    value_table_get_ptr(
				    &net4_compile->value_table, 0, idx
			    ),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
	}

	return 0;
}

static inline void
classify_compile_net4_free(
	struct memory_context *memory_context, void *compile
) {
	struct classify_compile_net4 *net4_compile = compile;

	classify_partition_free(memory_context, &net4_compile->partition);
	value_table_free(&net4_compile->value_table);

	memory_bfree(
		memory_context,
		net4_compile,
		sizeof(struct classify_compile_net4)
	);
}

static inline int
classify_compile_net4_commit(
	struct memory_context *memory_context, void *compile, void *attr
) {
	struct classify_compile_net4 *net4_compile = compile;
	struct classify_attr_net4 *net4_attr = attr;

	/*
	 * The match is built directly in the embedded attribute: its pages
	 * are addressed through relative pointers, so a built elsewhere
	 * tree cannot move in. The region identifiers of the partition are
	 * remapped through the compacted class table in one pass.
	 */
	char lpm_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(
		lpm_name, sizeof(lpm_name), net4_compile->name, "lpm"
	);
	if (lpm_init(&net4_attr->lpm, memory_context, lpm_name)) {
		// The init links the embedded context into its parent before
		// the root page allocates, so the zeroing goes through the
		// free to unlink it first.
		lpm_free(&net4_attr->lpm);
		memset(net4_attr, 0, sizeof(*net4_attr));
		return -1;
	}

	if (classify_partition_build_lpm(
		    &net4_compile->partition,
		    net4_compile->partition.values,
		    &net4_attr->lpm
	    )) {
		goto error;
	}

	lpm4_remap(&net4_attr->lpm, &net4_compile->value_table);
	lpm4_compact(&net4_attr->lpm);

	classify_compile_net4_free(memory_context, compile);

	return 0;

error:
	lpm_free(&net4_attr->lpm);
	memset(net4_attr, 0, sizeof(*net4_attr));
	return -1;
}

static inline uint32_t
classify_compile_net4_hash_nets(const struct filter_net4s *nets) {
	uint32_t hash = nets->count;
	for (uint32_t idx = 0; idx < nets->count; ++idx) {
		const struct net4 *net = nets->items + idx;
		for (uint32_t byte_idx = 0; byte_idx < 4; ++byte_idx) {
			hash = hash * 31 +
			       (net->addr[byte_idx] & net->mask[byte_idx]);
		}
		for (uint32_t byte_idx = 0; byte_idx < 4; ++byte_idx) {
			hash = hash * 31 + net->mask[byte_idx];
		}
	}
	return hash;
}

static inline int
classify_compile_net4_compare_nets(
	const struct filter_net4s *first_nets,
	const struct filter_net4s *second_nets
) {
	if (first_nets->count != second_nets->count) {
		return 1;
	}

	for (uint32_t idx = 0; idx < first_nets->count; ++idx) {
		const struct net4 *first_net = first_nets->items + idx;
		const struct net4 *second_net = second_nets->items + idx;

		for (uint32_t byte_idx = 0; byte_idx < 4; ++byte_idx) {
			if ((first_net->addr[byte_idx] &
			     first_net->mask[byte_idx]) !=
			    (second_net->addr[byte_idx] &
			     second_net->mask[byte_idx])) {
				return 1;
			}
			if (first_net->mask[byte_idx] !=
			    second_net->mask[byte_idx]) {
				return 1;
			}
		}
	}

	return 0;
}

static inline uint32_t
classify_compile_net4_hash(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_net4 *net4_compile = compile;

	struct filter_net4s nets;
	net4_compile->get_net4s(rule, &nets);
	return classify_compile_net4_hash_nets(&nets);
}

static inline int
classify_compile_net4_compare(
	const void *compile,
	const struct classifier_rule *first,
	const struct classifier_rule *second
) {
	const struct classify_compile_net4 *net4_compile = compile;

	struct filter_net4s first_nets;
	struct filter_net4s second_nets;
	net4_compile->get_net4s(first, &first_nets);
	net4_compile->get_net4s(second, &second_nets);
	return classify_compile_net4_compare_nets(&first_nets, &second_nets);
}

/*
 * Compiles the IPv4 network classifier of a ruleset into an embedded
 * attribute.
 *
 * The name scopes the memory contexts of the compile - the attribute
 * match and the stage registry - each titled with the name and its own
 * artifact leaf, in the memory tree.
 *
 * On success the attribute and the stage - the registry with the rule
 * group row - are owned by the caller, released through
 * classifier_fini; on failure every partial state is freed, the
 * attribute and the stage are left zeroed.
 */
static inline int
classify_net4_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_net4_get_net4s_func get_net4s,
	struct classify_attr_net4 *attr,
	struct classifier *cls
) {
	struct classify_compile_net4 *compile = classify_compile_net4_create(
		memory_context, rules, rule_count, get_net4s, name
	);
	if (compile == NULL) {
		memset(attr, 0, sizeof(*attr));
		memset(cls, 0, sizeof(*cls));
		return -1;
	}

	const struct classify_attr_ops ops = {
		.size = classify_compile_net4_size,
		.rule_is_any = classify_compile_net4_rule_is_any,
		.hash = classify_compile_net4_hash,
		.compare = classify_compile_net4_compare,
		.iter = classify_compile_net4_iter,
		.rule_iter = classify_compile_net4_rule_iter,
		.commit = classify_compile_net4_commit,
		.free_compile = classify_compile_net4_free,
	};

	return classify_attr_compile(
		memory_context,
		name,
		&ops,
		compile,
		rules,
		rule_count,
		attr,
		cls
	);
}

/*
 * Declares the IPv4 network compile entry of one consumer side: the
 * getter selects the source or the destination networks of the module
 * rule and adapts them to the rule agnostic core, and the entry takes
 * the module rule array the consumer owns.
 *
 * The tag must carry the consumer prefix, so the generated symbols
 * never collide with the library ones.
 */
#define CLASSIFY_NET4_COMPILE(tag, get_net4s)                                  \
	static inline int classify_##tag##_compile(                            \
		struct memory_context *memory_context,                         \
		const char *name,                                              \
		const struct classifier_rule **rules,                          \
		uint32_t rule_count,                                           \
		struct classify_attr_net4 *attr,                               \
		struct classifier *cls                                         \
	) {                                                                    \
		return classify_net4_compile(                                  \
			memory_context,                                        \
			name,                                                  \
			rules,                                                 \
			rule_count,                                            \
			get_net4s,                                             \
			attr,                                                  \
			cls                                                    \
		);                                                             \
	}
