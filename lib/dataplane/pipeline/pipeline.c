#include "pipeline.h"

#include <netinet/icmp6.h>
#include <netinet/ip_icmp.h>

#include <rte_arp.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "dataplane/config/zone.h"
#include "lib/logging/log.h"

/**
 * @brief Print contents of an rte_mbuf packet in a detailed format
 *
 * Prints detailed information about DPDK mbuf packet contents using LOG_TRACE
 * including:
 * - Ethernet header fields (MAC addresses, ether type)
 * - ARP header fields (if packet is ARP)
 * - IP header fields (v4 or v6, including addresses, protocol, TTL/hop limit)
 * - Protocol header fields:
 *   - UDP (ports, length, checksum)
 *   - TCP (ports, sequence numbers, flags, window)
 *   - ICMP (type, code, checksum)
 *   - ICMPv6 (type, code, checksum)
 * - Final packet data length
 *
 * Used for detailed packet inspection during debugging, development and
 * verification of packet processing.
 *
 * @param mbuf Pointer to the DPDK mbuf structure containing packet to print
 */
void
print_rte_mbuf(struct rte_mbuf *mbuf) {
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
}

static inline int
packet_list_counter(struct packet_list *list) {
	int count = 0;
	for (struct packet *pkt = list->first; pkt != NULL; pkt = pkt->next) {
		count++;
	}
	return count;
}

static inline void
packet_list_print(struct packet_list *list) {
	for (struct packet *pkt = list->first; pkt != NULL; pkt = pkt->next) {
		print_rte_mbuf(packet_to_mbuf(pkt));
	}
}

void
pipeline_process(
	struct dp_config *dp_config,
	struct cp_config_gen *cp_config_gen,
	uint64_t pipeline_idx,
	struct packet_front *packet_front
) {
	struct cp_pipeline *cp_pipeline =
		cp_config_gen_get_pipeline(cp_config_gen, pipeline_idx);
	if (cp_pipeline == NULL) {
		packet_list_concat(&packet_front->drop, &packet_front->output);
		packet_list_init(&packet_front->output);
		return;
	}

	uint64_t *module_indexes = cp_pipeline->module_indexes;

	for (uint64_t stage_idx = 0; stage_idx < cp_pipeline->length;
	     ++stage_idx) {
		struct module_data *module_data = cp_config_gen_get_module(
			cp_config_gen, module_indexes[stage_idx]
		);

		uint64_t module_index = module_data->index;
		struct dp_module *dp_module =
			ADDR_OF(&dp_config->dp_modules) + module_index;

		packet_front_switch(packet_front);
		LOG_TRACE(
			"processing packet with module [pre] %s, in %d, out "
			"%d, drop %d",
			dp_module->name,
			packet_list_counter(&packet_front->input),
			packet_list_counter(&packet_front->output),
			packet_list_counter(&packet_front->drop)
		);

		packet_list_print(&packet_front->input);

		dp_module->handler(dp_config, module_data, packet_front);

		LOG_TRACE(
			"processing packet with module [post] %s, in %d, out "
			"%d, drop %d",
			dp_module->name,
			packet_list_counter(&packet_front->input),
			packet_list_counter(&packet_front->output),
			packet_list_counter(&packet_front->drop)
		);

		packet_list_print(&packet_front->output);
	}
}
