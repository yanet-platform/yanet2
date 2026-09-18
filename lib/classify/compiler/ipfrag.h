#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "lib/classify/classifiers/ipfrag.h"
#include "lib/classify/rule.h"

#include <stdint.h>

// Compile-time attribute for the IP-fragment condition.
//
// The definition area is the single boolean "is this packet a fragment",
// split into two regions: 0 = non-fragment, 1 = fragment. A rule selects
// regions via its fragment field: ANY covers both, NONE covers region 0,
// FRAG covers region 1.
#define FILTER_IPFRAG_REGION_COUNT 2

struct classify_attr_ipfrag {
	struct classify_attr attr;
	struct classify_query_attr_ipfrag *query_attr;
};

static inline struct classify_attr *
classify_attr_ipfrag_create(
	struct memory_context *memory_context,
	const struct classify_attr_handlers *handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
) {
	(void)handlers;
	(void)rules;
	(void)rule_count;

	struct classify_attr_ipfrag *attr =
		(struct classify_attr_ipfrag *)memory_balloc(
			memory_context,
			sizeof(struct classify_attr_ipfrag)
		);
	if (attr == NULL) {
		return NULL;
	}

	attr->query_attr = (struct classify_query_attr_ipfrag *)memory_balloc(
		memory_context, sizeof(struct classify_query_attr_ipfrag)
	);
	if (attr->query_attr == NULL) {
		goto error_free;
	}

	if (value_table_init(
		    &attr->query_attr->value_table,
		    memory_context,
		    "filter:ipfrag",
		    1,
		    FILTER_IPFRAG_REGION_COUNT
	    )) {
		goto error_free_attr;
	}

	return &attr->attr;

error_free_attr:
	memory_bfree(
		memory_context,
		attr->query_attr,
		sizeof(struct classify_query_attr_ipfrag)
	);

error_free:
	memory_bfree(
		memory_context, attr, sizeof(struct classify_attr_ipfrag)
	);

	return NULL;
}

static inline uint32_t
classify_attr_ipfrag_size(const struct classify_attr *attr) {
	(void)attr;
	return FILTER_IPFRAG_REGION_COUNT;
}

static inline int
classify_attr_ipfrag_iter(
	struct classify_attr *attr,
	const struct classify_attr_handlers *attr_handlers,
	classify_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	(void)attr_handlers;

	struct classify_attr_ipfrag *ipfrag_attr =
		container_of(attr, struct classify_attr_ipfrag, attr);

	struct classify_query_attr_ipfrag *query_attr = ipfrag_attr->query_attr;

	for (uint32_t idx = 0; idx < FILTER_IPFRAG_REGION_COUNT; ++idx) {
		if (iter_cb_func(
			    value_table_get_ptr(
				    &query_attr->value_table, 0, idx
			    ),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
	}
	return 0;
}

static inline int
classify_attr_ipfrag_rule_is_any(
	const struct classify_attr *attr,
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {
	(void)attr;
	(void)attr_handlers;
	return rule->fragment == FILTER_IP_FRAG_ANY;
}

static inline int
classify_attr_ipfrag_rule_iter(
	struct classify_attr *attr,
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule *rule,
	classify_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	(void)attr_handlers;

	struct classify_attr_ipfrag *ipfrag_attr =
		container_of(attr, struct classify_attr_ipfrag, attr);

	struct classify_query_attr_ipfrag *query_attr = ipfrag_attr->query_attr;

	switch (rule->fragment) {
	case FILTER_IP_FRAG_ANY:
		if (iter_cb_func(
			    value_table_get_ptr(&query_attr->value_table, 0, 0),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
		if (iter_cb_func(
			    value_table_get_ptr(&query_attr->value_table, 0, 1),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
		break;
	case FILTER_IP_FRAG_NONE:
		if (iter_cb_func(
			    value_table_get_ptr(&query_attr->value_table, 0, 0),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
		break;
	case FILTER_IP_FRAG_FRAG:
		if (iter_cb_func(
			    value_table_get_ptr(&query_attr->value_table, 0, 1),
			    cb_func_data
		    ) < 0) {
			return -1;
		}
		break;
	}

	return 0;
}

static inline void
classify_attr_ipfrag_free(
	struct memory_context *memory_context, struct classify_attr *attr
) {
	struct classify_attr_ipfrag *ipfrag_attr =
		container_of(attr, struct classify_attr_ipfrag, attr);

	if (ipfrag_attr->query_attr != NULL) {
		value_table_free(&ipfrag_attr->query_attr->value_table);
		memory_bfree(
			memory_context,
			ipfrag_attr->query_attr,
			sizeof(struct classify_query_attr_ipfrag)
		);
	}

	memory_bfree(
		memory_context,
		ipfrag_attr,
		sizeof(struct classify_attr_ipfrag)
	);
}

static inline struct classify_query_attr *
classify_attr_ipfrag_commit(
	struct memory_context *memory_context, struct classify_attr *attr
) {
	struct classify_attr_ipfrag *ipfrag_attr =
		container_of(attr, struct classify_attr_ipfrag, attr);

	struct classify_query_attr_ipfrag *query_attr = ipfrag_attr->query_attr;
	ipfrag_attr->query_attr = NULL;

	classify_attr_ipfrag_free(memory_context, attr);

	return &query_attr->attr;
}

static inline uint32_t
classify_attr_ipfrag_hash(
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {
	(void)attr_handlers;
	return (uint32_t)rule->fragment;
}

static inline int
classify_attr_ipfrag_compare(
	const struct classify_attr_handlers *attr_handlers,
	const struct filter_rule *first,
	const struct filter_rule *second
) {
	(void)attr_handlers;
	return first->fragment != second->fragment;
}

static const struct classify_attr_handlers
	classify_attr_ipfrag_handlers = {
		.create = classify_attr_ipfrag_create,
		.size = classify_attr_ipfrag_size,
		.iter = classify_attr_ipfrag_iter,
		.rule_is_any = classify_attr_ipfrag_rule_is_any,
		.hash = classify_attr_ipfrag_hash,
		.compare = classify_attr_ipfrag_compare,
		.rule_iter = classify_attr_ipfrag_rule_iter,
		.commit = classify_attr_ipfrag_commit,
		.free_compile = classify_attr_ipfrag_free,
		.free_query = classify_query_attr_ipfrag_free,
};

// Wrapper so CLASSIFY_ATTR can reference classify_attr_ipfrag via
// its attr_handlers member, matching the other attributes' instance shape.
struct classify_attr_ipfrag_handlers {
	struct classify_attr_handlers attr_handlers;
};

static const struct classify_attr_ipfrag_handlers
	classify_attr_ipfrag = {
		.attr_handlers = classify_attr_ipfrag_handlers,
};
