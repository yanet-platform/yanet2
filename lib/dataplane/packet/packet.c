#include "packet.h"

#include <stdint.h>

#include <netinet/icmp6.h>
#include <netinet/ip_icmp.h>

#include <rte_arp.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <rte_ether.h>
#include <rte_ip.h>

#include "lib/logging/log.h"
#include "yanet_build_config.h"

/*
 * TODO: analyze if the valid packet parsing may
 * overflow the 65535 value in an offset.
 */

static inline int
parse_ether_header(struct packet *packet, uint16_t *type, uint16_t *offset) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)*offset + sizeof(struct rte_ether_hdr)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	const struct rte_ether_hdr *ether_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ether_hdr *, *offset);
	*type = ether_hdr->ether_type;
	*offset += sizeof(struct rte_ether_hdr);
	return 0;
}

static inline int
parse_vlan_header(struct packet *packet, uint16_t *type, uint16_t *offset) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)*offset + sizeof(struct rte_vlan_hdr)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	const struct rte_vlan_hdr *vlan_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_vlan_hdr *, *offset);

	packet->vlan = rte_be_to_cpu_16(vlan_hdr->vlan_tci);

	*type = vlan_hdr->eth_proto;
	*offset += sizeof(struct rte_vlan_hdr);
	return 0;
}

inline int
parse_ipv4_header(struct packet *packet, uint16_t *type, uint16_t *offset) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)*offset + sizeof(struct rte_ipv4_hdr)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	const struct rte_ipv4_hdr *ipv4_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ipv4_hdr *, *offset);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)*offset + rte_be_to_cpu_16(ipv4_hdr->total_length)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	if ((ipv4_hdr->version_ihl & 0x0F) < 0x05) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	if (rte_be_to_cpu_16(ipv4_hdr->total_length) <
	    4 * (ipv4_hdr->version_ihl & 0x0F)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	// FIXME: check if fragmented
	// FIXME: process extensions

	*type = ipv4_hdr->next_proto_id;
	*offset += 4 * (ipv4_hdr->version_ihl & 0x0F);

	return 0;
}

inline int
parse_ipv6_header(struct packet *packet, uint16_t *type, uint16_t *offset) {
	struct rte_mbuf *mbuf = packet_to_mbuf(packet);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    (uint32_t)*offset + sizeof(struct rte_ipv6_hdr)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	const struct rte_ipv6_hdr *ipv6_hdr =
		rte_pktmbuf_mtod_offset(mbuf, struct rte_ipv6_hdr *, *offset);

	if (rte_pktmbuf_pkt_len(mbuf) <
	    *offset + sizeof(struct rte_ipv6_hdr) +
		    rte_be_to_cpu_16(ipv6_hdr->payload_len)) {
		*type = PACKET_HEADER_TYPE_UNKNOWN;
		return -1;
	}

	// walk through extensions
	*offset += sizeof(struct rte_ipv6_hdr);
	uint16_t max_offset = *offset + rte_be_to_cpu_16(ipv6_hdr->payload_len);
	uint8_t ext_type = ipv6_hdr->proto;
	while (*offset < max_offset) {
		if (ext_type == IPPROTO_HOPOPTS ||
		    ext_type == IPPROTO_ROUTING ||
		    ext_type == IPPROTO_DSTOPTS) {
			if (max_offset < *offset + 8) {
				return -1;
			}

			const struct ipv6_ext_2byte *ext =
				rte_pktmbuf_mtod_offset(
					mbuf, struct ipv6_ext_2byte *, *offset
				);

			ext_type = ext->next_type;
			*offset += (1 + ext->size) * 8;

			// FIXME: packet->network_flags |=
			// NETWORK_FLAG_HAS_EXTENSION;
		} else if (ext_type == IPPROTO_AH) {
			if (max_offset < *offset + 8) {
				return -1;
			}

			const struct ipv6_ext_2byte *ext =
				rte_pktmbuf_mtod_offset(
					mbuf, struct ipv6_ext_2byte *, *offset
				);

			ext_type = ext->next_type;
			*offset += (2 + ext->size) * 4;
			// FIXME: packet->network_flags |=
			// NETWORK_FLAG_HAS_EXTENSION;
		} else if (ext_type == IPPROTO_FRAGMENT) {
			if (max_offset < *offset + 8) {
				return -1;
			}

			const struct ipv6_ext_fragment *ext =
				rte_pktmbuf_mtod_offset(
					mbuf,
					struct ipv6_ext_fragment *,
					*offset
				);

			if ((ext->offset_flag & 0xF9FF) != 0x0000) {
				// FIXME: NETWORK_FLAG_FRAGMENT
				if ((ext->offset_flag & 0xF8FF) != 0x0000) {
					// FIXME:
					// NETWORK_FLAG_NOT_FIRST_FRAGMENT;
				}
			}

			ext_type = ext->next_type;
			*offset += RTE_IPV6_FRAG_HDR_SIZE;

			// FIXME: packet->network_flags |=
			// NETWORK_FLAG_HAS_EXTENSION;
		} else {
			break;
		}
	}

	if (*offset > max_offset) {
		return -1;
	}

	*type = ext_type;

	return 0;
}

int
parse_packet(struct packet *packet) {
	uint16_t type = 0;
	uint16_t offset = 0;

	if (parse_ether_header(packet, &type, &offset)) {
		return -1;
	}

	if ((type == rte_cpu_to_be_16(RTE_ETHER_TYPE_VLAN)) &&
	    parse_vlan_header(packet, &type, &offset)) {
		return -1;
	}

	packet->network_header.type = type;
	packet->network_header.offset = offset;

	if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		if (parse_ipv4_header(packet, &type, &offset)) {
			return -1;
		}
	} else if (type == rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		if (parse_ipv6_header(packet, &type, &offset)) {
			return -1;
		}
	} else {
		// unknown header
		return -1;
	}

	// FIXME: separate routines for transport level parsing
	packet->transport_header.type = type;
	packet->transport_header.offset = offset;

	return 0;
}

struct packet *
mbuf_to_packet(struct rte_mbuf *mbuf) {
	return (struct packet *)((void *)mbuf->buf_addr);
}

void
logtrace_rte_mbuf(struct rte_mbuf *mbuf) {
#ifdef ENABLE_TRACE_LOG
	if (!mbuf) {
		LOG(ERROR, "Mbuf is NULL");
		return;
	}

	// Get the data pointer
	uint8_t *data = rte_pktmbuf_mtod(mbuf, uint8_t *);

	// Extract Ethernet header
	struct rte_ether_hdr *eth_hdr = (struct rte_ether_hdr *)data;
	LOG_TRACE("Ethernet Header:");
	LOG_TRACE(
		"  Destination MAC: " RTE_ETHER_ADDR_PRT_FMT,
		RTE_ETHER_ADDR_BYTES(&eth_hdr->dst_addr)
	);
	LOG_TRACE(
		"  Source MAC: " RTE_ETHER_ADDR_PRT_FMT,
		RTE_ETHER_ADDR_BYTES(&eth_hdr->src_addr)
	);
	LOG_TRACE("  Ether Type: 0x%04X", ntohs(eth_hdr->ether_type));

	uint16_t data_off = sizeof(struct rte_ether_hdr);

	// Determine the IP header type and extract it
	if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *ipv4_hdr =
			(struct rte_ipv4_hdr *)(eth_hdr + 1);
		data_off += rte_ipv4_hdr_len(ipv4_hdr);
		LOG_TRACE("IPv4 Header:");
		LOG_TRACE("  Version: %d", (ipv4_hdr->version_ihl & 0xF0) >> 4);
		LOG_TRACE("  IHL: %d", ipv4_hdr->version_ihl & 0x0F);
		LOG_TRACE(
			"  Type of Service: 0x%02X", ipv4_hdr->type_of_service
		);
		LOG_TRACE("  Total Length: %d", ntohs(ipv4_hdr->total_length));
		LOG_TRACE(
			"  Identification: 0x%04X", ntohs(ipv4_hdr->packet_id)
		);
		LOG_TRACE(
			"  Fragment Offset: %d",
			rte_be_to_cpu_16(ipv4_hdr->fragment_offset) &
				RTE_IPV4_HDR_OFFSET_MASK
		);
		LOG_TRACE("  Time to Live: %d", ipv4_hdr->time_to_live);
		LOG_TRACE("  Protocol: 0x%02X", ipv4_hdr->next_proto_id);
		LOG_TRACE(
			"  Header Checksum: 0x%04X",
			ntohs(ipv4_hdr->hdr_checksum)
		);

		char src_ip_str[INET_ADDRSTRLEN];
		inet_ntop(
			AF_INET,
			&ipv4_hdr->src_addr,
			src_ip_str,
			INET_ADDRSTRLEN
		);
		LOG_TRACE("  Source IP: %s", src_ip_str);

		char dst_ip_str[INET_ADDRSTRLEN];
		inet_ntop(
			AF_INET,
			&ipv4_hdr->dst_addr,
			dst_ip_str,
			INET_ADDRSTRLEN
		);
		LOG_TRACE("  Destination IP: %s", dst_ip_str);

		// Extract and print the protocol header
		uint8_t *proto_data = (uint8_t *)(ipv4_hdr + 1);

		switch (ipv4_hdr->next_proto_id) {
		case IPPROTO_UDP: {
			data_off += sizeof(struct rte_udp_hdr);
			struct rte_udp_hdr *udp_hdr =
				(struct rte_udp_hdr *)proto_data;
			LOG_TRACE("UDP Header:");
			LOG_TRACE(
				"  Source Port: %d", ntohs(udp_hdr->src_port)
			);
			LOG_TRACE(
				"  Destination Port: %d",
				ntohs(udp_hdr->dst_port)
			);
			LOG_TRACE("  Length: %d", ntohs(udp_hdr->dgram_len));
			LOG_TRACE(
				"  Checksum: 0x%04X",
				ntohs(udp_hdr->dgram_cksum)
			);
			break;
		}
		case IPPROTO_TCP: {
			data_off += sizeof(struct rte_tcp_hdr);
			struct rte_tcp_hdr *tcp_hdr =
				(struct rte_tcp_hdr *)proto_data;
			LOG_TRACE("TCP Header:");
			LOG_TRACE(
				"  Source Port: %d", ntohs(tcp_hdr->src_port)
			);
			LOG_TRACE(
				"  Destination Port: %d",
				ntohs(tcp_hdr->dst_port)
			);
			LOG_TRACE(
				"  Sequence Number: %u",
				ntohl(tcp_hdr->sent_seq)
			);
			LOG_TRACE(
				"  Acknowledgment Number: %u",
				ntohl(tcp_hdr->recv_ack)
			);
			LOG_TRACE(
				"  Data Offset: %d",
				(tcp_hdr->data_off & 0xF0) >> 4
			);
			LOG_TRACE("  Flags: 0x%02X", tcp_hdr->tcp_flags);
			LOG_TRACE("  Window Size: %d", ntohs(tcp_hdr->rx_win));
			LOG_TRACE("  Checksum: 0x%04X", ntohs(tcp_hdr->cksum));
			break;
		}
		case IPPROTO_ICMP: {
			data_off += sizeof(struct icmphdr);
			struct icmphdr *icmp_hdr = (struct icmphdr *)proto_data;
			LOG_TRACE("ICMP Header:");
			LOG_TRACE("  Type: 0x%02X", icmp_hdr->type);
			LOG_TRACE("  Code: 0x%02X", icmp_hdr->code);
			LOG_TRACE(
				"  Checksum: 0x%04X", ntohs(icmp_hdr->checksum)
			);
			break;
		}
		}
	} else if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_ARP)) {
		struct rte_arp_hdr *arp_hdr =
			(struct rte_arp_hdr *)(eth_hdr + 1);
		data_off += sizeof(struct rte_arp_hdr);
		LOG_TRACE("ARP Header:");
		LOG_TRACE(
			"  Hardware Type: 0x%04X", ntohs(arp_hdr->arp_hardware)
		);
		LOG_TRACE(
			"  Protocol Type: 0x%04X", ntohs(arp_hdr->arp_protocol)
		);
		LOG_TRACE("  Hardware Length: %d", arp_hdr->arp_hlen);
		LOG_TRACE("  Protocol Length: %d", arp_hdr->arp_plen);
		LOG_TRACE("  Opcode: %d", ntohs(arp_hdr->arp_opcode));

		LOG_TRACE(
			"  Sender MAC: " RTE_ETHER_ADDR_PRT_FMT,
			RTE_ETHER_ADDR_BYTES(&arp_hdr->arp_data.arp_sha)
		);

		struct in_addr sender_ip;
		sender_ip.s_addr = arp_hdr->arp_data.arp_sip;
		char sender_ip_str[INET_ADDRSTRLEN];
		inet_ntop(AF_INET, &sender_ip, sender_ip_str, INET_ADDRSTRLEN);
		LOG_TRACE("  Sender IP: %s", sender_ip_str);

		LOG_TRACE(
			"  Target MAC: " RTE_ETHER_ADDR_PRT_FMT,
			RTE_ETHER_ADDR_BYTES(&arp_hdr->arp_data.arp_tha)
		);

		struct in_addr target_ip;
		target_ip.s_addr = arp_hdr->arp_data.arp_tip;
		char target_ip_str[INET_ADDRSTRLEN];
		inet_ntop(AF_INET, &target_ip, target_ip_str, INET_ADDRSTRLEN);
		LOG_TRACE("  Target IP: %s", target_ip_str);
	} else if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *ipv6_hdr =
			(struct rte_ipv6_hdr *)(eth_hdr + 1);
		data_off += sizeof(struct rte_ipv6_hdr);
		LOG_TRACE("IPv6 Header:");
		LOG_TRACE(
			"  Version: %d",
			(htonl(ipv6_hdr->vtc_flow) & 0xF0000000) >> 28
		);
		LOG_TRACE(
			"  Traffic Class: 0x%02X",
			(htonl(ipv6_hdr->vtc_flow) & 0x0FF00000) >> 20
		);
		LOG_TRACE(
			"  Flow Label: 0x%05X",
			htonl(ipv6_hdr->vtc_flow) & 0x000FFFFF
		);
		LOG_TRACE("  Payload Length: %d", ntohs(ipv6_hdr->payload_len));
		LOG_TRACE("  Next Header: 0x%02X", ipv6_hdr->proto);
		LOG_TRACE("  Hop Limit: %d", ipv6_hdr->hop_limits);

		char src_ip_str[INET6_ADDRSTRLEN];
		inet_ntop(
			AF_INET6,
			&ipv6_hdr->src_addr,
			src_ip_str,
			INET6_ADDRSTRLEN
		);
		LOG_TRACE("  Source IP: %s", src_ip_str);

		char dst_ip_str[INET6_ADDRSTRLEN];
		inet_ntop(
			AF_INET6,
			&ipv6_hdr->dst_addr,
			dst_ip_str,
			INET6_ADDRSTRLEN
		);
		LOG_TRACE("  Destination IP: %s", dst_ip_str);

		// Extract and print the protocol header
		uint8_t *proto_data = (uint8_t *)(ipv6_hdr + 1);
		switch (ipv6_hdr->proto) {
		case IPPROTO_UDP:
			data_off += sizeof(struct rte_udp_hdr);
			struct rte_udp_hdr *udp_hdr =
				(struct rte_udp_hdr *)proto_data;
			LOG_TRACE("UDP Header:");
			LOG_TRACE(
				"  Source Port: %d", ntohs(udp_hdr->src_port)
			);
			LOG_TRACE(
				"  Destination Port: %d",
				ntohs(udp_hdr->dst_port)
			);
			LOG_TRACE("  Length: %d", ntohs(udp_hdr->dgram_len));
			LOG_TRACE(
				"  Checksum: 0x%04X",
				ntohs(udp_hdr->dgram_cksum)
			);
			break;
		case IPPROTO_TCP:
			data_off += sizeof(struct rte_tcp_hdr);
			struct rte_tcp_hdr *tcp_hdr =
				(struct rte_tcp_hdr *)proto_data;
			LOG_TRACE("TCP Header:");
			LOG_TRACE(
				"  Source Port: %d", ntohs(tcp_hdr->src_port)
			);
			LOG_TRACE(
				"  Destination Port: %d",
				ntohs(tcp_hdr->dst_port)
			);
			LOG_TRACE(
				"  Sequence Number: %u",
				ntohl(tcp_hdr->sent_seq)
			);
			LOG_TRACE(
				"  Acknowledgment Number: %u",
				ntohl(tcp_hdr->recv_ack)
			);
			LOG_TRACE(
				"  Data Offset: %d",
				(tcp_hdr->data_off & 0xF0) >> 4
			);
			LOG_TRACE("  Flags: 0x%02X", tcp_hdr->tcp_flags);
			LOG_TRACE("  Window Size: %d", ntohs(tcp_hdr->rx_win));
			LOG_TRACE("  Checksum: 0x%04X", ntohs(tcp_hdr->cksum));
			break;
		case IPPROTO_ICMPV6:
			data_off += sizeof(struct icmp6_hdr);
			struct icmp6_hdr *icmp6_hdr =
				(struct icmp6_hdr *)proto_data;
			LOG_TRACE("ICMPv6 Header:");
			LOG_TRACE("  Type: 0x%02X", icmp6_hdr->icmp6_type);
			LOG_TRACE("  Code: 0x%02X", icmp6_hdr->icmp6_code);
			LOG_TRACE(
				"  Checksum: 0x%04X",
				ntohs(icmp6_hdr->icmp6_cksum)
			);
			break;
		}
	}
	LOG_TRACE("Data Length: %d", mbuf->pkt_len - data_off);
#else
	(void)mbuf;
#endif // ENABLE_TRACE_LOG
}

int
packet_list_counter(struct packet_list *list) {
	int count = 0;
	for (struct packet *pkt = list->first; pkt != NULL; pkt = pkt->next) {
		count++;
	}
	return count;
}

void
packet_list_print(struct packet_list *list) {
	for (struct packet *pkt = list->first; pkt != NULL; pkt = pkt->next) {
		logtrace_rte_mbuf(packet_to_mbuf(pkt));
	}
}
