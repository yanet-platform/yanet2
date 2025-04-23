#include "pipeline.h"

#include <inttypes.h>
#include <netinet/icmp6.h>
#include <netinet/ip_icmp.h>
#include <arpa/inet.h>

#include <rte_common.h>
#include <rte_ether.h>
#include <rte_icmp.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "dataplane/module/module.h"
#include "dataplane/config/zone.h"
#include "lib/logging/log.h"

/**
 * @brief Print contents of an rte_mbuf packet
 *
 * Prints detailed information about DPDK mbuf packet contents including:
 * - Ethernet header fields
 * - IP header fields (v4 or v6)
 * - Protocol header fields (UDP, TCP, ICMP, ICMPv6)
 * - Packet data length
 *
 * Used for debugging and verifying packet translations.
 *
 * @param mbuf Pointer to the DPDK mbuf structure to print
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
    LOG(ERROR, "Ethernet Header:");
    LOG(ERROR,
        "  Destination MAC: " RTE_ETHER_ADDR_PRT_FMT,
        RTE_ETHER_ADDR_BYTES(&eth_hdr->dst_addr));
    LOG(ERROR,
        "  Source MAC: " RTE_ETHER_ADDR_PRT_FMT,
        RTE_ETHER_ADDR_BYTES(&eth_hdr->src_addr));
    LOG(ERROR,
        "  Ether Type: 0x%04X",
        ntohs(eth_hdr->ether_type));

    uint16_t data_off = sizeof(struct rte_ether_hdr);

    // Determine the IP header type and extract it
    if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV4)) {
        struct rte_ipv4_hdr *ipv4_hdr =
            (struct rte_ipv4_hdr *)(eth_hdr + 1);
        data_off += rte_ipv4_hdr_len(ipv4_hdr);
        LOG(ERROR, "IPv4 Header:");
        LOG(ERROR,
            "  Version: %d",
            (ipv4_hdr->version_ihl & 0xF0) >> 4);
        LOG(ERROR,
            "  IHL: %d",
            ipv4_hdr->version_ihl & 0x0F);
        LOG(ERROR,
            "  Type of Service: 0x%02X",
            ipv4_hdr->type_of_service);
        LOG(ERROR,
            "  Total Length: %d",
            ntohs(ipv4_hdr->total_length));
        LOG(ERROR,
            "  Identification: 0x%04X",
            ntohs(ipv4_hdr->packet_id));
        LOG(ERROR,
            "  Fragment Offset: %d",
            rte_be_to_cpu_16(ipv4_hdr->fragment_offset) &
                RTE_IPV4_HDR_OFFSET_MASK);
        LOG(ERROR,
            "  Time to Live: %d",
            ipv4_hdr->time_to_live);
        LOG(ERROR,
            "  Protocol: 0x%02X",
            ipv4_hdr->next_proto_id);
        LOG(ERROR,
            "  Header Checksum: 0x%04X",
            ntohs(ipv4_hdr->hdr_checksum));

        char src_ip_str[INET_ADDRSTRLEN];
        inet_ntop(
            AF_INET,
            &ipv4_hdr->src_addr,
            src_ip_str,
            INET_ADDRSTRLEN
        );
        LOG(ERROR, "  Source IP: %s", src_ip_str);

        char dst_ip_str[INET_ADDRSTRLEN];
        inet_ntop(
            AF_INET,
            &ipv4_hdr->dst_addr,
            dst_ip_str,
            INET_ADDRSTRLEN
        );
        LOG(ERROR, "  Destination IP: %s", dst_ip_str);

        // Extract and print the protocol header
        uint8_t *proto_data = (uint8_t *)(ipv4_hdr + 1);

        switch (ipv4_hdr->next_proto_id) {
        case IPPROTO_UDP: {
            data_off += sizeof(struct rte_udp_hdr);
            struct rte_udp_hdr *udp_hdr =
                (struct rte_udp_hdr *)proto_data;
            LOG(ERROR, "UDP Header:");
            LOG(ERROR,
                "  Source Port: %d",
                ntohs(udp_hdr->src_port));
            LOG(ERROR,
                "  Destination Port: %d",
                ntohs(udp_hdr->dst_port));
            LOG(ERROR,
                "  Length: %d",
                ntohs(udp_hdr->dgram_len));
            LOG(ERROR,
                "  Checksum: 0x%04X",
                ntohs(udp_hdr->dgram_cksum));
            break;
        }
        case IPPROTO_TCP: {
            data_off += sizeof(struct rte_tcp_hdr);
            struct rte_tcp_hdr *tcp_hdr =
                (struct rte_tcp_hdr *)proto_data;
            LOG(ERROR, "TCP Header:");
            LOG(ERROR,
                "  Source Port: %d",
                ntohs(tcp_hdr->src_port));
            LOG(ERROR,
                "  Destination Port: %d",
                ntohs(tcp_hdr->dst_port));
            LOG(ERROR,
                "  Sequence Number: %u",
                ntohl(tcp_hdr->sent_seq));
            LOG(ERROR,
                "  Acknowledgment Number: %u",
                ntohl(tcp_hdr->recv_ack));
            LOG(ERROR,
                "  Data Offset: %d",
                (tcp_hdr->data_off & 0xF0) >> 4);
            LOG(ERROR,
                "  Flags: 0x%02X",
                tcp_hdr->tcp_flags);
            LOG(ERROR,
                "  Window Size: %d",
                ntohs(tcp_hdr->rx_win));
            LOG(ERROR,
                "  Checksum: 0x%04X",
                ntohs(tcp_hdr->cksum));
            break;
        }
        case IPPROTO_ICMP: {
            data_off += sizeof(struct icmphdr);
            struct icmphdr *icmp_hdr = (struct icmphdr *)proto_data;
            LOG(ERROR, "ICMP Header:");
            LOG(ERROR,
                "  Type: 0x%02X",
                icmp_hdr->type);
            LOG(ERROR,
                "  Code: 0x%02X",
                icmp_hdr->code);
            LOG(ERROR,
                "  Checksum: 0x%04X",
                ntohs(icmp_hdr->checksum));
            break;
        }
        }
    } else if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV6)) {
        struct rte_ipv6_hdr *ipv6_hdr =
            (struct rte_ipv6_hdr *)(eth_hdr + 1);
        data_off += sizeof(struct rte_ipv6_hdr);
        LOG(ERROR, "IPv6 Header:");
        LOG(ERROR,
            "  Version: %d",
            (htonl(ipv6_hdr->vtc_flow) & 0xF0000000) >> 28);
        LOG(ERROR,
            "  Traffic Class: 0x%02X",
            (htonl(ipv6_hdr->vtc_flow) & 0x0FF00000) >> 20);
        LOG(ERROR,
            "  Flow Label: 0x%05X",
            htonl(ipv6_hdr->vtc_flow) & 0x000FFFFF);
        LOG(ERROR,
            "  Payload Length: %d",
            ntohs(ipv6_hdr->payload_len));
        LOG(ERROR,
            "  Next Header: 0x%02X",
            ipv6_hdr->proto);
        LOG(ERROR,
            "  Hop Limit: %d",
            ipv6_hdr->hop_limits);

        char src_ip_str[INET6_ADDRSTRLEN];
        inet_ntop(
            AF_INET6,
            &ipv6_hdr->src_addr,
            src_ip_str,
            INET6_ADDRSTRLEN
        );
        LOG(ERROR, "  Source IP: %s", src_ip_str);

        char dst_ip_str[INET6_ADDRSTRLEN];
        inet_ntop(
            AF_INET6,
            &ipv6_hdr->dst_addr,
            dst_ip_str,
            INET6_ADDRSTRLEN
        );
        LOG(ERROR, "  Destination IP: %s", dst_ip_str);

        // Extract and print the protocol header
        uint8_t *proto_data = (uint8_t *)(ipv6_hdr + 1);
        switch (ipv6_hdr->proto) {
        case IPPROTO_UDP:
            data_off += sizeof(struct rte_udp_hdr);
            struct rte_udp_hdr *udp_hdr =
                (struct rte_udp_hdr *)proto_data;
            LOG(ERROR, "UDP Header:");
            LOG(ERROR,
                "  Source Port: %d",
                ntohs(udp_hdr->src_port));
            LOG(ERROR,
                "  Destination Port: %d",
                ntohs(udp_hdr->dst_port));
            LOG(ERROR,
                "  Length: %d",
                ntohs(udp_hdr->dgram_len));
            LOG(ERROR,
                "  Checksum: 0x%04X",
                ntohs(udp_hdr->dgram_cksum));
            break;
        case IPPROTO_TCP:
            data_off += sizeof(struct rte_tcp_hdr);
            struct rte_tcp_hdr *tcp_hdr =
                (struct rte_tcp_hdr *)proto_data;
            LOG(ERROR, "TCP Header:");
            LOG(ERROR,
                "  Source Port: %d",
                ntohs(tcp_hdr->src_port));
            LOG(ERROR,
                "  Destination Port: %d",
                ntohs(tcp_hdr->dst_port));
            LOG(ERROR,
                "  Sequence Number: %u",
                ntohl(tcp_hdr->sent_seq));
            LOG(ERROR,
                "  Acknowledgment Number: %u",
                ntohl(tcp_hdr->recv_ack));
            LOG(ERROR,
                "  Data Offset: %d",
                (tcp_hdr->data_off & 0xF0) >> 4);
            LOG(ERROR,
                "  Flags: 0x%02X",
                tcp_hdr->tcp_flags);
            LOG(ERROR,
                "  Window Size: %d",
                ntohs(tcp_hdr->rx_win));
            LOG(ERROR,
                "  Checksum: 0x%04X",
                ntohs(tcp_hdr->cksum));
            break;
        case IPPROTO_ICMPV6:
            data_off += sizeof(struct icmp6_hdr);
            struct icmp6_hdr *icmp6_hdr =
                (struct icmp6_hdr *)proto_data;
            LOG(ERROR, "ICMPv6 Header:");
            LOG(ERROR,
                "  Type: 0x%02X",
                icmp6_hdr->icmp6_type);
            LOG(ERROR,
                "  Code: 0x%02X",
                icmp6_hdr->icmp6_code);
            LOG(ERROR,
                "  Checksum: 0x%04X",
                ntohs(icmp6_hdr->icmp6_cksum));
            break;
        }
    }
    LOG(ERROR, "Data Length: %d", mbuf->pkt_len - data_off);
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
		LOG(ERROR, "processing packet with module [pre] %s, in %d, out %d, drop %d",
			 dp_module->name,
			  packet_list_counter(&packet_front->input),
			  packet_list_counter(&packet_front->output),
			   packet_list_counter(&packet_front->drop));

            packet_list_print(&packet_front->input);

		dp_module->handler(dp_config, module_data, packet_front);

		LOG(ERROR, "processing packet with module [post] %s, in %d, out %d, drop %d",
			dp_module->name,
			 packet_list_counter(&packet_front->input),
			 packet_list_counter(&packet_front->output),
			  packet_list_counter(&packet_front->drop));

                packet_list_print(&packet_front->output);
	}
}
