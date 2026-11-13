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
#include "common/remap.h"
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
 * is a set of networks covering a cell, and a registry with one range
 * per network holds the region values the network covers. The high half
 * intervals of the networks nest, so a sweep over the high half carries
 * one class per low region and each interval changes only the cells of
 * its own networks; the region numbering is the one of touching every
 * cell of every network cross in network order, reproduced over one
 * cell per class. A network without a mask covers the whole domain and
 * contributes no distinction on its own, so its range holds every region.
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

	// Rows with a low half distinction, set by the first region sweep;
	// only these rows have cells in the join table.
	uint8_t *row_split;
	uint32_t row_count;
	// Whole row region of every row without a low half distinction.
	//
	// The line dies at commit, the resolved classes move into the high
	// half trie values.
	struct vline uniform;

	struct hash_index net_index;
	struct net6 *nets;
	uint32_t net_count;

	struct value_registry net_registry;
	uint32_t *region_values;
	uint32_t region_count;

	// The join table of the combined regions for the split rows only,
	// packed in the order of their first high ordinal, the order the
	// marked high half trie values index; it moves into the embedded
	// attribute at commit.
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
// Narrowed by the test build that grows the class store.
#ifndef FILTER_NET6_CLASS_CHUNK_BITS
#define FILTER_NET6_CLASS_CHUNK_BITS 14
#endif
#define FILTER_NET6_CLASS_CHUNK (1u << FILTER_NET6_CLASS_CHUNK_BITS)

/*
 * A masked network on the high half interval tree: its half region
 * ordinal bounds, its network index and the nearest network of the same
 * high interval enclosing its low interval.
 */
struct filter_net6_tree_net {
	uint32_t bounds[4];
	uint32_t net;
	uint32_t lo_parent;
};

/*
 * The networks sharing one high half interval, sorted by low interval.
 *
 * Entering the interval moves the low regions its networks cover into
 * classes numbered from the first class of the node, in the same order
 * on every sweep.
 */
struct filter_net6_tree_node {
	uint32_t first;
	uint32_t count;
	// Networks covering a strict sub range of the low half.
	uint32_t partial;
	uint32_t first_class;
	// Low ordinals under the outermost low intervals of the node.
	uint64_t span;
};

struct filter_net6_tree_frame {
	uint32_t node;
	uint32_t prev_uniform;
	uint32_t materialized;
};

/*
 * A class is a set of networks covering a cell: the networks of its
 * parent class plus a tree network and the networks of the same node
 * enclosing it.
 */
struct filter_net6_class {
	uint32_t parent;
	uint32_t net;
	// The order of the first cell holding the class, later its region.
	uint32_t rank;
	// The class the parent moves into under the network applied last.
	uint32_t memo_gen;
	uint32_t memo_child;
};

/*
 * Scratch of the combined region assignment.
 *
 * Class 0 is the empty set. The vector holds the class of every low
 * region at the current high ordinal; while no active network has a
 * partial low half every low region shares the uniform class and the
 * vector is not kept. Once kept, the low regions under the nodes
 * entered since hold their own classes and every other one the uniform
 * class.
 */
struct filter_net6_regions {
	struct memory_context *memory_context;
	const uint32_t *values_lo;
	uint32_t lo_count;
	uint32_t h_dim;

	// Sized for every network, masked or not.
	uint32_t tree_capacity;
	struct filter_net6_tree_net *nets;
	struct filter_net6_tree_node *nodes;
	uint32_t node_count;
	struct filter_net6_tree_frame *frames;
	uint32_t depth;

	uint32_t *vector;
	uint32_t uniform;
	uint32_t vector_mode;
	// The first frame of the kept vector and the low ordinals under it.
	uint32_t vector_depth;
	uint64_t vector_span;
	uint32_t partial_active;

	struct filter_net6_class **chunks;
	uint64_t chunk_count;
	uint64_t chunk_capacity;
	uint64_t class_count;
	// The next class of the node being entered again.
	uint32_t replay_class;
	uint32_t memo_gen;

	uint8_t *emitted;
	uint32_t row_count;
	// The packed table row of every split row, in the commit's order.
	uint32_t *dense_rows;
	uint32_t dense_count;
	// The low regions under the nodes of the kept vector, sorted when a
	// row is ranked.
	uint32_t *sparse;
	uint32_t *order;
	uint32_t ranked;
};

static inline struct filter_net6_class *
filter_net6_class_at(const struct filter_net6_regions *regions, uint32_t cls) {
	return regions->chunks[cls >> FILTER_NET6_CLASS_CHUNK_BITS] +
	       (cls & (FILTER_NET6_CLASS_CHUNK - 1));
}

static inline int
filter_net6_regions_new_class(
	struct filter_net6_regions *regions,
	uint32_t parent,
	uint32_t net,
	uint32_t *cls
) {
	if (regions->class_count == FILTER_NET6_NONE) {
		return -1;
	}
	if (regions->class_count ==
	    regions->chunk_count * FILTER_NET6_CLASS_CHUNK) {
		if (regions->chunk_count == regions->chunk_capacity) {
			uint64_t capacity = regions->chunk_capacity * 2;
			struct filter_net6_class **chunks = memory_brealloc(
				regions->memory_context,
				regions->chunks,
				regions->chunk_capacity * sizeof(*chunks),
				capacity * sizeof(*chunks)
			);
			if (chunks == NULL) {
				return -1;
			}
			regions->chunks = chunks;
			regions->chunk_capacity = capacity;
		}
		struct filter_net6_class *chunk = memory_balloc(
			regions->memory_context,
			FILTER_NET6_CLASS_CHUNK * sizeof(*chunk)
		);
		if (chunk == NULL) {
			return -1;
		}
		regions->chunks[regions->chunk_count++] = chunk;
	}

	*cls = (uint32_t)regions->class_count++;
	*filter_net6_class_at(regions, *cls) = (struct filter_net6_class){
		.parent = parent,
		.net = net,
		.rank = FILTER_NET6_NONE,
	};
	return 0;
}

/*
 * Moves the low regions of a range whose innermost node network is the
 * given one into the classes of the node.
 *
 * A low region repeats only in the gaps around a nested low interval,
 * where the innermost network is the same, so a region already moved by
 * the node keeps its class. Entering the node again numbers the classes
 * in the same order instead of creating them.
 */
static inline int
filter_net6_regions_move(
	struct filter_net6_regions *regions,
	const struct filter_net6_tree_node *node,
	uint32_t net,
	uint32_t from,
	uint32_t to,
	int replay
) {
	for (uint32_t idx_lo = from; idx_lo < to; ++idx_lo) {
		uint32_t col = regions->values_lo[idx_lo];
		uint32_t parent = regions->vector[col];
		if (parent >= node->first_class) {
			continue;
		}

		struct filter_net6_class *parent_class =
			filter_net6_class_at(regions, parent);
		if (parent_class->memo_gen != regions->memo_gen) {
			uint32_t cls = regions->replay_class++;
			if (!replay && filter_net6_regions_new_class(
					       regions, parent, net, &cls
				       )) {
				return -1;
			}
			// The class store grows by whole chunks, so the
			// parent stays in place.
			parent_class->memo_gen = regions->memo_gen;
			parent_class->memo_child = cls;
		}
		regions->vector[col] = parent_class->memo_child;
	}
	return 0;
}

/*
 * Enters a high interval node: every low region covered by a network of
 * the node moves into a class of its own for the node, the class of the
 * innermost network over it.
 *
 * The networks of the node are sorted by low start ascending and end
 * descending, so the networks nested in one follow it and its direct
 * children cut its own ranges.
 */
static inline int
filter_net6_regions_push(
	struct filter_net6_regions *regions, uint32_t node_idx, int replay
) {
	struct filter_net6_tree_node *node = regions->nodes + node_idx;
	struct filter_net6_tree_frame *frame = regions->frames + regions->depth;

	frame->node = node_idx;
	frame->prev_uniform = regions->uniform;
	frame->materialized = 0;
	if (node->partial && !regions->vector_mode) {
		for (uint32_t col = 0; col < regions->h_dim; ++col) {
			regions->vector[col] = regions->uniform;
		}
		regions->vector_mode = 1;
		regions->vector_depth = regions->depth;
		regions->vector_span = 0;
		frame->materialized = 1;
	}
	++regions->depth;
	regions->partial_active += node->partial;

	if (!replay) {
		node->first_class = (uint32_t)regions->class_count;
	}
	regions->replay_class = node->first_class;

	if (!regions->vector_mode) {
		// The only network of the node covers the whole low half.
		if (!replay && filter_net6_regions_new_class(
				       regions,
				       regions->uniform,
				       node->first,
				       &regions->uniform
			       )) {
			return -1;
		}
		regions->uniform = node->first_class;
		return 0;
	}

	regions->vector_span += node->span;
	uint32_t end = node->first + node->count;
	for (uint32_t net = node->first; net < end; ++net) {
		const struct filter_net6_tree_net *tree_net =
			regions->nets + net;
		++regions->memo_gen;

		uint32_t from = tree_net->bounds[2];
		for (uint32_t child = net + 1;
		     child < end &&
		     regions->nets[child].bounds[2] < tree_net->bounds[3];
		     ++child) {
			if (regions->nets[child].lo_parent != net) {
				continue;
			}
			if (filter_net6_regions_move(
				    regions,
				    node,
				    net,
				    from,
				    regions->nets[child].bounds[2],
				    replay
			    )) {
				return -1;
			}
			from = regions->nets[child].bounds[3];
		}
		if (filter_net6_regions_move(
			    regions,
			    node,
			    net,
			    from,
			    tree_net->bounds[3],
			    replay
		    )) {
			return -1;
		}
	}
	return 0;
}

// Leaves the innermost entered node: every low region it moved returns
// to the parent of its class.
static inline void
filter_net6_regions_pop(struct filter_net6_regions *regions) {
	const struct filter_net6_tree_frame *frame =
		regions->frames + --regions->depth;
	const struct filter_net6_tree_node *node = regions->nodes + frame->node;

	regions->uniform = frame->prev_uniform;
	regions->partial_active -= node->partial;
	if (!regions->vector_mode) {
		return;
	}

	uint32_t end = node->first + node->count;
	for (uint32_t net = node->first; net < end; ++net) {
		const struct filter_net6_tree_net *tree_net =
			regions->nets + net;
		if (tree_net->lo_parent != FILTER_NET6_NONE) {
			continue;
		}
		for (uint32_t idx_lo = tree_net->bounds[2];
		     idx_lo < tree_net->bounds[3];
		     ++idx_lo) {
			uint32_t col = regions->values_lo[idx_lo];
			uint32_t cls = regions->vector[col];
			if (cls >= node->first_class) {
				regions->vector[col] =
					filter_net6_class_at(regions, cls)
						->parent;
			}
		}
	}
	regions->vector_span -= node->span;
	if (frame->materialized) {
		regions->vector_mode = 0;
	}
}

// Gives the class its order the first time a cell holds it.
static inline void
filter_net6_regions_rank(struct filter_net6_regions *regions, uint32_t cls) {
	struct filter_net6_class *class = filter_net6_class_at(regions, cls);
	if (cls != 0 && class->rank == FILTER_NET6_NONE) {
		class->rank = regions->ranked++;
	}
}

static inline int
filter_net6_u32_cmp(const void *first, const void *second) {
	uint32_t a = *(const uint32_t *)first;
	uint32_t b = *(const uint32_t *)second;
	return a < b ? -1 : a > b;
}

/*
 * Collects the low regions under the nodes of the kept vector.
 *
 * The regions may repeat; none are collected when they are too many to
 * beat a walk over the row, which also keeps them within the buffer of
 * one row.
 */
static inline uint32_t
filter_net6_regions_sparse(struct filter_net6_regions *regions) {
	if (regions->vector_span * 8 >= regions->h_dim) {
		return 0;
	}

	uint32_t count = 0;
	for (uint32_t depth = regions->vector_depth; depth < regions->depth;
	     ++depth) {
		const struct filter_net6_tree_node *node =
			regions->nodes + regions->frames[depth].node;
		uint32_t end = node->first + node->count;
		for (uint32_t net = node->first; net < end; ++net) {
			const struct filter_net6_tree_net *tree_net =
				regions->nets + net;
			if (tree_net->lo_parent != FILTER_NET6_NONE) {
				continue;
			}
			for (uint32_t idx_lo = tree_net->bounds[2];
			     idx_lo < tree_net->bounds[3];
			     ++idx_lo) {
				regions->sparse[count++] =
					regions->values_lo[idx_lo];
			}
		}
	}
	return count;
}

/*
 * Ranks the classes of a row in low region order.
 *
 * The regions under the nodes of the kept vector hold their classes and
 * every other one the uniform class.
 */
static inline void
filter_net6_regions_rank_row(struct filter_net6_regions *regions) {
	uint32_t count = filter_net6_regions_sparse(regions);
	qsort(regions->sparse, count, sizeof(uint32_t), filter_net6_u32_cmp);
	if (count == 0) {
		for (uint32_t col = 0; col < regions->h_dim; ++col) {
			filter_net6_regions_rank(regions, regions->vector[col]);
		}
		return;
	}

	uint32_t next = 0;
	for (uint32_t idx = 0; idx < count; ++idx) {
		uint32_t col = regions->sparse[idx];
		if (col < next) {
			continue;
		}
		if (col > next) {
			filter_net6_regions_rank(regions, regions->uniform);
		}
		filter_net6_regions_rank(regions, regions->vector[col]);
		next = col + 1;
	}
	if (next < regions->h_dim) {
		filter_net6_regions_rank(regions, regions->uniform);
	}
}

/*
 * Writes the regions of a row: a sparse row writes the uniform region
 * only when it is not the zero the table starts with.
 */
static inline void
filter_net6_regions_write_row(
	struct filter_net6_regions *regions, uint32_t *cells
) {
	uint32_t count = filter_net6_regions_sparse(regions);
	if (count == 0) {
		for (uint32_t col = 0; col < regions->h_dim; ++col) {
			uint32_t cls = regions->vector[col];
			cells[col] = filter_net6_class_at(regions, cls)->rank;
		}
		return;
	}

	uint32_t uniform =
		filter_net6_class_at(regions, regions->uniform)->rank;
	if (uniform != 0) {
		for (uint32_t col = 0; col < regions->h_dim; ++col) {
			cells[col] = uniform;
		}
	}
	for (uint32_t idx = 0; idx < count; ++idx) {
		uint32_t col = regions->sparse[idx];
		cells[col] = filter_net6_class_at(regions, regions->vector[col])
				     ->rank;
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
filter_net6_regions_free(struct filter_net6_regions *regions) {
	struct memory_context *memory_context = regions->memory_context;

	uint32_t capacity = regions->tree_capacity;
	memory_bfree(
		memory_context, regions->nets, capacity * sizeof(*regions->nets)
	);
	memory_bfree(
		memory_context,
		regions->nodes,
		capacity * sizeof(*regions->nodes)
	);
	memory_bfree(
		memory_context,
		regions->frames,
		capacity * sizeof(*regions->frames)
	);

	uint32_t h_dim = regions->h_dim;
	memory_bfree(memory_context, regions->vector, h_dim * sizeof(uint32_t));
	memory_bfree(memory_context, regions->sparse, h_dim * sizeof(uint32_t));

	for (uint64_t idx = 0; idx < regions->chunk_count; ++idx) {
		memory_bfree(
			memory_context,
			regions->chunks[idx],
			FILTER_NET6_CLASS_CHUNK *
				sizeof(struct filter_net6_class)
		);
	}
	memory_bfree(
		memory_context,
		regions->chunks,
		regions->chunk_capacity * sizeof(*regions->chunks)
	);
	memory_bfree(
		memory_context,
		regions->order,
		(regions->ranked + 1) * sizeof(uint32_t)
	);
	memory_bfree(memory_context, regions->emitted, regions->row_count);
	memory_bfree(
		memory_context,
		regions->dense_rows,
		regions->row_count * sizeof(uint32_t)
	);
}

/*
 * Places the masked networks on the high half interval tree and
 * allocates the sweep scratch.
 */
static inline int
filter_net6_regions_init(
	struct filter_net6_regions *regions,
	struct memory_context *memory_context,
	struct classify_compile_net6 *net6_compile
) {
	memset(regions, 0, sizeof(*regions));
	regions->memory_context = memory_context;
	regions->values_lo = ADDR_OF(&net6_compile->ri_lo.values);
	regions->lo_count = net6_compile->ri_lo.count;
	regions->h_dim = net6_compile->ri_lo.max_value + 1;
	regions->row_count = net6_compile->row_count;

	uint32_t capacity = net6_compile->net_count + 1;
	regions->tree_capacity = capacity;
	regions->nets = memory_balloc(
		memory_context, capacity * sizeof(*regions->nets)
	);
	regions->nodes = memory_balloc(
		memory_context, capacity * sizeof(*regions->nodes)
	);
	regions->frames = memory_balloc(
		memory_context, capacity * sizeof(*regions->frames)
	);
	regions->vector = memory_balloc(
		memory_context, regions->h_dim * sizeof(uint32_t)
	);
	regions->sparse = memory_balloc(
		memory_context, regions->h_dim * sizeof(uint32_t)
	);
	regions->emitted = memory_balloc(memory_context, regions->row_count);
	regions->dense_rows = memory_balloc(
		memory_context, regions->row_count * sizeof(uint32_t)
	);
	regions->chunk_capacity = 1;
	regions->chunks =
		memory_balloc(memory_context, sizeof(*regions->chunks));
	if (regions->nets == NULL || regions->nodes == NULL ||
	    regions->frames == NULL || regions->vector == NULL ||
	    regions->sparse == NULL || regions->emitted == NULL ||
	    regions->dense_rows == NULL || regions->chunks == NULL) {
		return -1;
	}
	memset(regions->vector, 0, regions->h_dim * sizeof(uint32_t));

	// The empty set.
	uint32_t empty;
	if (filter_net6_regions_new_class(
		    regions, FILTER_NET6_NONE, FILTER_NET6_NONE, &empty
	    )) {
		return -1;
	}

	uint32_t count = 0;
	for (uint32_t net_idx = 0; net_idx < net6_compile->net_count;
	     ++net_idx) {
		const struct net6 *net = net6_compile->nets + net_idx;
		if (net6_is_any(net)) {
			continue;
		}
		struct filter_net6_tree_net *tree_net = regions->nets + count++;
		filter_net6_net_bounds(net6_compile, net, tree_net->bounds);
		tree_net->net = net_idx;
	}
	qsort(regions->nets,
	      count,
	      sizeof(*regions->nets),
	      filter_net6_tree_net_cmp);

	// Group the networks by high interval and link the low intervals of
	// each group; the frames serve as the stack of open low intervals.
	uint32_t *lo_stack = (uint32_t *)regions->frames;
	for (uint32_t first = 0; first < count;) {
		struct filter_net6_tree_node *node =
			regions->nodes + regions->node_count++;
		memset(node, 0, sizeof(*node));
		node->first = first;

		uint32_t idx = first;
		uint32_t depth = 0;
		while (idx < count &&
		       regions->nets[idx].bounds[0] ==
			       regions->nets[first].bounds[0] &&
		       regions->nets[idx].bounds[1] ==
			       regions->nets[first].bounds[1]) {
			struct filter_net6_tree_net *tree_net =
				regions->nets + idx;
			while (depth &&
			       regions->nets[lo_stack[depth - 1]].bounds[3] <=
				       tree_net->bounds[2]) {
				--depth;
			}
			tree_net->lo_parent =
				depth ? lo_stack[depth - 1] : FILTER_NET6_NONE;
			lo_stack[depth++] = idx;

			node->partial +=
				tree_net->bounds[2] != 0 ||
				tree_net->bounds[3] != regions->lo_count;
			if (tree_net->lo_parent == FILTER_NET6_NONE) {
				node->span += tree_net->bounds[3] -
					      tree_net->bounds[2];
			}
			++idx;
		}
		node->count = idx - first;
		first = idx;
	}
	return 0;
}

/*
 * Sweeps the high ordinals over the interval tree and visits every row
 * once, at its first ordinal.
 *
 * The first sweep creates the classes and ranks them by the first cell
 * holding them in high then low ordinal order - the partition numbers
 * the regions of both halves by their first ordinal, so the low regions
 * go in index order, and marks and packs the split rows. The second
 * sweep enters the nodes again and writes the regions of every row,
 * or its uniform line value.
 */
static inline int
filter_net6_regions_sweep(
	struct filter_net6_regions *regions,
	struct classify_compile_net6 *net6_compile,
	int write
) {
	const uint32_t *values_hi = ADDR_OF(&net6_compile->ri_hi.values);
	memset(regions->emitted, 0, regions->row_count);

	uint32_t next_node = 0;
	for (uint32_t idx_hi = 0; idx_hi < net6_compile->ri_hi.count;
	     ++idx_hi) {
		while (regions->depth) {
			const struct filter_net6_tree_node *node =
				regions->nodes +
				regions->frames[regions->depth - 1].node;
			if (regions->nets[node->first].bounds[1] > idx_hi) {
				break;
			}
			filter_net6_regions_pop(regions);
		}
		while (next_node < regions->node_count &&
		       regions->nets[regions->nodes[next_node].first]
				       .bounds[0] == idx_hi) {
			if (filter_net6_regions_push(
				    regions, next_node++, write
			    )) {
				return -1;
			}
		}

		uint32_t row = values_hi[idx_hi];
		if (regions->emitted[row]) {
			continue;
		}
		regions->emitted[row] = 1;

		if (!regions->partial_active) {
			if (write) {
				*vline_get_ptr(&net6_compile->uniform, row) =
					filter_net6_class_at(
						regions, regions->uniform
					)
						->rank;
			} else {
				filter_net6_regions_rank(
					regions, regions->uniform
				);
			}
		} else if (write) {
			filter_net6_regions_write_row(
				regions,
				value_table_get_ptr(
					&net6_compile->comb,
					regions->dense_rows[row],
					0
				)
			);
		} else {
			net6_compile->row_split[row] = 1;
			regions->dense_rows[row] = regions->dense_count++;
			filter_net6_regions_rank_row(regions);
		}
	}

	while (regions->depth) {
		filter_net6_regions_pop(regions);
	}
	return 0;
}

/*
 * Numbers the regions as touching every cell of every network cross in
 * network order would, and fills the registry range of every network.
 *
 * Every cell of a class shares its touch history, and a network
 * covering one cell of a class covers all of them, so touching one cell
 * per class - the classes of a network in their rank order - allocates
 * the same remap keys in the same order, as the remap table never reuses
 * a released key. The registry ranges collect
 * the classes of every network in rank order, the order a network
 * walking its cross meets them, and are then rewritten into regions.
 */
static inline int
filter_net6_regions_assign(
	struct filter_net6_regions *regions,
	struct classify_compile_net6 *net6_compile,
	const char *name
) {
	struct memory_context *memory_context = regions->memory_context;

	regions->order = memory_balloc(
		memory_context, (regions->ranked + 1) * sizeof(uint32_t)
	);
	if (regions->order == NULL) {
		return -1;
	}
	for (uint32_t cls = 1; cls < regions->class_count; ++cls) {
		uint32_t rank = filter_net6_class_at(regions, cls)->rank;
		if (rank != FILTER_NET6_NONE) {
			regions->order[rank] = cls;
		}
	}

	char nets_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(nets_name, sizeof(nets_name), name, "nets");
	if (value_registry_init(
		    &net6_compile->net_registry, memory_context, nets_name
	    )) {
		return -1;
	}
	struct value_registry *registry = &net6_compile->net_registry;
	for (uint32_t net_idx = 0; net_idx < net6_compile->net_count;
	     ++net_idx) {
		if (value_registry_start(registry)) {
			return -1;
		}
	}
	struct value_range *ranges = ADDR_OF(&registry->ranges);
	struct memory_context *registry_context =
		ADDR_OF(&registry->memory_context);

	for (uint32_t rank = 0; rank < regions->ranked; ++rank) {
		uint32_t cls = regions->order[rank];
		for (uint32_t link = cls; link != 0;
		     link = filter_net6_class_at(regions, link)->parent) {
			for (uint32_t net =
				     filter_net6_class_at(regions, link)->net;
			     net != FILTER_NET6_NONE;
			     net = regions->nets[net].lo_parent) {
				if (value_range_append(
					    registry_context,
					    ranges + regions->nets[net].net,
					    cls
				    )) {
					return -1;
				}
			}
		}
	}

	// The rank of a class now carries its remap key.
	for (uint32_t cls = 0; cls < regions->class_count; ++cls) {
		filter_net6_class_at(regions, cls)->rank = 0;
	}

	// The empty set keeps its key: fewer classes than the count it
	// starts with ever leave it.
	struct remap_table remap_table;
	if (remap_table_init(&remap_table, memory_context, UINT32_MAX)) {
		return -1;
	}
	for (uint32_t net_idx = 0; net_idx < net6_compile->net_count;
	     ++net_idx) {
		if (net6_is_any(net6_compile->nets + net_idx)) {
			continue;
		}
		remap_table_new_gen(&remap_table);

		const uint32_t *values = ADDR_OF(&ranges[net_idx].values);
		for (uint64_t idx = 0; idx < ranges[net_idx].count; ++idx) {
			uint32_t *key =
				&filter_net6_class_at(regions, values[idx])
					 ->rank;
			if (remap_table_touch(&remap_table, *key, key) < 0) {
				remap_table_free(&remap_table);
				return -1;
			}
		}
	}
	remap_table_compact(&remap_table);
	for (uint32_t cls = 0; cls < regions->class_count; ++cls) {
		uint32_t *key = &filter_net6_class_at(regions, cls)->rank;
		*key = remap_table_compacted(&remap_table, *key);
	}
	remap_table_free(&remap_table);

	uint32_t max_value = 0;
	for (uint64_t range_idx = 0; range_idx < registry->range_count;
	     ++range_idx) {
		uint32_t *values = ADDR_OF(&ranges[range_idx].values);
		for (uint64_t idx = 0; idx < ranges[range_idx].count; ++idx) {
			values[idx] = filter_net6_class_at(regions, values[idx])
					      ->rank;
			if (values[idx] > max_value) {
				max_value = values[idx];
			}
		}
	}
	registry->max_value = max_value;
	net6_compile->region_count = max_value + 1;

	return 0;
}

/*
 * Fills the row split map, the join table, the uniform line, the network
 * registry and the region count of the compile.
 */
static inline int
filter_net6_build_regions(
	struct memory_context *memory_context,
	struct classify_compile_net6 *net6_compile,
	const char *name
) {
	struct filter_net6_regions regions;
	int rc = filter_net6_regions_init(
		&regions, memory_context, net6_compile
	);
	if (rc == 0) {
		rc = filter_net6_regions_sweep(&regions, net6_compile, 0);
	}
	if (rc == 0) {
		rc = filter_net6_regions_assign(&regions, net6_compile, name);
	}
	if (rc == 0 && regions.dense_count != 0) {
		// The split rows packed in the order the commit reads them; the
		// cells of the uniform rows are never read.
		char comb_name[MEMORY_CONTEXT_NAME_SIZE];
		classify_leaf_name(comb_name, sizeof(comb_name), name, "comb");
		rc = value_table_init(
			&net6_compile->comb,
			memory_context,
			comb_name,
			regions.dense_count,
			regions.h_dim
		);
	}
	if (rc == 0) {
		rc = filter_net6_regions_sweep(&regions, net6_compile, 1);
	}
	filter_net6_regions_free(&regions);
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
	 * The dense table carries the join of the half region values into
	 * the combined regions for the rows with a low half distinction,
	 * packed; every other high region row carries its value in the
	 * uniform value line instead. The line values and the cells share
	 * one region space.
	 */
	compile->row_count = compile->ri_hi.max_value + 1;
	char rows_name[MEMORY_CONTEXT_NAME_SIZE];
	classify_leaf_name(rows_name, sizeof(rows_name), name, "rows");
	if (vline_init(
		    &compile->uniform,
		    memory_context,
		    rows_name,
		    compile->row_count
	    )) {
		goto error_free_parts;
	}

	compile->row_split = memory_balloc(memory_context, compile->row_count);
	if (compile->row_split == NULL) {
		goto error_free_uniform;
	}
	memset(compile->row_split, 0, compile->row_count);

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

	if (compile->row_split != NULL) {
		memory_bfree(
			memory_context, compile->row_split, compile->row_count
		);
	}

error_free_uniform:
	vline_free(&compile->uniform);

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

	if (net6_compile->row_split != NULL) {
		memory_bfree(
			memory_context,
			net6_compile->row_split,
			net6_compile->row_count
		);
	}

	// The uniform line is compile side scratch: it is released here in
	// every path, including the commit one.
	vline_free(&net6_compile->uniform);

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
	 * the rows of a low half distinction arrive packed in the join table
	 * referenced from the marked trie values, and their cells resolve in
	 * place.
	 */
	struct value_table *comb = &net6_compile->comb;

	// The high half trie build walks the partition by region ordinal,
	// so the resolved values are indexed by ordinal and reach the row
	// of every ordinal through the region values of the partition.
	const uint32_t *hi_values = ADDR_OF(&net6_compile->ri_hi.values);
	uint32_t hi_count = net6_compile->ri_hi.count;

	uint32_t *dense_of_row = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * net6_compile->row_count
	);
	uint32_t *lpm_values = (uint32_t *)memory_balloc(
		memory_context, sizeof(uint32_t) * (hi_count ? hi_count : 1)
	);
	if (dense_of_row == NULL || lpm_values == NULL) {
		if (dense_of_row != NULL) {
			memory_bfree(
				memory_context,
				dense_of_row,
				sizeof(uint32_t) * net6_compile->row_count
			);
		}
		if (lpm_values != NULL) {
			memory_bfree(
				memory_context,
				lpm_values,
				sizeof(uint32_t) * (hi_count ? hi_count : 1)
			);
		}
		goto error;
	}

	// A region value repeats across the ordinals of the deduplicated
	// partition, so a dense row is assigned per row and every ordinal
	// of the row shares the assigned one.
	memset(dense_of_row, 0xff, sizeof(uint32_t) * net6_compile->row_count);

	uint32_t dense_count = 0;
	for (uint32_t idx = 0; idx < hi_count; ++idx) {
		uint32_t row = hi_values[idx];
		if (net6_compile->row_split[row]) {
			if (dense_of_row[row] == 0xffffffffu) {
				dense_of_row[row] = dense_count;
				++dense_count;
			}
			lpm_values[idx] =
				FILTER_NET6_ROW_MARK | dense_of_row[row];
			continue;
		}

		lpm_values[idx] = net6_compile->region_values[vline_get(
			&net6_compile->uniform, row
		)];
	}

	// The table holds the split rows packed in this same order, so its
	// regions resolve into their classes in place.
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
		dense_of_row,
		sizeof(uint32_t) * net6_compile->row_count
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
