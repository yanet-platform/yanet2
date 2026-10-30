#pragma once

#include "common/hash_index.h"
#include "common/memory.h"
#include "common/network.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "lib/classify/classifiers/net4_hash.h"
#include "lib/classify/rule.h"

#include <stdint.h>
#include <string.h>

/*
 * The addresses a module rule names, in the raw big-endian wire form.
 */
struct classify_net4_addrs {
	const uint8_t (*items)[NET4_LEN];
	uint32_t count;
};

/*
 * Getter of the IPv4 addresses of a module rule - the source or the
 * destination side - filled by the module and adapted to the rule
 * agnostic core through the compile macro below.
 */
typedef void (*classify_net4_hash_get_addrs_func)(
	const struct classifier_rule *rule, struct classify_net4_addrs *addrs
);

// The compile scratch of the hashed IPv4 address attribute.
//
// The regions are the distinct configured addresses - a region
// identifier indexes the parallel keys array and the cell of the value
// table - plus the one miss region behind them, whose class an address
// the ruleset does not name resolves to. The value table cells start
// zero and the compile core rewrites them into the final classes in
// place, the same cell flow every attribute of the library runs.
struct classify_compile_net4_hash {
	classify_net4_hash_get_addrs_func get_addrs;
	const char *name;

	uint32_t region_count;
	uint32_t *keys;
	struct value_table value_table;
	struct hash_index dedup;
};

// The dedup equality of the collection pass: the slot value is the
// region identifier, the data the looked-up address word.
struct classify_compile_net4_hash_dedup_ctx {
	const struct classify_compile_net4_hash *compile;
	uint32_t key;
};

static inline int
classify_compile_net4_hash_dedup_eq(uint32_t region, const void *data) {
	const struct classify_compile_net4_hash_dedup_ctx *ctx = data;
	return ctx->compile->keys[region] == ctx->key ? 0 : 1;
}

static inline uint32_t
classify_compile_net4_hash_key(const uint8_t addr[NET4_LEN]) {
	uint32_t key = 0;
	for (uint32_t idx = 0; idx < NET4_LEN; ++idx) {
		key = (key << 8) | addr[idx];
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
	classify_net4_hash_get_addrs_func get_addrs,
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

	compile->get_addrs = get_addrs;
	compile->name = name;
	compile->region_count = 0;
	compile->keys = NULL;

	// The collection upper bound: every address of every rule names its
	// own region until the dedup collapses the shared ones, and the
	// index refuses to fill to its capacity, so it carries one slot
	// beyond the bound.
	uint32_t addr_total = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		if (rules[rule_idx] == NULL) {
			continue;
		}
		struct classify_net4_addrs addrs;
		get_addrs(rules[rule_idx], &addrs);
		addr_total += addrs.count;
	}
	if (addr_total == 0) {
		addr_total = 1;
	}

	if (hash_index_init(&compile->dedup, memory_context, addr_total + 1)) {
		memory_bfree(
			memory_context,
			compile,
			sizeof(struct classify_compile_net4_hash)
		);
		return NULL;
	}

	compile->keys = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * addr_total
	);
	if (compile->keys == NULL) {
		classify_compile_net4_hash_free(memory_context, compile);
		return NULL;
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct classifier_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		struct classify_net4_addrs addrs;
		get_addrs(rule, &addrs);
		for (uint32_t addr_idx = 0; addr_idx < addrs.count;
		     ++addr_idx) {
			const uint32_t key = classify_compile_net4_hash_key(
				addrs.items[addr_idx]
			);

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

	struct classify_net4_addrs addrs;
	net4_compile->get_addrs(rule, &addrs);

	return addrs.count == 0;
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

static inline int
classify_compile_net4_hash_rule_iter(
	void *compile,
	const struct classifier_rule *rule,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_net4_hash *net4_compile = compile;

	struct classify_net4_addrs addrs;
	net4_compile->get_addrs(rule, &addrs);

	for (uint32_t addr_idx = 0; addr_idx < addrs.count; ++addr_idx) {
		const uint32_t key =
			classify_compile_net4_hash_key(addrs.items[addr_idx]);
		struct classify_compile_net4_hash_dedup_ctx ctx = {
			.compile = net4_compile,
			.key = key,
		};
		uint32_t region = hash_index_lookup(
			&net4_compile->dedup,
			classify_net4_hash_key_hash(key),
			classify_compile_net4_hash_dedup_eq,
			&ctx
		);
		if (region == HASH_INDEX_INVALID) {
			continue;
		}
		if (iter_cb_func(
			    value_table_get_ptr(
				    &net4_compile->value_table, 0, region
			    ),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
	}

	return 0;
}

static inline uint32_t
classify_compile_net4_hash_hash(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_net4_hash *net4_compile = compile;

	struct classify_net4_addrs addrs;
	net4_compile->get_addrs(rule, &addrs);

	uint32_t hash = addrs.count;
	for (uint32_t idx = 0; idx < addrs.count; ++idx) {
		hash = hash * 31 +
		       classify_compile_net4_hash_key(addrs.items[idx]);
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

	struct classify_net4_addrs first_addrs;
	struct classify_net4_addrs second_addrs;
	net4_compile->get_addrs(first, &first_addrs);
	net4_compile->get_addrs(second, &second_addrs);

	if (first_addrs.count != second_addrs.count) {
		return 1;
	}
	for (uint32_t idx = 0; idx < first_addrs.count; ++idx) {
		if (classify_compile_net4_hash_key(first_addrs.items[idx]) !=
		    classify_compile_net4_hash_key(second_addrs.items[idx])) {
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
		memcpy(keys,
		       net4_compile->keys,
		       sizeof(uint32_t) * net4_compile->region_count);
		// The core rewrote the cells into the final classes in place;
		// the cell behind the regions is the miss class.
		for (uint32_t region = 0; region < net4_compile->region_count;
		     ++region) {
			classes[region] = *value_table_get_ptr(
				&net4_compile->value_table, 0, region
			);
		}
	}

	SET_OFFSET_OF(&net4_attr->keys, keys);
	SET_OFFSET_OF(&net4_attr->classes, classes);
	net4_attr->region_count = net4_compile->region_count;
	net4_attr->miss_class = *value_table_get_ptr(
		&net4_compile->value_table, 0, net4_compile->region_count
	);

	char index_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(
		index_name, sizeof(index_name), net4_compile->name, "index"
	);
	if (hash_index_init(
		    &net4_attr->index,
		    memory_context,
		    net4_compile->region_count + 1
	    )) {
		goto error;
	}
	for (uint32_t region = 0; region < net4_compile->region_count;
	     ++region) {
		if (hash_index_insert(
			    &net4_attr->index,
			    classify_net4_hash_key_hash(
				    net4_compile->keys[region]
			    ),
			    region
		    )) {
			goto error;
		}
	}

	classify_compile_net4_hash_free(memory_context, compile);

	return 0;

error:
	classify_attr_net4_hash_free(memory_context, net4_attr);
	return -1;
}

/*
 * Compiles the hashed IPv4 address classifier of a ruleset into an
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
	classify_net4_hash_get_addrs_func get_addrs,
	struct classify_attr_net4_hash *attr,
	struct classifier *cls
) {
	struct classify_compile_net4_hash *compile =
		classify_compile_net4_hash_create(
			memory_context, rules, rule_count, get_addrs, name
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
 * Declares the hashed IPv4 address compile entry of one consumer side.
 */
#define CLASSIFY_NET4_HASH_COMPILE(tag, get_addrs)                             \
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
			get_addrs,                                             \
			attr,                                                  \
			cls                                                    \
		);                                                             \
	}
