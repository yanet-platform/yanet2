#pragma once

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
struct filter_compile_attr;
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
