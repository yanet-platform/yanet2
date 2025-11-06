#pragma once

#include "../rule.h"
#include "common/memory.h"
#include "common/registry.h"
#include "dataplane/packet/packet.h"

#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

static inline uint16_t
packet_src_port(const struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		return rte_be_to_cpu_16(tcp_hdr->src_port);
	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);
		return rte_be_to_cpu_16(udp_hdr->src_port);
	} else {
		// TODO
		return 0;
	}
}

static inline uint16_t
packet_dst_port(const struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		return rte_be_to_cpu_16(tcp_hdr->dst_port);
	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);
		return rte_be_to_cpu_16(udp_hdr->dst_port);
	} else {
		// TODO
		return 0;
	}
}

// src port
static inline uint32_t
lookup_port_src(struct packet *packet, void *data) {
	(void)packet;
	struct value_table *table = data;
	return value_table_get(table, 0, packet_src_port(packet));
}

typedef void (*action_get_port_range_func)(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
);

static inline int
collect_port_values(
	struct memory_context *memory_context,
	const struct filter_rule *actions,
	uint32_t count,
	action_get_port_range_func get_port_range,
	struct value_table *table,
	struct value_registry *registry
) {
	if (value_table_init(table, memory_context, 1, 65536))
		return -1;

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {

		value_table_new_gen(table);

		struct filter_port_range *port_ranges;
		uint32_t port_range_count;
		get_port_range(action, &port_ranges, &port_range_count);
		for (struct filter_port_range *ports = port_ranges;
		     ports < port_ranges + port_range_count;
		     ++ports) {
			if (ports->to - ports->from == 65535)
				continue;
			for (uint32_t port = ports->from; port <= ports->to;
			     ++port) {
				value_table_touch(table, 0, port);
			}
		}
	}

	value_table_compact(table);

	for (const struct filter_rule *action = actions;
	     action < actions + count;
	     ++action) {
		value_registry_start(registry);

		struct filter_port_range *port_ranges;
		uint32_t port_range_count;
		get_port_range(action, &port_ranges, &port_range_count);
		for (struct filter_port_range *ports = port_ranges;
		     ports < port_ranges + port_range_count;
		     ++ports) {
			for (uint32_t port = ports->from; port <= ports->to;
			     ++port) {
				value_registry_collect(
					registry,
					value_table_get(table, 0, port)
				);
			}
		}
	}

	return 0;
}

static inline void
get_port_range_src(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
) {
	*ranges = action->transport.srcs;
	*count = action->transport.src_count;
}

static inline void
get_port_range_dst(
	const struct filter_rule *action,
	struct filter_port_range **ranges,
	uint32_t *count
) {
	*ranges = action->transport.dsts;
	*count = action->transport.dst_count;
}

static inline uint32_t
lookup_port_dst(struct packet *packet, void *data) {
	struct value_table *table = data;
	return value_table_get(table, 0, packet_dst_port(packet));
}

static inline int
init_port_dst(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct value_table *table =
		memory_balloc(memory_context, sizeof(struct value_table));
	if (table == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, table);
	return collect_port_values(
		memory_context,
		actions,
		actions_count,
		get_port_range_dst,
		table,
		registry
	);
}

static inline int
init_port_src(
	struct value_registry *registry,
	void **data,
	const struct filter_rule *actions,
	size_t actions_count,
	struct memory_context *memory_context
) {
	struct value_table *table =
		memory_balloc(memory_context, sizeof(struct value_table));
	if (table == NULL) {
		return -1;
	}
	SET_OFFSET_OF(data, table);
	return collect_port_values(
		memory_context,
		actions,
		actions_count,
		get_port_range_src,
		table,
		registry
	);
}

static inline void
free_port(void *data, struct memory_context *memory_context) {
	(void)memory_context;

	struct value_table *table = (struct value_table *)data;
	value_table_free(table);
}