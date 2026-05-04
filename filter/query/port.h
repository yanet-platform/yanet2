#pragma once

#include "common/value.h"
#include "lib/dataplane/packet/packet.h"

#include <netinet/in.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <stdint.h>

#include "declare.h"

#include "filter/classifiers/port.h"

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
		// non tcp/udp
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
		// non tcp/udp
		return 0;
	}
}

typedef uint16_t (*packet_get_port_func)(const struct packet *packet);

struct filter_query_attr_port_handlers {
	struct filter_query_attr_handlers attr_handlers;
	packet_get_port_func get_port;
};

static inline void
filter_query_attr_port_lookup(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *attr_handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	const struct filter_query_attr_port_handlers *port_handlers =
		container_of(
			attr_handlers,
			struct filter_query_attr_port_handlers,
			attr_handlers
		);

	const struct filter_query_attr_port *port_attr =
		container_of(attr, struct filter_query_attr_port, attr);

	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const uint16_t port = port_handlers->get_port(packets[idx]);
		results[idx] =
			*value_table_get_ptr(&port_attr->value_table, 0, port);
	}
}

static inline uint16_t
filter_packet_get_port_src(const struct packet *packet) {
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
	}
	// unreachable
	return 0;
}

static inline uint16_t
filter_packet_get_port_dst(const struct packet *packet) {
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
	}
	// unreachable
	return 0;
}

static const struct filter_query_attr_handlers filter_query_port = {
	.lookup = filter_query_attr_port_lookup,
};

static const struct filter_query_attr_port_handlers filter_query_attr_port_src =
	{
		.attr_handlers = filter_query_port,
		.get_port = filter_packet_get_port_src,
};

static const struct filter_query_attr_port_handlers filter_query_attr_port_dst =
	{
		.attr_handlers = filter_query_port,
		.get_port = filter_packet_get_port_dst,
};
