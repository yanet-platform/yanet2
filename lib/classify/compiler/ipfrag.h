#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "lib/classify/classifiers/ipfrag.h"
#include "lib/classify/rule.h"

#include <stdint.h>
#include <string.h>

/*
 * Compile scratch of the IP fragment attribute.
 *
 * The definition area is the single boolean "is this packet a
 * fragment", split into two regions: 0 = non-fragment, 1 = fragment. A
 * rule selects regions via its fragment field: ANY covers both, NONE
 * covers region 0, FRAG covers region 1.
 */
#define FILTER_IPFRAG_REGION_COUNT 2

/*
 * Getter of the fragment constraint of a module rule, filled by the
 * module and adapted to the rule agnostic core through the compile
 * macro below.
 */
typedef enum filter_ip_fragment (*classify_ipfrag_get_func)(
	const struct classifier_rule *rule
);

struct classify_compile_ipfrag {
	classify_ipfrag_get_func get_fragment;
	struct value_table value_table;
};

static inline struct classify_compile_ipfrag *
classify_compile_ipfrag_create(
	struct memory_context *memory_context,
	classify_ipfrag_get_func get_fragment
) {

	struct classify_compile_ipfrag *compile =
		(struct classify_compile_ipfrag *)memory_balloc(
			memory_context, sizeof(struct classify_compile_ipfrag)
		);
	if (compile == NULL) {
		return NULL;
	}

	compile->get_fragment = get_fragment;

	if (value_table_init(
		    &compile->value_table,
		    memory_context,
		    "filter:ipfrag",
		    1,
		    FILTER_IPFRAG_REGION_COUNT
	    )) {
		memory_bfree(
			memory_context,
			compile,
			sizeof(struct classify_compile_ipfrag)
		);

		return NULL;
	}

	return compile;
}

static inline uint32_t
classify_compile_ipfrag_size(const void *compile) {
	const struct classify_compile_ipfrag *ipfrag_compile = compile;

	return ipfrag_compile->value_table.h_dim *
	       ipfrag_compile->value_table.v_dim;
}

static inline int
classify_compile_ipfrag_iter(
	void *compile,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_ipfrag *ipfrag_compile = compile;

	for (uint32_t idx = 0; idx < FILTER_IPFRAG_REGION_COUNT; ++idx) {
		if (iter_cb_func(
			    value_table_get_ptr(
				    &ipfrag_compile->value_table, 0, idx
			    ),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
	}
	return 0;
}

static inline int
classify_compile_ipfrag_rule_is_any(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_ipfrag *ipfrag_compile = compile;

	return ipfrag_compile->get_fragment(rule) == FILTER_IP_FRAG_ANY;
}

static inline int
classify_compile_ipfrag_rule_iter(
	void *compile,
	const struct classifier_rule *rule,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_ipfrag *ipfrag_compile = compile;

	switch (ipfrag_compile->get_fragment(rule)) {
	case FILTER_IP_FRAG_ANY:
		if (iter_cb_func(
			    value_table_get_ptr(
				    &ipfrag_compile->value_table, 0, 0
			    ),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
		if (iter_cb_func(
			    value_table_get_ptr(
				    &ipfrag_compile->value_table, 0, 1
			    ),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
		break;
	case FILTER_IP_FRAG_NONE:
		if (iter_cb_func(
			    value_table_get_ptr(
				    &ipfrag_compile->value_table, 0, 0
			    ),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
		break;
	case FILTER_IP_FRAG_FRAG:
		if (iter_cb_func(
			    value_table_get_ptr(
				    &ipfrag_compile->value_table, 0, 1
			    ),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
		break;
	}

	return 0;
}

static inline void
classify_compile_ipfrag_free(
	struct memory_context *memory_context, void *compile
) {
	struct classify_compile_ipfrag *ipfrag_compile = compile;

	value_table_free(&ipfrag_compile->value_table);

	memory_bfree(
		memory_context,
		ipfrag_compile,
		sizeof(struct classify_compile_ipfrag)
	);
}

static inline int
classify_compile_ipfrag_commit(
	struct memory_context *memory_context, void *compile, void *attr
) {
	struct classify_compile_ipfrag *ipfrag_compile = compile;
	struct classify_attr_ipfrag *ipfrag_attr = attr;

	/*
	 * The table moves into the embedded attribute field by field: it
	 * carries relative pointers that a struct copy would strand.
	 */
	ipfrag_attr->value_table.v_dim = ipfrag_compile->value_table.v_dim;
	ipfrag_attr->value_table.h_dim = ipfrag_compile->value_table.h_dim;
	SET_OFFSET_OF(
		&ipfrag_attr->value_table.values,
		ADDR_OF(&ipfrag_compile->value_table.values)
	);
	SET_OFFSET_OF(
		&ipfrag_attr->value_table.memory_context,
		ADDR_OF(&ipfrag_compile->value_table.memory_context)
	);

	// The table internals now belong to the attribute, so only the
	// scratch shell is released here.
	memory_bfree(
		memory_context,
		ipfrag_compile,
		sizeof(struct classify_compile_ipfrag)
	);

	return 0;
}

static inline uint32_t
classify_compile_ipfrag_hash(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_ipfrag *ipfrag_compile = compile;

	return (uint32_t)ipfrag_compile->get_fragment(rule);
}

static inline int
classify_compile_ipfrag_compare(
	const void *compile,
	const struct classifier_rule *first,
	const struct classifier_rule *second
) {
	const struct classify_compile_ipfrag *ipfrag_compile = compile;

	return ipfrag_compile->get_fragment(first) !=
	       ipfrag_compile->get_fragment(second);
}

/*
 * Compiles the IP fragment classifier of a ruleset into an embedded
 * attribute.
 *
 * On success the attribute and the stage - the registry with the rule
 * group row - are owned by the caller, released through
 * classifier_fini; on failure every partial state is freed, the
 * attribute and the stage are left zeroed.
 */
static inline int
classify_ipfrag_compile(
	struct memory_context *memory_context,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_ipfrag_get_func get_fragment,
	struct classify_attr_ipfrag *attr,
	struct classifier *cls
) {
	struct classify_compile_ipfrag *compile =
		classify_compile_ipfrag_create(memory_context, get_fragment);
	if (compile == NULL) {
		memset(attr, 0, sizeof(*attr));
		memset(cls, 0, sizeof(*cls));
		return -1;
	}

	const struct classify_attr_ops ops = {
		.size = classify_compile_ipfrag_size,
		.rule_is_any = classify_compile_ipfrag_rule_is_any,
		.hash = classify_compile_ipfrag_hash,
		.compare = classify_compile_ipfrag_compare,
		.iter = classify_compile_ipfrag_iter,
		.rule_iter = classify_compile_ipfrag_rule_iter,
		.commit = classify_compile_ipfrag_commit,
		.free_compile = classify_compile_ipfrag_free,
	};

	return classify_attr_compile(
		memory_context, &ops, compile, rules, rule_count, attr, cls
	);
}

/*
 * Declares the IP fragment compile entry of one consumer: the getter
 * adapts the module rule type to the rule agnostic core, and the entry
 * takes the module rule array the consumer owns.
 *
 * The name must carry the consumer prefix, so the generated symbols
 * never collide with the library ones.
 */
#define CLASSIFY_IPFRAG_COMPILE(name, get_fragment)                            \
	static inline int classify_##name##_compile(                           \
		struct memory_context *memory_context,                         \
		const struct classifier_rule **rules,                          \
		uint32_t rule_count,                                           \
		struct classify_attr_ipfrag *attr,                             \
		struct classifier *cls                                         \
	) {                                                                    \
		return classify_ipfrag_compile(                                \
			memory_context,                                        \
			rules,                                                 \
			rule_count,                                            \
			get_fragment,                                          \
			attr,                                                  \
			cls                                                    \
		);                                                             \
	}
