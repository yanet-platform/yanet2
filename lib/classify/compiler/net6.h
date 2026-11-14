#pragma once

#include "common/exp_array.h"
#include "common/hash_index.h"
#include "common/key.h"
#include "common/lpm.h"
#include "common/memory.h"
#include "common/network.h"
#include "common/radix.h"
#include "common/range_collector.h"
#include "common/range_index.h"
#include "common/registry.h"
#include "common/value.h"

#include "declare.h"
#include "lib/classify/classifiers/net6.h"
#include "lib/classify/rule.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

/*
 * Region based builder for the network attribute of the 128 bit value
 * domain.
 *
 * An address splits into the high and the low 64 bit halves; every rule
 * network is a prefix, so a network selects an interval of the high half
 * and an interval of the low one. Each half is partitioned into regions
 * on its own, the high and low regions of a network cross into the
 * combined regions of the address domain, and a dense high by low value
 * table carries the join for the query side: a packet address resolves
 * to one high and one low region value through the per half longest
 * prefix match, and the table holds the combined region of the pair. A
 * high region row without any low half distinction carries its combined
 * region in a uniform value line over the rows instead - the query
 * skips the low half lookup for it.
 *
 * The builder runs in two layers. The networks repeat across the
 * rules, so the first layer groups the unique normalized networks with
 * a hash index and assigns the combined regions once: a combined region
 * is the set of networks covering a cell, and a registry with one range
 * per network holds the region values the network covers. The high half
 * intervals of the networks nest, so one sweep over the high half carries
 * the set of every low region and each interval changes only the cells
 * of its own networks. A network without a mask covers the whole domain
 * and contributes no distinction on its own, so its range holds every
 * region.
 *
 * The second layer is the shared attribute driver itself: it groups the
 * rules by their network list - hashed and compared as stored, with no
 * sorting or normalization, canonicalizing the lists is a preparation
 * stage outside the library - and the rule iteration resolves the
 * networks of the list through the net index and replays the values
 * assigned to them instead of walking the crosses again.
 */

/*
 * Getter of the IPv6 networks of a module rule - the source or the
 * destination side - filled by the module and adapted to the rule
 * agnostic core through the compile macro below.
 */
typedef void (*classify_net6_get_net6s_func)(
	const struct classifier_rule *rule, struct filter_net6s *net6s
);

/*
 * Compile scratch of the IPv6 network attribute.
 *
 * The rule field selector of the compiled side - source or destination
 * networks - is stored in the scratch, so one body serves both sides;
 * the grouping callbacks bake their side in and need no scratch.
 */
struct classify_compile_net6 {
	classify_net6_get_net6s_func get_net6s;
	const char *name;

	struct range_index ri_hi;
	struct range_index ri_lo;

	// The region of every high region row without a low half
	// distinction, the marked join table row of any other. The line dies
	// at commit, the resolved values move into the high half trie.
	struct vline rows;

	struct hash_index net_index;
	struct net6 *nets;
	uint32_t net_count;

	struct value_registry net_registry;
	uint32_t *region_values;
	uint32_t region_count;

	// The join table of the combined regions of the split rows; it moves
	// into the embedded attribute at commit.
	struct value_table comb;
};

typedef void (*net6_get_part_func)(
	struct net6 *net, uint8_t **addr, uint8_t **mask
);

static inline void
net6_get_hi_part(struct net6 *net, uint8_t **addr, uint8_t **mask) {
	*addr = net->addr;
	*mask = net->mask;
}

static inline void
net6_get_lo_part(struct net6 *net, uint8_t **addr, uint8_t **mask) {
	*addr = net->addr + 8;
	*mask = net->mask + 8;
}

static inline void
net6_normalize(const struct net6 *src, struct net6 *dst) {
	memcpy(dst->addr, src->addr, NET6_LEN);
	memcpy(dst->mask, src->mask, NET6_LEN);
	for (uint8_t idx = 0; idx < NET6_LEN; ++idx) {
		dst->addr[idx] &= src->mask[idx];
	}
}

static inline int
net6_is_any(const struct net6 *net) {
	// The halves read through the byte aligned view: the mask array
	// carries no integer alignment of its own.
	const net6_half *mask = (const net6_half *)net->mask;
	return mask[0] == 0 && mask[1] == 0;
}

// A list without networks, or holding a network without a mask, covers
// the whole address domain.
static inline int
net6s_is_any(const struct filter_net6s *nets) {
	if (nets->count == 0) {
		return 1;
	}
	for (uint32_t idx = 0; idx < nets->count; ++idx) {
		if (net6_is_any(nets->items + idx)) {
			return 1;
		}
	}
	return 0;
}

static inline uint32_t
net6_hash(const struct net6 *net) {
	uint32_t hash = 0;
	for (uint32_t idx = 0; idx < NET6_LEN; ++idx) {
		hash = hash * 31 + net->addr[idx];
	}
	for (uint32_t idx = 0; idx < NET6_LEN; ++idx) {
		hash = hash * 31 + net->mask[idx];
	}
	return hash;
}

struct filter_net6_group_ctx {
	struct classify_compile_net6 *net6_compile;
	const struct net6 *net;
};

static inline int
filter_net6_group_eq(uint32_t value, const void *data) {
	const struct filter_net6_group_ctx *group_ctx = data;
	const struct net6 *group_net = group_ctx->net6_compile->nets + value;

	return memcmp(group_net->addr, group_ctx->net->addr, NET6_LEN) != 0 ||
	       memcmp(group_net->mask, group_ctx->net->mask, NET6_LEN) != 0;
}

static inline int
filter_net6_nets_append(
	struct memory_context *memory_context,
	struct classify_compile_net6 *net6_compile,
	const struct net6 *net
) {
	uint64_t count = net6_compile->net_count;
	if (mem_array_expand_exp(
		    memory_context,
		    (void **)&net6_compile->nets,
		    sizeof(*net),
		    &count
	    )) {
		return -1;
	}

	net6_compile->nets[count - 1] = *net;
	net6_compile->net_count = count;

	return 0;
}

static inline int
filter_net6_build_part(
	struct memory_context *memory_context,
	struct net6 *nets,
	uint32_t net_count,
	net6_get_part_func get_part,
	struct range_index *ri
) {
	struct range_collector collector;
	if (range_collector_init(&collector, memory_context)) {
		return -1;
	}

	for (uint32_t net_idx = 0; net_idx < net_count; ++net_idx) {
		uint8_t *addr;
		uint8_t *mask;
		get_part(nets + net_idx, &addr, &mask);

		if (range8_collector_add(
			    &collector,
			    addr,
			    __builtin_popcountll(*(net6_half *)mask)
		    )) {
			goto error;
		}
	}

	if (range_index_init(ri, memory_context)) {
		goto error;
	}

	if (range_collector_collect(&collector, 8, ri)) {
		goto error_range_index;
	}

	range_collector_free(&collector, 8);

	return 0;

error_range_index:
	range_index_free(ri);

error:
	range_collector_free(&collector, 8);

	return -1;
}

/*
 * Resolves the half region index range of a network half: the indexes of
 * every region of the half the network interval covers.
 */
static inline void
filter_net6_part_bounds(
	const struct range_index *ri,
	const uint8_t *part_addr,
	const uint8_t *part_mask,
	uint32_t *start,
	uint32_t *stop
) {
	uint8_t from[8];
	uint8_t to[8];
	for (uint32_t idx = 0; idx < 8; ++idx) {
		from[idx] = part_addr[idx];
		to[idx] = part_addr[idx] | ~part_mask[idx];
	}
	filter_key_inc(8, to);

	*start = radix_lookup(&ri->radix, 8, from);
	*stop = radix_lookup(&ri->radix, 8, to);
	if (*stop == 0) {
		/*
		 * The only chance get zero here is for the last one
		 * item.
		 */
		*stop = ri->count;
	}
}

// Resolves both half region index ranges of a normalized network at
// once.
static inline void
filter_net6_net_bounds(
	struct classify_compile_net6 *net6_compile,
	const struct net6 *net,
	uint32_t bounds[4]
) {
	filter_net6_part_bounds(
		&net6_compile->ri_hi,
		net->addr,
		net->mask,
		&bounds[0],
		&bounds[1]
	);
	filter_net6_part_bounds(
		&net6_compile->ri_lo,
		net->addr + 8,
		net->mask + 8,
		&bounds[2],
		&bounds[3]
	);
}

#define FILTER_NET6_NONE 0xffffffffu
// The class store grows by chunks that never move; the tests narrow them.
#ifndef FILTER_NET6_CLASS_CHUNK_BITS
#define FILTER_NET6_CLASS_CHUNK_BITS 14
#endif
#define FILTER_NET6_CLASS_CHUNK (1u << FILTER_NET6_CLASS_CHUNK_BITS)

// A masked network: its half region ordinal bounds, its network index
// and the nearest network of its high interval enclosing its low one.
struct filter_net6_tree_net {
	uint32_t bounds[4];
	uint32_t net;
	uint32_t lo_parent;
};

// An entered high interval: its networks and the first class it made.
struct filter_net6_level {
	uint32_t first;
	uint32_t end;
	uint32_t first_class;
};

/*
 * A class is a set of networks covering a cell: the networks of its
 * parent class plus a tree network and the networks of its high interval
 * enclosing it.
 */
struct filter_net6_class {
	uint32_t parent;
	uint32_t net;
	// Assigned when the sweep first writes the class.
	uint32_t region;
	// The class the parent moved into under the network applied last.
	uint32_t child;
};

/*
 * Scratch of the combined region sweep.
 *
 * Class 0 is the empty set. While no entered network has a partial low
 * half every low region shares the uniform class; otherwise the vector
 * holds the class of every low region, and only the ones under the
 * levels entered since it was filled may hold another class than the
 * uniform.
 */
struct filter_net6_sweep {
	struct memory_context *memory_context;
	struct classify_compile_net6 *compile;
	const uint32_t *values_lo;
	uint32_t h_dim;

	// Sorted with the enclosing intervals first.
	struct filter_net6_tree_net *nets;
	uint32_t net_count;
	// The entered high intervals: nested prefixes differ in length, so
	// at most one per length from 0 to 64.
	struct filter_net6_level levels[65];
	uint32_t depth;

	uint32_t uniform;
	// The level that filled the vector, none while it is dead.
	uint32_t vector_level;
	uint32_t *vector;
	// Low ordinals under the levels from the vector one.
	uint32_t moved;

	struct filter_net6_class **chunks;
	uint64_t chunk_count;
	uint32_t class_count;
};

static inline struct filter_net6_class *
filter_net6_class_at(const struct filter_net6_sweep *sw, uint32_t cls) {
	return sw->chunks[cls >> FILTER_NET6_CLASS_CHUNK_BITS] +
	       (cls & (FILTER_NET6_CLASS_CHUNK - 1));
}

// Adds the class of a parent class plus a network, or returns none.
static inline uint32_t
filter_net6_new_class(
	struct filter_net6_sweep *sw, uint32_t parent, uint32_t net
) {
	// A row line value marks a table row above the regions, so the
	// classes stay below the mark.
	uint32_t cls = sw->class_count;
	if (cls == FILTER_NET6_ROW_MARK) {
		return FILTER_NET6_NONE;
	}
	if (cls % FILTER_NET6_CLASS_CHUNK == 0) {
		struct filter_net6_class *chunk = memory_balloc(
			sw->memory_context,
			FILTER_NET6_CLASS_CHUNK * sizeof(*chunk)
		);
		if (chunk == NULL) {
			return FILTER_NET6_NONE;
		}
		if (mem_array_expand_exp(
			    sw->memory_context,
			    (void **)&sw->chunks,
			    sizeof(*sw->chunks),
			    &sw->chunk_count
		    )) {
			memory_bfree(
				sw->memory_context,
				chunk,
				FILTER_NET6_CLASS_CHUNK * sizeof(*chunk)
			);
			return FILTER_NET6_NONE;
		}
		sw->chunks[sw->chunk_count - 1] = chunk;
	}
	++sw->class_count;
	*filter_net6_class_at(sw, cls) =
		(struct filter_net6_class){parent, net, FILTER_NET6_NONE, 0};
	return cls;
}

static inline int
filter_net6_same_hi(
	const struct filter_net6_tree_net *a,
	const struct filter_net6_tree_net *b
) {
	return a->bounds[0] == b->bounds[0] && a->bounds[1] == b->bounds[1];
}

static inline int
filter_net6_partial(
	const struct filter_net6_sweep *sw,
	const struct filter_net6_tree_net *net
) {
	return net->bounds[2] != 0 ||
	       net->bounds[3] != sw->compile->ri_lo.count;
}

/*
 * Moves the low regions of a range into the classes of a network of the
 * level entered last.
 *
 * A low region repeats only in the gaps around a nested low interval,
 * where the innermost network is the same, so a region the level already
 * moved keeps its class.
 */
static inline int
filter_net6_sweep_move(
	struct filter_net6_sweep *sw,
	uint32_t net,
	uint32_t from,
	uint32_t to,
	uint32_t first_class
) {
	for (uint32_t idx_lo = from; idx_lo < to; ++idx_lo) {
		uint32_t *cls = sw->vector + sw->values_lo[idx_lo];
		if (*cls >= first_class) {
			continue;
		}
		uint32_t child = filter_net6_class_at(sw, *cls)->child;
		if (filter_net6_class_at(sw, child)->net != net) {
			child = filter_net6_new_class(sw, *cls, net);
			if (child == FILTER_NET6_NONE) {
				return -1;
			}
			filter_net6_class_at(sw, *cls)->child = child;
		}
		*cls = child;
	}
	return 0;
}

/*
 * Enters the high interval of the networks from the given one: every low
 * region under them moves into a class of its own for the level, the
 * class of the innermost network over it.
 *
 * The networks nested in one follow it, so a network moves the gaps
 * between the networks directly nested in it.
 */
static inline int
filter_net6_sweep_push(struct filter_net6_sweep *sw, uint32_t first) {
	// Only a mask half that is not a prefix nests deeper.
	if (sw->depth == sizeof(sw->levels) / sizeof(sw->levels[0])) {
		return -1;
	}
	const struct filter_net6_tree_net *nets = sw->nets;
	struct filter_net6_level *level = sw->levels + sw->depth;
	*level = (struct filter_net6_level){
		.first = first,
		.end = first,
		.first_class = sw->class_count,
	};
	int partial = 0;
	while (level->end < sw->net_count &&
	       filter_net6_same_hi(nets + first, nets + level->end)) {
		partial |= filter_net6_partial(sw, nets + level->end++);
	}
	if (sw->vector_level == FILTER_NET6_NONE && partial) {
		for (uint32_t col = 0; col < sw->h_dim; ++col) {
			sw->vector[col] = sw->uniform;
		}
		sw->vector_level = sw->depth;
		sw->moved = 0;
	}
	++sw->depth;
	if (sw->vector_level == FILTER_NET6_NONE) {
		// The only network of the level covers the whole low half.
		sw->uniform = filter_net6_new_class(sw, sw->uniform, first);
		return sw->uniform == FILTER_NET6_NONE ? -1 : 0;
	}

	for (uint32_t net = first; net < level->end; ++net) {
		const uint32_t *bounds = nets[net].bounds;
		if (nets[net].lo_parent == FILTER_NET6_NONE) {
			sw->moved += bounds[3] - bounds[2];
		}
		uint32_t from = bounds[2];
		for (uint32_t child = net + 1; from < bounds[3]; ++child) {
			// The network end closes the gap after the last child.
			uint32_t to = bounds[3];
			uint32_t next = bounds[3];
			if (child < level->end &&
			    nets[child].bounds[2] < bounds[3]) {
				if (nets[child].lo_parent != net) {
					continue;
				}
				to = nets[child].bounds[2];
				next = nets[child].bounds[3];
			}
			if (filter_net6_sweep_move(
				    sw, net, from, to, level->first_class
			    )) {
				return -1;
			}
			from = next;
		}
	}
	return 0;
}

// Leaves the innermost level: every low region it moved returns to the
// parent of its class.
static inline void
filter_net6_sweep_pop(struct filter_net6_sweep *sw) {
	const struct filter_net6_level *level = sw->levels + --sw->depth;
	if (sw->vector_level == FILTER_NET6_NONE) {
		sw->uniform = filter_net6_class_at(sw, sw->uniform)->parent;
		return;
	}
	if (sw->vector_level == sw->depth) {
		// The vector dies with the level that filled it.
		sw->vector_level = FILTER_NET6_NONE;
		return;
	}

	for (uint32_t net = level->first; net < level->end; ++net) {
		const uint32_t *bounds = sw->nets[net].bounds;
		if (sw->nets[net].lo_parent != FILTER_NET6_NONE) {
			continue;
		}
		sw->moved -= bounds[3] - bounds[2];
		for (uint32_t idx_lo = bounds[2]; idx_lo < bounds[3];
		     ++idx_lo) {
			uint32_t *cls = sw->vector + sw->values_lo[idx_lo];
			if (*cls >= level->first_class) {
				*cls = filter_net6_class_at(sw, *cls)->parent;
			}
		}
	}
}

// Numbers a class the first time the sweep writes it.
static inline uint32_t
filter_net6_region(struct filter_net6_sweep *sw, uint32_t cls) {
	uint32_t *region = &filter_net6_class_at(sw, cls)->region;
	if (*region == FILTER_NET6_NONE) {
		*region = sw->compile->region_count++;
	}
	return *region;
}

/*
 * Writes the regions of a split row into its zeroed table row.
 *
 * A row with few low regions under the levels from the vector one writes
 * the uniform region over the rest, and only when it is not the empty set
 * one, then the regions under those levels.
 */
static inline void
filter_net6_sweep_write_row(struct filter_net6_sweep *sw, uint32_t *cells) {
	if ((uint64_t)sw->moved * 8 >= sw->h_dim) {
		for (uint32_t col = 0; col < sw->h_dim; ++col) {
			cells[col] = filter_net6_region(sw, sw->vector[col]);
		}
		return;
	}

	uint32_t uniform = filter_net6_region(sw, sw->uniform);
	for (uint32_t col = 0; uniform != 0 && col < sw->h_dim; ++col) {
		cells[col] = uniform;
	}
	for (uint32_t depth = sw->depth; depth-- > sw->vector_level;) {
		const struct filter_net6_level *level = sw->levels + depth;
		for (uint32_t net = level->first; net < level->end; ++net) {
			const uint32_t *bounds = sw->nets[net].bounds;
			if (sw->nets[net].lo_parent != FILTER_NET6_NONE) {
				continue;
			}
			for (uint32_t idx_lo = bounds[2]; idx_lo < bounds[3];
			     ++idx_lo) {
				uint32_t col = sw->values_lo[idx_lo];
				cells[col] =
					filter_net6_region(sw, sw->vector[col]);
			}
		}
	}
}

static inline int
filter_net6_tree_net_cmp(const void *first, const void *second) {
	const struct filter_net6_tree_net *a = first;
	const struct filter_net6_tree_net *b = second;

	// High start ascending, high end descending, then the same for the
	// low half: enclosing intervals precede the ones they enclose.
	static const int8_t order[4] = {1, -1, 1, -1};
	for (uint32_t idx = 0; idx < 4; ++idx) {
		if (a->bounds[idx] != b->bounds[idx]) {
			return a->bounds[idx] < b->bounds[idx] ? -order[idx]
							       : order[idx];
		}
	}
	return 0;
}

static inline void
filter_net6_sweep_free(struct filter_net6_sweep *sw) {
	struct memory_context *memory_context = sw->memory_context;
	struct classify_compile_net6 *net6_compile = sw->compile;

	memory_bfree(
		memory_context,
		sw->nets,
		(net6_compile->net_count + 1) * sizeof(*sw->nets)
	);
	memory_bfree(memory_context, sw->vector, sw->h_dim * sizeof(uint32_t));
	for (uint64_t idx = 0; idx < sw->chunk_count; ++idx) {
		memory_bfree(
			memory_context,
			sw->chunks[idx],
			FILTER_NET6_CLASS_CHUNK *
				sizeof(struct filter_net6_class)
		);
	}
	mem_array_free_exp(
		memory_context, sw->chunks, sizeof(*sw->chunks), sw->chunk_count
	);
}

// Sorts the masked networks and links their low intervals.
static inline int
filter_net6_sweep_init(
	struct filter_net6_sweep *sw,
	struct memory_context *memory_context,
	struct classify_compile_net6 *net6_compile
) {
	memset(sw, 0, sizeof(*sw));
	sw->memory_context = memory_context;
	sw->compile = net6_compile;
	sw->values_lo = ADDR_OF(&net6_compile->ri_lo.values);
	sw->h_dim = net6_compile->ri_lo.max_value + 1;
	sw->vector_level = FILTER_NET6_NONE;
	net6_compile->region_count = 1;

	sw->nets = memory_balloc(
		memory_context,
		(net6_compile->net_count + 1) * sizeof(*sw->nets)
	);
	sw->vector =
		memory_balloc(memory_context, sw->h_dim * sizeof(uint32_t));
	if (sw->nets == NULL || sw->vector == NULL ||
	    filter_net6_new_class(sw, FILTER_NET6_NONE, FILTER_NET6_NONE) ==
		    FILTER_NET6_NONE) {
		return -1;
	}
	filter_net6_class_at(sw, 0)->region = 0;

	struct filter_net6_tree_net *nets = sw->nets;
	for (uint32_t net_idx = 0; net_idx < net6_compile->net_count;
	     ++net_idx) {
		const struct net6 *net = net6_compile->nets + net_idx;
		if (net6_is_any(net)) {
			continue;
		}
		struct filter_net6_tree_net *tree_net = nets + sw->net_count++;
		filter_net6_net_bounds(net6_compile, net, tree_net->bounds);
		tree_net->net = net_idx;
	}
	qsort(nets, sw->net_count, sizeof(*nets), filter_net6_tree_net_cmp);

	// The nearest low interval enclosing a network of a high interval is
	// the previous network or one of the networks enclosing it.
	for (uint32_t idx = 0; idx < sw->net_count; ++idx) {
		uint32_t parent = idx - 1;
		if (idx == 0 ||
		    !filter_net6_same_hi(nets + parent, nets + idx)) {
			parent = FILTER_NET6_NONE;
		}
		while (parent != FILTER_NET6_NONE &&
		       nets[parent].bounds[3] <= nets[idx].bounds[2]) {
			parent = nets[parent].lo_parent;
		}
		nets[idx].lo_parent = parent;
	}
	return 0;
}

/*
 * Marks the line value of every row under a high interval with a partial
 * low half as pending a table row and counts the rows; the intervals
 * nest, so every ordinal is marked once.
 */
static inline uint32_t
filter_net6_sweep_mark_split(struct filter_net6_sweep *sw) {
	struct classify_compile_net6 *net6_compile = sw->compile;
	const uint32_t *values_hi = ADDR_OF(&net6_compile->ri_hi.values);

	uint32_t count = 0;
	uint32_t end = 0;
	for (uint32_t net = 0; net < sw->net_count; ++net) {
		const uint32_t *bounds = sw->nets[net].bounds;
		if (!filter_net6_partial(sw, sw->nets + net) ||
		    bounds[0] < end) {
			continue;
		}
		for (uint32_t idx_hi = bounds[0]; idx_hi < bounds[1];
		     ++idx_hi) {
			uint32_t *row = vline_get_ptr(
				&net6_compile->rows, values_hi[idx_hi]
			);
			count += *row != FILTER_NET6_NONE;
			*row = FILTER_NET6_NONE;
		}
		end = bounds[1];
	}
	return count;
}

/*
 * Sweeps the high ordinals over the nested high intervals and writes the
 * line value of every row: a pending row once into the next table row,
 * any other into its region.
 */
static inline int
filter_net6_sweep_rows(struct filter_net6_sweep *sw) {
	struct classify_compile_net6 *net6_compile = sw->compile;
	const uint32_t *values_hi = ADDR_OF(&net6_compile->ri_hi.values);
	const struct filter_net6_tree_net *nets = sw->nets;

	uint32_t next = 0;
	uint32_t table_row = 0;
	for (uint32_t idx_hi = 0; idx_hi < net6_compile->ri_hi.count;
	     ++idx_hi) {
		while (sw->depth != 0 &&
		       nets[sw->levels[sw->depth - 1].first].bounds[1] <= idx_hi
		) {
			filter_net6_sweep_pop(sw);
		}
		while (next < sw->net_count && nets[next].bounds[0] == idx_hi) {
			if (filter_net6_sweep_push(sw, next)) {
				return -1;
			}
			next = sw->levels[sw->depth - 1].end;
		}

		uint32_t *row =
			vline_get_ptr(&net6_compile->rows, values_hi[idx_hi]);
		if (*row == FILTER_NET6_NONE) {
			*row = FILTER_NET6_ROW_MARK | table_row;
			filter_net6_sweep_write_row(
				sw,
				value_table_get_ptr(
					&net6_compile->comb, table_row++, 0
				)
			);
		} else if (!(*row & FILTER_NET6_ROW_MARK)) {
			*row = filter_net6_region(sw, sw->uniform);
		}
	}
	return 0;
}

// Fills the registry range of every masked network with the regions of
// the classes holding it.
static inline int
filter_net6_sweep_assign(struct filter_net6_sweep *sw, const char *name) {
	struct classify_compile_net6 *net6_compile = sw->compile;

	char nets_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(nets_name, sizeof(nets_name), name, "nets");
	struct value_registry *registry = &net6_compile->net_registry;
	if (value_registry_init(registry, sw->memory_context, nets_name)) {
		return -1;
	}
	for (uint32_t net_idx = 0; net_idx < net6_compile->net_count;
	     ++net_idx) {
		if (value_registry_start(registry)) {
			return -1;
		}
	}
	struct value_range *ranges = ADDR_OF(&registry->ranges);
	struct memory_context *registry_context =
		ADDR_OF(&registry->memory_context);

	for (uint32_t first = 1; first < sw->class_count; ++first) {
		uint32_t region = filter_net6_class_at(sw, first)->region;
		for (uint32_t cls = first;
		     region != FILTER_NET6_NONE && cls != 0;
		     cls = filter_net6_class_at(sw, cls)->parent) {
			for (uint32_t net = filter_net6_class_at(sw, cls)->net;
			     net != FILTER_NET6_NONE;
			     net = sw->nets[net].lo_parent) {
				if (value_range_append(
					    registry_context,
					    ranges + sw->nets[net].net,
					    region
				    )) {
					return -1;
				}
			}
		}
	}
	registry->max_value = net6_compile->region_count - 1;
	return 0;
}

// Fills the rows line, the join table, the network registry and the
// region count of the compile.
static inline int
filter_net6_build_regions(
	struct memory_context *memory_context,
	struct classify_compile_net6 *net6_compile,
	const char *name
) {
	int rc = -1;
	struct filter_net6_sweep sw;
	if (filter_net6_sweep_init(&sw, memory_context, net6_compile)) {
		goto out;
	}

	uint32_t split_count = filter_net6_sweep_mark_split(&sw);
	if (split_count != 0) {
		char comb_name[MEMORY_CONTEXT_NAME_SIZE];
		classify_leaf_name(comb_name, sizeof(comb_name), name, "comb");
		if (value_table_init(
			    &net6_compile->comb,
			    memory_context,
			    comb_name,
			    split_count,
			    sw.h_dim
		    )) {
			goto out;
		}
	}
	if (filter_net6_sweep_rows(&sw) ||
	    filter_net6_sweep_assign(&sw, name)) {
		goto out;
	}
	rc = 0;

out:
	filter_net6_sweep_free(&sw);
	return rc;
}

// Resolves the network of a rule into its registry range of assigned
// region values through the network hash index.
static inline int
filter_net6_net_range(
	struct classify_compile_net6 *net6_compile,
	const struct net6 *net,
	const struct value_range **range
) {
	struct filter_net6_group_ctx group_ctx = {
		net6_compile,
		net,
	};
	uint32_t group = hash_index_lookup(
		&net6_compile->net_index,
		net6_hash(net),
		filter_net6_group_eq,
		&group_ctx
	);
	if (group == HASH_INDEX_INVALID) {
		return -1;
	}

	const struct value_range *ranges =
		ADDR_OF(&net6_compile->net_registry.ranges);
	*range = ranges + group;
	return 0;
}

static inline struct classify_compile_net6 *
classify_compile_net6_create(
	struct memory_context *memory_context,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_net6_get_net6s_func get_net6s,
	const char *name
) {
	struct classify_compile_net6 *compile =
		(struct classify_compile_net6 *)memory_balloc(
			memory_context, sizeof(struct classify_compile_net6)
		);
	if (compile == NULL) {
		return NULL;
	}
	memset(compile, 0, sizeof(struct classify_compile_net6));

	compile->get_net6s = get_net6s;
	compile->name = name;

	/*
	 * Collect the unique normalized networks of the ruleset: the first
	 * occurrence extends the network array, the hash index holds the
	 * probe table over the network indexes.
	 */
	uint32_t net_total = 0;
	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct classifier_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		struct filter_net6s net6s;
		compile->get_net6s(rule, &net6s);
		net_total += net6s.count;
	}

	if (hash_index_init(&compile->net_index, memory_context, net_total)) {
		goto error_free;
	}

	for (uint32_t rule_idx = 0; rule_idx < rule_count; ++rule_idx) {
		const struct classifier_rule *rule = rules[rule_idx];
		if (rule == NULL) {
			continue;
		}

		struct filter_net6s net6s;
		compile->get_net6s(rule, &net6s);

		for (uint32_t net_idx = 0; net_idx < net6s.count; ++net_idx) {
			struct net6 net6;
			net6_normalize(net6s.items + net_idx, &net6);

			struct filter_net6_group_ctx group_ctx = {
				compile,
				&net6,
			};
			uint32_t hash = net6_hash(&net6);
			uint32_t group = hash_index_lookup(
				&compile->net_index,
				hash,
				filter_net6_group_eq,
				&group_ctx
			);
			if (group == HASH_INDEX_INVALID) {
				if (hash_index_insert(
					    &compile->net_index,
					    hash,
					    compile->net_count
				    )) {
					goto error_free_nets;
				}
				if (filter_net6_nets_append(
					    memory_context, compile, &net6
				    )) {
					goto error_free_nets;
				}
			}
		}
	}

	/*
	 * Both halves are partitioned on their own over the unique
	 * networks, and the longest prefix match of every half resolves a
	 * packet address half into its half region value.
	 */
	if (filter_net6_build_part(
		    memory_context,
		    compile->nets,
		    compile->net_count,
		    net6_get_hi_part,
		    &compile->ri_hi
	    )) {
		goto error_free_nets;
	}

	if (filter_net6_build_part(
		    memory_context,
		    compile->nets,
		    compile->net_count,
		    net6_get_lo_part,
		    &compile->ri_lo
	    )) {
		goto error_free_part_hi;
	}

	/*
	 * The join table carries the combined regions of the high region
	 * rows with a low half distinction; the rows line carries the
	 * region of every other row, or the table row of a split one.
	 */
	char rows_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(rows_name, sizeof(rows_name), name, "rows");
	if (vline_init(
		    &compile->rows,
		    memory_context,
		    rows_name,
		    compile->ri_hi.max_value + 1
	    )) {
		goto error_free_parts;
	}

	if (filter_net6_build_regions(memory_context, compile, name)) {
		goto error_free_net_registry;
	}

	{
		struct value_range *ranges =
			ADDR_OF(&compile->net_registry.ranges);
		struct memory_context *registry_context =
			ADDR_OF(&compile->net_registry.memory_context);

		for (uint32_t net_idx = 0; net_idx < compile->net_count;
		     ++net_idx) {
			if (!net6_is_any(compile->nets + net_idx)) {
				continue;
			}

			for (uint32_t region_idx = 0;
			     region_idx < compile->region_count;
			     ++region_idx) {
				if (value_range_append(
					    registry_context,
					    ranges + net_idx,
					    region_idx
				    )) {
					goto error_free_net_registry;
				}
			}
		}
	}

	compile->region_values = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * compile->region_count
	);
	if (compile->region_values == NULL) {
		goto error_free_net_registry;
	}
	memset(compile->region_values,
	       0,
	       sizeof(uint32_t) * compile->region_count);

	return compile;

error_free_net_registry:
	if (compile->region_values != NULL) {
		memory_bfree(
			memory_context,
			compile->region_values,
			sizeof(uint32_t) * compile->region_count
		);
	}
	value_registry_fini(&compile->net_registry);
	vline_free(&compile->rows);

error_free_parts:
	value_table_free(&compile->comb);
	range_index_free(&compile->ri_lo);

error_free_part_hi:
	range_index_free(&compile->ri_hi);

error_free_nets:
	mem_array_free_exp(
		memory_context,
		compile->nets,
		sizeof(struct net6),
		compile->net_count
	);
	hash_index_fini(&compile->net_index);

error_free:
	memory_bfree(
		memory_context, compile, sizeof(struct classify_compile_net6)
	);

	return NULL;
}

static inline uint32_t
classify_compile_net6_size(const void *compile) {
	const struct classify_compile_net6 *net6_compile = compile;

	return net6_compile->region_count;
}

static inline int
classify_compile_net6_iter(
	void *compile,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_net6 *net6_compile = compile;

	for (uint32_t idx = 0; idx < net6_compile->region_count; ++idx) {
		if (iter_cb_func(
			    net6_compile->region_values + idx, cb_func_data
		    ) < 0) {
			return -1;
		}
	}

	return 0;
}

static inline int
classify_compile_net6_rule_is_any(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_net6 *net6_compile = compile;

	struct filter_net6s nets;
	net6_compile->get_net6s(rule, &nets);

	return net6s_is_any(&nets);
}

static inline int
classify_compile_net6_rule_iter(
	void *compile,
	const struct classifier_rule *rule,
	int (*iter_cb_func)(uint32_t *value, void *data),
	void *cb_func_data
) {
	struct classify_compile_net6 *net6_compile = compile;

	/*
	 * Every network of the rule resolves through the net index into
	 * its range of combined regions, so the iteration replays the
	 * values assigned to the networks of the list instead of walking
	 * the crosses again. A list without networks covers the whole
	 * domain and takes every region directly.
	 */
	struct filter_net6s nets;
	net6_compile->get_net6s(rule, &nets);

	if (nets.count == 0) {
		for (uint32_t idx = 0; idx < net6_compile->region_count;
		     ++idx) {
			if (iter_cb_func(
				    net6_compile->region_values + idx,
				    cb_func_data
			    ) < 0) {
				return -1;
			}
		}
		return 0;
	}

	for (uint32_t net_idx = 0; net_idx < nets.count; ++net_idx) {
		struct net6 net6;
		net6_normalize(nets.items + net_idx, &net6);

		const struct value_range *range;
		if (filter_net6_net_range(net6_compile, &net6, &range)) {
			return -1;
		}

		const uint32_t *values = ADDR_OF(&range->values);
		for (uint32_t idx = 0; idx < range->count; ++idx) {
			if (iter_cb_func(
				    net6_compile->region_values + values[idx],
				    cb_func_data
			    ) < 0) {
				return -1;
			}
		}
	}

	return 0;
}

static inline void
classify_compile_net6_free(
	struct memory_context *memory_context, void *compile
) {
	struct classify_compile_net6 *net6_compile = compile;

	range_index_free(&net6_compile->ri_hi);
	range_index_free(&net6_compile->ri_lo);

	if (net6_compile->region_values != NULL) {
		memory_bfree(
			memory_context,
			net6_compile->region_values,
			sizeof(uint32_t) * net6_compile->region_count
		);
	}

	value_registry_fini(&net6_compile->net_registry);

	mem_array_free_exp(
		memory_context,
		net6_compile->nets,
		sizeof(struct net6),
		net6_compile->net_count
	);
	hash_index_fini(&net6_compile->net_index);

	// The rows line is compile side scratch: it is released here in
	// every path, including the commit one.
	vline_free(&net6_compile->rows);

	// The join table is detached by a successful commit, so this
	// releases it only while it still belongs to the scratch.
	value_table_free(&net6_compile->comb);

	memory_bfree(
		memory_context,
		net6_compile,
		sizeof(struct classify_compile_net6)
	);
}

static inline int
classify_compile_net6_commit(
	struct memory_context *memory_context, void *compile, void *attr
) {
	struct classify_compile_net6 *net6_compile = compile;
	struct classify_attr_net6 *net6_attr = attr;

	// The attribute starts empty, so the common unwind frees only the
	// members a failed step already initialised.
	memset(net6_attr, 0, sizeof(*net6_attr));

	/*
	 * The half matches are built directly in the embedded attribute:
	 * their pages are addressed through relative pointers, so trees
	 * built elsewhere cannot move in.
	 *
	 * The high half trie stays empty until its values are resolved
	 * below.
	 */
	char hi_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(hi_name, sizeof(hi_name), net6_compile->name, "hi");
	if (lpm_init(&net6_attr->hi, memory_context, hi_name)) {
		goto error;
	}
	char lo_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(lo_name, sizeof(lo_name), net6_compile->name, "lo");
	if (lpm_init(&net6_attr->lo, memory_context, lo_name)) {
		goto error;
	}
	if (range_index_build_lpm(&net6_compile->ri_lo, 8, &net6_attr->lo)) {
		goto error;
	}

	/*
	 * The compile stages assign the final class values to the combined
	 * regions; the cells and the whole row values still hold the
	 * region identifiers. A row without any low half distinction
	 * resolves into its final class carried by the high half trie
	 * value itself, so the lookup finishes after the high half walk;
	 * the rows of a low half distinction keep their join table rows
	 * marked in the trie values, and their cells resolve in place.
	 */
	struct value_table *comb = &net6_compile->comb;

	// The high half trie build walks the partition by region ordinal,
	// so the resolved values are indexed by ordinal and reach the row
	// of every ordinal through the region values of the partition.
	const uint32_t *hi_values = ADDR_OF(&net6_compile->ri_hi.values);
	uint32_t hi_count = net6_compile->ri_hi.count;

	uint32_t *lpm_values = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * (hi_count ? hi_count : 1)
	);
	if (lpm_values == NULL) {
		goto error;
	}
	for (uint32_t idx = 0; idx < hi_count; ++idx) {
		uint32_t value = vline_get(&net6_compile->rows, hi_values[idx]);
		lpm_values[idx] = value & FILTER_NET6_ROW_MARK
					  ? value
					  : net6_compile->region_values[value];
	}

	for (uint32_t row = 0; row < comb->v_dim; ++row) {
		uint32_t *cells = value_table_get_ptr(comb, row, 0);
		for (uint32_t h_idx = 0; h_idx < comb->h_dim; ++h_idx) {
			cells[h_idx] =
				net6_compile->region_values[cells[h_idx]];
		}
	}

	int hi_rc = range_index_build_lpm_values(
		&net6_compile->ri_hi, 8, lpm_values, &net6_attr->hi
	);

	memory_bfree(
		memory_context,
		lpm_values,
		sizeof(uint32_t) * (hi_count ? hi_count : 1)
	);

	if (hi_rc) {
		goto error;
	}

	/*
	 * The join table moves into the embedded attribute field by field:
	 * it carries relative pointers that a struct copy would strand.
	 */
	net6_attr->comb.v_dim = comb->v_dim;
	net6_attr->comb.h_dim = comb->h_dim;
	SET_OFFSET_OF(&net6_attr->comb.values, ADDR_OF(&comb->values));
	SET_OFFSET_OF(
		&net6_attr->comb.memory_context, ADDR_OF(&comb->memory_context)
	);

	// The table internals now belong to the attribute: the scratch
	// copy is detached, so its release below leaves them alive.
	memset(comb, 0, sizeof(*comb));

	classify_compile_net6_free(memory_context, compile);

	return 0;

error:
	lpm_free(&net6_attr->hi);
	lpm_free(&net6_attr->lo);
	value_table_free(&net6_attr->comb);
	memset(net6_attr, 0, sizeof(*net6_attr));
	return -1;
}

static inline uint32_t
classify_compile_net6_hash(
	const void *compile, const struct classifier_rule *rule
) {
	const struct classify_compile_net6 *net6_compile = compile;

	struct filter_net6s nets;
	net6_compile->get_net6s(rule, &nets);

	uint32_t hash = nets.count;
	for (uint32_t idx = 0; idx < nets.count; ++idx) {
		const struct net6 *net = nets.items + idx;
		for (uint32_t byte_idx = 0; byte_idx < NET6_LEN; ++byte_idx) {
			hash = hash * 31 +
			       (net->addr[byte_idx] & net->mask[byte_idx]);
		}
		for (uint32_t byte_idx = 0; byte_idx < NET6_LEN; ++byte_idx) {
			hash = hash * 31 + net->mask[byte_idx];
		}
	}
	return hash;
}

static inline int
classify_compile_net6_compare(
	const void *compile,
	const struct classifier_rule *first,
	const struct classifier_rule *second
) {
	const struct classify_compile_net6 *net6_compile = compile;

	struct filter_net6s first_nets;
	struct filter_net6s second_nets;
	net6_compile->get_net6s(first, &first_nets);
	net6_compile->get_net6s(second, &second_nets);

	if (first_nets.count != second_nets.count) {
		return 1;
	}

	for (uint32_t idx = 0; idx < first_nets.count; ++idx) {
		const struct net6 *first_net = first_nets.items + idx;
		const struct net6 *second_net = second_nets.items + idx;

		for (uint32_t byte_idx = 0; byte_idx < NET6_LEN; ++byte_idx) {
			if ((first_net->addr[byte_idx] &
			     first_net->mask[byte_idx]) !=
			    (second_net->addr[byte_idx] &
			     second_net->mask[byte_idx])) {
				return 1;
			}
			if (first_net->mask[byte_idx] !=
			    second_net->mask[byte_idx]) {
				return 1;
			}
		}
	}

	return 0;
}

/*
 * Compiles the IPv6 network classifier of a ruleset into an embedded
 * attribute.
 *
 * The name scopes the memory contexts of the compile - the attribute
 * half matches, the join table and the stage registry - each titled
 * with the name and its own artifact leaf, in the memory tree.
 *
 * On success the attribute and the stage - the registry with the rule
 * group row - are owned by the caller, released through
 * classifier_fini; on failure every partial state is freed, the
 * attribute and the stage are left zeroed.
 */
static inline int
classify_net6_compile(
	struct memory_context *memory_context,
	const char *name,
	const struct classifier_rule *const *rules,
	uint32_t rule_count,
	classify_net6_get_net6s_func get_net6s,
	struct classify_attr_net6 *attr,
	struct classifier *cls
) {
	struct classify_compile_net6 *compile = classify_compile_net6_create(
		memory_context, rules, rule_count, get_net6s, name
	);
	if (compile == NULL) {
		memset(attr, 0, sizeof(*attr));
		memset(cls, 0, sizeof(*cls));
		return -1;
	}

	const struct classify_attr_ops ops = {
		.size = classify_compile_net6_size,
		.rule_is_any = classify_compile_net6_rule_is_any,
		.hash = classify_compile_net6_hash,
		.compare = classify_compile_net6_compare,
		.iter = classify_compile_net6_iter,
		.rule_iter = classify_compile_net6_rule_iter,
		.commit = classify_compile_net6_commit,
		.free_compile = classify_compile_net6_free,
	};

	return classify_attr_compile(
		memory_context,
		name,
		&ops,
		compile,
		rules,
		rule_count,
		attr,
		cls
	);
}

/*
 * Declares the IPv6 network compile entry of one consumer side: the
 * getter selects the source or the destination networks of the module
 * rule and adapts them to the rule agnostic core, and the entry takes
 * the module rule array the consumer owns.
 *
 * The tag must carry the consumer prefix, so the generated symbols
 * never collide with the library ones.
 */
#define CLASSIFY_NET6_COMPILE(tag, get_net6s)                                  \
	static inline int classify_##tag##_compile(                            \
		struct memory_context *memory_context,                         \
		const char *name,                                              \
		const struct classifier_rule **rules,                          \
		uint32_t rule_count,                                           \
		struct classify_attr_net6 *attr,                               \
		struct classifier *cls                                         \
	) {                                                                    \
		return classify_net6_compile(                                  \
			memory_context,                                        \
			name,                                                  \
			rules,                                                 \
			rule_count,                                            \
			get_net6s,                                             \
			attr,                                                  \
			cls                                                    \
		);                                                             \
	}
