#pragma once

#include "common/registry.h"
#include "common/remap.h"

#include "common/for_each.h"

#define FILTER_ATTR_COMPILER(name) name##_attr_compiler

#define FILTER_ATTR_COMPILER_INIT_FUNC(name) name##_attr_init
#define FILTER_ATTR_COMPILER_FREE_FUNC(name) name##_attr_free

#define FILTER_ATTR_LOOKUP_HANDLER(name)                                       \
	{                                                                      \
		name##_attr_init,                                              \
		name##_attr_free,                                              \
	}

#define FILTER_COMPILER_DECLARE(tag, ...)                                      \
	static const struct filter_compiler *tag = &(struct filter_compiler){  \
		sizeof((struct filter_lookup_handler[]                         \
		){FOR_EACH(FILTER_ATTR_LOOKUP_HANDLER, __VA_ARGS__)}) /        \
			sizeof(struct filter_lookup_handler),                  \
		(struct filter_lookup_handler[]                                \
		){FOR_EACH(FILTER_ATTR_LOOKUP_HANDLER, __VA_ARGS__)},          \
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
 */
struct filter_compile_attr_handlers {
	filter_compile_attr_create_func create;
	filter_compile_attr_size_func size;
	filter_compile_attr_rule_is_any_func rule_is_any;
	filter_compile_attr_rule_iter_func rule_iter;
	filter_compile_attr_iter_func iter;
	filter_compile_attr_commit_func commit;
	filter_compile_attr_free_compile_func free_compile;
	filter_compile_attr_free_query_func free_query;
};

static inline int
filter_compile_attr_touch(uint32_t *value, void *data) {
	struct remap_table *remap_table = (struct remap_table *)data;
	return remap_table_touch(remap_table, *value, value);
}

static inline int
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
 * The routine provides the backward compatibility with the current
 * compilation procedure and defined attributes.
 */
static inline int
filter_compile_attr_build(
	const struct filter_compile_attr_handlers *attr_handlers,
	struct value_registry *registry,
	void **data,
	const struct filter_rule **rules,
	size_t rule_count,
	struct memory_context *memory_context
) {
	/*
	 * Splits the definition area int regions and initialize all of them
	 * with zero values.
	 */
	struct filter_compile_attr *attr = attr_handlers->create(
		memory_context, attr_handlers, rules, rule_count
	);
	if (attr == NULL)
		return -1;

	/*
	 * `remap_table is used to enumerate regions - each rule touch its
	 * regions so each region gains a value specific to matching set of
	 * rules.
	 */
	struct remap_table remap_table;
	if (remap_table_init(
		    &remap_table, memory_context, attr_handlers->size(attr)
	    )) {
		goto error;
	}

	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		if (rules[idx] == NULL)
			continue;
		/*
		 * If a rule covers the whole area there is no meaning to
		 * touch values - all of them just gain update without any
		 * distinction result.
		 */
		if (attr_handlers->rule_is_any(attr, attr_handlers, rules[idx]))
			continue;

		remap_table_new_gen(&remap_table);

		// Touch the rule matching values
		if (attr_handlers->rule_iter(
			    attr,
			    attr_handlers,
			    rules[idx],
			    filter_compile_attr_touch,
			    &remap_table
		    )) {
			remap_table_free(&remap_table);
			goto error;
		}
	}

	/*
	 * Remap table compaction removes gaps of unused values and makes the
	 * resulting set of values smaller
	 */

	remap_table_compact(&remap_table);

	/*
	 * Now reassign compacted values to each region defined for the
	 * attribute.
	 */

	if (attr_handlers->iter(
		    attr,
		    attr_handlers,
		    filter_compile_attr_compact,
		    &remap_table
	    )) {
		remap_table_free(&remap_table);
		goto error;
	}

	remap_table_free(&remap_table);

	for (uint32_t idx = 0; idx < rule_count; ++idx) {
		if (value_registry_start(registry))
			goto error;
		if (rules[idx] == NULL)
			continue;
		/*
		 * Collect rule matching values - there are two options:
		 *  - collect all known values in case if rule is matching any
		 *  - collect rule specific attributes
		 * In the first case we could collect all the values only
		 * once which is the subject of further investigation.
		 */
		if (attr_handlers->rule_is_any(
			    attr, attr_handlers, rules[idx]
		    )) {
			if (attr_handlers->iter(
				    attr,
				    attr_handlers,
				    filter_compile_attr_collect,
				    registry
			    ))
				goto error;

		} else {
			if (attr_handlers->rule_iter(
				    attr,
				    attr_handlers,
				    rules[idx],
				    filter_compile_attr_collect,
				    registry
			    ))
				goto error;
		}
	}

	void *dp_data = attr_handlers->commit(memory_context, attr);
	SET_OFFSET_OF(data, dp_data);
	if (dp_data == NULL)
		goto error;

	return 0;

error:
	attr_handlers->free_compile(memory_context, attr);
	return -1;
}
