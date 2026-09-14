#pragma once

/*
 * Region based builder for attributes whose definition area is the 16
 * bit value domain and whose rules select arbitrary intervals of it —
 * the port and proto range attributes.
 *
 * The ruleset's interval endpoints partition the domain into regions
 * inside which every point is covered by the same set of rules. The
 * range collector computes that partition, the build touches and
 * collects whole regions instead of every covered value, and rules
 * sharing an identical interval list are grouped so each distinct list
 * runs once. The classes are committed into the attribute's value line
 * indexed by the raw value, so the query side reads them exactly as
 * the per value builders did.
 */

#include "common/memory.h"
#include "common/radix.h"
#include "common/range_collector.h"
#include "common/range_index.h"
#include "common/registry.h"
#include "common/value.h"
#include "lib/filter2/filter.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// Sentinel group ids: a rule outside any group (NULL rule slot) and a
// rule covering the whole area through an empty or full interval list.
#define FILTER_U16_GROUP_NONE (UINT32_MAX - 1)
#define FILTER_U16_GROUP_WHOLE (UINT32_MAX)

// Content group of rules: the identical interval list is touched once
// and every member replays the group's classes.
struct filter_u16_group {
	uint64_t hash;
	uint32_t id;
	uint32_t range_count;
	struct filter_u16_span {
		uint16_t from;
		uint16_t to;
	} spans[];
};

static inline void
filter_u16_key_of(uint32_t value, uint8_t key[2]) {
	key[0] = value >> 8;
	key[1] = value;
}

// Adds the interval [from, to] (inclusive) to the collector as the
// minimal set of aligned prefix blocks covering it. The collector takes
// masks, so a block of 2^width values is a mask of 16 - width bits.
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
		if (range_collector_add(collector, 2, key, 16 - width)) {
			return -1;
		}
		from += 1u << width;
	}
	return 0;
}

// Region of a value: the ids follow key order, so the last region
// start not above the value identifies it.
static inline uint32_t
filter_u16_region_of(
	const uint32_t *region_keys, uint32_t region_count, uint32_t value
) {
	uint32_t lo = 0;
	uint32_t hi = region_count;
	while (lo + 1 < hi) {
		uint32_t mid = (lo + hi) / 2;
		if (region_keys[mid] <= value) {
			lo = mid;
		} else {
			hi = mid;
		}
	}
	return lo;
}

struct filter_u16_boundary_ctx {
	uint32_t *keys;
	uint32_t count;
	uint32_t capacity;
	int failed;
};

// Region start keys by id: the walk delivers every radix node with its
// region id as the value.
static inline int
filter_u16_boundary_cb(
	uint8_t key_size, const uint8_t *key, uint32_t value, void *data
) {
	(void)key_size;
	struct filter_u16_boundary_ctx *ctx = data;
	if (value >= ctx->capacity) {
		ctx->failed = 1;
		return -1;
	}
	ctx->keys[value] = ((uint32_t)key[0] << 8) | key[1];
	if (value + 1 > ctx->count) {
		ctx->count = value + 1;
	}
	return 0;
}

/*
 * Generates the per variant build entry for a 16 bit range attribute.
 * family is the attribute family (its create, free and commit own the
 * compile attribute and the value line); ranges_type and ranges_field
 * name the rule accessor the family handlers carry. The generated
 * routine returns the committed query attribute.
 */
#define FILTER_COMPILE_ATTR_U16_RANGES_BUILD_AS(                                \
	variant, family, ranges_type, ranges_field                                  \
)                                                                               \
	static inline struct filter_query_attr *                               \
	filter_compile_attr_##variant##_build(                                 \
		struct value_registry *registry,                               \
		const struct filter_rule **rules,                              \
		uint32_t rule_count,                                           \
		struct memory_context *memory_context                          \
	) {                                                                    \
		const struct filter_compile_attr_handlers *attr_handlers =     \
			&filter_compile_attr_##variant.attr_handlers;          \
		const struct family##_handlers *family_handlers =              \
			container_of(                                          \
				attr_handlers,                                 \
				const struct family##_handlers,                 \
				attr_handlers                                    \
			);                                                     \
		struct filter_compile_attr *attr = family##_create(           \
			memory_context, attr_handlers, rules, rule_count        \
		);                                                             \
		if (attr == NULL) {                                            \
			return NULL;                                           \
		}                                                              \
		struct family *compile_attr =                                  \
			container_of(attr, struct family, attr);               \
		struct filter_query_attr *result = NULL;                       \
		struct range_collector collector;                              \
		range_collector_init(&collector, memory_context);              \
		struct range_index range_index;                                \
		range_index_init(&range_index, memory_context);                \
		uint32_t *region_keys = NULL;                                  \
		uint32_t *region_val = NULL;                                   \
		uint32_t *group_of = calloc(rule_count, sizeof(uint32_t));     \
		struct filter_u16_group **groups = NULL;                       \
		struct filter_u16_group **group_by_id = NULL;                  \
		uint32_t group_count = 0;                                      \
		uint32_t group_cap = 64;                                       \
                                                                               \
		/* One pass over the rules: read the interval list, feed      \
		 * every interval to the collector and group rules by        \
		 * identical content. */                                     \
		groups = calloc(group_cap, sizeof(*groups));                   \
		if (group_of == NULL || groups == NULL) {                      \
			goto out;                                              \
		}                                                              \
		for (uint32_t rule_idx = 0; rule_idx < rule_count;             \
		     ++rule_idx) {                                             \
			const struct filter_rule *rule = rules[rule_idx];      \
			if (rule == NULL) {                                    \
				group_of[rule_idx] = FILTER_U16_GROUP_NONE;    \
				continue;                                      \
			}                                                      \
			ranges_type ranges;                                    \
			family_handlers->ranges_field(rule, &ranges);          \
			int is_any = ranges.count == 0 ||                       \
				(ranges.count == 1 &&                           \
				 ranges.items[0].from == 0 &&                   \
				 ranges.items[0].to == 65535);                  \
			if (is_any) {                                          \
				group_of[rule_idx] = FILTER_U16_GROUP_WHOLE;    \
				continue;                                      \
			}                                                      \
			for (uint32_t idx = 0; idx < ranges.count; ++idx) {    \
				if (filter_u16_add_range(                       \
					    &collector,                            \
					    ranges.items[idx].from,                \
					    ranges.items[idx].to                  \
				    )) {                                       \
					goto out;                              \
				}                                              \
			}                                                      \
			uint64_t hash = 1469598103934665603ULL;                \
			for (uint32_t idx = 0; idx < ranges.count; ++idx) {    \
				hash = (hash ^ ranges.items[idx].from) *       \
				       1099511628211ULL;                       \
				hash = (hash ^ ranges.items[idx].to) *         \
				       1099511628211ULL;                       \
			}                                                      \
			if (hash == 0) {                                       \
				hash = 1;                                      \
			}                                                      \
			uint32_t slot = (uint32_t)hash & (group_cap - 1);      \
			while (groups[slot] != NULL) {                         \
				struct filter_u16_group *g = groups[slot];     \
				if (g->hash == hash &&                         \
				    g->range_count == ranges.count &&           \
				    memcmp(                                    \
					    g->spans, ranges.items,            \
					    ranges.count *                     \
						    sizeof(g->spans[0])            \
				    ) == 0) {                                \
					group_of[rule_idx] = g->id;           \
					goto next_rule;                       \
				}                                              \
				slot = (slot + 1) & (group_cap - 1);           \
			}                                                      \
			if ((group_count + 1) * 2 + 2 > group_cap) {           \
				uint32_t new_cap = group_cap * 2;              \
				struct filter_u16_group **grown =              \
					calloc(new_cap, sizeof(*grown));       \
				if (grown == NULL) {                           \
					goto out;                              \
				}                                              \
				for (uint32_t s = 0; s < group_cap; ++s) {     \
					struct filter_u16_group *g = groups[s]; \
					if (g == NULL) {                     \
						continue;                      \
					}                                    \
					uint32_t nslot =                    \
						(uint32_t)g->hash &             \
						(new_cap - 1);                \
					while (grown[nslot] != NULL)         \
						nslot = (nslot + 1) &        \
							(new_cap - 1);         \
					grown[nslot] = g;                   \
				}                                              \
				free(groups);                                  \
				groups = grown;                                \
				group_cap = new_cap;                           \
				slot = (uint32_t)hash & (group_cap - 1);       \
				while (groups[slot] != NULL) {                 \
					slot = (slot + 1) & (group_cap - 1);   \
				}                                              \
			}                                                      \
			struct filter_u16_group *g = calloc(                    \
				1,                                                 \
				sizeof(*g) +                                       \
					ranges.count * sizeof(g->spans[0])         \
			);                                                     \
			if (g == NULL) {                                        \
				goto out;                                          \
			}                                                      \
			g->hash = hash;                                        \
			g->id = group_count;                                  \
			g->range_count = ranges.count;                          \
			memcpy(g->spans,                                       \
			       ranges.items,                                   \
			       ranges.count * sizeof(g->spans[0]));             \
			groups[slot] = g;                                      \
			group_of[rule_idx] = g->id;                            \
			++group_count;                                         \
		next_rule:;                                                    \
		}                                                              \
		group_by_id = calloc(group_count + 1, sizeof(*group_by_id));   \
		if (group_by_id == NULL) {                                     \
			goto out;                                              \
		}                                                              \
		for (uint32_t s = 0; s < group_cap; ++s) {                     \
			if (groups[s] != NULL) {                              \
				group_by_id[groups[s]->id] = groups[s];        \
			}                                                      \
		}                                                              \
                                                                               \
		/* Partition: regions in value order with sequential ids. */  \
		if (range_collector_collect(                                  \
			    &collector, 2, &range_index                          \
		    )) {                                                     \
			goto out;                                              \
		}                                                              \
		uint32_t region_count = range_index.count;                     \
		if (region_count == 0) {                                       \
			/* No constrained interval: one region over the whole  \
			 * area, carrying the image of the fill. */            \
			region_keys = calloc(2, sizeof(uint32_t));             \
			region_val = calloc(1, sizeof(uint32_t));              \
			if (region_keys == NULL || region_val == NULL) {       \
				goto out;                                      \
			}                                                      \
			region_count = 1;                                      \
		} else {                                                       \
			region_keys = calloc(                                  \
				region_count + 1, sizeof(uint32_t)             \
			);                                                     \
			region_val = calloc(region_count, sizeof(uint32_t));   \
			if (region_keys == NULL || region_val == NULL) {       \
				goto out;                                      \
			}                                                      \
			struct filter_u16_boundary_ctx bctx = {                \
				.keys = region_keys,                           \
				.count = 0,                                    \
				.capacity = region_count,                      \
				.failed = 0,                                   \
			};                                                     \
			if (radix_walk(                                        \
				    &range_index.radix,                        \
				    2,                                         \
				    filter_u16_boundary_cb,                    \
				    &bctx                                      \
			    ) != 0 ||                                          \
			    bctx.failed || bctx.count != region_count) {      \
				goto out;                                      \
			}                                                      \
		}                                                              \
		region_keys[region_count] = 65536;                             \
                                                                               \
		/* Touch per region, one generation per content group. */     \
		struct remap_table remap;                                      \
		if (remap_table_init(&remap, memory_context, region_count)) {  \
			goto out;                                              \
		}                                                              \
		for (uint32_t gid = 0; gid < group_count; ++gid) {             \
			const struct filter_u16_group *g = group_by_id[gid];   \
			remap_table_new_gen(&remap);                           \
			for (uint32_t ridx = 0; ridx < g->range_count;         \
			     ++ridx) {                                         \
				uint32_t lo = filter_u16_region_of(             \
					region_keys,                           \
					region_count,                          \
					g->spans[ridx].from                    \
				);                                             \
				uint32_t hi = filter_u16_region_of(             \
					region_keys,                           \
					region_count,                          \
					g->spans[ridx].to                      \
				);                                                 \
				for (uint32_t id = lo; id <= hi; ++id) {         \
					remap_table_touch(                      \
						&remap,                              \
						region_val[id],                     \
						&region_val[id]                     \
					);                                     \
				}                                                 \
			}                                                      \
		}                                                              \
		remap_table_compact(&remap);                                   \
		for (uint32_t id = 0; id < region_count; ++id) {               \
			region_val[id] =                                     \
				remap_table_compacted(&remap, region_val[id]);   \
		}                                                              \
		remap_table_free(&remap);                                      \
                                                                               \
		/* Registry ranges: per rule replay of its group's classes. */ \
		for (uint32_t rule_idx = 0; rule_idx < rule_count;             \
		     ++rule_idx) {                                             \
			if (value_registry_start(registry)) {                  \
				goto out;                                      \
			}                                                      \
			uint32_t gid = group_of[rule_idx];                     \
			if (gid == FILTER_U16_GROUP_NONE) {                    \
				continue;                                      \
			}                                                      \
			if (gid == FILTER_U16_GROUP_WHOLE) {                   \
				for (uint32_t id = 0; id < region_count;      \
				     ++id) {                                  \
					if (value_registry_collect(             \
						    registry, region_val[id]      \
					    )) {                               \
						goto out;                      \
					}                                          \
				}                                              \
				continue;                                      \
			}                                                      \
			const struct filter_u16_group *g = group_by_id[gid];   \
			for (uint32_t ridx = 0; ridx < g->range_count;         \
			     ++ridx) {                                         \
				uint32_t lo = filter_u16_region_of(             \
					region_keys,                           \
					region_count,                          \
					g->spans[ridx].from                    \
				);                                             \
				uint32_t hi = filter_u16_region_of(             \
					region_keys,                           \
					region_count,                          \
					g->spans[ridx].to                      \
				);                                             \
				for (uint32_t id = lo; id <= hi; ++id) {         \
					if (value_registry_collect(             \
						    registry, region_val[id]      \
					    )) {                               \
						goto out;                      \
					}                                          \
				}                                                 \
			}                                                      \
		}                                                              \
                                                                               \
		/* Commit: spread each region's class over its value span. */ \
		for (uint32_t id = 0; id < region_count; ++id) {               \
			for (uint32_t value = region_keys[id];                 \
			     value < region_keys[id + 1];                       \
			     ++value) {                                        \
				*vline_get_ptr(                                 \
					&compile_attr->query_attr->line, value  \
				) = region_val[id];                           \
			}                                                      \
		}                                                              \
		result = family##_commit(memory_context, attr);                \
		attr = NULL;                                                   \
                                                                               \
	out:                                                               \
		free(region_keys);                                             \
		free(region_val);                                              \
		free(group_of);                                                \
		if (groups != NULL) {                                          \
			for (uint32_t s = 0; s < group_cap; ++s) {             \
				free(groups[s]);                               \
			}                                                      \
			free(groups);                                          \
		}                                                              \
		free(group_by_id);                                             \
		range_collector_free(&collector, 2);                           \
		range_index_free(&range_index);                                \
		if (result == NULL && attr != NULL) {                          \
			family##_free(memory_context, attr);                   \
		}                                                              \
		return result;                                                 \
	}
