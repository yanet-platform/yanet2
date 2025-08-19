#include "../helper.h"
#include "../rule.h"
#include "common/lpm.h"
#include "common/range_collector.h"
#include "dataplane/packet/packet.h"

#include "util.h"

#include <rte_ip.h>
#include <rte_mbuf.h>

////////////////////////////////////////////////////////////////////////////////

typedef void (*rule_get_net4_func)(
	const struct filter_rule *rule, struct net4 **net, uint32_t *count
);

static inline void
action_get_net4_src(
	const struct filter_rule *rule, struct net4 **net, uint32_t *count
) {
	*net = rule->net4.srcs;
	*count = rule->net4.src_count;
}

static inline void
action_get_net4_dst(
	const struct filter_rule *action, struct net4 **net, uint32_t *count
) {
	*net = action->net4.dsts;
	*count = action->net4.dst_count;
}

static inline void
net4_collect_values(
	struct net4 *start,
	uint32_t count,
	struct lpm *lpm,
	struct value_table *table
) {
	for (struct net4 *net4 = start; net4 < start + count; ++net4) {
		uint32_t addr = htobe32(net4->addr);
		uint32_t mask = htobe32(net4->mask);
		uint32_t to = addr | ~mask;
		lpm4_collect_values(
			lpm,
			(uint8_t *)&addr,
			(uint8_t *)&to,
			lpm_collect_value_iterator,
			table
		);
	}
}

static inline void
net4_collect_registry(
	struct net4 *start,
	uint32_t count,
	struct lpm *lpm,
	struct value_registry *registry
) {
	for (struct net4 *net4 = start; net4 < start + count; ++net4) {
		uint32_t addr = htobe32(net4->addr);
		uint32_t mask = htobe32(net4->mask);
		uint32_t to = addr | ~mask;
		lpm4_collect_values(
			lpm,
			(uint8_t *)&addr,
			(uint8_t *)&to,
			lpm_collect_registry_iterator,
			registry
		);
	}
}

static inline int
collect_net4_values(
	struct memory_context *memory_context,
	const struct filter_rule *actions,
	uint32_t count,
	rule_get_net4_func get_net4,
	struct lpm *lpm,
	struct value_registry *registry
) {

	struct range_collector collector;
	if (range_collector_init(&collector, memory_context))
		goto error;

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {
		if (action->net4.src_count == 0 &&
		    action->net4.dst_count == 0) {
			continue;
		}

		struct net4 *nets;
		uint32_t net_count;
		get_net4(action, &nets, &net_count);

		for (struct net4 *net4 = nets; net4 < nets + net_count;
		     ++net4) {
			uint32_t addr = htobe32(net4->addr);
			if (range4_collector_add(
				    &collector,
				    (uint8_t *)&addr,
				    __builtin_popcountll(net4->mask)
			    ))
				goto error_collector;
		}
	}
	if (lpm_init(lpm, memory_context)) {
		goto error_lpm;
	}
	if (range_collector_collect(&collector, 4, lpm)) {
		goto error_collector;
	}

	struct value_table table;
	if (value_table_init(&table, memory_context, 1, collector.count))
		goto error_vtab;

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {

		value_table_new_gen(&table);

		struct net4 *nets;
		uint32_t net_count;
		get_net4(action, &nets, &net_count);

		net4_collect_values(nets, net_count, lpm, &table);
	}

	value_table_compact(&table);
	lpm4_remap(lpm, &table);
	lpm4_compact(lpm);

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {
		value_registry_start(registry);

		struct net4 *nets;
		uint32_t net_count;
		get_net4(action, &nets, &net_count);

		net4_collect_registry(nets, net_count, lpm, registry);
	}

	value_table_free(&table);
	return 0;

error_collector:
	range_collector_free(&collector, 4);
error_lpm:
	lpm_free(lpm);

error_vtab:

error:
	return -1;
}

////////////////////////////////////////////////////////////////////////////////

// Allows to initialize attribute for IPv4 source address.
static inline int
init_net4_src(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct lpm *lpm = memory_balloc(memory_context, sizeof(struct lpm));
	*data = lpm;
	return collect_net4_values(
		memory_context,
		actions,
		actions_count,
		action_get_net4_src,
		lpm,
		registry
	);
}

// Allows to lookup classifier for packet IPv4 source address.
static inline uint32_t
lookup_net4_src(struct packet *packet, void *data) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);
	struct lpm *lpm = (struct lpm *)data;
	return lpm4_lookup(lpm, (uint8_t *)&ipv4_hdr->src_addr);
}

// Allows to initialize attribute for IPv4 destination address.
static inline int
init_net4_dst(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct lpm *lpm = memory_balloc(memory_context, sizeof(struct lpm));
	*data = lpm;
	return collect_net4_values(
		memory_context,
		actions,
		actions_count,
		action_get_net4_dst,
		lpm,
		registry
	);
}

// Allows to lookup classifier for packet IPv4 destination address.
static inline uint32_t
lookup_net4_dst(struct packet *packet, void *data) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv4_hdr *, packet->network_header.offset
	);

	struct lpm *lpm = (struct lpm *)data;

	return lpm4_lookup(lpm, (uint8_t *)&ipv4_hdr->dst_addr);
}

// Allows to free data for IPv4 classification.
static inline void
free_net4(void *data, struct memory_context *memory_context) {
	(void)memory_context;
	struct lpm *lpm = (struct lpm *)data;
	lpm_free(lpm);
}