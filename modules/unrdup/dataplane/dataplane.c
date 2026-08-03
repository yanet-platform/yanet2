#include "dataplane.h"

#include "config.h"

#include <netinet/icmp6.h>
#include <netinet/ip_icmp.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "common/container_of.h"
#include "common/crc32.h"
#include "common/memory_address.h"

#include "lib/dataplane/module/module.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/encap.h"
#include "lib/dataplane/packet/icmp.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/dataplane/worker/worker.h"
#include "lib/filter/query.h"

FILTER_QUERY_DECLARE(unrdup_query4, net4_src, port_src);
FILTER_QUERY_DECLARE(unrdup_query6, net6_src, port_src);

#define UNRDUP_TUNNEL_TTL 64

struct unrdup_module {
	struct module module;
};

struct unrdup_fanout {
	struct dp_worker *dp_worker;
	struct unrdup_module_config *config;
	struct packet_front *packet_front;
	struct counter_storage *counter_storage;
};

static inline void
unrdup_count_event(
	struct counter_storage *counter_storage, uint64_t counter_id
) {
	uint64_t *counters = counter_get_address(counter_id, counter_storage);
	counters[0] += 1;
}

static inline void
unrdup_count_packet(
	struct counter_storage *counter_storage,
	uint64_t counter_id,
	uint16_t bytes
) {
	uint64_t *counters = counter_get_address(counter_id, counter_storage);
	counters[0] += 1;
	counters[1] += bytes;
}

struct unrdup_offending_flow {
	struct net_addr vip;
	enum ip_family family;
	uint8_t proto;
	uint32_t entropy;
	uint32_t network_offset;
	uint32_t transport_offset;
};

static int
unrdup_outer_end(struct packet *packet, uint32_t *end) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint16_t offset = packet->network_header.offset;
	uint16_t data_len = packet_data_len(packet);

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		if (offset + sizeof(struct rte_ipv4_hdr) > data_len) {
			return -1;
		}

		const struct rte_ipv4_hdr *outer = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv4_hdr *, offset
		);

		if ((outer->version_ihl >> 4) != 4) {
			return -1;
		}

		*end = offset + rte_be_to_cpu_16(outer->total_length);
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		if (offset + sizeof(struct rte_ipv6_hdr) > data_len) {
			return -1;
		}

		const struct rte_ipv6_hdr *outer = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv6_hdr *, offset
		);

		if ((rte_be_to_cpu_32(outer->vtc_flow) >> 28) != 6) {
			return -1;
		}

		*end = offset + sizeof(struct rte_ipv6_hdr) +
		       rte_be_to_cpu_16(outer->payload_len);
	} else {
		return -1;
	}

	if (*end > data_len) {
		*end = data_len;
	}

	return 0;
}

static int
unrdup_is_icmp4_error(uint8_t type) {
	return type == ICMP_DEST_UNREACH || type == ICMP_TIME_EXCEEDED ||
	       type == ICMP_PARAMETERPROB;
}

static int
unrdup_is_icmp6_error(uint8_t type) {
	return type == ICMP6_DST_UNREACH || type == ICMP6_PACKET_TOO_BIG ||
	       type == ICMP6_TIME_EXCEEDED || type == ICMP6_PARAM_PROB;
}

static int
unrdup_is_ipv6_ext(uint8_t proto) {
	return proto == IPPROTO_HOPOPTS || proto == IPPROTO_ROUTING ||
	       proto == IPPROTO_DSTOPTS || proto == IPPROTO_AH ||
	       proto == IPPROTO_FRAGMENT;
}

static int
unrdup_skip_ipv6_ext(
	struct rte_mbuf *mbuf, uint32_t end, uint8_t *proto, uint32_t *offset
) {
	while (unrdup_is_ipv6_ext(*proto)) {
		if (*offset + 8 > end) {
			return -1;
		}

		uint32_t length;
		uint8_t next;

		if (*proto == IPPROTO_FRAGMENT) {
			const struct yanet_ipv6_ext_fragment *ext =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct yanet_ipv6_ext_fragment *,
					*offset
				);

			if ((rte_be_to_cpu_16(ext->offset_flag) &
			     RTE_IPV6_EHDR_FO_MASK) != 0) {
				return -1;
			}

			next = ext->next_header;
			length = RTE_IPV6_FRAG_HDR_SIZE;
		} else {
			const struct yanet_ipv6_ext_2byte *ext =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct yanet_ipv6_ext_2byte *,
					*offset
				);

			next = ext->next_header;

			if (*proto == IPPROTO_AH) {
				if (ext->extension_length < 1) {
					return -1;
				}

				length = (2 + (uint32_t)ext->extension_length) *
					 4;
			} else {
				length = (1 + (uint32_t)ext->extension_length) *
					 8;
			}
		}

		if (*offset + length > end) {
			return -1;
		}

		*proto = next;
		*offset += length;
	}

	return 0;
}

static int
unrdup_is_tunneled_icmp_error(struct packet *packet, uint32_t outer_end) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint32_t offset = packet->transport_header.offset;

	if (packet->transport_header.type == IPPROTO_IPIP) {
		if (offset + sizeof(struct rte_ipv4_hdr) > outer_end) {
			return 0;
		}

		const struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv4_hdr *, offset
		);

		if ((inner->version_ihl >> 4) != 4 ||
		    inner->next_proto_id != IPPROTO_ICMP) {
			return 0;
		}

		uint8_t header_len = rte_ipv4_hdr_len(inner);
		if (header_len < sizeof(struct rte_ipv4_hdr)) {
			return 0;
		}

		uint32_t icmp_offset = offset + header_len;
		if (icmp_offset + sizeof(struct yanet_icmp_hdr) > outer_end) {
			return 0;
		}

		const struct yanet_icmp_hdr *icmp = rte_pktmbuf_mtod_offset(
			mbuf, struct yanet_icmp_hdr *, icmp_offset
		);

		return unrdup_is_icmp4_error(icmp->icmp_type);
	}

	if (packet->transport_header.type == IPPROTO_IPV6) {
		if (offset + sizeof(struct rte_ipv6_hdr) > outer_end) {
			return 0;
		}

		const struct rte_ipv6_hdr *inner = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv6_hdr *, offset
		);

		if ((rte_be_to_cpu_32(inner->vtc_flow) >> 28) != 6) {
			return 0;
		}

		uint8_t proto = inner->proto;
		uint32_t icmp_offset = offset + sizeof(struct rte_ipv6_hdr);

		if (unrdup_skip_ipv6_ext(
			    mbuf, outer_end, &proto, &icmp_offset
		    )) {
			return 0;
		}

		if (proto != IPPROTO_ICMPV6) {
			return 0;
		}

		if (icmp_offset + sizeof(struct yanet_icmp6_hdr) > outer_end) {
			return 0;
		}

		const struct yanet_icmp6_hdr *icmp = rte_pktmbuf_mtod_offset(
			mbuf, struct yanet_icmp6_hdr *, icmp_offset
		);

		return unrdup_is_icmp6_error(icmp->icmp6_type);
	}

	return 0;
}

static int
unrdup_is_icmp_error(struct packet *packet, uint32_t outer_end) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint16_t offset = packet->transport_header.offset;

	if (packet->flags & (1 << PACKET_FLAG_FRAGMENTED)) {
		return 0;
	}

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		if (packet->transport_header.type != IPPROTO_ICMP ||
		    offset + sizeof(struct yanet_icmp_hdr) > outer_end) {
			return 0;
		}

		const struct yanet_icmp_hdr *icmp = rte_pktmbuf_mtod_offset(
			mbuf, struct yanet_icmp_hdr *, offset
		);

		return unrdup_is_icmp4_error(icmp->icmp_type);
	}

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		if (packet->transport_header.type != IPPROTO_ICMPV6 ||
		    offset + sizeof(struct yanet_icmp6_hdr) > outer_end) {
			return 0;
		}

		const struct yanet_icmp6_hdr *icmp = rte_pktmbuf_mtod_offset(
			mbuf, struct yanet_icmp6_hdr *, offset
		);

		return unrdup_is_icmp6_error(icmp->icmp6_type);
	}

	return 0;
}

static int
unrdup_parse_offending_flow(
	struct packet *packet,
	uint32_t outer_end,
	struct unrdup_offending_flow *flow
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint32_t transport_offset = 0;

	uint32_t quoted_end = 0;
	uint32_t declared_end = 0;

	memset(flow, 0, sizeof(*flow));

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		uint32_t inner_offset = packet->transport_header.offset +
					sizeof(struct yanet_icmp_hdr);

		if (inner_offset + sizeof(struct rte_ipv4_hdr) > outer_end) {
			return -1;
		}

		const struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv4_hdr *, inner_offset
		);

		if ((inner->version_ihl >> 4) != 4) {
			return -1;
		}

		uint8_t header_len = rte_ipv4_hdr_len(inner);
		if (header_len < sizeof(struct rte_ipv4_hdr)) {
			return -1;
		}

		uint16_t total_length = rte_be_to_cpu_16(inner->total_length);
		if (total_length < header_len) {
			return -1;
		}

		declared_end = inner_offset + total_length;

		quoted_end = declared_end;
		if (quoted_end > outer_end) {
			quoted_end = outer_end;
		}

		uint16_t fragment_offset =
			rte_be_to_cpu_16(inner->fragment_offset);
		if ((fragment_offset & RTE_IPV4_HDR_OFFSET_MASK) != 0) {
			return -1;
		}

		flow->family = ip_family_ip4;
		memcpy(flow->vip.v4.bytes, &inner->src_addr, NET4_LEN);
		flow->proto = inner->next_proto_id;
		flow->network_offset = inner_offset;
		transport_offset = inner_offset + header_len;

		flow->entropy = crc32(&inner->src_addr, NET4_LEN, 0);
		flow->entropy =
			crc32(&inner->dst_addr, NET4_LEN, flow->entropy);
	} else {
		uint32_t inner_offset = packet->transport_header.offset +
					sizeof(struct yanet_icmp6_hdr);

		if (inner_offset + sizeof(struct rte_ipv6_hdr) > outer_end) {
			return -1;
		}

		const struct rte_ipv6_hdr *inner = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv6_hdr *, inner_offset
		);

		if ((rte_be_to_cpu_32(inner->vtc_flow) >> 28) != 6) {
			return -1;
		}

		declared_end = inner_offset + sizeof(struct rte_ipv6_hdr) +
			       rte_be_to_cpu_16(inner->payload_len);

		quoted_end = declared_end;
		if (quoted_end > outer_end) {
			quoted_end = outer_end;
		}

		flow->family = ip_family_ip6;
		memcpy(flow->vip.v6.bytes, &inner->src_addr, NET6_LEN);
		flow->proto = inner->proto;
		flow->network_offset = inner_offset;
		transport_offset = inner_offset + sizeof(struct rte_ipv6_hdr);

		flow->entropy = crc32(&inner->src_addr, NET6_LEN, 0);
		flow->entropy =
			crc32(&inner->dst_addr, NET6_LEN, flow->entropy);

		if (unrdup_skip_ipv6_ext(
			    mbuf, quoted_end, &flow->proto, &transport_offset
		    )) {
			return -1;
		}
	}

	if (flow->proto != IPPROTO_TCP && flow->proto != IPPROTO_UDP) {
		return -1;
	}

	uint32_t transport_len = flow->proto == IPPROTO_TCP
					 ? sizeof(struct rte_tcp_hdr)
					 : sizeof(struct rte_udp_hdr);
	if (transport_offset + transport_len > declared_end) {
		return -1;
	}

	if (transport_offset + sizeof(uint16_t) > quoted_end) {
		return -1;
	}

	uint64_t ports_len =
		transport_offset + 2 * sizeof(uint16_t) <= quoted_end
			? 2 * sizeof(uint16_t)
			: sizeof(uint16_t);

	flow->entropy = crc32(
		rte_pktmbuf_mtod_offset(mbuf, uint8_t *, transport_offset),
		ports_len,
		flow->entropy
	);

	flow->transport_offset = transport_offset;

	flow->entropy ^= flow->entropy >> 16;
	flow->entropy ^= flow->entropy >> 8;

	return 0;
}

static int
unrdup_is_addressed_to_vip(
	struct packet *packet, const struct unrdup_offending_flow *flow
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint16_t offset = packet->network_header.offset;

	if (flow->family == ip_family_ip4) {
		const struct rte_ipv4_hdr *outer = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_ipv4_hdr *, offset
		);

		return memcmp(&outer->dst_addr, flow->vip.v4.bytes, NET4_LEN) ==
		       0;
	}

	const struct rte_ipv6_hdr *outer =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ipv6_hdr *, offset);

	return memcmp(&outer->dst_addr, flow->vip.v6.bytes, NET6_LEN) == 0;
}

static struct unrdup_service *
unrdup_find_service(
	struct unrdup_module_config *config,
	struct packet *packet,
	const struct unrdup_offending_flow *flow
) {
	struct filter *filter;
	const struct filter_query *query;
	struct unrdup_endpoint *endpoints;
	uint64_t endpoint_count;
	uint16_t network_type;

	if (flow->family == ip_family_ip4) {
		filter = ADDR_OF(&config->filter4);
		query = unrdup_query4;
		endpoints = ADDR_OF(&config->endpoints4);
		endpoint_count = config->endpoint4_count;
		network_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
	} else {
		filter = ADDR_OF(&config->filter6);
		query = unrdup_query6;
		endpoints = ADDR_OF(&config->endpoints6);
		endpoint_count = config->endpoint6_count;
		network_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
	}

	if (endpoint_count == 0 || filter == NULL) {
		return NULL;
	}

	// The attributes read a packet's own headers, so the quoted headers are
	// offered as a packet of their own over the same mbuf.
	struct packet quoted = *packet;
	quoted.network_header.type = network_type;
	quoted.network_header.offset = (uint16_t)flow->network_offset;
	quoted.transport_header.type = flow->proto;
	quoted.transport_header.offset = (uint16_t)flow->transport_offset;

	struct packet *quoted_ptr = &quoted;
	uint32_t action = FILTER_RULE_INVALID;
	filter_query(filter, query, &quoted_ptr, &action, 1);

	if (action == FILTER_RULE_INVALID || action >= endpoint_count) {
		return NULL;
	}

	const struct unrdup_endpoint *endpoint = endpoints + action;

	uint32_t bit = flow->proto == IPPROTO_TCP ? UNRDUP_PROTO_TCP_BIT
						  : UNRDUP_PROTO_UDP_BIT;
	if ((endpoint->proto_mask & bit) == 0) {
		return NULL;
	}

	if (endpoint->service_idx >= config->service_count) {
		return NULL;
	}

	return ADDR_OF(&config->services) + endpoint->service_idx;
}

static void
unrdup_avoid_reserved_v4(const uint8_t *mask, uint8_t *source) {
	uint32_t host_mask = 0;
	uint32_t value = 0;

	for (uint8_t idx = 0; idx < NET4_LEN; ++idx) {
		host_mask = (host_mask << 8) | (uint8_t)~mask[idx];
		value = (value << 8) | source[idx];
	}

	if (__builtin_popcount(host_mask) < 2) {
		return;
	}

	uint32_t lowest = host_mask & (~host_mask + 1);
	uint32_t host = value & host_mask;

	if (host == 0) {
		value |= lowest;
	} else if (host == host_mask) {
		value &= ~lowest;
	} else {
		return;
	}

	for (uint8_t idx = 0; idx < NET4_LEN; ++idx) {
		source[NET4_LEN - 1 - idx] = (uint8_t)(value >> (idx * 8));
	}
}

static void
unrdup_avoid_reserved_v6(const uint8_t *mask, uint8_t *source) {
	uint32_t free_bits = 0;
	uint8_t lowest_byte = 0;
	uint8_t lowest_bit = 0;
	int host_is_zero = 1;

	for (uint8_t idx = 0; idx < NET6_LEN; ++idx) {
		uint8_t host = (uint8_t)~mask[idx];

		free_bits += (uint32_t)__builtin_popcount(host);
		if (host != 0) {
			lowest_byte = idx;
			lowest_bit = (uint8_t)(host & (uint8_t)(~host + 1));
		}
		if ((source[idx] & host) != 0) {
			host_is_zero = 0;
		}
	}

	if (free_bits < 2 || !host_is_zero) {
		return;
	}

	source[lowest_byte] |= lowest_bit;
}

static void
unrdup_source_addr(
	const uint8_t *addr,
	const uint8_t *mask,
	uint32_t entropy,
	uint8_t addr_len,
	uint8_t *source
) {
	for (uint8_t idx = 0; idx < addr_len; ++idx) {
		source[idx] = addr[idx] & mask[idx];
	}

	for (uint8_t idx = 0; idx < sizeof(entropy) && idx < addr_len; ++idx) {
		uint8_t pos = addr_len - 1 - idx;
		source[pos] |= (uint8_t)(entropy >> (idx * 8)) & ~mask[pos];
	}

	if (addr_len == NET4_LEN) {
		unrdup_avoid_reserved_v4(mask, source);
	} else {
		unrdup_avoid_reserved_v6(mask, source);
	}
}

static int
unrdup_source_is_set(const uint8_t *addr, uint8_t addr_len) {
	for (uint8_t idx = 0; idx < addr_len; ++idx) {
		if (addr[idx] != 0) {
			return 1;
		}
	}

	return 0;
}

static void
unrdup_forward_to_peer(
	const struct unrdup_fanout *fanout,
	struct packet *packet,
	const struct unrdup_peer *peer,
	uint32_t entropy
) {
	struct unrdup_module_config *config = fanout->config;

	const struct net *source = peer->family == ip_family_ip4
					   ? &config->source4
					   : &config->source6;
	const uint8_t *source_addr = peer->family == ip_family_ip4
					     ? source->v4.addr
					     : source->v6.addr;
	const uint8_t *source_mask = peer->family == ip_family_ip4
					     ? source->v4.mask
					     : source->v6.mask;
	uint8_t addr_len = peer->family == ip_family_ip4 ? NET4_LEN : NET6_LEN;

	if (!unrdup_source_is_set(source_addr, addr_len)) {
		unrdup_count_event(
			fanout->counter_storage,
			config->peer_no_source_counter_id
		);
		return;
	}

	struct packet *clone = worker_clone_packet(fanout->dp_worker, packet);
	if (clone == NULL) {
		unrdup_count_event(
			fanout->counter_storage, config->clone_failed_counter_id
		);
		return;
	}

	uint8_t outer_source[NET6_LEN];
	unrdup_source_addr(
		source_addr, source_mask, entropy, addr_len, outer_source
	);

	int rc = peer->family == ip_family_ip4 ? packet_ip4_encap(
							 clone,
							 peer->addr.v4.bytes,
							 outer_source,
							 DSCP_MARK_NEVER
						 )
					       : packet_ip6_encap(
							 clone,
							 peer->addr.v6.bytes,
							 outer_source,
							 DSCP_MARK_NEVER
						 );
	if (rc) {
		worker_packet_free(clone);
		unrdup_count_event(
			fanout->counter_storage, config->encap_failed_counter_id
		);
		return;
	}

	clone->transport_header.type =
		packet->network_header.type ==
				rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)
			? IPPROTO_IPIP
			: IPPROTO_IPV6;
	clone->transport_header.offset =
		clone->network_header.offset +
		(peer->family == ip_family_ip4 ? sizeof(struct rte_ipv4_hdr)
					       : sizeof(struct rte_ipv6_hdr));
	clone->hash = entropy;

	if (peer->family == ip_family_ip4) {
		struct rte_ipv4_hdr *outer = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(clone),
			struct rte_ipv4_hdr *,
			clone->network_header.offset
		);

		outer->time_to_live = UNRDUP_TUNNEL_TTL;
		outer->hdr_checksum = 0;
		outer->hdr_checksum = rte_ipv4_cksum(outer);
	} else {
		struct rte_ipv6_hdr *outer = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(clone),
			struct rte_ipv6_hdr *,
			clone->network_header.offset
		);

		outer->hop_limits = UNRDUP_TUNNEL_TTL;
	}

	unrdup_count_packet(
		fanout->counter_storage,
		config->clones_sent_counter_id,
		packet_data_len(clone)
	);

	packet_front_output(fanout->packet_front, clone);
}

void
unrdup_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
) {
	struct unrdup_module_config *config = container_of(
		ADDR_OF(&module_ectx->cp_module),
		struct unrdup_module_config,
		cp_module
	);

	struct counter_storage *counter_storage =
		ADDR_OF_NONNULL(&module_ectx->counter_storage);

	struct unrdup_fanout fanout = {
		.dp_worker = dp_worker,
		.config = config,
		.packet_front = packet_front,
		.counter_storage = counter_storage,
	};

	struct packet *packet;
	while ((packet = packet_list_pop(&packet_front->input)) != NULL) {
		struct unrdup_service *service = NULL;
		struct unrdup_offending_flow flow;
		uint32_t entropy = 0;
		uint32_t outer_end = 0;

		if (unrdup_outer_end(packet, &outer_end)) {
			packet_front_output(packet_front, packet);
			continue;
		}

		// TODO: ask the balancer session table whether this balancer
		// holds the offending flow and drop the clone when it does
		// not, instead of passing every one on. Blocked until
		// balancer2 exposes a shared session table. unrdup has to run
		// ahead of decap for a clone to arrive here still tunnelled.
		if (unrdup_is_tunneled_icmp_error(packet, outer_end)) {
			unrdup_count_packet(
				counter_storage,
				config->tunneled_received_counter_id,
				packet_data_len(packet)
			);
			packet_front_output(packet_front, packet);
			continue;
		}

		if (unrdup_is_icmp_error(packet, outer_end)) {
			if (unrdup_parse_offending_flow(
				    packet, outer_end, &flow
			    )) {
				unrdup_count_event(
					counter_storage,
					config->malformed_counter_id
				);
			} else if (!unrdup_is_addressed_to_vip(packet, &flow)) {
				unrdup_count_event(
					counter_storage,
					config->misaddressed_counter_id
				);
			} else {
				service = unrdup_find_service(
					config, packet, &flow
				);
				if (service == NULL) {
					unrdup_count_event(
						counter_storage,
						config->unserved_counter_id
					);
				} else {
					entropy = flow.entropy;
				}
			}
		}

		if (service == NULL) {
			packet_front_output(packet_front, packet);
			continue;
		}

		unrdup_count_packet(
			counter_storage,
			config->redistributed_counter_id,
			packet_data_len(packet)
		);

		struct unrdup_peer *peers = ADDR_OF(&service->peers);
		for (uint64_t idx = 0; idx < service->peer_count; ++idx) {
			unrdup_forward_to_peer(
				&fanout, packet, peers + idx, entropy
			);
		}

		packet_front_drop(packet_front, packet);
	}
}

struct module *
new_module_unrdup() {
	struct unrdup_module *module =
		(struct unrdup_module *)malloc(sizeof(struct unrdup_module));

	if (module == NULL) {
		return NULL;
	}

	snprintf(
		module->module.name, sizeof(module->module.name), "%s", "unrdup"
	);
	module->module.handler = unrdup_handle_packets;

	return &module->module;
}
