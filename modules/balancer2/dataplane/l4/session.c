#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>
#include <string.h>

#include "common/network.h"

#include "lib/dataplane/packet/data.h"

#include "packet.h"
#include "session.h"
#include "types/session.h"

static void
extract_ipv4(struct packet *packet, struct session *session) {
	struct rte_ipv4_hdr *hdr = rte_pktmbuf_mtod_offset(
		packet->mbuf,
		struct rte_ipv4_hdr *,
		packet->network_header.offset
	);
	rte_memcpy(&session->id.client_ip, (uint8_t *)&hdr->src_addr, NET4_LEN);
	rte_memcpy(&session->id.vip, (uint8_t *)&hdr->dst_addr, NET4_LEN);
}

static void
extract_ipv6(struct packet *packet, struct session *session) {
	struct rte_ipv6_hdr *hdr = rte_pktmbuf_mtod_offset(
		packet->mbuf,
		struct rte_ipv6_hdr *,
		packet->network_header.offset
	);
	rte_memcpy(&session->id.client_ip, hdr->src_addr, NET6_LEN);
	rte_memcpy(&session->id.vip, hdr->dst_addr, NET6_LEN);
}

static void
extract_network(struct packet *packet, struct session *session, bool is_ipv6) {
	if (is_ipv6) {
		extract_ipv6(packet, session);
	} else {
		extract_ipv4(packet, session);
	}
}

static uint32_t
tcp_timeout(struct balancer_session_timeouts *timeouts, uint16_t tcp_flags) {
	if ((tcp_flags & RTE_TCP_SYN_FLAG) == RTE_TCP_SYN_FLAG) {
		if ((tcp_flags & RTE_TCP_ACK_FLAG) == RTE_TCP_ACK_FLAG) {
			return timeouts->tcp_syn_ack;
		}
		return timeouts->tcp_syn;
	}
	if (tcp_flags & RTE_TCP_FIN_FLAG) {
		return timeouts->tcp_fin;
	}
	return timeouts->tcp;
}

static void
extract_tcp(
	struct packet *packet,
	struct session *session,
	struct balancer_session_timeouts *timeouts
) {
	struct rte_tcp_hdr *hdr = rte_pktmbuf_mtod_offset(
		packet->mbuf,
		struct rte_tcp_hdr *,
		packet->transport_header.offset
	);

	uint8_t flags = hdr->tcp_flags;

	session->id.client_port = hdr->src_port;
	session->id.vs_port = hdr->dst_port;
	session->id.transport = transport_proto_tcp;

	session->timeout = tcp_timeout(timeouts, flags);
	session->can_reschedule = (flags & (RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG)
				  ) == RTE_TCP_SYN_FLAG;
}

static void
extract_udp(
	struct packet *packet,
	struct session *session,
	struct balancer_session_timeouts *timeouts
) {
	struct rte_udp_hdr *hdr = rte_pktmbuf_mtod_offset(
		packet->mbuf,
		struct rte_udp_hdr *,
		packet->transport_header.offset
	);

	session->id.client_port = hdr->src_port;
	session->id.vs_port = hdr->dst_port;
	session->id.transport = transport_proto_udp;

	session->timeout = timeouts->udp;
	session->can_reschedule = true;
}

static void
extract_transport(
	struct packet *packet,
	struct session *session,
	struct balancer_session_timeouts *timeouts
) {
	if (packet->transport_header.type == IPPROTO_TCP) {
		extract_tcp(packet, session, timeouts);
	} else {
		extract_udp(packet, session, timeouts);
	}
}

void
fill_sessions(
	struct session *sessions,
	struct packet_context *pkt_ctxs,
	size_t count,
	struct balancer_session_timeouts *timeouts,
	bool is_ipv6
) {
	const size_t prefetch_distance = 2;

	for (size_t pkt_idx = 0; pkt_idx < count; ++pkt_idx) {
		if (pkt_idx + prefetch_distance < count) {
			struct rte_mbuf *mbuf = packet_to_mbuf(
				pkt_ctxs[pkt_idx + prefetch_distance].packet
			);
			rte_prefetch0(mbuf);
		}

		struct packet *packet = pkt_ctxs[pkt_idx].packet;
		struct session *session = &sessions[pkt_idx];

		memset(&session->id, 0, sizeof(struct balancer_session_id));
		session->id.ip_family = is_ipv6 ? ip_family_ip6 : ip_family_ip4;

		extract_network(packet, session, is_ipv6);
		extract_transport(packet, session, timeouts);
	}
}
