#pragma once

/*
 * Region based builder for attributes whose definition area is the 16
 * bit value domain and whose rules select arbitrary intervals of it -
 * the port and protocol range attributes.
 *
 * The ruleset intervals partition the domain into regions with every
 * point of a region covered by the same set of rules. The range
 * collector computes the partition, the build touches and collects
 * whole regions instead of every covered value, and the classes are
 * committed into a flat attribute line indexed by the raw value, so
 * the query side reads them with one flat load.
 *
 * One expansion of the declaration below yields the full compile
 * machinery of one attribute side: the compile scratch with its ops
 * callbacks and the named compile entry filling an embedded attribute
 * of the given type. The rule field selector of the module rule type
 * is adapted into a rule agnostic getter stored in the scratch, so the
 * generated callbacks carry no rule knowledge beyond it.
 */

#include "common/key.h"
#include "common/memory.h"
#include "common/radix.h"
#include "common/range_collector.h"
#include "common/range_index.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "lib/classify/rule.h"

#include <stdint.h>
#include <string.h>

struct filter_u16_span {
	uint16_t from;
	uint16_t to;
};

struct filter_u16_ranges {
	uint32_t count;
	const struct filter_u16_span *items;
};

/*
 * Getter of the u16 ranges of a module rule, adapted from the module
 * field selector by the declaration macro below.
 */
typedef void (*classify_u16_get_ranges_func)(
	const struct classifier_rule *rule, struct filter_u16_ranges *ranges
);

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
		key[0] = from >> 8;
		key[1] = from;
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

#define CLASSIFY_U16_RANGES_COMPILE(tag, attr_type, get_ranges)                \
	struct classify_compile_##tag {                                        \
		classify_u16_get_ranges_func get_ranges;                       \
		struct range_index range_index;                                \
		struct value_table value_table;                                \
	};                                                                     \
                                                                               \
	static inline struct classify_compile_##tag                            \
		*classify_compile_##tag##_create(                              \
			struct memory_context *memory_context,                 \
			const struct classifier_rule *const *rules,            \
			uint32_t rule_count,                                   \
			classify_u16_get_ranges_func adapted_get_ranges        \
		) {                                                            \
		struct classify_compile_##tag *compile =                       \
			(struct classify_compile_##tag *)memory_balloc(        \
				memory_context,                                \
				sizeof(struct classify_compile_##tag)          \
			);                                                     \
		if (compile == NULL) {                                         \
			return NULL;                                           \
		}                                                              \
                                                                               \
		compile->get_ranges = adapted_get_ranges;                      \
                                                                               \
		struct range_collector collector;                              \
		if (range_collector_init(&collector, memory_context)) {        \
			goto error_free;                                       \
		}                                                              \
                                                                               \
		for (uint32_t rule_idx = 0; rule_idx < rule_count;             \
		     ++rule_idx) {                                             \
			const struct classifier_rule *rule = rules[rule_idx];  \
			if (rule == NULL) {                                    \
				continue;                                      \
			}                                                      \
                                                                               \
			struct filter_u16_ranges ranges;                       \
			compile->get_ranges(rule, &ranges);                    \
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
		if (range_index_init(&compile->range_index, memory_context)) { \
			goto error_collector;                                  \
		}                                                              \
                                                                               \
		if (range_collector_collect(                                   \
			    &collector, 2, &compile->range_index               \
		    )) {                                                       \
			goto error_range_index;                                \
		}                                                              \
                                                                               \
		if (value_table_init(                                          \
			    &compile->value_table,                             \
			    memory_context,                                    \
			    "filter:" #tag,                                    \
			    1,                                                 \
			    compile->range_index.count                         \
		    )) {                                                       \
			goto error_range_index;                                \
		}                                                              \
                                                                               \
		range_collector_free(&collector, 2);                           \
                                                                               \
		return compile;                                                \
                                                                               \
	error_range_index:                                                     \
		range_index_free(&compile->range_index);                       \
                                                                               \
	error_collector:                                                       \
		range_collector_free(&collector, 2);                           \
                                                                               \
	error_free:                                                            \
		memory_bfree(                                                  \
			memory_context,                                        \
			compile,                                               \
			sizeof(struct classify_compile_##tag)                  \
		);                                                             \
                                                                               \
		return NULL;                                                   \
	}                                                                      \
                                                                               \
	static inline uint32_t classify_compile_##tag##_size(                  \
		const void *compile                                            \
	) {                                                                    \
		const struct classify_compile_##tag *tag_compile = compile;    \
                                                                               \
		return tag_compile->value_table.h_dim *                        \
		       tag_compile->value_table.v_dim;                         \
	}                                                                      \
                                                                               \
	static inline int classify_compile_##tag##_rule_is_any(                \
		const void *compile, const struct classifier_rule *rule        \
	) {                                                                    \
		const struct classify_compile_##tag *tag_compile = compile;    \
                                                                               \
		struct filter_u16_ranges ranges;                               \
		tag_compile->get_ranges(rule, &ranges);                        \
                                                                               \
		return ranges.count == 0 ||                                    \
		       ranges.items[0].to - ranges.items[0].from == 65535;     \
	}                                                                      \
                                                                               \
	static inline uint32_t classify_compile_##tag##_hash(                  \
		const void *compile, const struct classifier_rule *rule        \
	) {                                                                    \
		const struct classify_compile_##tag *tag_compile = compile;    \
                                                                               \
		struct filter_u16_ranges ranges;                               \
		tag_compile->get_ranges(rule, &ranges);                        \
                                                                               \
		uint32_t hash = ranges.count;                                  \
		for (uint32_t idx = 0; idx < ranges.count; ++idx) {            \
			hash = hash * 31 + ranges.items[idx].from;             \
			hash = hash * 31 + ranges.items[idx].to;               \
		}                                                              \
		return hash;                                                   \
	}                                                                      \
                                                                               \
	static inline int classify_compile_##tag##_compare(                    \
		const void *compile,                                           \
		const struct classifier_rule *first,                           \
		const struct classifier_rule *second                           \
	) {                                                                    \
		const struct classify_compile_##tag *tag_compile = compile;    \
                                                                               \
		struct filter_u16_ranges first_ranges;                         \
		struct filter_u16_ranges second_ranges;                        \
		tag_compile->get_ranges(first, &first_ranges);                 \
		tag_compile->get_ranges(second, &second_ranges);               \
                                                                               \
		if (first_ranges.count != second_ranges.count) {               \
			return 1;                                              \
		}                                                              \
		if (first_ranges.count == 0) {                                 \
			return 0;                                              \
		}                                                              \
                                                                               \
		return memcmp(first_ranges.items,                              \
			      second_ranges.items,                             \
			      first_ranges.count * sizeof(*first_ranges.items) \
		       ) != 0;                                                 \
	}                                                                      \
                                                                               \
	static inline int classify_compile_##tag##_rule_iter(                  \
		void *compile,                                                 \
		const struct classifier_rule *rule,                            \
		int (*iter_cb_func)(uint32_t *value, void *data),              \
		void *cb_func_data                                             \
	) {                                                                    \
		struct classify_compile_##tag *tag_compile = compile;          \
                                                                               \
		struct filter_u16_ranges ranges;                               \
		tag_compile->get_ranges(rule, &ranges);                        \
                                                                               \
		for (uint32_t range_idx = 0; range_idx < ranges.count;         \
		     ++range_idx) {                                            \
			const struct filter_u16_span *span =                   \
				ranges.items + range_idx;                      \
                                                                               \
			uint8_t from_key[2];                                   \
			uint8_t to_key[2];                                     \
			from_key[0] = span->from >> 8;                         \
			from_key[1] = span->from;                              \
			to_key[0] = span->to >> 8;                             \
			to_key[1] = span->to;                                  \
			filter_key_inc(2, to_key);                             \
                                                                               \
			uint32_t start = radix_lookup(                         \
				&tag_compile->range_index.radix, 2, from_key   \
			);                                                     \
			uint32_t stop = radix_lookup(                          \
				&tag_compile->range_index.radix, 2, to_key     \
			);                                                     \
			if (stop == 0) {                                       \
				/*                                             \
				 * The only chance get zero here is for        \
				 * the last one item.                          \
				 */                                            \
				stop = tag_compile->range_index.count;         \
			}                                                      \
                                                                               \
			for (uint32_t idx = start; idx < stop; ++idx) {        \
				if (iter_cb_func(                              \
					    value_table_get_ptr(               \
						    &tag_compile->value_table, \
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
	static inline int classify_compile_##tag##_iter(                       \
		void *compile,                                                 \
		int (*iter_cb_func)(uint32_t *value, void *data),              \
		void *cb_func_data                                             \
	) {                                                                    \
		struct classify_compile_##tag *tag_compile = compile;          \
                                                                               \
		for (uint32_t idx = 0; idx < tag_compile->value_table.h_dim;   \
		     ++idx) {                                                  \
			if (iter_cb_func(                                      \
				    value_table_get_ptr(                       \
					    &tag_compile->value_table, 0, idx  \
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
	static inline void classify_compile_##tag##_free(                      \
		struct memory_context *memory_context, void *compile           \
	) {                                                                    \
		struct classify_compile_##tag *tag_compile = compile;          \
                                                                               \
		range_index_free(&tag_compile->range_index);                   \
		value_table_free(&tag_compile->value_table);                   \
                                                                               \
		memory_bfree(                                                  \
			memory_context,                                        \
			tag_compile,                                           \
			sizeof(struct classify_compile_##tag)                  \
		);                                                             \
	}                                                                      \
                                                                               \
	static inline int classify_compile_##tag##_commit(                     \
		struct memory_context *memory_context,                         \
		void *compile,                                                 \
		void *attr                                                     \
	) {                                                                    \
		struct classify_compile_##tag *tag_compile = compile;          \
		attr_type *tag_attr = attr;                                    \
                                                                               \
		struct vline staging;                                          \
		if (vline_init(                                                \
			    &staging, memory_context, "filter:" #tag, 65536    \
		    )) {                                                       \
			memset(tag_attr, 0, sizeof(*tag_attr));                \
			return -1;                                             \
		}                                                              \
                                                                               \
		struct filter_u16_commit_ctx commit_ctx;                       \
		commit_ctx.line = &staging;                                    \
		commit_ctx.value_table = &tag_compile->value_table;            \
		commit_ctx.value = 0;                                          \
		commit_ctx.pos = 0;                                            \
                                                                               \
		if (radix_walk(                                                \
			    &tag_compile->range_index.radix,                   \
			    2,                                                 \
			    filter_u16_commit_iterate,                         \
			    &commit_ctx                                        \
		    )) {                                                       \
			vline_free(&staging);                                  \
			memset(tag_attr, 0, sizeof(*tag_attr));                \
			return -1;                                             \
		}                                                              \
                                                                               \
		while (commit_ctx.pos < 65536) {                               \
			*vline_get_ptr(&staging, commit_ctx.pos++) =           \
				commit_ctx.value;                              \
		}                                                              \
                                                                               \
		/*                                                             \
		 * The staging line moves into the embedded attribute field    \
		 * by field: it carries relative pointers that a struct copy   \
		 * would strand.                                               \
		 */                                                            \
		tag_attr->line.size = staging.size;                            \
		SET_OFFSET_OF(                                                 \
			&tag_attr->line.values, ADDR_OF(&staging.values)       \
		);                                                             \
		SET_OFFSET_OF(                                                 \
			&tag_attr->line.memory_context,                        \
			ADDR_OF(&staging.memory_context)                       \
		);                                                             \
                                                                               \
		classify_compile_##tag##_free(memory_context, compile);        \
                                                                               \
		return 0;                                                      \
	}                                                                      \
                                                                               \
	static inline int classify_##tag##_compile(                            \
		struct memory_context *memory_context,                         \
		const struct classifier_rule **rules,                          \
		uint32_t rule_count,                                           \
		attr_type *attr,                                               \
		struct classifier *cls                                         \
	) {                                                                    \
		struct classify_compile_##tag *compile =                       \
			classify_compile_##tag##_create(                       \
				memory_context, rules, rule_count, get_ranges  \
			);                                                     \
		if (compile == NULL) {                                         \
			memset(attr, 0, sizeof(*attr));                        \
			memset(cls, 0, sizeof(*cls));                          \
			return -1;                                             \
		}                                                              \
                                                                               \
		const struct classify_attr_ops ops = {                         \
			.size = classify_compile_##tag##_size,                 \
			.rule_is_any = classify_compile_##tag##_rule_is_any,   \
			.hash = classify_compile_##tag##_hash,                 \
			.compare = classify_compile_##tag##_compare,           \
			.iter = classify_compile_##tag##_iter,                 \
			.rule_iter = classify_compile_##tag##_rule_iter,       \
			.commit = classify_compile_##tag##_commit,             \
			.free_compile = classify_compile_##tag##_free,         \
		};                                                             \
                                                                               \
		return classify_attr_compile(                                  \
			memory_context,                                        \
			&ops,                                                  \
			compile,                                               \
			rules,                                                 \
			rule_count,                                            \
			attr,                                                  \
			cls                                                    \
		);                                                             \
	}
