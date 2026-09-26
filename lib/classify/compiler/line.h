#pragma once

#include "common/memory.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "lib/classify/classifiers/line.h"
#include "lib/classify/rule.h"

#include <stdint.h>
#include <string.h>

/*
 * Region based builder for attributes whose definition area is a dense
 * small integer domain and whose rules select arbitrary intervals of
 * it - the vlan identifiers, the IP protocol numbers, the transport
 * specific byte.
 *
 * The ruleset intervals partition the domain into regions with every
 * point of a region covered by the same set of rules; the line cells
 * double as the remap scratch of the enumeration, and the commit moves
 * the compacted line into the embedded attribute as it is.
 */

/*
 * One closed interval of the domain. The bounds are clamped to the
 * domain of the instantiated attribute by the builder below.
 */
/*
 * One closed interval of the domain. The bounds are clamped to the
 * domain of the instantiated attribute by the builder below; the
 * interval and view types live in rule.h, where the rule owned
 * derived intervals are declared.
 */

/*
 * Getter of the domain intervals of a module rule, filled by the
 * module: the getter hands out the view over the intervals the rule
 * storage owns; the compile clamps them to the domain of the
 * instantiated attribute.
 */
typedef void (*classify_line_get_ranges_func)(
	const struct classifier_rule *rule, struct classify_line_ranges *ranges
);

#define CLASSIFY_LINE_COMPILE(tag, attr_type, domain_size, get_ranges)         \
	struct classify_compile_##tag {                                        \
		classify_line_get_ranges_func get_ranges;                      \
		struct vline line;                                             \
	};                                                                     \
                                                                               \
	static inline struct classify_compile_##tag                            \
		*classify_compile_##tag##_create(                              \
			struct memory_context *memory_context,                 \
			const struct classifier_rule *const *rules,            \
			uint32_t rule_count,                                   \
			classify_line_get_ranges_func adapted_get_ranges       \
		) {                                                            \
		(void)rules;                                                   \
		(void)rule_count;                                              \
                                                                               \
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
		if (vline_init(                                                \
			    &compile->line,                                    \
			    memory_context,                                    \
			    "filter:" #tag,                                    \
			    domain_size                                        \
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
		return tag_compile->line.size;                                 \
	}                                                                      \
                                                                               \
	static inline int classify_compile_##tag##_iter(                       \
		void *compile,                                                 \
		int (*iter_cb_func)(uint32_t *value, void *data),              \
		void *cb_func_data                                             \
	) {                                                                    \
		struct classify_compile_##tag *tag_compile = compile;          \
                                                                               \
		for (uint32_t idx = 0; idx < tag_compile->line.size; ++idx) {  \
			if (iter_cb_func(                                      \
				    vline_get_ptr(&tag_compile->line, idx),    \
				    cb_func_data                               \
			    ) < 0) {                                           \
				return -1;                                     \
			}                                                      \
		}                                                              \
                                                                               \
		return 0;                                                      \
	}                                                                      \
                                                                               \
	static inline int classify_compile_##tag##_rule_is_any(                \
		const void *compile, const struct classifier_rule *rule        \
	) {                                                                    \
		const struct classify_compile_##tag *tag_compile = compile;    \
                                                                               \
		struct classify_line_ranges ranges;                            \
		memset(&ranges, 0, sizeof(ranges));                            \
		tag_compile->get_ranges(rule, &ranges);                        \
                                                                               \
		return ranges.count == 0 ||                                    \
		       (ranges.items[0].from == 0 &&                           \
			ranges.items[0].to >= domain_size - 1);                \
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
		struct classify_line_ranges ranges;                            \
		memset(&ranges, 0, sizeof(ranges));                            \
		tag_compile->get_ranges(rule, &ranges);                        \
                                                                               \
		for (uint32_t range_idx = 0; range_idx < ranges.count;         \
		     ++range_idx) {                                            \
			const struct classify_line_range *range =              \
				ranges.items + range_idx;                      \
			uint32_t from = range->from > 0 ? range->from : 0;     \
			uint32_t to = range->to < domain_size - 1              \
					      ? range->to                      \
					      : domain_size - 1;               \
			for (uint32_t value = from; value <= to; ++value) {    \
				if (iter_cb_func(                              \
					    vline_get_ptr(                     \
						    &tag_compile->line, value  \
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
	static inline void classify_compile_##tag##_free(                      \
		struct memory_context *memory_context, void *compile           \
	) {                                                                    \
		struct classify_compile_##tag *tag_compile = compile;          \
                                                                               \
		vline_free(&tag_compile->line);                                \
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
		/*                                                             \
		 * The line moves into the embedded attribute field by         \
		 * field: it carries relative pointers that a struct copy      \
		 * would strand.                                               \
		 */                                                            \
		tag_attr->line.size = tag_compile->line.size;                  \
		SET_OFFSET_OF(                                                 \
			&tag_attr->line.values,                                \
			ADDR_OF(&tag_compile->line.values)                     \
		);                                                             \
		SET_OFFSET_OF(                                                 \
			&tag_attr->line.memory_context,                        \
			ADDR_OF(&tag_compile->line.memory_context)             \
		);                                                             \
                                                                               \
		/* The line internals now belong to the attribute, so only     \
		 * the scratch shell is released here. */                      \
		memory_bfree(                                                  \
			memory_context,                                        \
			tag_compile,                                           \
			sizeof(struct classify_compile_##tag)                  \
		);                                                             \
                                                                               \
		return 0;                                                      \
	}                                                                      \
                                                                               \
	static inline uint32_t classify_compile_##tag##_hash(                  \
		const void *compile, const struct classifier_rule *rule        \
	) {                                                                    \
		const struct classify_compile_##tag *tag_compile = compile;    \
                                                                               \
		struct classify_line_ranges ranges;                            \
		memset(&ranges, 0, sizeof(ranges));                            \
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
		struct classify_line_ranges first_ranges;                      \
		struct classify_line_ranges second_ranges;                     \
		memset(&first_ranges, 0, sizeof(first_ranges));                \
		memset(&second_ranges, 0, sizeof(second_ranges));              \
		tag_compile->get_ranges(first, &first_ranges);                 \
		tag_compile->get_ranges(second, &second_ranges);               \
                                                                               \
		if (first_ranges.count != second_ranges.count) {               \
			return 1;                                              \
		}                                                              \
		for (uint32_t idx = 0; idx < first_ranges.count; ++idx) {      \
			if (first_ranges.items[idx].from !=                    \
				    second_ranges.items[idx].from ||           \
			    first_ranges.items[idx].to !=                      \
				    second_ranges.items[idx].to) {             \
				return 1;                                      \
			}                                                      \
		}                                                              \
		return 0;                                                      \
	}                                                                      \
                                                                               \
	/*                                                                     \
	 * Declares the line compile entry of one consumer: the getter         \
	 * derives the domain intervals of the module rule, and the entry      \
	 * takes the module rule array the consumer owns.                      \
	 *                                                                     \
	 * On success the attribute and the stage - the registry with the      \
	 * rule group row - are owned by the caller, released through          \
	 * classifier_fini; on failure every partial state is freed, the       \
	 * attribute and the stage are left zeroed.                            \
	 */                                                                    \
	static inline int classify_##tag##_compile(                            \
		struct memory_context *memory_context,                         \
		const struct classifier_rule **rules,                          \
		uint32_t rule_count,                                           \
		attr_type *attr,                                               \
		struct classifier *cls                                         \
	) {                                                                    \
		struct classify_compile_##tag *compile =                       \
			classify_compile_##tag##_create(                       \
				memory_context,                                \
				(const struct classifier_rule *const *)rules,  \
				rule_count,                                    \
				get_ranges                                     \
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
			(const struct classifier_rule *const *)rules,          \
			rule_count,                                            \
			attr,                                                  \
			cls                                                    \
		);                                                             \
	}
