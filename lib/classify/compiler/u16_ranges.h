#pragma once

/*
 * Region based builder for attributes whose definition area is the 16
 * bit value domain and whose rules select arbitrary intervals of it -
 * the port and proto range attributes.
 *
 * The ruleset intervals partition the domain into regions with every
 * point of a region covered by the same set of rules. The range
 * collector computes the partition, the build touches and collects
 * whole regions instead of every covered value, and the classes are
 * committed into the attribute value line indexed by the raw value, so
 * the query side reads them exactly as the per value builders did.
 */

#include "common/container_of.h"
#include "common/memory.h"
#include "common/radix.h"
#include "common/range_collector.h"
#include "common/range_index.h"
#include "common/registry.h"
#include "common/value.h"

#include "lib/classify/classify.h"
#include "lib/classify/rule.h"

#include "declare.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

struct filter_u16_span {
	uint16_t from;
	uint16_t to;
};

struct filter_u16_ranges {
	uint32_t count;
	const struct filter_u16_span *items;
};

typedef void (*filter_u16_get_ranges_func)(
	const struct filter_rule *rule, struct filter_u16_ranges *ranges
);

struct filter_compile_u16_handlers {
	struct classify_attr_handlers attr_handlers;
	filter_u16_get_ranges_func get_ranges;
};

static inline void
filter_u16_key_of(uint16_t value, uint8_t key[2]) {
	key[0] = value >> 8;
	key[1] = value;
}

/*
 * Adds the interval [from, to] to the collector as the minimal set of
 * aligned prefix blocks covering it. The collector takes masks, so a
 * block of 2^width values is a mask of 16 - width bits.
 */
static inline int
filter_u16_add_range(
	struct range_collector *collector, uint32_t from, uint32_t to
) {
	while (from <= to) {
		uint32_t aligned = (from == 0) ? 1u << 16 : from & -from;
		uint32_t width = 31 - __builtin_clz(aligned);
		uint32_t room = to - from + 1;
		while ((1u << width) > room) {
			--width;
		}
		uint8_t key[2];
		filter_u16_key_of(from, key);
		if (range2_collector_add(collector, key, 16 - width)) {
			return -1;
		}
		from += 1u << width;
	}
	return 0;
}

/*
 * Fills the committed line region by region: the walk delivers the
 * region start keys in ascending order along with the region indexes,
 * every raw value of a region gains the region class.
 */
struct filter_u16_commit_ctx {
	struct vline *line;
	struct value_table *value_table;

	uint32_t value;
	uint32_t pos;
};

static inline int
filter_u16_commit_iterate(
	uint8_t key_size, const uint8_t *key, uint32_t index, void *data
) {
	(void)key_size;
	struct filter_u16_commit_ctx *ctx =
		(struct filter_u16_commit_ctx *)data;

	uint32_t to = ((uint32_t)key[0] << 8) | key[1];
	while (ctx->pos < to) {
		*vline_get_ptr(ctx->line, ctx->pos++) = ctx->value;
	}
	ctx->value = value_table_get(ctx->value_table, 0, index);

	return 0;
}

#define FILTER_U16_RANGES_DECLARE(tag, query_type, query_free)                 \
	struct classify_attr_##tag {                                           \
		struct classify_attr attr;                                     \
		struct range_index range_index;                                \
		struct value_table value_table;                                \
	};                                                                     \
                                                                               \
	static inline struct classify_attr *classify_attr_##tag##_create(      \
		struct memory_context *memory_context,                         \
		const struct classify_attr_handlers *attr_handlers,            \
		const struct filter_rule **rules,                              \
		uint32_t rule_count                                            \
	) {                                                                    \
		const struct filter_compile_u16_handlers *u16_handlers =       \
			container_of(                                          \
				attr_handlers,                                 \
				struct filter_compile_u16_handlers,            \
				attr_handlers                                  \
			);                                                     \
                                                                               \
		struct classify_attr_##tag *attr =                             \
			(struct classify_attr_##tag *)memory_balloc(           \
				memory_context,                                \
				sizeof(struct classify_attr_##tag)             \
			);                                                     \
		if (attr == NULL) {                                            \
			return NULL;                                           \
		}                                                              \
                                                                               \
		struct range_collector collector;                              \
		if (range_collector_init(&collector, memory_context)) {        \
			goto error_free;                                       \
		}                                                              \
                                                                               \
		for (uint32_t rule_idx = 0; rule_idx < rule_count;             \
		     ++rule_idx) {                                             \
			const struct filter_rule *rule = rules[rule_idx];      \
			if (rule == NULL) {                                    \
				continue;                                      \
			}                                                      \
                                                                               \
			struct filter_u16_ranges ranges;                       \
			u16_handlers->get_ranges(rule, &ranges);               \
			for (uint32_t idx = 0; idx < ranges.count; ++idx) {    \
				if (filter_u16_add_range(                      \
					    &collector,                        \
					    ranges.items[idx].from,            \
					    ranges.items[idx].to               \
				    )) {                                       \
					goto error_collector;                  \
				}                                              \
			}                                                      \
		}                                                              \
                                                                               \
		if (range_index_init(&attr->range_index, memory_context)) {    \
			goto error_collector;                                  \
		}                                                              \
                                                                               \
		if (range_collector_collect(                                   \
			    &collector, 2, &attr->range_index                  \
		    )) {                                                       \
			goto error_range_index;                                \
		}                                                              \
                                                                               \
		if (value_table_init(                                          \
			    &attr->value_table,                                \
			    memory_context,                                    \
			    "filter:" #tag,                                    \
			    1,                                                 \
			    attr->range_index.count                            \
		    )) {                                                       \
			goto error_range_index;                                \
		}                                                              \
                                                                               \
		range_collector_free(&collector, 2);                           \
                                                                               \
		return &attr->attr;                                            \
                                                                               \
	error_range_index:                                                     \
		range_index_free(&attr->range_index);                          \
                                                                               \
	error_collector:                                                       \
		range_collector_free(&collector, 2);                           \
                                                                               \
	error_free:                                                            \
		memory_bfree(                                                  \
			memory_context,                                        \
			attr,                                                  \
			sizeof(struct classify_attr_##tag)                     \
		);                                                             \
                                                                               \
		return NULL;                                                   \
	}                                                                      \
                                                                               \
	static inline uint32_t classify_attr_##tag##_size(                     \
		const struct classify_attr *attr                               \
	) {                                                                    \
		struct classify_attr_##tag *u16_attr =                         \
			container_of(attr, struct classify_attr_##tag, attr);  \
                                                                               \
		return u16_attr->value_table.v_dim *                           \
		       u16_attr->value_table.h_dim;                            \
	}                                                                      \
                                                                               \
	static inline int classify_attr_##tag##_rule_is_any(                   \
		const struct classify_attr *attr,                              \
		const struct classify_attr_handlers *attr_handlers,            \
		const struct filter_rule *rule                                 \
	) {                                                                    \
		(void)attr;                                                    \
		const struct filter_compile_u16_handlers *u16_handlers =       \
			container_of(                                          \
				attr_handlers,                                 \
				struct filter_compile_u16_handlers,            \
				attr_handlers                                  \
			);                                                     \
                                                                               \
		struct filter_u16_ranges ranges;                               \
		u16_handlers->get_ranges(rule, &ranges);                       \
                                                                               \
		return ranges.count == 0 ||                                    \
		       ranges.items[0].to - ranges.items[0].from == 65535;     \
	}                                                                      \
                                                                               \
	static inline uint32_t classify_attr_##tag##_hash(                     \
		const struct classify_attr_handlers *attr_handlers,            \
		const struct filter_rule *rule                                 \
	) {                                                                    \
		const struct filter_compile_u16_handlers *u16_handlers =       \
			container_of(                                          \
				attr_handlers,                                 \
				struct filter_compile_u16_handlers,            \
				attr_handlers                                  \
			);                                                     \
                                                                               \
		struct filter_u16_ranges ranges;                               \
		u16_handlers->get_ranges(rule, &ranges);                       \
                                                                               \
		uint32_t hash = ranges.count;                                  \
		for (uint32_t idx = 0; idx < ranges.count; ++idx) {            \
			hash = hash * 31 + ranges.items[idx].from;             \
			hash = hash * 31 + ranges.items[idx].to;               \
		}                                                              \
		return hash;                                                   \
	}                                                                      \
                                                                               \
	static inline int classify_attr_##tag##_compare(                       \
		const struct classify_attr_handlers *attr_handlers,            \
		const struct filter_rule *first,                               \
		const struct filter_rule *second                               \
	) {                                                                    \
		const struct filter_compile_u16_handlers *u16_handlers =       \
			container_of(                                          \
				attr_handlers,                                 \
				struct filter_compile_u16_handlers,            \
				attr_handlers                                  \
			);                                                     \
                                                                               \
		struct filter_u16_ranges first_ranges;                         \
		struct filter_u16_ranges second_ranges;                        \
		u16_handlers->get_ranges(first, &first_ranges);                \
		u16_handlers->get_ranges(second, &second_ranges);              \
                                                                               \
		if (first_ranges.count != second_ranges.count) {               \
			return 1;                                              \
		}                                                              \
                                                                               \
		return memcmp(first_ranges.items,                              \
			      second_ranges.items,                             \
			      first_ranges.count *                             \
				      sizeof(*first_ranges.items)) != 0;       \
	}                                                                      \
                                                                               \
	static inline int classify_attr_##tag##_rule_iter(                     \
		struct classify_attr *attr,                                    \
		const struct classify_attr_handlers *attr_handlers,            \
		const struct filter_rule *rule,                                \
		classify_attr_iter_cb_func iter_cb_func,                       \
		void *cb_func_data                                             \
	) {                                                                    \
		const struct filter_compile_u16_handlers *u16_handlers =       \
			container_of(                                          \
				attr_handlers,                                 \
				struct filter_compile_u16_handlers,            \
				attr_handlers                                  \
			);                                                     \
                                                                               \
		struct classify_attr_##tag *u16_attr =                         \
			container_of(attr, struct classify_attr_##tag, attr);  \
                                                                               \
		struct filter_u16_ranges ranges;                               \
		u16_handlers->get_ranges(rule, &ranges);                       \
                                                                               \
		for (uint32_t range_idx = 0; range_idx < ranges.count;         \
		     ++range_idx) {                                            \
			const struct filter_u16_span *span =                   \
				ranges.items + range_idx;                      \
                                                                               \
			uint8_t from_key[2];                                   \
			uint8_t to_key[2];                                     \
			filter_u16_key_of(span->from, from_key);               \
			filter_u16_key_of(span->to, to_key);                   \
			filter_key_inc(2, to_key);                             \
                                                                               \
			uint32_t start = radix_lookup(                         \
				&u16_attr->range_index.radix, 2, from_key      \
			);                                                     \
			uint32_t stop = radix_lookup(                          \
				&u16_attr->range_index.radix, 2, to_key        \
			);                                                     \
			if (stop == 0) {                                       \
				/*                                             \
				 * The only chance get zero here is for        \
				 * the last one item.                          \
				 */                                            \
				stop = u16_attr->range_index.count;            \
			}                                                      \
                                                                               \
			for (uint32_t idx = start; idx < stop; ++idx) {        \
				if (iter_cb_func(                              \
					    value_table_get_ptr(               \
						    &u16_attr->value_table,    \
						    0,                         \
						    idx                        \
					    ),                                 \
					    cb_func_data                       \
				    ) < 0) {                                   \
					return -1;                             \
				}                                              \
			}                                                      \
		}                                                              \
                                                                               \
		return 0;                                                      \
	}                                                                      \
                                                                               \
	static inline int classify_attr_##tag##_iter(                          \
		struct classify_attr *attr,                                    \
		const struct classify_attr_handlers *attr_handlers,            \
		classify_attr_iter_cb_func iter_cb_func,                       \
		void *cb_func_data                                             \
	) {                                                                    \
		(void)attr_handlers;                                           \
                                                                               \
		struct classify_attr_##tag *u16_attr =                         \
			container_of(attr, struct classify_attr_##tag, attr);  \
                                                                               \
		for (uint32_t idx = 0; idx < u16_attr->value_table.h_dim;      \
		     ++idx) {                                                  \
			if (iter_cb_func(                                      \
				    value_table_get_ptr(                       \
					    &u16_attr->value_table, 0, idx     \
				    ),                                         \
				    cb_func_data                               \
			    ) < 0) {                                           \
				return -1;                                     \
			}                                                      \
		}                                                              \
                                                                               \
		return 0;                                                      \
	}                                                                      \
                                                                               \
	static inline void classify_attr_##tag##_free(                         \
		struct memory_context *memory_context,                         \
		struct classify_attr *attr                                     \
	) {                                                                    \
		struct classify_attr_##tag *u16_attr =                         \
			container_of(attr, struct classify_attr_##tag, attr);  \
                                                                               \
		range_index_free(&u16_attr->range_index);                      \
		value_table_free(&u16_attr->value_table);                      \
                                                                               \
		memory_bfree(                                                  \
			memory_context,                                        \
			u16_attr,                                              \
			sizeof(struct classify_attr_##tag)                     \
		);                                                             \
	}                                                                      \
                                                                               \
	static inline struct classify_query_attr *                             \
	classify_attr_##tag##_commit(                                          \
		struct memory_context *memory_context,                         \
		struct classify_attr *attr                                     \
	) {                                                                    \
		struct classify_attr_##tag *u16_attr =                         \
			container_of(attr, struct classify_attr_##tag, attr);  \
                                                                               \
		query_type *query_attr = (query_type *)memory_balloc(          \
			memory_context, sizeof(query_type)                     \
		);                                                             \
		if (query_attr == NULL) {                                      \
			return NULL;                                           \
		}                                                              \
                                                                               \
		if (vline_init(                                                \
			    &query_attr->line,                                 \
			    memory_context,                                    \
			    "filter:" #tag,                                    \
			    65536                                              \
		    )) {                                                       \
			memory_bfree(                                          \
				memory_context, query_attr, sizeof(query_type) \
			);                                                     \
			return NULL;                                           \
		}                                                              \
                                                                               \
		struct filter_u16_commit_ctx commit_ctx;                       \
		commit_ctx.line = &query_attr->line;                           \
		commit_ctx.value_table = &u16_attr->value_table;               \
		commit_ctx.value = 0;                                          \
		commit_ctx.pos = 0;                                            \
                                                                               \
		if (radix_walk(                                                \
			    &u16_attr->range_index.radix,                      \
			    2,                                                 \
			    filter_u16_commit_iterate,                         \
			    &commit_ctx                                        \
		    )) {                                                       \
			vline_free(&query_attr->line);                         \
			memory_bfree(                                          \
				memory_context, query_attr, sizeof(query_type) \
			);                                                     \
			return NULL;                                           \
		}                                                              \
                                                                               \
		while (commit_ctx.pos < 65536) {                               \
			*vline_get_ptr(&query_attr->line, commit_ctx.pos++) =  \
				commit_ctx.value;                              \
		}                                                              \
                                                                               \
		classify_attr_##tag##_free(memory_context, attr);              \
                                                                               \
		return &query_attr->attr;                                      \
	}                                                                      \
                                                                               \
	static const struct classify_attr_handlers                             \
		classify_attr_##tag##_handlers = {                             \
			.create = classify_attr_##tag##_create,                \
			.size = classify_attr_##tag##_size,                    \
			.iter = classify_attr_##tag##_iter,                    \
			.rule_is_any = classify_attr_##tag##_rule_is_any,      \
			.hash = classify_attr_##tag##_hash,                    \
			.compare = classify_attr_##tag##_compare,              \
			.rule_iter = classify_attr_##tag##_rule_iter,          \
			.commit = classify_attr_##tag##_commit,                \
			.free_compile = classify_attr_##tag##_free,            \
			.free_query = query_free,                              \
	};
