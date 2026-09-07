#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "declare.h"
#include "lib/filter2/classifiers/ipfrag.h"
#include "lib/filter2/rule.h"

#include <stdint.h>

// Compile-time attribute for the IP-fragment condition.
//
// The definition area is the single boolean "is this packet a fragment",
// split into two regions: 0 = non-fragment, 1 = fragment. A rule selects
// regions via its fragment field: ANY covers both, NONE covers region 0,
// FRAG covers region 1.
#define FILTER_IPFRAG_REGION_COUNT 2

struct filter_compile_attr_ipfrag {
	struct filter_compile_attr attr;
	struct filter_query_attr_ip_frag *query_attr;
};

static inline struct filter_compile_attr *
filter_compile_attr_ipfrag_create(
	struct memory_context *memory_context,
	const struct filter_compile_attr_handlers *handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
) {
	(void)handlers;
	(void)rules;
	(void)rule_count;

	struct filter_compile_attr_ipfrag *attr =
		(struct filter_compile_attr_ipfrag *)memory_balloc(
			memory_context,
			sizeof(struct filter_compile_attr_ipfrag)
		);
	if (attr == NULL) {
		return NULL;
	}

	attr->query_attr = (struct filter_query_attr_ip_frag *)memory_balloc(
		memory_context, sizeof(struct filter_query_attr_ip_frag)
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
		sizeof(struct filter_query_attr_ip_frag)
	);

error_free:
	memory_bfree(
		memory_context, attr, sizeof(struct filter_compile_attr_ipfrag)
	);

	return NULL;
}

static inline uint32_t
filter_compile_attr_ipfrag_size(const struct filter_compile_attr *attr) {
	(void)attr;
	return FILTER_IPFRAG_REGION_COUNT;
}

static inline int
filter_compile_attr_ipfrag_iter(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	(void)attr_handlers;

	struct filter_compile_attr_ipfrag *ipfrag_attr =
		container_of(attr, struct filter_compile_attr_ipfrag, attr);

	struct filter_query_attr_ip_frag *query_attr = ipfrag_attr->query_attr;

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
filter_compile_attr_ipfrag_rule_is_any(
	const struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule
) {
	(void)attr;
	(void)attr_handlers;
	return rule->fragment == FILTER_IP_FRAG_ANY;
}

static inline int
filter_compile_attr_ipfrag_rule_iter(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *attr_handlers,
	const struct filter_rule *rule,
	uint32_t rule_idx,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
) {
	(void)rule_idx;
	(void)attr_handlers;

	struct filter_compile_attr_ipfrag *ipfrag_attr =
		container_of(attr, struct filter_compile_attr_ipfrag, attr);

	struct filter_query_attr_ip_frag *query_attr = ipfrag_attr->query_attr;

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
filter_compile_attr_ipfrag_free(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_attr_ipfrag *ipfrag_attr =
		container_of(attr, struct filter_compile_attr_ipfrag, attr);

	if (ipfrag_attr->query_attr != NULL) {
		value_table_free(&ipfrag_attr->query_attr->value_table);
		memory_bfree(
			memory_context,
			ipfrag_attr->query_attr,
			sizeof(struct filter_query_attr_ip_frag)
		);
	}

	memory_bfree(
		memory_context,
		ipfrag_attr,
		sizeof(struct filter_compile_attr_ipfrag)
	);
}

static inline struct filter_query_attr *
filter_compile_attr_ipfrag_commit(
	struct memory_context *memory_context, struct filter_compile_attr *attr
) {
	struct filter_compile_attr_ipfrag *ipfrag_attr =
		container_of(attr, struct filter_compile_attr_ipfrag, attr);

	struct filter_query_attr_ip_frag *query_attr = ipfrag_attr->query_attr;
	ipfrag_attr->query_attr = NULL;

	filter_compile_attr_ipfrag_free(memory_context, attr);

	return &query_attr->attr;
}

FILTER_COMPILE_ATTR_BUILD_AS_DECLARE(ip_frag, filter_compile_attr_ipfrag)

static const struct filter_compile_attr_handlers
	filter_compile_attr_ipfrag_handlers = {
		.build = filter_compile_attr_ip_frag_build,
		.free_query = filter_query_attr_ip_frag_free,
};

// Wrapper so FILTER_ATTR_COMPILE can reference filter_compile_attr_ip_frag
// via its attr_handlers member, matching the other attributes' instance
// shape.
struct filter_compile_attr_ipfrag_handlers {
	struct filter_compile_attr_handlers attr_handlers;
};

static const struct filter_compile_attr_ipfrag_handlers
	filter_compile_attr_ip_frag = {
		.attr_handlers = filter_compile_attr_ipfrag_handlers,
};

FILTER_COMPILE_ATTR_BUILD_AS(ip_frag, filter_compile_attr_ipfrag)
