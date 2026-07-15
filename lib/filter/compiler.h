#pragma once

#include "lib/filter/compiler/attribute.h"
#include "lib/filter/compiler/declare.h"
#include "lib/filter/compiler/helper.h"
#include "lib/filter/filter.h"

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "lib/errors/errors.h"
#include <assert.h>

// Build and teardown functions for filter classification trees.
//
// filter_init: build a filter for a declared attribute signature; reports the
// failing step via err. filter_free: release all resources allocated by
// filter_init.
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
		value_registry_fini(&filter->v[i].registry);
	}
	for (size_t i = 1; i < filter_compiler->lookup_count; ++i) {
		value_table_free(&filter->v[i].table);
	}
	if (filter_compiler->lookup_count == 1) {
		struct filter_vertex *v0 = filter->v;
		value_registry_fini(&v0->registry);
		value_table_free(&v0->table);
	}
	memory_context_fini(&filter->memory_context);
}

static inline bool
rule_net4_masks_are_valid(const struct net4 *nets, uint32_t count) {
	for (uint32_t net_idx = 0; net_idx < count; ++net_idx) {
		if (!filter_net4_mask_is_valid(nets[net_idx].mask)) {
			return false;
		}
	}
	return true;
}

static inline bool
rule_net6_masks_are_valid(const struct net6 *nets, uint32_t count) {
	for (uint32_t net_idx = 0; net_idx < count; ++net_idx) {
		if (!filter_net6_mask_is_valid(nets[net_idx].mask)) {
			return false;
		}
	}
	return true;
}

static inline int
filter_init(
	struct filter *filter,
	const struct filter_compiler *filter_compiler,
	const struct filter_rule **rules,
	uint32_t rule_count,
	struct memory_context *memory_context,
	yanet_error **err
) {
	if (filter_compiler->lookup_count == 0) {
		yanet_error_add(err, "filter has no lookups configured");
		return -1;
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct filter_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		if (!rule_net4_masks_are_valid(
			    rule->net4.srcs, rule->net4.src_count
		    ) ||
		    !rule_net4_masks_are_valid(
			    rule->net4.dsts, rule->net4.dst_count
		    )) {
			yanet_error_add(
				err,
				"filter rule %u has a non-contiguous IPv4 "
				"network mask",
				rule_idx
			);
			return -1;
		}

		if (!rule_net6_masks_are_valid(
			    rule->net6.srcs, rule->net6.src_count
		    ) ||
		    !rule_net6_masks_are_valid(
			    rule->net6.dsts, rule->net6.dst_count
		    )) {
			yanet_error_add(
				err,
				"filter rule %u has an IPv6 network mask "
				"that is not bi-contiguous",
				rule_idx
			);
			return -1;
		}
	}

	memset(filter, 0, sizeof(struct filter));

	if (memory_context_init_from(
		    &filter->memory_context, memory_context, "filter"
	    )) {
		yanet_error_add(
			err,
			"out of memory: failed to init filter memory context"
		);
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
			yanet_error_add(
				err,
				"out of memory: failed to init registry for "
				"lookup %zu",
				(size_t)lookup_idx
			);
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
			yanet_error_add(
				err,
				"out of memory: failed to compile attribute "
				"lookup %zu",
				(size_t)lookup_idx
			);
			goto init_failed;
		}
	}

	if (filter_compiler->lookup_count == 1) {
		struct value_registry dummy;
		if (init_dummy_registry(
			    &filter->memory_context, rule_count, &dummy
		    )) {
			yanet_error_add(
				err,
				"out of memory: failed to init dummy registry"
			);
			value_registry_fini(&dummy);
			goto init_failed;
		}

		if (merge_and_set_registry_values(
			    &filter->memory_context,
			    &dummy,
			    &filter->v[1].registry,
			    &filter->v[0].table
		    )) {
			yanet_error_add(
				err,
				"out of memory: failed to merge registry values"
			);
			value_registry_fini(&dummy);
			goto init_failed;
		}

		value_registry_fini(&dummy);
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
			yanet_error_add(
				err,
				"out of memory: failed to collect registry at "
				"index %zu",
				idx
			);
			goto init_failed;
		}
	}

	if (merge_and_set_registry_values(
		    &filter->memory_context,
		    &filter->v[2 * 1].registry,
		    &filter->v[2 * 1 + 1].registry,
		    &filter->v[1].table
	    )) {
		yanet_error_add(
			err,
			"out of memory: failed to merge final registry values"
		);
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
