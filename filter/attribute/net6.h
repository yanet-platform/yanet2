#pragma once

#include "../helper.h"
#include "../rule.h"
#include "common/lpm.h"
#include "common/range_collector.h"

#include "util.h"

#include "common/registry.h"
#include "dataplane/packet/packet.h"

#include <endian.h>
#include <rte_ip.h>
#include <rte_mbuf.h>

////////////////////////////////////////////////////////////////////////////////

typedef void (*action_get_net6_func)(
	const struct filter_rule *rule, struct net6 **net, uint32_t *count
);

static inline void
action_get_net6_src(
	const struct filter_rule *rule, struct net6 **net, uint32_t *count
) {
	*net = rule->net6.srcs;
	*count = rule->net6.src_count;
}

static inline void
action_get_net6_dst(
	const struct filter_rule *action, struct net6 **net, uint32_t *count
) {
	*net = action->net6.dsts;
	*count = action->net6.dst_count;
}

////////////////////////////////////////////////////////////////////////////////

struct net6_part {
	// Bytes in host byte order
	uint64_t bytes;

	// Bytes in host byte order
	uint64_t mask;
};

typedef struct net6_part (*net6_get_part_func)(struct net6 *net);

static struct net6_part
net6_get_hi_part(struct net6 *net) {
	struct net6_part part;
	part.bytes = 0;
	for (size_t i = 0, shift = 0; i < NET6_LEN / 2; ++i, shift += 8) {
		part.bytes |= ((uint64_t)net->ip[NET6_LEN / 2 + i]) << shift;
	}
	part.mask = -1ull << (64 - net->pref_hi);
	part.mask = be64toh(part.mask);
	part.bytes &= part.mask;
	return part;
}

static struct net6_part
net6_get_lo_part(struct net6 *net) {
	struct net6_part part;
	part.bytes = 0;
	for (size_t i = 0, shift = 0; i < NET6_LEN / 2; ++i, shift += 8) {
		part.bytes |= ((uint64_t)net->ip[i]) << shift;
	}
	part.mask = -1ull << (64 - net->pref_lo);
	part.mask = be64toh(part.mask);
	part.bytes &= part.mask;
	return part;
}
////////////////////////////////////////////////////////////////////////////////

static inline void
net6_collect_values(
	struct net6 *start,
	uint32_t count,
	net6_get_part_func get_part,
	struct lpm *lpm,
	struct value_table *table
) {
	for (struct net6 *net6 = start; net6 < start + count; ++net6) {
		struct net6_part part = get_part(net6);
		uint64_t to = part.bytes | ~part.mask;
		lpm8_collect_values(
			lpm,
			(uint8_t *)&part.bytes,
			(uint8_t *)&to,
			lpm_collect_value_iterator,
			table
		);
	}
}

////////////////////////////////////////////////////////////////////////////////

static inline void
net6_collect_registry(
	struct net6 *start,
	uint32_t count,
	net6_get_part_func get_part,
	struct lpm *lpm,
	struct value_registry *registry
) {
	for (struct net6 *net6 = start; net6 < start + count; ++net6) {
		struct net6_part part = get_part(net6);
		uint64_t to = part.bytes | ~part.mask;
		lpm8_collect_values(
			lpm,
			(uint8_t *)&part.bytes,
			(uint8_t *)&to,
			lpm_collect_registry_iterator,
			registry
		);
	}
}

////////////////////////////////////////////////////////////////////////////////

static int
collect_net6_values(
	struct memory_context *memory_context,
	const struct filter_rule *rules,
	uint32_t count,
	action_get_net6_func get_net6,
	net6_get_part_func get_part,
	struct lpm *lpm,
	struct value_registry *registry
) {
	struct range_collector collector;
	if (range_collector_init(&collector, memory_context))
		goto error;

	for (const struct filter_rule *rule = rules; rule < rules + count;
	     ++rule) {

		if (rule->net6.src_count == 0 && rule->net6.dst_count == 0) {
			continue;
		}

		struct net6 *nets;
		uint32_t net_count;
		get_net6(rule, &nets, &net_count);

		for (struct net6 *net6 = nets; net6 < nets + net_count;
		     ++net6) {
			struct net6_part part = get_part(net6);
			if (range8_collector_add(
				    &collector,
				    (uint8_t *)&part.bytes,
				    __builtin_popcountll(part.mask)
			    ))
				goto error_collector;
		}
	}
	if (lpm_init(lpm, memory_context)) {
		goto error_lpm;
	}
	if (range_collector_collect(&collector, 8, lpm)) {
		goto error_collector;
	}

	struct value_table table;
	if (value_table_init(&table, memory_context, 1, collector.count))
		goto error_vtab;

	for (const struct filter_rule *action = rules; action < rules + count;
	     ++action) {
		if (action->net6.dst_count == 0 &&
		    action->net6.src_count == 0) {
			continue;
		}

		value_table_new_gen(&table);

		struct net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);

		net6_collect_values(nets, net_count, get_part, lpm, &table);
	}

	value_table_compact(&table);
	lpm8_remap(lpm, &table);
	lpm8_compact(lpm);

	if (value_registry_init(registry, memory_context))
		goto error_reg;

	for (const struct filter_rule *action = rules; action < rules + count;
	     ++action) {
		if (action->net6.dst_count == 0 &&
		    action->net6.src_count == 0) {
			continue;
		}

		value_registry_start(registry);

		struct net6 *nets;
		uint32_t net_count;
		get_net6(action, &nets, &net_count);

		net6_collect_registry(nets, net_count, get_part, lpm, registry);
	}

	value_table_free(&table);
	return 0;

error_reg:
	value_table_free(&table);
error_collector:
	range_collector_free(&collector, 8);
error_lpm:
	lpm_free(lpm);

error_vtab:

error:
	return -1;
}

////////////////////////////////////////////////////////////////////////////////

struct net6_classifier {
	struct lpm hi;
	struct lpm lo;
	struct value_table comb;
};

////////////////////////////////////////////////////////////////////////////////
// Initialization
////////////////////////////////////////////////////////////////////////////////

static inline int
init_net6(
	struct value_registry *registry,
	action_get_net6_func get_net6,
	void **data,
	const struct filter_rule *rules,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct net6_classifier *net6 =
		memory_balloc(memory_context, sizeof(struct net6_classifier));
	*data = net6;

	struct value_registry hi_registry;
	int res = collect_net6_values(
		memory_context,
		rules,
		actions_count,
		get_net6,
		net6_get_hi_part,
		&net6->hi,
		&hi_registry
	);
	if (res < 0) {
		goto free;
	}

	struct value_registry lo_registry;
	res = collect_net6_values(
		memory_context,
		rules,
		actions_count,
		get_net6,
		net6_get_lo_part,
		&net6->lo,
		&lo_registry
	);
	if (res < 0) {
		goto free;
	}

	res = merge_and_collect_registry(
		memory_context,
		&hi_registry,
		&lo_registry,
		&net6->comb,
		registry
	);

free:
	value_registry_free(&hi_registry);
	value_registry_free(&lo_registry);

	return res;
}

// Allows to initialize attribute for IPv6 destination address.
static inline int
init_net6_src(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t actions_count,
	struct memory_context *memory_context
) {
	return init_net6(
		registry,
		action_get_net6_src,
		data,
		rules,
		actions_count,
		memory_context
	);
}

// Allows to initialize attribute for IPv6 source address.
static inline int
init_net6_dst(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *rules,
	size_t actions_count,
	struct memory_context *memory_context
) {
	return init_net6(
		registry,
		action_get_net6_dst,
		data,
		rules,
		actions_count,
		memory_context
	);
}

////////////////////////////////////////////////////////////////////////////////
// Lookup
////////////////////////////////////////////////////////////////////////////////

// Allows to lookup classifier for packet IPv6 destination address.
static inline uint32_t
lookup_net6_dst(struct packet *packet, void *data) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	struct net6_classifier *c = (struct net6_classifier *)data;
	uint32_t hi = lpm8_lookup(&c->hi, (const uint8_t *)ipv6_hdr->dst_addr);
	uint32_t lo =
		lpm8_lookup(&c->lo, (const uint8_t *)ipv6_hdr->dst_addr + 8);

	return value_table_get(&c->comb, hi, lo);
}

// Allows to lookup classifier for packet IPv6 destination address.
static inline uint32_t
lookup_net6_src(struct packet *packet, void *data) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);

	struct net6_classifier *c = (struct net6_classifier *)data;
	uint32_t hi = lpm8_lookup(&c->hi, (const uint8_t *)ipv6_hdr->src_addr);
	uint32_t lo =
		lpm8_lookup(&c->lo, (const uint8_t *)ipv6_hdr->src_addr + 8);

	return value_table_get(&c->comb, hi, lo);
}

////////////////////////////////////////////////////////////////////////////////
// Free
////////////////////////////////////////////////////////////////////////////////

// Allows to free data for IPv6 classification.
static inline void
free_net6(void *data, struct memory_context *memory_context) {
	(void)memory_context;
	struct net6_classifier *c = (struct net6_classifier *)data;
	lpm_free(&c->lo);
	lpm_free(&c->hi);
	value_table_free(&c->comb);
}