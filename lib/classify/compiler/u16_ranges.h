#pragma once

/*
 * Region based builder for attributes whose definition area is the 16
 * bit value domain and whose rules select arbitrary intervals of it -
 * the port and protocol range attributes.
 *
 * The ruleset intervals partition the domain into regions with every
 * point of a region covered by the same set of rules. The boundary
 * partition below splits the domain into pieces at the range ends, the
 * build touches and collects whole pieces instead of every covered
 * value, and the classes are committed into a flat attribute line
 * indexed by the raw value, so the query side reads them with one flat
 * load.
 *
 * One expansion of the declaration below yields the full compile
 * machinery of one attribute side: the compile scratch with its ops
 * callbacks and the named compile entry filling an embedded attribute
 * of the given type. The rule field selector of the module rule type
 * is adapted into a rule agnostic getter stored in the scratch, so the
 * generated callbacks carry no rule knowledge beyond it.
 */

#include "common/memory.h"
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

#define FILTER_U16_WORDS (65536 / 64)

/*
 * Partition of the 16 bit domain by the range ends.
 *
 * A piece starts at zero, at the start of every range and past the end
 * of every range; the bitmap marks the piece starts. The rank of a word
 * counts the starts in the words before it, so a value resolves into
 * its piece with one popcount.
 */
struct filter_u16_partition {
	uint64_t starts[FILTER_U16_WORDS];
	uint16_t rank[FILTER_U16_WORDS];
	uint32_t count;
};

static inline void
filter_u16_partition_init(struct filter_u16_partition *partition) {
	memset(partition, 0, sizeof(*partition));
	partition->starts[0] = 1;
}

// Splits the domain at both ends of the range; a reversed range covers
// no values and splits nothing.
static inline void
filter_u16_partition_add(
	struct filter_u16_partition *partition, uint32_t from, uint32_t to
) {
	if (from > to) {
		return;
	}
	partition->starts[from / 64] |= (uint64_t)1 << (from % 64);
	if (to < 65535) {
		++to;
		partition->starts[to / 64] |= (uint64_t)1 << (to % 64);
	}
}

// Counts the pieces once every range is added.
static inline void
filter_u16_partition_rank(struct filter_u16_partition *partition) {
	uint32_t count = 0;
	for (uint32_t word = 0; word < FILTER_U16_WORDS; ++word) {
		partition->rank[word] = count;
		count += __builtin_popcountll(partition->starts[word]);
	}
	partition->count = count;
}

static inline int
filter_u16_partition_is_start(
	const struct filter_u16_partition *partition, uint32_t value
) {
	return (partition->starts[value / 64] >> (value % 64)) & 1;
}

// The index of the piece holding the value: the number of piece starts
// up to the value, the value included, less one.
static inline uint32_t
filter_u16_partition_piece(
	const struct filter_u16_partition *partition, uint32_t value
) {
	uint64_t upto = partition->starts[value / 64] &
			(((uint64_t)2 << (value % 64)) - 1);
	return partition->rank[value / 64] + __builtin_popcountll(upto) - 1;
}

/*
 * Fills the committed line piece by piece: every raw value of a piece
 * gains the class of the piece.
 */
static inline void
filter_u16_fill_line(
	const struct filter_u16_partition *partition,
	const struct value_table *value_table,
	struct vline *line
) {
	// Zero starts the first piece.
	uint32_t piece = UINT32_MAX;
	uint32_t value = 0;
	for (uint32_t pos = 0; pos < 65536; ++pos) {
		if (filter_u16_partition_is_start(partition, pos)) {
			value = value_table_get(value_table, 0, ++piece);
		}
		*vline_get_ptr(line, pos) = value;
	}
}

#define CLASSIFY_U16_RANGES_COMPILE(tag, attr_type, get_ranges)                \
	struct classify_compile_##tag {                                        \
		classify_u16_get_ranges_func get_ranges;                       \
		const char *name;                                              \
		struct filter_u16_partition partition;                         \
		struct value_table value_table;                                \
	};                                                                     \
                                                                               \
	static inline struct classify_compile_##tag                            \
		*classify_compile_##tag##_create(                              \
			struct memory_context *memory_context,                 \
			const struct classifier_rule *const *rules,            \
			uint32_t rule_count,                                   \
			classify_u16_get_ranges_func adapted_get_ranges,       \
			const char *name                                       \
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
		compile->name = name;                                          \
                                                                               \
		filter_u16_partition_init(&compile->partition);                \
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
				filter_u16_partition_add(                      \
					&compile->partition,                   \
					ranges.items[idx].from,                \
					ranges.items[idx].to                   \
				);                                             \
			}                                                      \
		}                                                              \
		filter_u16_partition_rank(&compile->partition);                \
                                                                               \
		char table_name[MEMORY_CONTEXT_NAME_SIZE];                     \
		classify_leaf_name(                                            \
			table_name, sizeof(table_name), name, "table"          \
		);                                                             \
		if (value_table_init(                                          \
			    &compile->value_table,                             \
			    memory_context,                                    \
			    table_name,                                        \
			    1,                                                 \
			    compile->partition.count                           \
		    )) {                                                       \
			memory_bfree(                                          \
				memory_context,                                \
				compile,                                       \
				sizeof(struct classify_compile_##tag)          \
			);                                                     \
			return NULL;                                           \
		}                                                              \
                                                                               \
		return compile;                                                \
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
			/*                                                     \
			 * Reversed endpoint pairs cover no values.            \
			 *                                                     \
			 * They split nothing, so their boundaries need not    \
			 * exist among the piece starts.                       \
			 */                                                    \
			if (span->from > span->to) {                           \
				continue;                                      \
			}                                                      \
                                                                               \
			/*                                                     \
			 * Valid boundaries must resolve to piece starts.      \
			 * A missing boundary fails compilation safely.        \
			 */                                                    \
			const struct filter_u16_partition *partition =         \
				&tag_compile->partition;                       \
			if (!filter_u16_partition_is_start(                    \
				    partition, span->from                      \
			    ) ||                                               \
			    (span->to != 65535 &&                              \
			     !filter_u16_partition_is_start(                   \
				     partition, span->to + 1u                  \
			     ))) {                                             \
				return -1;                                     \
			}                                                      \
			uint32_t start = filter_u16_partition_piece(           \
				partition, span->from                          \
			);                                                     \
			uint32_t stop =                                        \
				span->to == 65535                              \
					? partition->count                     \
					: filter_u16_partition_piece(          \
						  partition, span->to + 1u     \
					  );                                   \
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
		char line_name[MEMORY_CONTEXT_NAME_SIZE];                      \
		classify_leaf_name(                                            \
			line_name,                                             \
			sizeof(line_name),                                     \
			tag_compile->name,                                     \
			"line"                                                 \
		);                                                             \
		if (vline_init(&staging, memory_context, line_name, 65536)) {  \
			memset(tag_attr, 0, sizeof(*tag_attr));                \
			return -1;                                             \
		}                                                              \
                                                                               \
		filter_u16_fill_line(                                          \
			&tag_compile->partition,                               \
			&tag_compile->value_table,                             \
			&staging                                               \
		);                                                             \
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
		const char *name,                                              \
		const struct classifier_rule **rules,                          \
		uint32_t rule_count,                                           \
		attr_type *attr,                                               \
		struct classifier *cls                                         \
	) {                                                                    \
		struct classify_compile_##tag *compile =                       \
			classify_compile_##tag##_create(                       \
				memory_context,                                \
				rules,                                         \
				rule_count,                                    \
				get_ranges,                                    \
				name                                           \
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
			name,                                                  \
			&ops,                                                  \
			compile,                                               \
			rules,                                                 \
			rule_count,                                            \
			attr,                                                  \
			cls                                                    \
		);                                                             \
	}
