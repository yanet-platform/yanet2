#pragma once

#include "filter/compiler/attribute.h"
#include "filter/compiler/declare.h"
#include "filter/compiler/helper.h"
#include "filter/filter.h"

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include <assert.h>

/**
 * @file compiler.h
 * @brief Build/teardown macros for filter classification trees.
 *
 * Defines:
 *  - filter_init: build a filter for a declared attribute signature
 *  - filter_free: free resources allocated by filter_init
 */
/**
 * @def filter_init(filter, tag, rules, rule_count, ctx)
 * @brief Build filter for signature tag into filter using rules.
 * @param filter struct filter*
 * @param tag name used in FILTER_COMPILER_DECLARE(...)
 * @param rules const struct filter_rule* array
 * @param rule_count size_t number of rules
 * @param ctx const struct memory_context* source context
 * @return int 0 on success, negative on error
 */
typedef int (*filter_lookup_init_func)(
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *mctx
);

typedef void (*filter_lookup_free_func)(
	void *data, struct memory_context *memory_context
);

struct filter_lookup_handler {
	filter_lookup_init_func init;
	filter_lookup_free_func free;
};

struct filter_compiler {
	uint64_t lookup_count;
	struct filter_lookup_handler *lookups;
};

static inline void
filter_free(
	struct filter *filter, const struct filter_compiler *filter_compiler
) {
	for (size_t i = 0; i < filter_compiler->lookup_count; ++i) {
		struct filter_vertex *v =
			filter->v + filter_compiler->lookup_count + i;
		if (v->data != NULL) {
			filter_compiler->lookups[i].free(
				ADDR_OF(&v->data), &filter->memory_context
			);
		}
		SET_OFFSET_OF(&v->data, NULL);
	}
	for (size_t i = 1; i < 2 * filter_compiler->lookup_count; ++i) {
		value_registry_free(&filter->v[i].registry);
	}
	for (size_t i = 1; i < filter_compiler->lookup_count; ++i) {
		value_table_free(&filter->v[i].table);
	}
	if (filter_compiler->lookup_count == 1) {
		struct filter_vertex *v0 = filter->v;
		value_registry_free(&v0->registry);
		value_table_free(&v0->table);
	}
}

static inline int
filter_init(
	struct filter *filter,
	const struct filter_compiler *filter_compiler,
	const struct filter_rule **rules,
	uint32_t rule_count,
	struct memory_context *memory_context
) {
	if (filter_compiler->lookup_count == 0)
		return -1;

	memset(filter, 0, sizeof(struct filter));

	if (memory_context_init_from(
		    &filter->memory_context, memory_context, "filter"
	    )) {
		return -1;
	}

	for (uint64_t lookup_idx = 0;
	     lookup_idx < filter_compiler->lookup_count;
	     ++lookup_idx) {
		struct filter_vertex *v =
			filter->v + filter_compiler->lookup_count + lookup_idx;
		if (value_registry_init(
			    &v->registry, &filter->memory_context
		    )) {
			goto init_failed;
		}
		v->data = NULL;
		if (filter_compiler->lookups[lookup_idx].init(
			    &v->registry,
			    &v->data,
			    rules,
			    rule_count,
			    &filter->memory_context
		    )) {
			goto init_failed;
		}
	}

	if (filter_compiler->lookup_count == 1) {
		struct value_registry dummy;
		if (init_dummy_registry(
			    &filter->memory_context, rule_count, &dummy
		    )) {
			value_registry_free(&dummy);
			goto init_failed;
		}

		if (merge_and_set_registry_values(
			    &filter->memory_context,
			    &dummy,
			    &filter->v[1].registry,
			    &filter->v[0].table
		    )) {
			value_registry_free(&dummy);
			goto init_failed;
		}

		value_registry_free(&dummy);
		goto init_finish;
	}

	for (size_t idx = filter_compiler->lookup_count - 1; idx >= 2; --idx) {
		if (merge_and_collect_registry(
			    &filter->memory_context,
			    &filter->v[2 * idx].registry,
			    &filter->v[2 * idx + 1].registry,
			    &filter->v[idx].table,
			    &filter->v[idx].registry
		    )) {
			goto init_failed;
		}
	}

	if (merge_and_set_registry_values(
		    &filter->memory_context,
		    &filter->v[2 * 1].registry,
		    &filter->v[2 * 1 + 1].registry,
		    &filter->v[1].table
	    )) {
		goto init_failed;
	}

init_finish:
	return 0;

init_failed:
	filter_free(filter, filter_compiler);
	return -1;
}

// TODO: docs
static inline uint64_t
filter_memory_usage(struct filter *filter) {
	struct memory_context *mctx = &filter->memory_context;
	assert(mctx->balloc_size >= mctx->bfree_size);
	return mctx->balloc_size - mctx->bfree_size;
}
