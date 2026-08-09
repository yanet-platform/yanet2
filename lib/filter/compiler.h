#pragma once

#include "lib/filter/compiler/attribute.h"
#include "lib/filter/compiler/declare.h"
#include "lib/filter/compiler/helper.h"
#include "lib/filter/compiler/net6_share.h"
#include "lib/filter/filter.h"

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"
#include "lib/errors/errors.h"
#include <stdio.h>

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
	const char *name;
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
	// Calling value_registry_fini below clears the registry's
	// memory_context, so capture each leaf's attribute context now,
	// before any teardown runs.
	struct memory_context *attr_ctx[MAX_ATTRIBUTES];
	for (size_t i = 0; i < filter_compiler->lookup_count; ++i) {
		struct filter_vertex *v =
			filter->v + filter_compiler->lookup_count + i;
		struct memory_context *registry_ctx =
			ADDR_OF(&v->registry.memory_context);
		attr_ctx[i] = registry_ctx != NULL
				      ? ADDR_OF(&registry_ctx->parent)
				      : NULL;
	}

	for (size_t i = 0; i < filter_compiler->lookup_count; ++i) {
		struct filter_vertex *v =
			filter->v + filter_compiler->lookup_count + i;
		if (v->data != NULL) {
			// Free against the attribute context the payload was
			// allocated from (see filter_init), not the
			// registry's own context, which value_registry_fini
			// below releases on its own.
			filter_compiler->lookups[i].free(
				ADDR_OF(&v->data), attr_ctx[i]
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
	// Release each leaf's own attribute context back into the filter
	// context, now that the registry and payload it parented are gone.
	for (size_t i = 0; i < filter_compiler->lookup_count; ++i) {
		if (attr_ctx[i] == NULL) {
			continue;
		}
		memory_context_fini(attr_ctx[i]);
		memory_bfree(
			&filter->memory_context,
			attr_ctx[i],
			sizeof(*attr_ctx[i])
		);
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
	const char *name,
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
		    &filter->memory_context, memory_context, name
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

		// Each leaf gets its own attribute context, named after the
		// attribute, so the registry and the attribute's payload
		// below can be siblings instead of one nesting under the
		// other's teardown-sensitive context.
		struct memory_context *attr_ctx =
			(struct memory_context *)memory_balloc(
				&filter->memory_context,
				sizeof(struct memory_context)
			);
		if (attr_ctx == NULL) {
			yanet_error_add(
				err,
				"out of memory: failed to init attribute "
				"context for lookup %zu",
				(size_t)lookup_idx
			);
			goto init_failed;
		}
		memory_context_init_from(
			attr_ctx,
			&filter->memory_context,
			filter_compiler->lookups[lookup_idx].name
		);

		if (value_registry_init(&v->registry, attr_ctx, "registry")) {
			yanet_error_add(
				err,
				"out of memory: failed to init registry for "
				"lookup %zu",
				(size_t)lookup_idx
			);
			memory_context_fini(attr_ctx);
			memory_bfree(
				&filter->memory_context,
				attr_ctx,
				sizeof(*attr_ctx)
			);
			goto init_failed;
		}
		v->data = NULL;
		// The attribute context must outlive the registry it
		// parents, so the attribute receives it directly rather than
		// the registry's own context.
		if (filter_compiler->lookups[lookup_idx].init(
			    &v->registry, &v->data, rules, rule_count, attr_ctx
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

	// Each entry first_leaf[n] holds the attribute name of the first leaf
	// reachable from vertex n, computed bottom-up from the leaves filled
	// by the loop above. It gives every inner node a name derived from
	// its own subtree instead of its heap index.
	const char *first_leaf[2 * MAX_ATTRIBUTES];
	for (uint64_t lookup_idx = 0;
	     lookup_idx < filter_compiler->lookup_count;
	     ++lookup_idx) {
		first_leaf[filter_compiler->lookup_count + lookup_idx] =
			filter_compiler->lookups[lookup_idx].name;
	}
	for (size_t idx = filter_compiler->lookup_count - 1; idx >= 1; --idx) {
		first_leaf[idx] = first_leaf[2 * idx];
	}

	for (size_t idx = filter_compiler->lookup_count - 1; idx >= 2; --idx) {
		char name[64];
		snprintf(
			name,
			sizeof(name),
			"merge(%s,%s)",
			first_leaf[2 * idx],
			first_leaf[2 * idx + 1]
		);
		// The function merge_and_collect_registry only populates an
		// already-initialised registry. The loop above initialised one
		// only for leaf attributes, so this inner-node registry needs
		// an explicit init first.
		if (value_registry_init(
			    &filter->v[idx].registry,
			    &filter->memory_context,
			    name
		    )) {
			yanet_error_add(
				err,
				"out of memory: failed to init registry at "
				"index %zu",
				idx
			);
			goto init_failed;
		}
		char table_name[64];
		snprintf(
			table_name,
			sizeof(table_name),
			"merge-table(%s,%s)",
			first_leaf[2 * idx],
			first_leaf[2 * idx + 1]
		);
		if (merge_and_collect_registry(
			    &filter->memory_context,
			    &filter->v[2 * idx].registry,
			    &filter->v[2 * idx + 1].registry,
			    &filter->v[idx].table,
			    &filter->v[idx].registry,
			    table_name
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
