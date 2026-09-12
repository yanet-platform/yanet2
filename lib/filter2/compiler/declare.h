#pragma once

#include "common/registry.h"
#include "common/remap.h"

#include "lib/filter2/filter.h"

#include "common/for_each.h"

#define FILTER_ATTR_COMPILE(name) &filter_compile_attr_##name.attr_handlers

#define FILTER_COMPILER_DECLARE(tag, ...)                                      \
	static const struct filter_compile_attr_handlers *tag[] = {            \
		FOR_EACH(FILTER_ATTR_COMPILE, __VA_ARGS__),                    \
	};

/*
 * There are following key concepts of filter compilation:
 *  - filter consists of a set of attributes each attribute is responsible
 *    for one of rule conditions - source network, protocol, vlan, e.t.c.
 *  - each attribute has some definition area where it works on
 *  - attribute splits the definition ares into regions with the following
 *    requirements:
 *    - all points of a region have the same set of rules matching
 *    - each region has it's own identifier (value)
 *    to the point as any other point of the region
 *  - attribute provides following interfaces:
 *    - size retrieves count of regions known to the attribute
 *    - iterate over the whole definition area
 *    - iterate over a rule matching regions
 *    - check if a rule covers the whole definition area - used in sake of
 *      efficiency
 *    - commit produces a classifier suitable for obtaining matching region
 *      value for a packet. Commit detaches the classifier from attribute
 *      and frees resources assigned to the attribute in case of success
 *    - free releases all associated resources and should not be called in
 *      case of commit success
 */

/*
 * Filter rule forward declaration.
 *
 * The structure is used to obtain specific rule attribute like network,
 * port range or protocol.
 */
struct filter_rule;

/*
 * Compile attribute forward declaration - compilation API handler.
 *
 * The structure used to store attribute specific temporary compilation data.
 */
struct filter_compile_attr {};
/*
 * Compilation attribute virtual table forward declaration.
 *
 * The structure also used as compilation API handler.
 */
struct filter_compile_attr_handlers;

/*
 * Attribute creation routine builds a set of regions for provided ruleset
 * and initializes each region value to zero.
 */
typedef struct filter_compile_attr *(*filter_compile_attr_create_func)(
	struct memory_context *memory_context,
	const struct filter_compile_attr_handlers *handlers,
	const struct filter_rule **rules,
	uint32_t rule_count
);

/*
 * Retrieves count of regions of an attribute
 */
typedef uint32_t (*filter_compile_attr_size_func)(
	const struct filter_compile_attr *attr
);

/*
 * Checks if a rule covers the whole definition area
 */
typedef int (*filter_compile_attr_rule_is_any_func)(
	const struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *handlers,
	const struct filter_rule *rule
);

/*
 * A callback with pointer to a region value and associated custom pointer
 */
typedef int (*filter_compile_attr_iter_cb_func)(uint32_t *value, void *data);

/*
 * Iterates over an attribute definition area with a callback for the each
 * region value
 */
typedef int (*filter_compile_attr_iter_func)(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *handlers,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
);

/*
 * Iterates over a rule regions with a callback for the each region value
 */
typedef int (*filter_compile_attr_rule_iter_func)(
	struct filter_compile_attr *attr,
	const struct filter_compile_attr_handlers *handlers,
	const struct filter_rule *rule,
	uint32_t rule_idx,
	filter_compile_attr_iter_cb_func iter_cb_func,
	void *cb_func_data
);

/*
 * Produces a classifier and frees the attribute in case of success
 */
typedef struct filter_query_attr *(*filter_compile_attr_commit_func)(
	struct memory_context *memory_context, struct filter_compile_attr *attr
);

/*
 * Frees attribute compilation data
 */
typedef void (*filter_compile_attr_free_compile_func)(
	struct memory_context *memory_context, struct filter_compile_attr *attr
);

/*
 * Frees attribute classifier data
 */
typedef void (*filter_compile_attr_free_query_func)(
	struct memory_context *memory_context, struct filter_query_attr *attr
);

/*
 * Compilation attribute virtual table. The table may be incorporated into
 * a structure providing additional routines, for example obtaining specific
 * conditions - networks, protocols and etc
 *
 * build is the compound entry point generated per attribute by
 * FILTER_COMPILE_ATTR_BUILD: it runs the whole region-enumeration pipeline
 * by calling the attribute's own handlers directly, so they get inlined
 * instead of dispatched through the remaining function pointers (which are
 * kept for the single-attribute path in filter_compile_single_attr).
 */
typedef struct filter_query_attr *(*filter_compile_attr_build_func)(
	struct value_registry *registry,
	const struct filter_rule **rules,
	uint32_t rule_count,
	struct memory_context *memory_context
);

struct filter_compile_attr_handlers {
	filter_compile_attr_build_func build;
	filter_compile_attr_free_query_func free_query;
};

static inline int
filter_compile_attr_touch(uint32_t *value, void *data) {
	struct remap_table *remap_table = (struct remap_table *)data;
	return remap_table_touch(remap_table, *value, value);
}

// Always-inlined at the direct call sites the generated builders emit;
// the out-of-line copy exists only because the iteration interface takes
// the callback by pointer.
__attribute__((always_inline)) static inline int
filter_compile_attr_collect(uint32_t *value, void *data) {
	struct value_registry *value_registry = (struct value_registry *)data;
	return value_registry_collect(value_registry, *value);
}

static inline int
filter_compile_attr_compact(uint32_t *value, void *data) {
	struct remap_table *remap_table = (struct remap_table *)data;
	*value = remap_table_compacted(remap_table, *value);
	return 0;
}

/*
 * Compound per-attribute compile builder.
 *
 * Each attribute instantiates these to produce a dedicated build routine
 * that runs the region-enumeration pipeline by calling the attribute's own
 * static inline handlers directly, so they are inlined at the call site
 * instead of dispatched through the vtable function pointers.
 *
 * The _AS form lets variants of one kind (source/destination halves and
 * alike) share a single family of handler functions: the builder is named
 * after the variant while the pipeline calls the shared family prefixed
 * handlers. The plain form is the common case where the variant name is
 * the family prefix.
 *
 * The _DECLARE forms must precede the attribute's vtable instance (which
 * references the builder via the build pointer), and the definitions must
 * follow the instance (the builder reads it).
 */
#define FILTER_COMPILE_ATTR_BUILD_AS_DECLARE(variant, prefix)                  \
	static inline struct filter_query_attr                                 \
		*filter_compile_attr_##variant##_build(                        \
			struct value_registry *registry,                       \
			const struct filter_rule **rules,                      \
			uint32_t rule_count,                                   \
			struct memory_context *memory_context                  \
		);

#define FILTER_COMPILE_ATTR_BUILD_AS(variant, prefix)                          \
	static inline struct filter_query_attr                                 \
		*filter_compile_attr_##variant##_build(                        \
			struct value_registry *registry,                       \
			const struct filter_rule **rules,                      \
			uint32_t rule_count,                                   \
			struct memory_context *memory_context                  \
		) {                                                            \
		const struct filter_compile_attr_handlers *attr_handlers =     \
			&filter_compile_attr_##variant.attr_handlers;          \
		struct filter_compile_attr *attr = prefix##_create(            \
			memory_context, attr_handlers, rules, rule_count       \
		);                                                             \
		if (attr == NULL)                                              \
			return NULL;                                           \
		struct remap_table remap_table;                                \
		if (remap_table_init(                                          \
			    &remap_table, memory_context, prefix##_size(attr)  \
		    )) {                                                       \
			goto error;                                            \
		}                                                              \
		for (uint32_t idx = 0; idx < rule_count; ++idx) {              \
			if (rules[idx] == NULL)                                \
				continue;                                      \
			if (prefix##_rule_is_any(                              \
				    attr, attr_handlers, rules[idx]            \
			    ))                                                 \
				continue;                                      \
			remap_table_new_gen(&remap_table);                     \
			if (prefix##_rule_iter(                                \
				    attr,                                      \
				    attr_handlers,                             \
				    rules[idx],                                \
				    idx,                                       \
				    filter_compile_attr_touch,                 \
				    &remap_table                               \
			    )) {                                               \
				remap_table_free(&remap_table);                \
				goto error;                                    \
			}                                                      \
		}                                                              \
		remap_table_compact(&remap_table);                             \
		if (prefix##_iter(                                             \
			    attr,                                              \
			    attr_handlers,                                     \
			    filter_compile_attr_compact,                       \
			    &remap_table                                       \
		    )) {                                                       \
			remap_table_free(&remap_table);                        \
			goto error;                                            \
		}                                                              \
		remap_table_free(&remap_table);                                \
		for (uint32_t idx = 0; idx < rule_count; ++idx) {              \
			if (value_registry_start(registry))                    \
				goto error;                                    \
			if (rules[idx] == NULL)                                \
				continue;                                      \
			if (prefix##_rule_is_any(                              \
				    attr, attr_handlers, rules[idx]            \
			    )) {                                               \
				if (prefix##_iter(                             \
					    attr,                              \
					    attr_handlers,                     \
					    filter_compile_attr_collect,       \
					    registry                           \
				    ))                                         \
					goto error;                            \
			} else {                                               \
				if (prefix##_rule_iter(                        \
					    attr,                              \
					    attr_handlers,                     \
					    rules[idx],                        \
					    idx,                               \
					    filter_compile_attr_collect,       \
					    registry                           \
				    ))                                         \
					goto error;                            \
			}                                                      \
		}                                                              \
		struct filter_query_attr *query_attr =                         \
			prefix##_commit(memory_context, attr);                 \
		if (query_attr == NULL)                                        \
			goto error;                                            \
		return query_attr;                                             \
	error:                                                                 \
		prefix##_free(memory_context, attr);                           \
		return NULL;                                                   \
	}

#define FILTER_COMPILE_ATTR_BUILD_DECLARE(name)                                \
	FILTER_COMPILE_ATTR_BUILD_AS_DECLARE(name, filter_compile_attr_##name)

#define FILTER_COMPILE_ATTR_BUILD(name)                                        \
	FILTER_COMPILE_ATTR_BUILD_AS(name, filter_compile_attr_##name)
