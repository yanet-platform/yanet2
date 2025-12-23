#pragma once

#include "filter/compiler/attribute.h"
#include "filter/compiler/declare.h"
#include "filter/compiler/helper.h"
#include "filter/filter.h"

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

/**
 * @file compiler.h
 * @brief Build/teardown macros for filter classification trees.
 *
 * Defines:
 *  - FILTER_INIT: build a filter for a declared attribute signature
 *  - FILTER_FREE: free resources allocated by FILTER_INIT
 */
/**
 * @def FILTER_INIT(filter, tag, rules, rule_count, ctx)
 * @brief Build filter for signature tag into filter using rules.
 * @param filter struct filter*
 * @param tag name used in FILTER_COMPILER_DECLARE(...)
 * @param rules const struct filter_rule* array
 * @param rule_count size_t number of rules
 * @param ctx const struct memory_context* source context
 * @return int 0 on success, negative on error
 */
#define FILTER_INIT(filter, tag, rules, rule_count, ctx)                       \
	__extension__({                                                        \
		__label__ init_failed;                                         \
		__label__ init_finish;                                         \
		int __res;                                                     \
		if (sizeof(__filter_attrs_compiler_##tag) == 0) {              \
			__res = -1;                                            \
			goto init_failed;                                      \
		}                                                              \
		__res = memory_context_init_from(                              \
			&((filter)->memory_context), (ctx), "filter"           \
		);                                                             \
		if (__res < 0) {                                               \
			goto init_failed;                                      \
		}                                                              \
		const size_t __n = sizeof(__filter_attrs_compiler_##tag) /     \
				   sizeof(__filter_attrs_compiler_##tag[0]);   \
		/* init leaves */                                              \
		for (size_t __i = 0; __i < __n; ++__i) {                       \
			struct filter_vertex *__v = &((filter)->v[__n + __i]); \
			__res = value_registry_init(                           \
				&__v->registry, &((filter)->memory_context)    \
			);                                                     \
			if (__res < 0) {                                       \
				goto init_failed;                              \
			}                                                      \
			__v->data = NULL;                                      \
			__res = __filter_attrs_compiler_##tag[__i].init(       \
				&__v->registry,                                \
				&__v->data,                                    \
				(rules),                                       \
				(rule_count),                                  \
				&((filter)->memory_context)                    \
			);                                                     \
			if (__res < 0) {                                       \
				goto init_failed;                              \
			}                                                      \
		}                                                              \
		if (__n == 1) {                                                \
			struct value_registry __dummy;                         \
			__res = init_dummy_registry(                           \
				&((filter)->memory_context),                   \
				(rule_count),                                  \
				&__dummy                                       \
			);                                                     \
			if (__res < 0) {                                       \
				value_registry_free(&__dummy);                 \
				goto init_failed;                              \
			}                                                      \
			__res = merge_and_set_registry_values(                 \
				&((filter)->memory_context),                   \
				(rules),                                       \
				&__dummy,                                      \
				&((filter)->v[1].registry),                    \
				&((filter)->v[0].table),                       \
				&((filter)->v[0].registry)                     \
			);                                                     \
			if (__res < 0) {                                       \
				value_registry_free(&__dummy);                 \
				goto init_failed;                              \
			}                                                      \
			goto init_finish;                                      \
		}                                                              \
		for (size_t __idx = __n - 1; __idx >= 2; --__idx) {            \
			__res = merge_and_collect_registry(                    \
				&((filter)->memory_context),                   \
				&((filter)->v[2 * __idx].registry),            \
				&((filter)->v[2 * __idx + 1].registry),        \
				&((filter)->v[__idx].table),                   \
				&((filter)->v[__idx].registry)                 \
			);                                                     \
			if (__res < 0) {                                       \
				goto init_failed;                              \
			}                                                      \
		}                                                              \
		__res = merge_and_set_registry_values(                         \
			&((filter)->memory_context),                           \
			(rules),                                               \
			&((filter)->v[2 * 1].registry),                        \
			&((filter)->v[2 * 1 + 1].registry),                    \
			&((filter)->v[1].table),                               \
			&((filter)->v[1].registry)                             \
		);                                                             \
	init_failed:                                                           \
	init_finish:                                                           \
		__res;                                                         \
	})

/**
 * @def FILTER_FREE(filter, tag)
 * @brief Release resources allocated by FILTER_INIT for signature tag.
 * @param filter struct filter*
 * @param tag name used in FILTER_COMPILER_DECLARE(...)
 */
#define FILTER_FREE(filter, tag)                                               \
	__extension__({                                                        \
		const size_t __n = sizeof(__filter_attrs_compiler_##tag) /     \
				   sizeof(__filter_attrs_compiler_##tag[0]);   \
		for (size_t __i = 0; __i < __n; ++__i) {                       \
			struct filter_vertex *__v = &((filter)->v[__n + __i]); \
			__filter_attrs_compiler_##tag[__i].free(               \
				ADDR_OF(&__v->data),                           \
				&((filter)->memory_context)                    \
			);                                                     \
			SET_OFFSET_OF(&__v->data, NULL);                       \
		}                                                              \
		for (size_t __i = 1; __i < 2 * __n; ++__i) {                   \
			value_registry_free(&((filter)->v[__i].registry));     \
		}                                                              \
		for (size_t __i = 1; __i < __n; ++__i) {                       \
			value_table_free(&((filter)->v[__i].table));           \
		}                                                              \
		if (__n == 1) {                                                \
			struct filter_vertex *__v0 = &((filter)->v[0]);        \
			value_registry_free(&__v0->registry);                  \
			value_table_free(&__v0->table);                        \
		}                                                              \
	})
