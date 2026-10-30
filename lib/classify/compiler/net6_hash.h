#pragma once

#include "common/hash_index.h"
#include "common/memory.h"
#include "common/network.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "lib/classify/classifiers/net6_hash.h"
#include "lib/classify/rule.h"

#include <stdint.h>
#include <string.h>

/*
 * Getter of the IPv6 networks of a module rule - the source or the
 * destination side - the same getter type the LPM variant uses.
 */
typedef void (*classify_net6_hash_get_net6s_func)(
	const struct classifier_rule *rule, struct filter_net6s *net
);

// The compile scratch of the hashed IPv6 network attribute, mirroring
// the IPv4 one over sixteen byte keys: the regions are the distinct
// configured prefixes plus the miss region behind them, their classes
// live in value table cells the compile core rewrites in place, and a
// rule touches every region its prefixes cover, contained prefixes
// included.
struct classify_compile_net6_hash {
	classify_net6_hash_get_net6s_func get_net6s;
	const char *name;

	uint32_t region_count;
	uint8_t *keys;
	uint32_t *lengths;
	struct value_table value_table;
	struct hash_index dedup;
};

// The dedup equality of the collection pass: the slot value is the
// region identifier, the data the looked-up masked sixteen bytes.
struct classify_compile_net6_hash_dedup_ctx {
	const struct classify_compile_net6_hash *compile;
	uint8_t key[NET6_LEN];
};

static inline int
classify_compile_net6_hash_dedup_eq(uint32_t region, const void *data) {
	const struct classify_compile_net6_hash_dedup_ctx *ctx = data;
	return memcmp(ctx->compile->keys + region * NET6_LEN, ctx->key, NET6_LEN
	       ) == 0
		       ? 0
		       : 1;
}

// The prefix length of one network, zero for the unspecified mask.
static inline uint32_t
classify_compile_net6_hash_length(const struct net6 *net6) {
	uint32_t length = 0;
	for (uint32_t idx = 0; idx < NET6_LEN; ++idx) {
		length += __builtin_popcount(net6->mask[idx]);
	}
	return length;
}

static inline void
classify_compile_net6_hash_key(const struct net6 *net6, uint8_t *key) {
	for (uint32_t idx = 0; idx < NET6_LEN; ++idx) {
		key[idx] = net6->addr[idx] & net6->mask[idx];
	}
}

static inline void
classify_compile_net6_hash_free(
	struct memory_context *memory_context, void *compile
) {
	struct classify_compile_net6_hash *net6_compile = compile;

	hash_index_fini(&net6_compile->dedup);
	value_table_free(&net6_compile->value_table);
	if (net6_compile->keys != NULL) {
		memory_bfree(
			memory_context,
			net6_compile->keys,
			NET6_LEN * net6_compile->region_count
		);
	}
	if (net6_compile->lengths != NULL) {
		memory_bfree(
			memory_context,
			net6_compile->lengths,
			sizeof(uint32_t) * net6_compile->region_count
		);
	}

	memory_bfree(
		memory_context,
		net6_compile,
		sizeof(struct classify_compile_net6_hash)
	);
}

static inline struct classify_compile_net6_hash *
classify_compile_net6_hash_create(
	struct memory_context *memory_context,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_net6_hash_get_net6s_func get_net6s,
	const char *name
) {
	struct classify_compile_net6_hash *compile =
		(struct classify_compile_net6_hash *)memory_balloc(
			memory_context,
			sizeof(struct classify_compile_net6_hash)
		);
	if (compile == NULL) {
		return NULL;
	}

	compile->get_net6s = get_net6s;
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
		struct filter_net6s nets;
		get_net6s(rules[rule_idx], &nets);
		net_total += nets.count;
	}
	if (net_total == 0) {
		net_total = 1;
	}

	if (hash_index_init(&compile->dedup, memory_context, net_total + 1)) {
		memory_bfree(
			memory_context,
			compile,
			sizeof(struct classify_compile_net6_hash)
		);
		return NULL;
	}

	compile->keys =
		(uint8_t *)memory_balloc(memory_context, NET6_LEN * net_total);
	compile->lengths = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * net_total
	);
	if (compile->keys == NULL || compile->lengths == NULL) {
		classify_compile_net6_hash_free(memory_context, compile);
		return NULL;
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct classifier_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		struct filter_net6s nets;
		get_net6s(rule, &nets);
		for (const struct net6 *net6 = nets.items;
		     net6 < nets.items + nets.count;
		     ++net6) {
			// The unspecified network is the rule-is-any case of
			// the core, not a region.
			const uint32_t length =
				classify_compile_net6_hash_length(net6);
			if (length == 0) {
				continue;
			}
			uint8_t key[NET6_LEN];
			classify_compile_net6_hash_key(net6, key);

			struct classify_compile_net6_hash_dedup_ctx ctx;
			memcpy(ctx.key, key, NET6_LEN);
			ctx.compile = compile;
			uint32_t region = hash_index_lookup(
				&compile->dedup,
				classify_net6_hash_key_hash(key),
				classify_compile_net6_hash_dedup_eq,
				&ctx
			);
			if (region != HASH_INDEX_INVALID) {
				continue;
			}
			if (hash_index_insert(
				    &compile->dedup,
				    classify_net6_hash_key_hash(key),
				    compile->region_count
			    )) {
				classify_compile_net6_hash_free(
					memory_context, compile
				);
				return NULL;
			}

			memcpy(compile->keys + compile->region_count * NET6_LEN,
			       key,
			       NET6_LEN);
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
		classify_compile_net6_hash_free(memory_context, compile);
		return NULL;
	}

	return compile;
}

static inline uint32_t
classify_compile_net6_hash_size(const void *compile) {
	const struct classify_compile_net6_hash *net6_compile = compile;

	// One cell per region plus the miss cell.
	return net6_compile->region_count + 1;
}

static inline int
classify_compile_net6_hash_rule_is_any(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_net6_hash *net6_compile = compile;

	struct filter_net6s nets;
	net6_compile->get_net6s(rule, &nets);
	if (nets.count == 0) {
		return 1;
	}
	for (uint32_t idx = 0; idx < NET6_LEN; ++idx) {
		if (nets.items[0].mask[idx] != 0) {
			return 0;
		}
	}
	return 1;
}

static inline int
classify_compile_net6_hash_iter(
	void *compile,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_net6_hash *net6_compile = compile;

	for (uint32_t idx = 0; idx <= net6_compile->region_count; ++idx) {
		if (iter_cb_func(
			    value_table_get_ptr(
				    &net6_compile->value_table, 0, idx
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
// masked to the network length carries the network address on every
// byte.
static inline int
classify_compile_net6_hash_contains(
	const struct classify_compile_net6_hash *net6_compile,
	uint32_t region,
	const struct net6 *net6
) {
	const uint8_t *key = net6_compile->keys + region * NET6_LEN;
	const uint32_t length = net6_compile->lengths[region];

	uint32_t net_length = 0;
	for (uint32_t idx = 0; idx < NET6_LEN; ++idx) {
		net_length += __builtin_popcount(net6->mask[idx]);
	}
	if (net_length == 0 || net_length > length) {
		return 0;
	}

	for (uint32_t idx = 0; idx < NET6_LEN; ++idx) {
		// The network mask keeps its first net_length bits; the region
		// key must carry the network address wherever that mask is set.
		const uint8_t net_mask =
			net_length >= (idx + 1) * 8
				? 0xff
				: (net_length > idx * 8
					   ? (uint8_t)(0xffu
						       << ((idx + 1) * 8 -
							   net_length))
					   : 0);
		if ((key[idx] & net_mask) !=
		    (net6->addr[idx] & net6->mask[idx])) {
			return 0;
		}
	}

	return 1;
}

static inline int
classify_compile_net6_hash_rule_iter(
	void *compile,
	const struct classifier_rule *rule,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_net6_hash *net6_compile = compile;

	struct filter_net6s nets;
	net6_compile->get_net6s(rule, &nets);

	for (const struct net6 *net6 = nets.items;
	     net6 < nets.items + nets.count;
	     ++net6) {
		if (classify_compile_net6_hash_length(net6) == 0) {
			continue;
		}
		for (uint32_t region = 0; region < net6_compile->region_count;
		     ++region) {
			if (!classify_compile_net6_hash_contains(
				    net6_compile, region, net6
			    )) {
				continue;
			}
			if (iter_cb_func(
				    value_table_get_ptr(
					    &net6_compile->value_table,
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
classify_compile_net6_hash_hash(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_net6_hash *net6_compile = compile;

	struct filter_net6s nets;
	net6_compile->get_net6s(rule, &nets);

	uint32_t hash = nets.count;
	uint8_t key[NET6_LEN];
	for (uint32_t idx = 0; idx < nets.count; ++idx) {
		classify_compile_net6_hash_key(nets.items + idx, key);
		hash = hash * 31 + classify_net6_hash_key_hash(key);
		hash = hash * 31 +
		       classify_compile_net6_hash_length(nets.items + idx);
	}
	return hash;
}

static inline int
classify_compile_net6_hash_compare(
	const void *compile,
	const struct classifier_rule *first,
	const struct classifier_rule *second
) {
	const struct classify_compile_net6_hash *net6_compile = compile;

	struct filter_net6s first_nets;
	struct filter_net6s second_nets;
	net6_compile->get_net6s(first, &first_nets);
	net6_compile->get_net6s(second, &second_nets);

	if (first_nets.count != second_nets.count) {
		return 1;
	}
	uint8_t first_key[NET6_LEN];
	uint8_t second_key[NET6_LEN];
	for (uint32_t idx = 0; idx < first_nets.count; ++idx) {
		classify_compile_net6_hash_key(
			first_nets.items + idx, first_key
		);
		classify_compile_net6_hash_key(
			second_nets.items + idx, second_key
		);
		if (memcmp(first_key, second_key, NET6_LEN) != 0 ||
		    classify_compile_net6_hash_length(first_nets.items + idx) !=
			    classify_compile_net6_hash_length(
				    second_nets.items + idx
			    )) {
			return 1;
		}
	}

	return 0;
}

static inline int
classify_compile_net6_hash_commit(
	struct memory_context *memory_context, void *compile, void *attr
) {
	struct classify_compile_net6_hash *net6_compile = compile;
	struct classify_attr_net6_hash *net6_attr = attr;

	// The probe order: the distinct lengths of the regions, descending.
	uint32_t lengths[CLASSIFY_NET6_HASH_LENGTH_MAX];
	uint32_t length_count = 0;
	for (uint32_t region = 0; region < net6_compile->region_count;
	     ++region) {
		const uint32_t length = net6_compile->lengths[region];
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
	uint8_t *keys = NULL;
	uint32_t *classes = NULL;
	if (net6_compile->region_count > 0) {
		keys = (uint8_t *)memory_balloc(
			memory_context, NET6_LEN * net6_compile->region_count
		);
		classes = (uint32_t *)memory_balloc(
			memory_context,
			sizeof(uint32_t) * net6_compile->region_count
		);
		if (keys == NULL || classes == NULL) {
			if (keys != NULL) {
				memory_bfree(
					memory_context,
					keys,
					NET6_LEN * net6_compile->region_count
				);
			}
			if (classes != NULL) {
				memory_bfree(
					memory_context,
					classes,
					sizeof(uint32_t) *
						net6_compile->region_count
				);
			}
			return -1;
		}
	}
	if (net6_compile->region_count > 0) {
		memcpy(keys,
		       net6_compile->keys,
		       NET6_LEN * net6_compile->region_count);
	}
	// The core rewrote the cells into the final classes in place; the
	// cell behind the regions is the miss class.
	for (uint32_t region = 0; region < net6_compile->region_count;
	     ++region) {
		classes[region] = *value_table_get_ptr(
			&net6_compile->value_table, 0, region
		);
	}

	SET_OFFSET_OF(&net6_attr->keys, keys);
	SET_OFFSET_OF(&net6_attr->classes, classes);
	net6_attr->region_count = net6_compile->region_count;
	net6_attr->miss_class = *value_table_get_ptr(
		&net6_compile->value_table, 0, net6_compile->region_count
	);
	net6_attr->length_count = length_count;
	memcpy(net6_attr->lengths, lengths, sizeof(uint32_t) * length_count);

	for (uint32_t length_idx = 0; length_idx < length_count; ++length_idx) {
		const uint32_t length = lengths[length_idx];
		uint32_t count = 0;
		for (uint32_t region = 0; region < net6_compile->region_count;
		     ++region) {
			count += net6_compile->lengths[region] == length;
		}

		char index_name[MEMORY_CONTEXT_NAME_SIZE];
		classify_leaf_name(
			index_name,
			sizeof(index_name),
			net6_compile->name,
			"index"
		);
		if (hash_index_init(
			    &net6_attr->index[length_idx],
			    memory_context,
			    count + 1
		    )) {
			goto error;
		}
		for (uint32_t region = 0; region < net6_compile->region_count;
		     ++region) {
			if (net6_compile->lengths[region] != length) {
				continue;
			}
			if (hash_index_insert(
				    &net6_attr->index[length_idx],
				    classify_net6_hash_key_hash(
					    net6_compile->keys +
					    region * NET6_LEN
				    ),
				    region
			    )) {
				goto error;
			}
		}
	}

	classify_compile_net6_hash_free(memory_context, compile);

	return 0;

error:
	classify_attr_net6_hash_free(memory_context, net6_attr);
	return -1;
}

/*
 * Compiles the hashed IPv6 network classifier of a ruleset into an
 * embedded attribute.
 *
 * On success the attribute and the stage are owned by the caller, the
 * attribute released through classify_attr_net6_hash_free and the stage
 * through classifier_fini; on failure every partial state is freed,
 * the attribute and the stage are left zeroed.
 */
static inline int
classify_net6_hash_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_net6_hash_get_net6s_func get_net6s,
	struct classify_attr_net6_hash *attr,
	struct classifier *cls
) {
	struct classify_compile_net6_hash *compile =
		classify_compile_net6_hash_create(
			memory_context, rules, rule_count, get_net6s, name
		);
	if (compile == NULL) {
		memset(attr, 0, sizeof(*attr));
		memset(cls, 0, sizeof(*cls));
		return -1;
	}

	const struct classify_attr_ops ops = {
		.size = classify_compile_net6_hash_size,
		.rule_is_any = classify_compile_net6_hash_rule_is_any,
		.hash = classify_compile_net6_hash_hash,
		.compare = classify_compile_net6_hash_compare,
		.iter = classify_compile_net6_hash_iter,
		.rule_iter = classify_compile_net6_hash_rule_iter,
		.commit = classify_compile_net6_hash_commit,
		.free_compile = classify_compile_net6_hash_free,
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
 * Declares the hashed IPv6 network compile entry of one consumer side,
 * mirroring the LPM variant's declaration.
 */
#define CLASSIFY_NET6_HASH_COMPILE(tag, get_net6s)                             \
	static inline int classify_##tag##_compile(                            \
		struct memory_context *memory_context,                         \
		const char *name,                                              \
		const struct classifier_rule **rules,                          \
		uint32_t rule_count,                                           \
		struct classify_attr_net6_hash *attr,                          \
		struct classifier *cls                                         \
	) {                                                                    \
		return classify_net6_hash_compile(                             \
			memory_context,                                        \
			name,                                                  \
			rules,                                                 \
			rule_count,                                            \
			get_net6s,                                             \
			attr,                                                  \
			cls                                                    \
		);                                                             \
	}
