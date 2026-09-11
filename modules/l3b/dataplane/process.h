#pragma once

#include "config.h"

#include <string.h>

#include <netinet/ip_icmp.h>

#include <rte_ether.h>
#include <rte_icmp.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "lib/dataplane/worker/worker.h"

#include "lib/l3state/lookup.h"

#include "objects/l3b/api/l3b_session_table_object.h"
#include "objects/l3b/api/l3b_virtual_service_object.h"

#include "common/network.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/dscp.h"
#include "lib/dataplane/packet/encap.h"
#include "lib/dataplane/packet/icmp.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"

#include <lib/filter/query.h>

// Per-service source filter: classifies incoming packets by source network and
// destination (service) port.
FILTER_QUERY_DECLARE(l3b_source_filter_ip4, net4_src, port_dst);
FILTER_QUERY_DECLARE(l3b_source_filter_ip6, net6_src, port_dst);

// Module-level destination filter: classifies incoming packets by destination
// network and protocol into a virtual service index.
FILTER_QUERY_DECLARE(l3b_destination_filter_ip4, net4_dst, proto_range);
FILTER_QUERY_DECLARE(l3b_destination_filter_ip6, net6_dst, proto_range);

// The ICMP type byte of an initial packet, or -1 when the packet carries
// no ICMP header.
static inline int
l3b_packet_icmp_type(const struct packet *packet) {
	if (packet->fragment_offset != 0) {
		return -1;
	}

	if (packet->transport_header.type == IPPROTO_ICMP) {
		const struct yanet_icmp_hdr *icmp = rte_pktmbuf_mtod_offset(
			packet_to_mbuf((struct packet *)packet),
			const struct yanet_icmp_hdr *,
			packet->transport_header.offset
		);
		return icmp->icmp_type;
	}
	if (packet->transport_header.type == IPPROTO_ICMPV6) {
		const struct yanet_icmp6_hdr *icmp6 = rte_pktmbuf_mtod_offset(
			packet_to_mbuf((struct packet *)packet),
			const struct yanet_icmp6_hdr *,
			packet->transport_header.offset
		);
		return icmp6->icmp6_type;
	}
	return -1;
}

// Whether the packet is an ICMP echo request towards a service address: the
// balancer answers it on the service's behalf instead of dispatching it to a
// real server.
static inline bool
l3b_packet_is_icmp_echo(const struct packet *packet) {
	int icmp_type = l3b_packet_icmp_type(packet);
	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		return icmp_type == ICMP_ECHO;
	}
	return icmp_type == RTE_ICMP6_ECHO_REQUEST;
}

// Add a 16-bit value to a one's-complement checksum accumulator.
static inline uint16_t
l3b_csum_add(uint16_t sum, uint16_t value) {
	uint32_t acc = (uint32_t)sum + value;
	acc = (acc & 0xFFFF) + (acc >> 16);
	return (uint16_t)acc;
}

/*
 * Answer an ICMP echo request in place, the same way the first-generation
 * balancer did: flip the message type to echo reply, swap the source and
 * destination addresses, restart the TTL at 64 and update the checksums
 * incrementally — swapping both addresses of the pseudo-header keeps its
 * sum, so only the type field change enters the ICMP checksum.
 *
 * Returns 0 on success, -1 on a malformed packet.
 */
static inline int
l3b_icmp_echo_reply(struct packet *packet) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *ip = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		struct yanet_icmp_hdr *icmp = rte_pktmbuf_mtod_offset(
			mbuf,
			struct yanet_icmp_hdr *,
			packet->transport_header.offset
		);

		uint16_t before = rte_cpu_to_be_16((uint16_t)ICMP_ECHO << 8);
		icmp->icmp_type = ICMP_ECHOREPLY;
		icmp->icmp_code = 0;
		uint16_t after =
			rte_cpu_to_be_16((uint16_t)ICMP_ECHOREPLY << 8);

		uint32_t tmp = ip->src_addr;
		ip->src_addr = ip->dst_addr;
		ip->dst_addr = tmp;
		ip->time_to_live = 64;

		ip->hdr_checksum = 0;
		ip->hdr_checksum = rte_ipv4_cksum(ip);

		// The reply is a fresh datagram: its checksum starts anew.
		uint16_t sum = l3b_csum_add(~icmp->icmp_cksum, ~before);
		icmp->icmp_cksum = ~l3b_csum_add(sum, after);
		return 0;
	}

	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *ip6 = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		struct yanet_icmp6_hdr *icmp6 = rte_pktmbuf_mtod_offset(
			mbuf,
			struct yanet_icmp6_hdr *,
			packet->transport_header.offset
		);

		uint16_t before =
			rte_cpu_to_be_16((uint16_t)RTE_ICMP6_ECHO_REQUEST << 8);
		icmp6->icmp6_type = RTE_ICMP6_ECHO_REPLY;
		icmp6->icmp6_code = 0;
		uint16_t after =
			rte_cpu_to_be_16((uint16_t)RTE_ICMP6_ECHO_REPLY << 8);

		uint8_t tmp[16];
		memcpy(tmp, ip6->src_addr, sizeof(tmp));
		memcpy(ip6->src_addr, ip6->dst_addr, sizeof(tmp));
		memcpy(ip6->dst_addr, tmp, sizeof(tmp));
		ip6->hop_limits = 64;

		// Swapping both pseudo-header addresses keeps its checksum,
		// so the type change alone enters incrementally.
		uint16_t sum = l3b_csum_add(~icmp6->icmp6_cksum, ~before);
		icmp6->icmp6_cksum = ~l3b_csum_add(sum, after);
		return 0;
	}

	return -1;
}

/*
 * Resolve a module link's per-worker packets counter.
 *
 * Returns the counter value of the linked service object's link packets
 * counter on this worker's link storage, or NULL when the link, its counter
 * storage or the counter id is absent. The value counts every packet the
 * module dispatched to the service through this link on this worker.
 */
static inline uint64_t *
l3b_link_packets_counter(
	struct module_ectx *module_ectx, uint64_t link_idx, uint64_t counter_id
) {
	if (counter_id == COUNTER_INVALID) {
		return NULL;
	}

	struct module_object_link_ectx *link =
		object_link_get_address(module_ectx, link_idx);
	if (link == NULL) {
		return NULL;
	}

	struct counter_storage *counter_storage =
		ADDR_OF(&link->counter_storage);
	if (counter_storage == NULL) {
		return NULL;
	}

	return counter_get_address(counter_id, counter_storage);
}

/*
 * Resolve a module link's service-object counters.
 *
 * Returns the linked service object's per-worker counter storage, or NULL
 * when the link or the storage is absent.
 */
static inline struct counter_storage *
l3b_module_ectx_service_counters(
	struct module_ectx *module_ectx, uint64_t link_idx
) {
	struct module_object_link_ectx *link =
		object_link_get_address(module_ectx, link_idx);
	if (link == NULL) {
		return NULL;
	}

	struct object_ectx *object_ectx = ADDR_OF(&link->object_ectx);
	if (object_ectx == NULL) {
		return NULL;
	}

	return ADDR_OF(&object_ectx->counter_storage);
}

/*
 * Add one packet to a [packets, bytes] service counter.
 */
static inline void
l3b_counter_add(
	struct counter_storage *counters,
	uint64_t counter_id,
	const struct packet *packet
) {
	if (counters == NULL || counter_id == COUNTER_INVALID) {
		return;
	}

	uint64_t *slot = counter_get_address(counter_id, counters);
	slot[0] += 1;
	slot[1] += rte_pktmbuf_pkt_len(packet_to_mbuf((struct packet *)packet));
}

/*
 * Resolve a module's object link to the virtual service it names.
 *
 * Walks the per-worker object link at link_idx to the linked object's
 * execution context and returns the service embedded in the l3b_virtual_service
 * object, or NULL when the link is missing or unresolved.
 */
static inline struct virtual_service *
l3b_module_ectx_virtual_service(
	struct module_ectx *module_ectx, uint64_t link_idx
) {
	struct module_object_link_ectx *link =
		object_link_get_address(module_ectx, link_idx);
	if (link == NULL) {
		return NULL;
	}

	struct object_ectx *object_ectx = ADDR_OF(&link->object_ectx);
	if (object_ectx == NULL) {
		return NULL;
	}

	struct cp_object *cp_object = ADDR_OF(&object_ectx->cp_object);
	if (cp_object == NULL) {
		return NULL;
	}

	struct l3b_virtual_service_object *object = container_of(
		cp_object, struct l3b_virtual_service_object, cp_object
	);
	return &object->virtual_service;
}

/*
 * Clamp the TCP MSS option of a pure SYN packet down to L3B_FIX_MSS_SIZE,
 * updating the checksum incrementally; values at or below the clamp and
 * non-SYN packets are left untouched, mirroring the first-generation
 * balancer.
 */
static inline void
l3b_fix_mss(struct packet *packet) {
	if (packet->transport_header.type != IPPROTO_TCP ||
	    packet->fragment_offset != 0) {
		return;
	}

	struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(packet),
		struct rte_tcp_hdr *,
		packet->transport_header.offset
	);

	const uint8_t syn = RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG;
	if ((tcp->tcp_flags & syn) != RTE_TCP_SYN_FLAG) {
		return;
	}

	const uint16_t data_offset = (tcp->data_off >> 4) * 4;
	if (data_offset < sizeof(struct rte_tcp_hdr)) {
		return;
	}

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint16_t option_offset = sizeof(struct rte_tcp_hdr);
	while (option_offset + 4 <= data_offset) {
		const uint8_t *option = rte_pktmbuf_mtod_offset(
			mbuf,
			const uint8_t *,
			packet->transport_header.offset + option_offset
		);

		if (option[0] == 1 /* NOP */ || option[0] == 0 /* EOL */) {
			option_offset += 1;
			continue;
		}
		if (option[0] != 2 /* MSS */) {
			if (option[1] == 0) {
				return;
			}
			option_offset += option[1];
			continue;
		}
		if (option[1] != 4) {
			return;
		}

		rte_be16_t *mss = (rte_be16_t *)(option + 2);
		const uint16_t current = rte_be_to_cpu_16(*mss);
		if (current <= L3B_FIX_MSS_SIZE) {
			return;
		}

		uint16_t sum = l3b_csum_add(~tcp->cksum, ~*mss);
		*mss = rte_cpu_to_be_16(L3B_FIX_MSS_SIZE);
		tcp->cksum = ~l3b_csum_add(sum, *mss);
		return;
	}
}

// Apply the service's behavior flags to a packet about to be dispatched.
static inline void
l3b_service_flags_apply(
	const struct virtual_service *virtual_service, struct packet *packet
) {
	if (virtual_service->flags & L3B_FLAG_FIX_MSS) {
		l3b_fix_mss(packet);
	}
}

/*
 * Mark the outer tunnel header's DSCP, after the first-generation policy.
 *
 * Runs after a successful encapsulation, so the network header is the fresh
 * outer one. The mode selects between inheriting the inner DSCP (the encap
 * default), marking only when the inherited DSCP is zero, and marking
 * unconditionally; the ECN bits always survive and the IPv4 header checksum
 * is updated incrementally.
 */
static inline void
l3b_mark_outer_dscp(
	const struct virtual_service *virtual_service, struct packet *packet
) {
	const uint32_t dscp_flags = virtual_service->dscp_flags;
	const uint32_t mode = dscp_flags & 3;
	if (mode == L3B_DSCP_MARK_NEVER) {
		return;
	}
	const uint8_t mark = (uint8_t)(dscp_flags & 0xFC);

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *ip = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);

		if (mode == L3B_DSCP_MARK && (ip->type_of_service & 0xFC)) {
			return;
		}

		uint16_t sum = l3b_csum_add(
			~ip->hdr_checksum,
			~rte_cpu_to_be_16(ip->type_of_service)
		);
		ip->type_of_service =
			(uint8_t)((ip->type_of_service & 0x03) | mark);
		ip->hdr_checksum = ~l3b_csum_add(
			sum, rte_cpu_to_be_16(ip->type_of_service)
		);
		return;
	}

	struct rte_ipv6_hdr *ip6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	uint32_t traffic_class = (rte_be_to_cpu_32(ip6->vtc_flow) >> 20) & 0xFF;
	if (mode == L3B_DSCP_MARK && (traffic_class & 0xFC)) {
		return;
	}

	traffic_class = (traffic_class & 0x03) | mark;
	const uint32_t flow = rte_be_to_cpu_32(ip6->vtc_flow);
	ip6->vtc_flow =
		rte_cpu_to_be_32((traffic_class << 20) | (flow & 0xFF0FFFFFu));
}

/*
 * Encapsulate packet into an IP-in-IP tunnel towards real_server.
 *
 * Returns -1 immediately when the server is disabled or the inner network
 * type is unsupported. The outer destination is the real server address; the
 * outer source is
 *
 *	source_net.addr XOR (inner_source AND ~source_net.mask)
 *
 * evaluated over the width of the real server address family.
 *
 * Returns 0 on success, -1 on failure.
 */
static inline int
l3b_real_server_process(
	const struct virtual_service *virtual_service,
	struct real_server *real_server,
	struct packet *packet
) {
	// Acquire load pairs with the controlplane's release store of a state
	// change; mixing a plain read with those stores is a data race.
	if (__atomic_load_n(&real_server->state, __ATOMIC_ACQUIRE) ==
	    real_state_disabled) {
		return -1;
	}

	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	uint16_t inner_type = packet->network_header.type;

	const uint8_t *real_addr;
	const uint8_t *real_mask;
	const uint8_t *real_dst;
	size_t width;

	if (real_server->type == ip_family_ip4) {
		real_addr = real_server->source_net.v4.addr;
		real_mask = real_server->source_net.v4.mask;
		real_dst = real_server->destination_addr.v4.bytes;
		width = NET4_LEN;
	} else if (real_server->type == ip_family_ip6) {
		real_addr = real_server->source_net.v6.addr;
		real_mask = real_server->source_net.v6.mask;
		real_dst = real_server->destination_addr.v6.bytes;
		width = NET6_LEN;
	} else {
		return -1;
	}

	uint8_t inner_src[NET6_LEN] = {0};
	if (inner_type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *inner_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		memcpy(inner_src, &inner_hdr->src_addr, NET4_LEN);
	} else if (inner_type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *inner_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		memcpy(inner_src, &inner_hdr->src_addr, NET6_LEN);
	} else {
		return -1;
	}

	uint8_t outer_src[NET6_LEN];
	for (size_t idx = 0; idx < width; ++idx) {
		outer_src[idx] =
			real_addr[idx] ^ (inner_src[idx] & ~real_mask[idx]);
	}

	int result;
	if (real_server->type == ip_family_ip4) {
		result = packet_ip4_encap(
			packet, real_dst, outer_src, DSCP_MARK_ALWAYS
		);
	} else {
		result = packet_ip6_encap(
			packet, real_dst, outer_src, DSCP_MARK_ALWAYS
		);
	}
	if (result == 0) {
		l3b_mark_outer_dscp(virtual_service, packet);
	}
	return result;
}

/*
 * Map a scheduler value onto a real server array index through the ring.
 *
 * Reads the ring under its seqlock: the selection is retried while the
 * control plane is replacing the entries, so a reader never acts on a mixture
 * of the old and new rings. An empty or permanently unstable ring fails with
 * -1 and the packet is dropped.
 */
static inline int
l3b_real_ring_select(
	struct real_ring *ring, uint32_t value, uint32_t *real_index
) {
	for (uint32_t attempt = 0; attempt < 4; ++attempt) {
		uint32_t sequence =
			__atomic_load_n(&ring->sequence, __ATOMIC_ACQUIRE);
		if (sequence & 1) {
			continue;
		}

		uint32_t count =
			__atomic_load_n(&ring->count, __ATOMIC_ACQUIRE);
		if (count == 0) {
			return -1;
		}

		uint32_t *server_indexes = ADDR_OF(&ring->server_indexes);
		uint32_t candidate = __atomic_load_n(
			&server_indexes[value % count], __ATOMIC_RELAXED
		);

		if (__atomic_load_n(&ring->sequence, __ATOMIC_ACQUIRE) ==
		    sequence) {
			*real_index = candidate;
			return 0;
		}
	}

	return -1;
}

/*
 * Whether the real server at the index can take traffic.
 */
static inline bool
l3b_real_is_ready(
	const struct virtual_service *virtual_service, uint32_t real_index
) {
	if (real_index >= virtual_service->real_server_count) {
		return false;
	}

	struct real_server *real_servers =
		ADDR_OF(&virtual_service->real_servers);
	// Acquire load pairs with the controlplane's release store of a state
	// change.
	enum real_state state = __atomic_load_n(
		&real_servers[real_index].state, __ATOMIC_ACQUIRE
	);
	return state == real_state_enabled;
}

/*
 * Resolve the real server a session is pinned to against the service's
 * current list.
 *
 * Matching is by destination address, not position: a session recorded
 * before an update keeps its backend as long as that backend is still listed.
 * Returns 0 and stores the index when found and ready; -1 when the pinned
 * backend is listed but disabled; -2 when it left the list.
 */
static inline int
l3b_real_by_destination(
	const struct virtual_service *virtual_service,
	const struct l3s_value *pinned,
	bool *disabled
) {
	struct real_server *real_servers =
		ADDR_OF(&virtual_service->real_servers);

	for (uint32_t real_index = 0;
	     real_index < virtual_service->real_server_count;
	     ++real_index) {
		const struct real_server *real = &real_servers[real_index];

		bool family_matches =
			(pinned->family == 4 && real->type == ip_family_ip4) ||
			(pinned->family == 6 && real->type == ip_family_ip6);
		if (!family_matches) {
			continue;
		}

		const uint8_t *destination =
			pinned->family == 4 ? real->destination_addr.v4.bytes
					    : real->destination_addr.v6.bytes;
		size_t width = pinned->family == 4 ? 4 : 16;
		if (memcmp(destination, pinned->destination, width) != 0) {
			continue;
		}

		if (!l3b_real_is_ready(virtual_service, real_index)) {
			*disabled = true;
			return -1;
		}
		return (int)real_index;
	}

	return -2;
}

/*
 * Record the identity of a chosen real server into a session value.
 */
static inline void
l3b_session_value_of_real(
	const struct real_server *real, struct l3s_value *value
) {
	if (real->type == ip_family_ip4) {
		l3s_value_of(4, real->destination_addr.v4.bytes, value);
	} else {
		l3s_value_of(6, real->destination_addr.v6.bytes, value);
	}
}

// The TCP flags of the packet, 0 when it carries no TCP header.
static inline uint8_t
l3b_packet_tcp_flags(const struct packet *packet) {
	if (packet->transport_header.type != IPPROTO_TCP ||
	    packet->fragment_offset != 0) {
		return 0;
	}

	const struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
		packet_to_mbuf((struct packet *)packet),
		const struct rte_tcp_hdr *,
		packet->transport_header.offset
	);
	return tcp->tcp_flags;
}

/*
 * The session lifetime a packet grants its record, after the
 * first-generation policy: TCP by its flags — SYN alone shortest, established
 * flows the full TCP timeout — UDP its own value, everything else the
 * catch-all.
 */
static inline uint64_t
l3b_session_ttl(
	const struct virtual_service *virtual_service,
	const struct packet *packet
) {
	const struct l3b_session_timeouts *timeouts =
		&virtual_service->session_timeouts;

	if (packet->transport_header.type == IPPROTO_TCP) {
		const uint8_t flags = l3b_packet_tcp_flags(packet);
		const uint8_t syn = RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG;
		if ((flags & syn) == RTE_TCP_SYN_FLAG) {
			const uint8_t syn_ack =
				RTE_TCP_SYN_FLAG | RTE_TCP_ACK_FLAG;
			if ((flags & syn_ack) == syn_ack) {
				return (uint64_t)timeouts->tcp_syn_ack *
				       1000000000ull;
			}
			return (uint64_t)timeouts->tcp_syn * 1000000000ull;
		}
		if (flags & RTE_TCP_FIN_FLAG) {
			return (uint64_t)timeouts->tcp_fin * 1000000000ull;
		}
		return (uint64_t)timeouts->tcp * 1000000000ull;
	}

	if (packet->transport_header.type == IPPROTO_UDP) {
		return (uint64_t)timeouts->udp * 1000000000ull;
	}
	return (uint64_t)timeouts->other * 1000000000ull;
}

/*
 * Whether a flow may be rescheduled to a different real after its pinned
 * backend went away or was disabled, after the first-generation policy: UDP
 * flows and TCP connection setups may, an established TCP flow may not —
 * moving it mid-connection breaks the stream, so the packet is dropped
 * instead.
 */
static inline bool
l3b_packet_may_reschedule(const struct packet *packet) {
	if (packet->transport_header.type == IPPROTO_UDP) {
		return true;
	}
	if (packet->transport_header.type == IPPROTO_TCP) {
		const uint8_t flags = l3b_packet_tcp_flags(packet);
		return (flags & (RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG)) ==
		       RTE_TCP_SYN_FLAG;
	}
	return true;
}

/*
 * Compute the scheduling value for a packet.
 *
 * The per-link packets counter drives it in one-packet scheduling mode
 * (link_packets non-NULL) — the masks disable packet-hash bits and enable
 * counter bits instead — and the packet hash does otherwise.
 */
static inline uint32_t
l3b_schedule_value(
	const struct virtual_service *virtual_service,
	const struct packet *packet,
	const uint64_t *link_packets
) {
	if (link_packets != NULL &&
	    (virtual_service->scheduler_flags & L3B_SCHEDULER_COUNTER)) {
		return (uint32_t)*link_packets &
		       virtual_service->scheduler_index_mask;
	}

	return packet->hash & virtual_service->scheduler_hash_mask &
	       virtual_service->scheduler_index_mask;
}

/*
 * Process a single packet through a virtual service.
 *
 * The per-family filter is queried first; a non-match aborts with -1. A live
 * session record then pins the flow to its real server; only flows without
 * one go through the hash-derived scheduler, and the choice is written back
 * as a new session record. The chosen real performs the encapsulation.
 *
 * Returns 0 on success, -1 on failure.
 */
static inline int
l3b_virtual_service_process(
	struct dp_worker *dp_worker,
	struct virtual_service *virtual_service,
	struct packet *packet,
	const uint64_t *link_packets,
	struct counter_storage *counters
) {
	uint16_t type = packet->network_header.type;

	l3b_counter_add(counters, virtual_service->counter_incoming, packet);

	// An echo request towards the service address is answered by the
	// balancer itself, like the first-generation one did: no session, no
	// scheduler, no real server, and no source filter — an echo message
	// has no ports for it to match.
	if (l3b_packet_is_icmp_echo(packet)) {
		if (l3b_icmp_echo_reply(packet) < 0) {
			return -1;
		}
		l3b_counter_add(
			counters, virtual_service->counter_icmp_replied, packet
		);
		return 0;
	}

	const struct filter_query *query;
	struct filter *filter;

	if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		filter = &virtual_service->filter_ip4;
		query = l3b_source_filter_ip4;
	} else if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		filter = &virtual_service->filter_ip6;
		query = l3b_source_filter_ip6;
	} else {
		return -1;
	}

	struct packet *packets[1] = {packet};
	uint32_t result[1];
	filter_query(filter, query, packets, result, 1);
	if (result[0] == FILTER_RULE_INVALID) {
		l3b_counter_add(
			counters,
			virtual_service->counter_filter_rejected,
			packet
		);
		return -1;
	}

	struct real_server *real_servers =
		ADDR_OF(&virtual_service->real_servers);

	// An existing session keeps its backend while that backend is still
	// listed — matched by address, so the session survives service
	// updates that reorder or replace the list. A disabled backend counts
	// as a rejection by state and falls through to the scheduler.
	uint32_t real_index;
	struct l3s_key key;
	l3s_key_of_packet(packet, &key);
	if (virtual_service->flags & L3B_FLAG_PURE_L3) {
		// Pure L3: the client's ports stay out of the session
		// identity, so every flow of the source shares one pin.
		key.src_port = 0;
	}
	struct l3s_value pinned;

	struct l3b_session_table_object *session_table =
		ADDR_OF(&virtual_service->session_table);
	if (session_table != NULL && l3s_table_lookup(
					     &session_table->table,
					     dp_worker->current_time,
					     &key,
					     &pinned
				     ) == 0) {
		bool disabled = false;
		int pinned_index = l3b_real_by_destination(
			virtual_service, &pinned, &disabled
		);
		if (pinned_index >= 0) {
			// Activity refreshes the record under the current
			// packet's policy, like the first-generation touch.
			l3s_table_insert(
				&session_table->table,
				dp_worker->idx,
				dp_worker->current_time,
				l3b_session_ttl(virtual_service, packet),
				&key,
				&pinned
			);

			l3b_counter_add(
				counters,
				virtual_service->real_counter_ids[pinned_index],
				packet
			);
			l3b_service_flags_apply(virtual_service, packet);
			return l3b_real_server_process(
				virtual_service,
				&real_servers[pinned_index],
				packet
			);
		}
		if (disabled) {
			l3b_counter_add(
				counters,
				virtual_service->counter_real_disabled,
				packet
			);
			// An established TCP flow must not move between reals:
			// drop it instead of breaking the stream. UDP flows and
			// TCP setups fall through to the scheduler and re-pin.
			if (!l3b_packet_may_reschedule(packet)) {
				return -1;
			}
		}
	}

	if (l3b_real_ring_select(
		    &virtual_service->real_ring,
		    l3b_schedule_value(virtual_service, packet, link_packets),
		    &real_index
	    ) < 0) {
		l3b_counter_add(
			counters, virtual_service->counter_ring_empty, packet
		);
		return -1;
	}

	if (!l3b_real_is_ready(virtual_service, real_index)) {
		l3b_counter_add(
			counters, virtual_service->counter_real_disabled, packet
		);
		return -1;
	}

	if (session_table != NULL) {
		l3b_session_value_of_real(&real_servers[real_index], &pinned);
		l3s_table_insert(
			&session_table->table,
			dp_worker->idx,
			dp_worker->current_time,
			l3b_session_ttl(virtual_service, packet),
			&key,
			&pinned
		);
	}

	l3b_counter_add(
		counters, virtual_service->real_counter_ids[real_index], packet
	);
	l3b_service_flags_apply(virtual_service, packet);
	return l3b_real_server_process(
		virtual_service, &real_servers[real_index], packet
	);
}
