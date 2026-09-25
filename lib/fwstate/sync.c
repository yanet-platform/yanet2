#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <string.h>

#include "common/network.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/worker/worker.h"
#include "stash.h"
#include "sync.h"
#include "types.h"

/**
 * Fill sync frame with packet 5-tuple information.
 * For INGRESS direction, stores as-is; for EGRESS, swaps src/dst to match
 * the initial state.
 * Sets fib field: 0 for INGRESS (forward), 1 for EGRESS (backward).
 */
static inline void
fwstate_fill_sync_frame(
	const struct packet *packet,
	const enum sync_packet_direction direction,
	struct fw_state_sync_frame *sync_frame
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	memset(sync_frame, 0, sizeof(struct fw_state_sync_frame));

	// Set fib to indicate direction: 0 = forward (INGRESS), 1 = backward
	// (EGRESS)
	sync_frame->fib = direction == SYNC_EGRESS;

	// Store 5-tuple: INGRESS stores as-is, EGRESS swaps src/dst to match
	// the initial state
	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		sync_frame->proto = ipv4_hdr->next_proto_id;
		sync_frame->addr_type = FW_STATE_ADDR_TYPE_IP4;
		// Swap src/dst for EGRESS to match initial 5-tuple
		if (direction == SYNC_EGRESS) {
			sync_frame->src_ip = ipv4_hdr->dst_addr;
			sync_frame->dst_ip = ipv4_hdr->src_addr;
		} else {
			sync_frame->src_ip = ipv4_hdr->src_addr;
			sync_frame->dst_ip = ipv4_hdr->dst_addr;
		}
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		sync_frame->proto = ipv6_hdr->proto;
		sync_frame->addr_type = FW_STATE_ADDR_TYPE_IP6;
		// Swap src/dst for EGRESS to match initial 5-tuple
		if (direction == SYNC_EGRESS) {
			rte_memcpy(sync_frame->src_ip6, ipv6_hdr->dst_addr, 16);
			rte_memcpy(sync_frame->dst_ip6, ipv6_hdr->src_addr, 16);
		} else {
			rte_memcpy(sync_frame->src_ip6, ipv6_hdr->src_addr, 16);
			rte_memcpy(sync_frame->dst_ip6, ipv6_hdr->dst_addr, 16);
		}
		sync_frame->flow_id6 =
			rte_be_to_cpu_32(ipv6_hdr->vtc_flow) & 0x000FFFFF;
	}

	// Extract transport layer information
	switch (sync_frame->proto) {
	case IPPROTO_TCP: {
		struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		// Swap src/dst ports for EGRESS to match initial 5-tuple
		//
		// TCP flags are not merged to allow proper timeout selection.
		// The timeout config has separate values for tcp_syn,
		// tcp_syn_ack, tcp_fin, and tcp (established). Merging flags
		// would cause a SYN flag to persist after seeing ACK,
		// preventing the state from transitioning to the longer (120s)
		// established timeout. Only current flags are sent to match the
		// appropriate timeout for each state.
		if (direction == SYNC_EGRESS) {
			sync_frame->src_port =
				rte_be_to_cpu_16(tcp_hdr->dst_port);
			sync_frame->dst_port =
				rte_be_to_cpu_16(tcp_hdr->src_port);
			sync_frame->flags.tcp.dst =
				fwstate_flags_from_tcp(tcp_hdr->tcp_flags);
		} else {
			sync_frame->src_port =
				rte_be_to_cpu_16(tcp_hdr->src_port);
			sync_frame->dst_port =
				rte_be_to_cpu_16(tcp_hdr->dst_port);
			sync_frame->flags.tcp.src =
				fwstate_flags_from_tcp(tcp_hdr->tcp_flags);
		}
	} break;
	case IPPROTO_UDP: {
		struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);
		// Swap src/dst ports for EGRESS to match initial 5-tuple
		if (direction == SYNC_EGRESS) {
			sync_frame->src_port =
				rte_be_to_cpu_16(udp_hdr->dst_port);
			sync_frame->dst_port =
				rte_be_to_cpu_16(udp_hdr->src_port);
		} else {
			sync_frame->src_port =
				rte_be_to_cpu_16(udp_hdr->src_port);
			sync_frame->dst_port =
				rte_be_to_cpu_16(udp_hdr->dst_port);
		}
	} break;
		// TODO: add support for other protocols
	}
}

int
fwstate_fill_sync_record(
	const struct packet *packet,
	const enum sync_packet_direction direction,
	struct fwstate_sync_record *record
) {
	if ((packet->transport_header.type & PACKET_TRANSPORT_HEADER_UNAVAILABLE
	    ) != 0) {
		// There is no transport header to copy ports and flags
		// from; refuse to fabricate a sync frame from payload.
		return -1;
	}

	fwstate_fill_sync_frame(packet, direction, &record->frame);
	record->rx_device_id = packet->rx_device_id;
	record->tx_device_id = packet->tx_device_id;
	record->status = FWSTATE_SYNC_RECORD_PENDING;
	return 0;
}

int
fwstate_build_sync_packet(
	const struct fw_state_sync_frame *const *frames,
	uint32_t count,
	uint16_t rx_device_id,
	uint16_t tx_device_id,
	struct packet *sync_pkt
) {
	struct rte_mbuf *sync_mbuf = packet_to_mbuf(sync_pkt);

	// The packet structure is: Ethernet + VLAN + IPv6 + UDP + frames.
	const uint16_t eth_offset = 0;
	const uint16_t vlan_offset = sizeof(struct rte_ether_hdr);
	const uint16_t ipv6_offset = vlan_offset + sizeof(struct rte_vlan_hdr);
	const uint16_t udp_offset = ipv6_offset + sizeof(struct rte_ipv6_hdr);
	const uint16_t payload_offset = udp_offset + sizeof(struct rte_udp_hdr);
	const uint32_t frame_size = sizeof(struct fw_state_sync_frame);

	uint32_t tailroom = rte_pktmbuf_tailroom(sync_mbuf);
	if (count == 0 || tailroom < payload_offset + frame_size) {
		return -1;
	}
	uint32_t fit = (tailroom - payload_offset) / frame_size;
	if (count > fit) {
		count = fit;
	}
	const uint16_t payload_len = (uint16_t)(count * frame_size);

	char *pkt_data =
		rte_pktmbuf_append(sync_mbuf, payload_offset + payload_len);
	if (pkt_data == NULL) {
		return -1;
	}
	// Every header byte not set below, the Ethernet source and the VLAN
	// tag among them, is zero rather than stale mbuf contents.
	memset(pkt_data, 0, payload_offset);

	// Fill Ethernet header
	struct rte_ether_hdr *eth_hdr = rte_pktmbuf_mtod_offset(
		sync_mbuf, struct rte_ether_hdr *, eth_offset
	);
	eth_hdr->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_VLAN);

	// Fill VLAN header
	struct rte_vlan_hdr *vlan_hdr = rte_pktmbuf_mtod_offset(
		sync_mbuf, struct rte_vlan_hdr *, vlan_offset
	);
	vlan_hdr->eth_proto = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
	// FIXME: set VLAN ID from config or packet

	// Fill IPv6 header
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		sync_mbuf, struct rte_ipv6_hdr *, ipv6_offset
	);
	ipv6_hdr->vtc_flow = rte_cpu_to_be_32(0x6 << 28); // IPv6 version
	ipv6_hdr->payload_len =
		rte_cpu_to_be_16(sizeof(struct rte_udp_hdr) + payload_len);
	ipv6_hdr->proto = IPPROTO_UDP;
	ipv6_hdr->hop_limits = 64;
	// Outer addressing stays neutral until fwstate assigns a destination.

	// Fill UDP header
	struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
		sync_mbuf, struct rte_udp_hdr *, udp_offset
	);
	udp_hdr->dgram_len =
		rte_cpu_to_be_16(sizeof(struct rte_udp_hdr) + payload_len);
	// NOTE: Checksum will be calculated by the fwstate module

	uint8_t *payload =
		rte_pktmbuf_mtod_offset(sync_mbuf, uint8_t *, payload_offset);
	for (uint32_t idx = 0; idx < count; ++idx) {
		rte_memcpy(payload + idx * frame_size, frames[idx], frame_size);
	}

	sync_pkt->rx_device_id = rx_device_id;
	sync_pkt->tx_device_id = tx_device_id;

	// Set packet header offsets directly (we know the structure)
	sync_pkt->network_header.type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
	sync_pkt->network_header.offset = ipv6_offset;
	sync_pkt->transport_header.type = IPPROTO_UDP;
	sync_pkt->transport_header.offset = udp_offset;
	packet_refresh_data_len(sync_pkt);

	return (int)count;
}

void
fwstate_sync_set_destination(
	struct packet *packet,
	const struct ether_addr *dst_ether,
	const uint8_t dst_addr[16],
	uint16_t dst_port
) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);
	struct rte_ether_hdr *ether_hdr =
		rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);
	struct ether_addr *ether_dst =
		(struct ether_addr *)&ether_hdr->dst_addr;
	*ether_dst = *dst_ether;
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, packet->network_header.offset
	);
	rte_memcpy(ipv6_hdr->dst_addr, dst_addr, 16);

	struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_udp_hdr *, packet->transport_header.offset
	);
	udp_hdr->src_port = dst_port;
	udp_hdr->dst_port = dst_port;
	udp_hdr->dgram_cksum = 0;
}
