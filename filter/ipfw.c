#include "ipfw.h"

#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

#include <endian.h>

#include "radix.h"

#include "registry.h"
#include "value.h"

#include "classify.h"

#include <stdio.h>


static inline uint64_t
net6_next(uint64_t value)
{
	return htobe64(be64toh(value) + 1);
}

static inline uint64_t
net6_prev(uint64_t value)
{
	return htobe64(be64toh(value) - 1);
}

struct net6_collector {
	struct radix64 radix64;
	uint64_t *masks;
	uint32_t mask_count;
	uint32_t count;
};

static int
net6_collector_init(struct net6_collector *collector)
{
	if (radix64_init(&collector->radix64))
		return -1;
	collector->masks = NULL;
	collector->mask_count = 0;
	return 0;
}

static int
net6_collector_add_mask(struct net6_collector *collector, uint32_t *mask_index)
{
	if (!(collector->mask_count & (collector->mask_count + 1))) {
		uint64_t *masks =
			(uint64_t *)realloc(collector->masks,
					      sizeof(uint64_t) *
					      (collector->mask_count + 1) * 2);
		if (masks == NULL)
			return -1;
		collector->masks = masks;
	}

	memset(collector->masks + collector->mask_count, 0, sizeof(uint64_t));
	*mask_index = collector->mask_count++;
	return 0;
}

static int
net6_collector_add(
	struct net6_collector *collector,
	uint64_t value,
	uint64_t mask)
{
	if (!mask)
		return 0;

	uint32_t mask_index = radix64_lookup(&collector->radix64, value);
	if (mask_index == RADIX_VALUE_INVALID) {
		if (net6_collector_add_mask(collector, &mask_index)) {
			return -1;
		}

		if (radix64_insert(&collector->radix64, value, mask_index)) {
			// FIXME: one mask item leaked here but well
			return -1;
		}
	}

	uint8_t prefix = __builtin_popcountll(mask);
	collector->masks[mask_index] |= 1 << (prefix - 1);

	return 0;
}

struct net6_stack {
	uint64_t from;
	uint64_t to;
};

struct net6_collect_ctx {
	struct net6_collector *collector;

	struct net6_stack stack[64];
	uint32_t values[64];
	uint32_t stack_depth;

	uint32_t max_value;
	uint64_t last_to;

	struct lpm64 lpm64;
};

static inline uint32_t
net6_collect_ctx_top_value(struct net6_collect_ctx *ctx)
{
	if (ctx->values[ctx->stack_depth - 1] == LPM_VALUE_INVALID) {
		ctx->values[ctx->stack_depth - 1]  = ctx->max_value++;
	}
	return ctx->values[ctx->stack_depth - 1];
}

static inline uint64_t
one_if_zero(uint64_t value)
{
	// endian ignorant
	return (value - 1) / 0xffffffffffffffff;
}

static inline uint64_t
trailing_z_mask(uint64_t value)
{
	return (value ^ (value - 1)) >> (1 - one_if_zero(value));
}

static int
net6_collector_emit_range(
	uint64_t from,
	uint64_t to,
	uint32_t value,
	struct net6_collect_ctx *ctx)
{
	if (from == net6_next(to)) {
		// /0 prefix
		return lpm64_insert(&ctx->lpm64, from, to, value);
	}

	from = be64toh(from);
	to = be64toh(to);

	while (from != to + 1) {
		uint64_t delta = to - from + 1;
		delta >>= 1;
		delta |= delta >> 1;
		delta |= delta >> 2;
		delta |= delta >> 4;
		delta |= delta >> 8;
		delta |= delta >> 16;
		delta |= delta >> 32;

		uint64_t mask = trailing_z_mask(from);
		mask &= delta & mask;

		if (lpm64_insert(&ctx->lpm64, htobe64(from), htobe64(from | mask), value))
			return -1;

		from = (from | mask) + 1;
	}
	return 0;
}

static void
net6_collector_add_network(
	uint64_t from,
	uint64_t to,
	struct net6_collect_ctx *ctx)
{
		while (ctx->stack_depth > 0) {
			uint64_t upper_mask =
				~(ctx->stack[ctx->stack_depth - 1].to ^
				  ctx->stack[ctx->stack_depth - 1].from);
			if (!((from ^ ctx->stack[ctx->stack_depth - 1].from) & upper_mask)) {
				break;
			}
			if (!(ctx->last_to == ctx->stack[ctx->stack_depth - 1].to)) {
				net6_collector_emit_range(
					net6_next(ctx->last_to),
					ctx->stack[ctx->stack_depth - 1].to,
					net6_collect_ctx_top_value(ctx),
					ctx);
				ctx->last_to = ctx->stack[ctx->stack_depth - 1].to;
			}
			--ctx->stack_depth;
		}

		if (ctx->stack_depth > 0 &&
		    !(net6_next(ctx->last_to) == from)) {
			net6_collector_emit_range(
					net6_next(ctx->last_to),
					net6_prev(ctx->stack[ctx->stack_depth - 1].from),
					net6_collect_ctx_top_value(ctx),
					ctx);
				ctx->last_to = net6_prev(ctx->stack[ctx->stack_depth - 1].from);
		}

		ctx->last_to = net6_prev(from);

		ctx->stack[ctx->stack_depth] = (struct net6_stack){from, to};
		ctx->values[ctx->stack_depth] = LPM_VALUE_INVALID;
		ctx->stack_depth++;
}

static void
net6_collector_iterate(
	uint64_t key,
	uint32_t value,
	void *data)
{
	struct net6_collect_ctx *ctx = (struct net6_collect_ctx *)data;
	uint64_t mask = ctx->collector->masks[value];

	while (mask) {
		uint64_t shift = __builtin_ctzll(mask);
		uint64_t from = key;
		uint64_t to = from | be64toh(0x7fffffffffffffff >> shift); // big endian
		net6_collector_add_network(from, to, ctx);
		mask ^= 0x01 << shift;
	}
}

static struct lpm64
net6_collector_collect(
	struct net6_collector *collector)
{
	struct net6_collect_ctx ctx;
	ctx.collector = collector;
	ctx.max_value = 0;
	lpm64_init(&ctx.lpm64);

	ctx.stack[0] = (struct net6_stack){0, -1};
	ctx.values[0] = LPM_VALUE_INVALID;
	ctx.stack_depth = 1;
	ctx.last_to = -1;

	radix64_iterate(&collector->radix64, net6_collector_iterate, &ctx);

	while (ctx.stack_depth > 0) {
		if (!(ctx.last_to == ctx.stack[ctx.stack_depth - 1].to)
		    || ctx.max_value == 0) {
			net6_collector_emit_range(
				net6_next(ctx.last_to),
				ctx.stack[ctx.stack_depth - 1].to,
				net6_collect_ctx_top_value(&ctx),
				&ctx);
			ctx.last_to = ctx.stack[ctx.stack_depth - 1].to;
		}
		--ctx.stack_depth;
	}

	collector->count = ctx.max_value;

	return ctx.lpm64;
}

typedef void (*action_get_net6_func)(
	struct ipfw_filter_action *action,
	struct ipfw_net6 **net,
	uint32_t *count);

static void
action_get_net6_src(
	struct ipfw_filter_action *action,
	struct ipfw_net6 **net,
	uint32_t *count)
{
	*net = action->filter.net6.srcs;
	*count = action->filter.net6.src_count;
}

static void
action_get_net6_dst(
	struct ipfw_filter_action *action,
	struct ipfw_net6 **net,
	uint32_t *count)
{
	*net = action->filter.net6.dsts;
	*count = action->filter.net6.dst_count;
}

typedef void (*net6_get_part_func)(
	struct ipfw_net6 *net,
	uint64_t *addr,
	uint64_t *mask);

static void
net6_get_hi_part(struct ipfw_net6 *net,
	uint64_t *addr,
	uint64_t *mask)
{
	*addr = net->addr_hi;
	*mask = net->mask_hi;
}

static void
net6_get_lo_part(struct ipfw_net6 *net,
	uint64_t *addr,
	uint64_t *mask)
{
	*addr = net->addr_lo;
	*mask = net->mask_lo;
}


static void
lpm64_value_iterator(uint64_t key, uint32_t value, void *data)
{
	(void) key;

	struct value_table *table = (struct value_table *)data;
	value_table_touch(table, 0, value);
}

static void
net6_collect_values(
	struct ipfw_net6 *start,
	uint32_t count,
	net6_get_part_func get_part,
	struct lpm64 *lpm,
	struct value_table *table)
{
	for (struct ipfw_net6 *net6 = start; net6 < start + count; ++net6) {
		uint64_t addr;
		uint64_t mask;
		get_part(net6, &addr, &mask);
		lpm64_walk(
			lpm,
			addr,
			addr | ~mask,
			lpm64_value_iterator,
			table);
	}
}

struct net_collect_cxt {
	struct value_table *table;
	struct value_registry *registry;
};

static void
lpm64_registry_iterator(uint64_t key, uint32_t value, void *data)
{
	(void) key;

	struct value_registry *registry = (struct value_registry *)data;
	value_registry_collect(registry, value);
}

static void
net6_collect_registry(
	struct ipfw_net6 *start,
	uint32_t count,
	net6_get_part_func get_part,
	struct lpm64 *lpm,
	struct value_registry *registry)
{
	for (struct ipfw_net6 *net6 = start; net6 < start + count; ++net6) {
		uint64_t addr;
		uint64_t mask;
		get_part(net6, &addr, &mask);
		lpm64_walk(
			lpm,
			addr,
			addr | ~mask,
			lpm64_registry_iterator,
			registry);
	}
}

static int
value_table_touch_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data)
{
	(void) idx;
	struct value_table *table = (struct value_table *)data;
	if (value_table_touch(table, v1, v2) < 0)
		return -1;
	return 0;
}

static int
merge_registry_values(
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table)
{
	if (value_table_init(
		table,
		value_registry_capacity(registry1),
		value_registry_capacity(registry2))) {
		return -1;
	}

	for (uint32_t range_idx = 0;
	     range_idx < registry1->range_count; ++range_idx) {
		value_table_new_gen(table);
		value_registry_join_range(
			registry1,
			registry2,
			range_idx,
			value_table_touch_action,
			table);
	}

	value_table_compact(table);

	return 0;
}

struct value_collect_ctx {
	struct value_table *table;
	struct value_registry *registry;
};

static int
value_table_collect_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data)
{
	(void) idx;
	struct value_collect_ctx *collect_ctx =
		(struct value_collect_ctx *)data;
	return value_registry_collect(
		collect_ctx->registry,
		value_table_get(collect_ctx->table, v1, v2));

	return 0;
}

static int
collect_registry_values(
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry)
{
	if (value_registry_init(registry)) {
		return -1;
	}

	struct value_collect_ctx collect_ctx;
	collect_ctx.table = table;
	collect_ctx.registry = registry;

	for (uint32_t range_idx = 0;
	     range_idx < registry1->range_count; ++range_idx) {
		value_registry_start(registry);
		value_registry_join_range(
			registry1,
			registry2,
			range_idx,
			value_table_collect_action,
			&collect_ctx);
	}

	return 0;
}

static int
merge_and_collect_registry(
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry)
{
	if (merge_registry_values(registry1, registry2, table)) {
		return -1;
	}

	if (collect_registry_values(registry1, registry2, table, registry)) {
		value_table_free(table);
		return -1;
	}

	return 0;
}

struct value_set_ctx {
	struct value_table *table;
	struct value_registry *registry;
};

static int
action_list_is_term(struct value_registry *registry, uint32_t range_idx)
{
	struct value_range *range = registry->ranges + range_idx;

	for (uint32_t ridx = range->from;
	     ridx < range->from + range->count;
	     ++ridx) {
		uint32_t action_id = registry->values[ridx];
		(void) action_id;
		return 1;
	}
	return 0;
}

static int
value_table_set_action(uint32_t v1, uint32_t v2, uint32_t idx, void *data)
{
	struct value_set_ctx *set_ctx = (struct value_set_ctx *)data;
	uint32_t prev_value = value_table_get(set_ctx->table, v1, v2);

	if (!action_list_is_term(set_ctx->registry, prev_value)) {
		int res = value_table_touch(set_ctx->table, v1, v2);

		if (res <= 0)
			return res;

		value_registry_start(set_ctx->registry);

		struct value_range *copy_range =
			set_ctx->registry->ranges + prev_value;

		for (uint32_t ridx = copy_range->from;
		     ridx < copy_range->from + copy_range->count;
		     ++ridx) {
			value_registry_collect(
				set_ctx->registry,
				set_ctx->registry->values[ridx]);
		}

		value_registry_collect(set_ctx->registry, idx);
	}

	return 0;
}

static int
set_registry_values(
	struct value_registry *registry1,
	struct value_registry *registry2,
	struct value_table *table,
	struct value_registry *registry)
{
	if (value_table_init(
		table,
		value_registry_capacity(registry1),
		value_registry_capacity(registry2))) {
		return -1;
	}

	if (value_registry_init(registry)) {
		value_table_free(table);
		return -1;
	}
	// Empty action list
	if (value_registry_start(registry)) {
		value_registry_free(registry);
		value_table_free(table);
	}

	struct value_set_ctx set_ctx;
	set_ctx.table = table;
	set_ctx.registry = registry;

	for (uint32_t range_idx = 0;
	     range_idx < registry1->range_count; ++range_idx) {
		value_table_new_gen(table);
		value_registry_join_range(
			registry1,
			registry2,
			range_idx,
			value_table_set_action,
			&set_ctx);
	}

	return 0;
}

static int
collect_network_values(
	struct ipfw_filter_action *actions,
	uint32_t count,
	action_get_net6_func get_net6,
	net6_get_part_func get_part,
	struct lpm64 *lpm,
	struct value_registry *registry)
{
	struct value_table table;

	struct net6_collector collector;
	if (net6_collector_init(&collector))
		goto error;

	for (struct ipfw_filter_action *action = actions;
	       action < actions + count;
	       ++action) {
		struct ipfw_net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);

		for (struct ipfw_net6 *net6 = nets;
		     net6 < nets + net_count;
		     ++net6) {
			uint64_t addr;
			uint64_t mask;
			get_part(net6, &addr, &mask);

			net6_collector_add(&collector, addr, mask);
		}
	}
	*lpm = net6_collector_collect(&collector);

	if (value_table_init(&table, 1, collector.count))
		goto error_vtab;

	for (struct ipfw_filter_action *action = actions;
	       action < actions + count;
	       ++action) {
		value_table_new_gen(&table);

		struct ipfw_net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);

		net6_collect_values(
			nets,
			net_count,
			get_part,
			lpm,
			&table);
	}

	value_table_compact(&table);
	lpm64_compact(lpm, &table);

	if (value_registry_init(registry))
		goto error_reg;

	for (struct ipfw_filter_action *action = actions;
	       action < actions + count;
	       ++action) {
		value_registry_start(registry);

		struct ipfw_net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);

		net6_collect_registry(
			nets,
			net_count,
			get_part,
			lpm,
			registry);
	}

	value_table_free(&table);
	return 0;


error_reg:
	value_table_free(&table);

error_vtab:

error:
	return -1;
}

typedef void (*action_get_port_range_func)(
	struct ipfw_filter_action *action,
	struct ipfw_port_range **ranges,
	uint32_t *count);

static void
get_port_range_src(
	struct ipfw_filter_action *action,
	struct ipfw_port_range **ranges,
	uint32_t *count)
{
	*ranges = action->filter.transport.srcs;
	*count = action->filter.transport.src_count;
}

static void
get_port_range_dst(
	struct ipfw_filter_action *action,
	struct ipfw_port_range **ranges,
	uint32_t *count)
{
	*ranges = action->filter.transport.dsts;
	*count = action->filter.transport.dst_count;
}

static int
collect_port_values(
	struct ipfw_filter_action *actions,
	uint32_t count,
	action_get_port_range_func get_port_range,
	struct value_table *table,
	struct value_registry *registry)
{
	if (value_table_init(table, 1, 65536))
		return -1;

	for (struct ipfw_filter_action *action = actions;
	       action < actions + count;
	       ++action) {
		value_table_new_gen(table);

		struct ipfw_port_range *port_ranges;
		uint32_t port_range_count;
		get_port_range(action, &port_ranges, &port_range_count);
		for (struct ipfw_port_range *ports = port_ranges;
		     ports < port_ranges + port_range_count;
		     ++ports) {
			if (ports->to - ports->from == 65535)
				continue;
			for (uint32_t port = ports->from;
			     port <= ports->to;
			     ++port) {
				value_table_touch(table, 0, port);
			}
		}
	}

	value_table_compact(table);

	if (value_registry_init(registry))
		goto error_reg;

	for (struct ipfw_filter_action *action = actions;
	       action < actions + count;
	       ++action) {
		value_registry_start(registry);

		struct ipfw_port_range *port_ranges;
		uint32_t port_range_count;
		get_port_range(action, &port_ranges, &port_range_count);
		for (struct ipfw_port_range *ports = port_ranges;
		     ports < port_ranges + port_range_count;
		     ++ports) {
			for (uint32_t port = ports->from;
			     port <= ports->to;
			     ++port) {
				value_registry_collect(
					registry,
					value_table_get(table, 0, port));
			}
		}
	}

	return 0;

error_reg:
	value_table_free(table);
	return -1;
}

static void
print_vtab(struct value_table *vtab)
{
	fprintf(stderr, "TAB\n");
	for (uint32_t vidx = 0; vidx < vtab->v_dim; ++vidx) {
		for (uint32_t hidx = 0; hidx < vtab->h_dim; ++hidx) {
			fprintf(stderr, "%8u", value_table_get(vtab, hidx, vidx));
		}
		fprintf(stderr, "\n");
	}
}

static void
print_vreg(struct value_registry *registry)
{
	fprintf(stderr, "REG\n");
	for (uint32_t ridx = 0; ridx < registry->range_count; ++ridx) {
		struct value_range *rng = registry->ranges + ridx;
		for (uint32_t vidx = rng->from; vidx < rng->from + rng->count; ++vidx) {
			fprintf(stderr, "%8u", registry->values[vidx]);
		}
		fprintf(stderr, "\n");
	}
}

static int
filter_table_copy(
	struct filter_table *ftab,
	struct value_table *vtab)
{
	if (filter_table_init(ftab, vtab->h_dim, vtab->v_dim))
		return -1;

	memcpy(ftab->values, vtab->values, sizeof(uint32_t) * vtab->h_dim * vtab->v_dim);
	return 0;
}

int
ipfw_packet_filter_create(
	struct ipfw_filter_action *actions,
	uint32_t count,
	struct ipfw_packet_filter *filter)
{
	struct value_registry src_net6_hi_registry;

	struct value_registry src_net6_lo_registry;

	struct value_registry dst_net6_hi_registry;

	struct value_registry dst_net6_lo_registry;

	collect_network_values(
		actions,
		count,
		action_get_net6_src,
		net6_get_hi_part,
		&filter->src_net6_hi,
		&src_net6_hi_registry);

	collect_network_values(
		actions,
		count,
		action_get_net6_src,
		net6_get_lo_part,
		&filter->src_net6_lo,
		&src_net6_lo_registry);

	collect_network_values(
		actions,
		count,
		action_get_net6_dst,
		net6_get_hi_part,
		&filter->dst_net6_hi,
		&dst_net6_hi_registry);

	collect_network_values(
		actions,
		count,
		action_get_net6_dst,
		net6_get_lo_part,
		&filter->dst_net6_lo,
		&dst_net6_lo_registry);

	struct value_table src_port_vtab;
	struct value_table dst_port_vtab;
	struct value_registry src_port_registry;
	struct value_registry dst_port_registry;

	collect_port_values(
		actions,
		count,
		get_port_range_src,
		&src_port_vtab,
		&src_port_registry);

	collect_port_values(
		actions,
		count,
		get_port_range_dst,
		&dst_port_vtab,
		&dst_port_registry);

	fprintf(stderr, "SRC_HI_R\n");
	print_vreg(&src_net6_hi_registry);

	fprintf(stderr, "DST_HI_R\n");
	print_vreg(&dst_net6_hi_registry);

	fprintf(stderr, "SRC_LO_R\n");
	print_vreg(&src_net6_lo_registry);

	fprintf(stderr, "DST_LO_R\n");
	print_vreg(&dst_net6_lo_registry);

	fprintf(stderr, "SRC PORT\n");
	print_vreg(&src_port_registry);
	fprintf(stderr, "DST PORT\n");
	print_vreg(&dst_port_registry);

	struct value_table vtab1;
	struct value_registry vtab1_registry;
	merge_and_collect_registry(
		&src_net6_hi_registry,
		&dst_net6_hi_registry,
		&vtab1,
		&vtab1_registry);

	fprintf(stderr, "vtab1\n");
	print_vtab(&vtab1);
	fprintf(stderr, "vtab1_reg\n");
	print_vreg(&vtab1_registry);

	struct value_table vtab2;
	struct value_registry vtab2_registry;
	merge_and_collect_registry(
		&src_net6_lo_registry,
		&dst_net6_lo_registry,
		&vtab2,
		&vtab2_registry);

	fprintf(stderr, "vtab2\n");
	print_vtab(&vtab2);
	fprintf(stderr, "vtab2_reg\n");
	print_vreg(&vtab2_registry);

	struct value_table vtab3;
	struct value_registry vtab3_registry;
	merge_and_collect_registry(
		&src_port_registry,
		&dst_port_registry,
		&vtab3,
		&vtab3_registry);

	fprintf(stderr, "vtab3\n");
	print_vtab(&vtab3);
	fprintf(stderr, "vtab3_reg\n");
	print_vreg(&vtab3_registry);

	struct value_table vtab12;
	struct value_registry vtab12_registry;
	merge_and_collect_registry(
		&vtab1_registry,
		&vtab2_registry,
		&vtab12,
		&vtab12_registry);

	fprintf(stderr, "vtab12\n");
	print_vtab(&vtab12);
	fprintf(stderr, "vtab12_reg\n");
	print_vreg(&vtab12_registry);

	struct value_table vtab123;
	struct value_registry vtab123_registry;
	set_registry_values(
		&vtab12_registry,
		&vtab3_registry,
		&vtab123,
		&vtab123_registry);

	fprintf(stderr, "vtab123\n");
	print_vtab(&vtab123);
	fprintf(stderr, "vtab123_reg\n");
	print_vreg(&vtab123_registry);



	filter->classify[0] = filter_classify_src_net_hi;
	filter->classify[1] = filter_classify_src_net_lo;
	filter->classify[2] = filter_classify_dst_net_hi;
	filter->classify[3] = filter_classify_dst_net_lo;
	filter->classify[4] = filter_classify_src_port;
	filter->classify[5] = filter_classify_dst_port;
	filter->filter.classify_count = 6;
	filter->filter.classify = filter->classify;

	// src hi X dst hi
	filter->lookups[0] = (struct filter_lookup){
		.first_arg = 0,
		.second_arg = 1,
		.table_idx = 0,
	};
	// src lo X dst lo
	filter->lookups[1] = (struct filter_lookup){
		.first_arg = 2,
		.second_arg = 3,
		.table_idx = 1,
	};
	// src port X dst port
	filter->lookups[2] = (struct filter_lookup){
		.first_arg = 4,
		.second_arg = 5,
		.table_idx = 2,
	};
	// src X dst
	filter->lookups[3] = (struct filter_lookup){
		.first_arg = 6,
		.second_arg = 7,
		.table_idx = 3,
	};
	// net X port
	filter->lookups[4] = (struct filter_lookup){
		.first_arg = 9,
		.second_arg = 8,
		.table_idx = 4,
	};

	filter->filter.lookup_count = 5;
	filter->filter.lookups = filter->lookups;

	filter_table_copy(filter->tables + 0, &vtab1);
	filter_table_copy(filter->tables + 1, &vtab2);
	filter_table_copy(filter->tables + 2, &vtab3);
	filter_table_copy(filter->tables + 3, &vtab12);
	filter_table_copy(filter->tables + 4, &vtab123);

	filter->filter.tables = filter->tables;

	return 0;
}
