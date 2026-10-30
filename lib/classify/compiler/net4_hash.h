#pragma once

#include "common/hash_index.h"
#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "lib/classify/classifiers/net4_hash.h"
#include "lib/classify/rule.h"

#include <stdint.h>
#include <string.h>

/*
 * Getter of the IPv4 networks of a module rule - the source or the
 * destination side - filled by the module and adapted to the rule
 * agnostic core through the compile macro below. The same getter type
 * the LPM variant uses, so one field selector serves both variants.
 */
typedef void (*classify_net4_hash_get_net4s_func)(
	const struct classifier_rule *rule, struct filter_net4s *net
);

// The compile scratch of the hashed IPv4 network attribute.
//
// The regions are the distinct configured prefixes - a region identifier
// indexes the parallel keys and lengths arrays and the cell of the value
// table - plus the one miss region behind them, whose class an address
// outside every prefix resolves to. The value table cells start zero and
// the compile core rewrites them into the final classes in place, the
// same cell flow the LPM variant's table runs.
//
// A rule matches every address of a prefix it names, and the addresses
// of a shorter prefix it contains: the rule iteration below touches
// every region the rule's prefixes cover, so the class of a region
// carries the full match set of its addresses and the lookup resolves
// the longest prefix without losing the containing ones.
struct classify_compile_net4_hash {
	classify_net4_hash_get_net4s_func get_net4s;
	const char *name;

	uint32_t region_count;
	uint32_t *keys;
	uint32_t *lengths;
	struct value_table value_table;
	struct hash_index dedup;
};

// The dedup equality of the collection pass: the slot value is the
// region identifier, the data the looked-up masked word.
struct classify_compile_net4_hash_dedup_ctx {
	const struct classify_compile_net4_hash *compile;
	uint32_t key;
};

static inline int
classify_compile_net4_hash_dedup_eq(uint32_t region, const void *data) {
	const struct classify_compile_net4_hash_dedup_ctx *ctx = data;
	return ctx->compile->keys[region] == ctx->key ? 0 : 1;
}

// The prefix length of one network, zero for the unspecified mask.
static inline uint32_t
classify_compile_net4_hash_length(const struct net4 *net4) {
	uint32_t length = 0;
	for (uint32_t idx = 0; idx < NET4_LEN; ++idx) {
		length += __builtin_popcount(net4->mask[idx]);
	}
	return length;
}

static inline uint32_t
classify_compile_net4_hash_key(const struct net4 *net4) {
	uint32_t key = 0;
	for (uint32_t idx = 0; idx < NET4_LEN; ++idx) {
		key = (key << 8) | (net4->addr[idx] & net4->mask[idx]);
	}
	return key;
}

static inline void
classify_compile_net4_hash_free(
	struct memory_context *memory_context, void *compile
) {
	struct classify_compile_net4_hash *net4_compile = compile;

	hash_index_fini(&net4_compile->dedup);
	value_table_free(&net4_compile->value_table);
	if (net4_compile->keys != NULL) {
		memory_bfree(
			memory_context,
			net4_compile->keys,
			sizeof(uint32_t) * net4_compile->region_count
		);
	}
	if (net4_compile->lengths != NULL) {
		memory_bfree(
			memory_context,
			net4_compile->lengths,
			sizeof(uint32_t) * net4_compile->region_count
		);
	}

	memory_bfree(
		memory_context,
		net4_compile,
		sizeof(struct classify_compile_net4_hash)
	);
}

static inline struct classify_compile_net4_hash *
classify_compile_net4_hash_create(
	struct memory_context *memory_context,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_net4_hash_get_net4s_func get_net4s,
	const char *name
) {
	struct classify_compile_net4_hash *compile =
		(struct classify_compile_net4_hash *)memory_balloc(
			memory_context,
			sizeof(struct classify_compile_net4_hash)
		);
	if (compile == NULL) {
		return NULL;
	}

	compile->get_net4s = get_net4s;
	compile->name = name;
	compile->region_count = 0;
	compile->keys = NULL;
	compile->lengths = NULL;

	// The collection upper bound: every network of every rule names its
	// own region until the dedup collapses the shared ones, and the
	// index refuses to fill to its capacity, so it carries one slot
	// beyond the bound.
	uint32_t net_total = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		if (rules[rule_idx] == NULL) {
			continue;
		}
		struct filter_net4s nets;
		get_net4s(rules[rule_idx], &nets);
		net_total += nets.count;
	}
	if (net_total == 0) {
		net_total = 1;
	}

	if (hash_index_init(&compile->dedup, memory_context, net_total + 1)) {
		memory_bfree(
			memory_context,
			compile,
			sizeof(struct classify_compile_net4_hash)
		);
		return NULL;
	}

	compile->keys = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * net_total
	);
	compile->lengths = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * net_total
	);
	if (compile->keys == NULL || compile->lengths == NULL) {
		classify_compile_net4_hash_free(memory_context, compile);
		return NULL;
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct classifier_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		struct filter_net4s nets;
		get_net4s(rule, &nets);
		for (const struct net4 *net4 = nets.items;
		     net4 < nets.items + nets.count;
		     ++net4) {
			// The unspecified network is the rule-is-any case of
			// the core, not a region.
			const uint32_t length =
				classify_compile_net4_hash_length(net4);
			if (length == 0) {
				continue;
			}
			const uint32_t key =
				classify_compile_net4_hash_key(net4);

			struct classify_compile_net4_hash_dedup_ctx ctx = {
				.compile = compile,
				.key = key,
			};
			uint32_t region = hash_index_lookup(
				&compile->dedup,
				classify_net4_hash_key_hash(key),
				classify_compile_net4_hash_dedup_eq,
				&ctx
			);
			if (region != HASH_INDEX_INVALID) {
				continue;
			}
			if (hash_index_insert(
				    &compile->dedup,
				    classify_net4_hash_key_hash(key),
				    compile->region_count
			    )) {
				classify_compile_net4_hash_free(
					memory_context, compile
				);
				return NULL;
			}

			compile->keys[compile->region_count] = key;
			compile->lengths[compile->region_count] = length;
			++compile->region_count;
		}
	}

	// One cell per region plus the miss cell behind them, all starting
	// zero so the compile core's touch pass reaches every region through
	// the zero key the core owns.
	char table_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(
		table_name, sizeof(table_name), compile->name, "table"
	);
	if (value_table_init(
		    &compile->value_table,
		    memory_context,
		    table_name,
		    1,
		    compile->region_count + 1
	    )) {
		classify_compile_net4_hash_free(memory_context, compile);
		return NULL;
	}

	return compile;
}

static inline uint32_t
classify_compile_net4_hash_size(const void *compile) {
	const struct classify_compile_net4_hash *net4_compile = compile;

	// One cell per region plus the miss cell.
	return net4_compile->region_count + 1;
}

static inline int
classify_compile_net4_hash_rule_is_any(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_net4_hash *net4_compile = compile;

	struct filter_net4s nets;
	net4_compile->get_net4s(rule, &nets);

	return nets.count == 0 ||
	       (nets.items[0].mask[0] == 0 && nets.items[0].mask[1] == 0 &&
		nets.items[0].mask[2] == 0 && nets.items[0].mask[3] == 0);
}

static inline int
classify_compile_net4_hash_iter(
	void *compile,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_net4_hash *net4_compile = compile;

	for (uint32_t idx = 0; idx <= net4_compile->region_count; ++idx) {
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

// Reports whether the region names a prefix inside the network: the
// network is at most as long as the region prefix, and the region key
// masked to the network length carries the network address.
static inline int
classify_compile_net4_hash_contains(
	const struct classify_compile_net4_hash *net4_compile,
	uint32_t region,
	const struct net4 *net4
) {
	const uint32_t key = net4_compile->keys[region];
	const uint32_t length = net4_compile->lengths[region];

	uint32_t net_key = 0;
	uint32_t net_length = 0;
	for (uint32_t idx = 0; idx < NET4_LEN; ++idx) {
		net_key = (net_key << 8) | (net4->addr[idx] & net4->mask[idx]);
		net_length += __builtin_popcount(net4->mask[idx]);
	}
	if (net_length == 0 || net_length > length) {
		return 0;
	}
	const uint32_t net_mask =
		net_length == 32 ? 0xffffffffu : ~0u << (32 - net_length);

	return (key & net_mask) == net_key;
}

static inline int
classify_compile_net4_hash_rule_iter(
	void *compile,
	const struct classifier_rule *rule,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_net4_hash *net4_compile = compile;

	struct filter_net4s nets;
	net4_compile->get_net4s(rule, &nets);

	for (const struct net4 *net4 = nets.items;
	     net4 < nets.items + nets.count;
	     ++net4) {
		if (classify_compile_net4_hash_length(net4) == 0) {
			continue;
		}
		for (uint32_t region = 0; region < net4_compile->region_count;
		     ++region) {
			if (!classify_compile_net4_hash_contains(
				    net4_compile, region, net4
			    )) {
				continue;
			}
			if (iter_cb_func(
				    value_table_get_ptr(
					    &net4_compile->value_table,
					    0,
					    region
				    ),
				    cb_func_data
			    ) < 0) {
				return -1;
			}
		}
	}

	return 0;
}

static inline uint32_t
classify_compile_net4_hash_hash(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_net4_hash *net4_compile = compile;

	struct filter_net4s nets;
	net4_compile->get_net4s(rule, &nets);

	uint32_t hash = nets.count;
	for (uint32_t idx = 0; idx < nets.count; ++idx) {
		const struct net4 *net = nets.items + idx;
		hash = hash * 31 + classify_compile_net4_hash_key(net);
		hash = hash * 31 + classify_compile_net4_hash_length(net);
	}
	return hash;
}

static inline int
classify_compile_net4_hash_compare(
	const void *compile,
	const struct classifier_rule *first,
	const struct classifier_rule *second
) {
	const struct classify_compile_net4_hash *net4_compile = compile;

	struct filter_net4s first_nets;
	struct filter_net4s second_nets;
	net4_compile->get_net4s(first, &first_nets);
	net4_compile->get_net4s(second, &second_nets);

	if (first_nets.count != second_nets.count) {
		return 1;
	}
	for (uint32_t idx = 0; idx < first_nets.count; ++idx) {
		const uint32_t first_key =
			classify_compile_net4_hash_key(first_nets.items + idx);
		const uint32_t second_key =
			classify_compile_net4_hash_key(second_nets.items + idx);
		if (first_key != second_key ||
		    classify_compile_net4_hash_length(first_nets.items + idx) !=
			    classify_compile_net4_hash_length(
				    second_nets.items + idx
			    )) {
			return 1;
		}
	}

	return 0;
}

static inline int
classify_compile_net4_hash_commit(
	struct memory_context *memory_context, void *compile, void *attr
) {
	struct classify_compile_net4_hash *net4_compile = compile;
	struct classify_attr_net4_hash *net4_attr = attr;

	// The probe order: the distinct lengths of the regions, descending.
	uint32_t lengths[CLASSIFY_NET4_HASH_LENGTH_MAX];
	uint32_t length_count = 0;
	for (uint32_t region = 0; region < net4_compile->region_count;
	     ++region) {
		const uint32_t length = net4_compile->lengths[region];
		uint32_t idx = 0;
		while (idx < length_count && lengths[idx] != length) {
			++idx;
		}
		if (idx < length_count) {
			continue;
		}
		for (idx = 0; idx < length_count && lengths[idx] > length;
		     ++idx) {
		}
		memmove(lengths + idx + 1,
			lengths + idx,
			(length_count - idx) * sizeof(uint32_t));
		lengths[idx] = length;
		++length_count;
	}

	// A family no rule names keeps no regions: the attribute stays
	// zeroed with only the miss class, and the lookup always misses.
	uint32_t *keys = NULL;
	uint32_t *classes = NULL;
	if (net4_compile->region_count > 0) {
		keys = (uint32_t *)memory_balloc(
			memory_context,
			sizeof(uint32_t) * net4_compile->region_count
		);
		classes = (uint32_t *)memory_balloc(
			memory_context,
			sizeof(uint32_t) * net4_compile->region_count
		);
		if (keys == NULL || classes == NULL) {
			if (keys != NULL) {
				memory_bfree(
					memory_context,
					keys,
					sizeof(uint32_t) *
						net4_compile->region_count
				);
			}
			if (classes != NULL) {
				memory_bfree(
					memory_context,
					classes,
					sizeof(uint32_t) *
						net4_compile->region_count
				);
			}
			return -1;
		}
	}
	if (net4_compile->region_count > 0) {
		memcpy(keys,
		       net4_compile->keys,
		       sizeof(uint32_t) * net4_compile->region_count);
	}
	// The core rewrote the cells into the final classes in place; the
	// cell behind the regions is the miss class.
	for (uint32_t region = 0; region < net4_compile->region_count;
	     ++region) {
		classes[region] = *value_table_get_ptr(
			&net4_compile->value_table, 0, region
		);
	}

	SET_OFFSET_OF(&net4_attr->keys, keys);
	SET_OFFSET_OF(&net4_attr->classes, classes);
	net4_attr->region_count = net4_compile->region_count;
	net4_attr->miss_class = *value_table_get_ptr(
		&net4_compile->value_table, 0, net4_compile->region_count
	);
	net4_attr->length_count = length_count;
	memcpy(net4_attr->lengths, lengths, sizeof(uint32_t) * length_count);

	for (uint32_t length_idx = 0; length_idx < length_count; ++length_idx) {
		const uint32_t length = lengths[length_idx];
		uint32_t count = 0;
		for (uint32_t region = 0; region < net4_compile->region_count;
		     ++region) {
			count += net4_compile->lengths[region] == length;
		}

		char index_name[MEMORY_CONTEXT_NAME_SIZE];
		classify_leaf_name(
			index_name,
			sizeof(index_name),
			net4_compile->name,
			"index"
		);
		if (hash_index_init(
			    &net4_attr->index[length_idx],
			    memory_context,
			    count + 1
		    )) {
			goto error;
		}
		for (uint32_t region = 0; region < net4_compile->region_count;
		     ++region) {
			if (net4_compile->lengths[region] != length) {
				continue;
			}
			if (hash_index_insert(
				    &net4_attr->index[length_idx],
				    classify_net4_hash_key_hash(
					    net4_compile->keys[region]
				    ),
				    region
			    )) {
				goto error;
			}
		}
	}

	classify_compile_net4_hash_free(memory_context, compile);

	return 0;

error:
	classify_attr_net4_hash_free(memory_context, net4_attr);
	return -1;
}

/*
 * Compiles the hashed IPv4 network classifier of a ruleset into an
 * embedded attribute.
 *
 * On success the attribute and the stage are owned by the caller, the
 * attribute released through classify_attr_net4_hash_free and the stage
 * through classifier_fini; on failure every partial state is freed,
 * the attribute and the stage are left zeroed.
 */
static inline int
classify_net4_hash_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_net4_hash_get_net4s_func get_net4s,
	struct classify_attr_net4_hash *attr,
	struct classifier *cls
) {
	struct classify_compile_net4_hash *compile =
		classify_compile_net4_hash_create(
			memory_context, rules, rule_count, get_net4s, name
		);
	if (compile == NULL) {
		memset(attr, 0, sizeof(*attr));
		memset(cls, 0, sizeof(*cls));
		return -1;
	}

	const struct classify_attr_ops ops = {
		.size = classify_compile_net4_hash_size,
		.rule_is_any = classify_compile_net4_hash_rule_is_any,
		.hash = classify_compile_net4_hash_hash,
		.compare = classify_compile_net4_hash_compare,
		.iter = classify_compile_net4_hash_iter,
		.rule_iter = classify_compile_net4_hash_rule_iter,
		.commit = classify_compile_net4_hash_commit,
		.free_compile = classify_compile_net4_hash_free,
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
 * Declares the hashed IPv4 network compile entry of one consumer side,
 * mirroring the LPM variant's declaration.
 */
#define CLASSIFY_NET4_HASH_COMPILE(tag, get_net4s)                             \
	static inline int classify_##tag##_compile(                            \
		struct memory_context *memory_context,                         \
		const char *name,                                              \
		const struct classifier_rule **rules,                          \
		uint32_t rule_count,                                           \
		struct classify_attr_net4_hash *attr,                          \
		struct classifier *cls                                         \
	) {                                                                    \
		return classify_net4_hash_compile(                             \
			memory_context,                                        \
			name,                                                  \
			rules,                                                 \
			rule_count,                                            \
			get_net4s,                                             \
			attr,                                                  \
			cls                                                    \
		);                                                             \
	}
