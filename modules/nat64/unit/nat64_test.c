#include <stdio.h>

#include <dlfcn.h>
#include <string.h>

#include <netinet/icmp6.h>
#include <netinet/ip_icmp.h>
#include <pcap.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "dataplane/dpdk.h"

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include <rte_common.h>
#include <rte_memcpy.h>

#include "dataplane/module/module.h"

#include "test.h"

#include "nat64dp.h"

#define RTE_LOGTYPE_NAT64_TEST RTE_LOGTYPE_USER8

struct nat64_unittest_params {
	struct packet_front packet_front;
	struct module *module;
	struct module_config *module_config;

	struct rte_mempool *mbuf_pool;
	uint8_t *config;
	uint32_t config_size;
};

static struct nat64_unittest_params test_params = {

	.mbuf_pool = NULL,
};

static uint32_t outer_ip4 = RTE_BE32(RTE_IPV4(192, 0, 2, 34));

// 198.51.100.0/24 - TEST-NET-2, rfc5737
// 2001:DB8::/32 - rfc3849
static struct {
	uint32_t count;
	struct {
		uint32_t ip4;
		uint32_t ip6[4];
	} mapping[8]; // Увеличиваем размер массива на 4 элемента
} __rte_packed config_data =
	{.count = 8,
	 .mapping = {
		 {
			 .ip4 = RTE_BE32(RTE_IPV4(198, 51, 100, 1)),
			 .ip6 = {RTE_BE32(0x20010DB8), 0, 0, RTE_BE32(0x4)},
		 },
		 {
			 .ip4 = RTE_BE32(RTE_IPV4(198, 51, 100, 2)),
			 .ip6 = {RTE_BE32(0x20010DB8), 0, 0, RTE_BE32(0x3)},
		 },
		 {
			 .ip4 = RTE_BE32(RTE_IPV4(198, 51, 100, 3)),
			 .ip6 = {RTE_BE32(0x20010DB8), 0, 0, RTE_BE32(0x2)},
		 },
		 {
			 .ip4 = RTE_BE32(RTE_IPV4(198, 51, 100, 4)),
			 .ip6 = {RTE_BE32(0x20010DB8), 0, 0, RTE_BE32(0x1)},
		 },
		 {
			 .ip4 = RTE_BE32(RTE_IPV4(198, 51, 100, 5)),
			 .ip6 = {RTE_BE32(0x20010DB8), 0, 0, RTE_BE32(0x8)},
		 },
		 {
			 .ip4 = RTE_BE32(RTE_IPV4(198, 51, 100, 6)),
			 .ip6 = {RTE_BE32(0x20010DB8), 0, 0, RTE_BE32(0x7)},
		 },
		 {
			 .ip4 = RTE_BE32(RTE_IPV4(198, 51, 100, 7)),
			 .ip6 = {RTE_BE32(0x20010DB8), 0, 0, RTE_BE32(0x6)},
		 },
		 {
			 .ip4 = RTE_BE32(RTE_IPV4(198, 51, 100, 8)),
			 .ip6 = {RTE_BE32(0x20010DB8), 0, 0, RTE_BE32(0x5)},
		 },
	 }};

static int
test_setup(void) {
	#ifdef NAT64_DEBUG
    rte_log_set_level(RTE_LOGTYPE_NAT64, RTE_LOG_DEBUG);
    rte_log_set_level(RTE_LOGTYPE_NAT64_TEST, RTE_LOG_DEBUG);
    #endif
	const uint8_t socket_id = rte_socket_id();
	if (test_params.mbuf_pool == NULL) {
		test_params.mbuf_pool = rte_pktmbuf_pool_create(
			"TEST_NAT64",
			4096,
			250,
			0,
			RTE_MBUF_DEFAULT_BUF_SIZE,
			socket_id
		);

		TEST_ASSERT_NOT_NULL(
			test_params.mbuf_pool, "rte_mempool_create failed\n"
		);
	}

	packet_front_init(&test_params.packet_front);
	RTE_LOG(DEBUG, NAT64_TEST, "Init packet front done.\n");

	return TEST_SUCCESS;
}

static int
test_module_config_handler(void) {
	test_params.module->config_handler(
		test_params.module,
		&config_data,
		sizeof(config_data),
		&test_params.module_config
	);
	TEST_ASSERT_NOT_NULL(
		test_params.module_config, "module_config_handler failed\n"
	);
	return TEST_SUCCESS;
}

static int
test_new_module_nat64(void) {
	test_params.module = new_module_nat64();
	TEST_ASSERT_NOT_NULL(test_params.module, "new_module_nat64 failed\n");
	return TEST_SUCCESS;
}

/**
 * @brief Creates an IPv6 packet.
 *
 * This function creates a new IPv6 packet with the specified mapping number and
 * protocol. The packet contains Ethernet, IPv6, and TCP headers, as well as
 * data filled with random values.
 *
 * @param num Mapping number in the `config_data.mapping` array used to
 * configure the source IPv6 addresses. Must be within the range from 0 to
 * `config_data.count - 1`.
 *
 * @param proto Protocol used in the IPv6 header. For example, IPPROTO_TCP for
 * TCP packets.
 *
 * @return Pointer to a `struct packet` containing the created packet if the
 * operation is successful. In case of failure (e.g., when mbuf allocation
 * fails), returns `NULL`.
 */
static struct packet *
create_ipv6_packet(uint8_t num, uint8_t proto) {
	struct rte_mbuf *mbuf = rte_pktmbuf_alloc(test_params.mbuf_pool);
	if (!mbuf) {
		return NULL;
	}
	struct rte_ether_hdr *eth_hdr =
		(struct rte_ether_hdr *)rte_pktmbuf_append(mbuf, 1500);
	memset(eth_hdr, 0xff, 2 * RTE_ETHER_ADDR_LEN);
	eth_hdr->ether_type = RTE_BE16(RTE_ETHER_TYPE_IPV6);

	struct rte_ipv6_hdr *ipv6Header = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, sizeof(struct rte_ether_hdr)
	);

	memset(ipv6Header, 0, sizeof(struct rte_ipv6_hdr));

	ipv6Header->vtc_flow = rte_cpu_to_be_32(0xa5a5a5a5);
	ipv6Header->payload_len =
		rte_cpu_to_be_16(sizeof(struct rte_tcp_hdr) + 10);
	ipv6Header->proto = proto;
	ipv6Header->hop_limits = 2;
	rte_memcpy(ipv6Header->src_addr, config_data.mapping[num].ip6, 16);
	rte_memcpy(ipv6Header->dst_addr + 12, &(outer_ip4), 16);

	struct rte_tcp_hdr *tcpHeader = rte_pktmbuf_mtod_offset(
		mbuf,
		struct rte_tcp_hdr *,
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv6_hdr)
	);

	tcpHeader->src_port = rte_cpu_to_be_16(1234);
	tcpHeader->dst_port = rte_cpu_to_be_16(80);

	mbuf->data_len = ipv6Header->payload_len + sizeof(struct rte_ipv6_hdr) +
			 sizeof(struct rte_ether_hdr);
	mbuf->pkt_len = mbuf->data_len;
	mbuf->port = 0;

	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(struct packet));
	packet->mbuf = mbuf;
	packet->rx_device_id = 0;
	packet->tx_device_id = 0;

	parse_packet(packet);

	return packet;
}

static int
test_nat64_v6_to_v4_generic() {

	// Create an IPv6 packet
	for (int i = 0; i < 4; i++) {
		RTE_LOG(INFO, NAT64_TEST, "Create ipv6 packet %d.\n", i);
		struct packet *pkt = create_ipv6_packet(i, IPPROTO_TCP);
		TEST_ASSERT_NOT_NULL(pkt, "Failed to create IPv6 packet\n");
		packet_list_add(&test_params.packet_front.input, pkt);
	}

	// Convert IPv6 to IPv4
	test_params.module->handler(
		test_params.module,
		test_params.module_config,
		&test_params.packet_front
	);

	int count = 0;
	struct packet *pkt;
	while ((pkt = packet_list_pop(&test_params.packet_front.output)) != NULL
	) {

		// parse again
		struct packet *packet = mbuf_to_packet(packet_to_mbuf(pkt));
		parse_packet(packet);

		struct rte_ipv4_hdr *ipv4_header = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		char ip_str[2 * INET_ADDRSTRLEN + 2];
		inet_ntop(
			AF_INET, &ipv4_header->src_addr, ip_str, INET_ADDRSTRLEN
		);
		inet_ntop(
			AF_INET,
			&ipv4_header->dst_addr,
			ip_str + INET_ADDRSTRLEN + 1,
			INET_ADDRSTRLEN
		);
		RTE_LOG(INFO,
			NAT64_TEST,
			"Got packet %s -> %s\n",
			ip_str,
			ip_str + INET_ADDRSTRLEN + 1);

		TEST_ASSERT_EQUAL(
			packet->network_header.type,
			RTE_BE16(RTE_ETHER_TYPE_IPV4),
			"Expected IPv4 packet, got %x\n",
			packet->network_header.type
		);
		TEST_ASSERT_EQUAL(
			packet->network_header.offset,
			sizeof(struct rte_ether_hdr),
			"Expected offset %lu, got %d\n",
			sizeof(struct rte_ether_hdr),
			packet->network_header.offset
		);
		TEST_ASSERT_EQUAL(
			packet->transport_header.offset,
			sizeof(struct rte_ether_hdr) +
				sizeof(struct rte_ipv4_hdr),
			"Expected offset %lu, got %d\n",
			sizeof(struct rte_ether_hdr) +
				sizeof(struct rte_ipv4_hdr),
			packet->transport_header.offset
		);
		TEST_ASSERT_EQUAL(
			ipv4_header->version_ihl,
			RTE_IPV4_VHL_DEF,
			"Expected version_ihl 0x45, got %d\n",
			ipv4_header->version_ihl
		);
		TEST_ASSERT_EQUAL(
			ipv4_header->total_length,
			RTE_BE16(50),
			"Expected total_length 50, got %d\n",
			rte_be_to_cpu_16(ipv4_header->total_length)
		);
		TEST_ASSERT_EQUAL(
			ipv4_header->src_addr,
			config_data.mapping[count].ip4,
			"Expected src_addr %x, got %x\n",
			config_data.mapping[count].ip4,
			ipv4_header->src_addr
		);
		count++;
	}
	TEST_ASSERT_EQUAL(count, 4, "Expected 4 packets, got %d\n", count);

	return 0;
}
struct upkt {
	struct rte_ether_hdr eth;
	union {
		struct rte_ipv4_hdr ipv4;
		struct rte_ipv6_hdr ipv6;
	} ip;
	union {
		struct rte_udp_hdr udp;
		struct rte_tcp_hdr tcp;
		struct icmp icmp;
		struct icmp6_hdr icmp6;
	} proto;
	uint16_t data_len;
	void *data;
};

void
print_upkt(struct upkt *pkt) {
	if (!pkt) {
		RTE_LOG(ERR, NAT64_TEST, "Packet is NULL\n");
		return;
	}

	// Print Ethernet Header
	RTE_LOG(INFO, NAT64_TEST, "Ethernet Header:\n");
	RTE_LOG(INFO, NAT64_TEST, "  Destination MAC: " RTE_ETHER_ADDR_PRT_FMT "\n", RTE_ETHER_ADDR_BYTES(&pkt->eth.dst_addr));
	RTE_LOG(INFO, NAT64_TEST, "  Source MAC: " RTE_ETHER_ADDR_PRT_FMT "\n", RTE_ETHER_ADDR_BYTES(&pkt->eth.src_addr));

	RTE_LOG(INFO, NAT64_TEST, "  Ether Type: 0x%04X\n", ntohs(pkt->eth.ether_type));

	// Print IP Header
	if (pkt->eth.ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV4)) {
		RTE_LOG(INFO, NAT64_TEST, "IPv4 Header:\n");
		RTE_LOG(INFO, NAT64_TEST, "  Version: %d\n",
		       (pkt->ip.ipv4.version_ihl & 0xF0) >> 4);
		RTE_LOG(INFO, NAT64_TEST, "  IHL: %d\n", pkt->ip.ipv4.version_ihl & 0x0F);
		RTE_LOG(INFO, NAT64_TEST, "  Type of Service: 0x%02X\n",
		       pkt->ip.ipv4.type_of_service);
		RTE_LOG(INFO, NAT64_TEST, "  Total Length: %d\n",
		       ntohs(pkt->ip.ipv4.total_length));
		RTE_LOG(INFO, NAT64_TEST, "  Identification: 0x%04X\n",
		       ntohs(pkt->ip.ipv4.packet_id));
		RTE_LOG(INFO, NAT64_TEST, "  Flags: 0x%01X\n",
		       (pkt->ip.ipv4.fragment_offset & 0xE000) >> 13);
		RTE_LOG(INFO, NAT64_TEST, "  Fragment Offset: %d\n",
		       pkt->ip.ipv4.fragment_offset & 0x1FFF);
		RTE_LOG(INFO, NAT64_TEST, "  Time to Live: %d\n", pkt->ip.ipv4.time_to_live);
		RTE_LOG(INFO, NAT64_TEST, "  Protocol: 0x%02X\n", pkt->ip.ipv4.next_proto_id);
		RTE_LOG(INFO, NAT64_TEST, "  Header Checksum: 0x%04X\n",
		       ntohs(pkt->ip.ipv4.hdr_checksum));

		char src_ip_str[INET_ADDRSTRLEN];
		inet_ntop(
			AF_INET,
			&pkt->ip.ipv4.src_addr,
			src_ip_str,
			INET_ADDRSTRLEN
		);
		RTE_LOG(INFO, NAT64_TEST, "  Source IP: %s\n", src_ip_str);

		char dst_ip_str[INET_ADDRSTRLEN];
		inet_ntop(
			AF_INET,
			&pkt->ip.ipv4.dst_addr,
			dst_ip_str,
			INET_ADDRSTRLEN
		);
		RTE_LOG(INFO, NAT64_TEST, "  Destination IP: %s\n", dst_ip_str);
	} else if (pkt->eth.ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV6)) {
		RTE_LOG(INFO, NAT64_TEST, "IPv6 Header:\n");
		RTE_LOG(INFO, NAT64_TEST, "  Version: %d\n",
		       (pkt->ip.ipv6.vtc_flow & 0xF0000000) >> 28);
		RTE_LOG(INFO, NAT64_TEST, "  Traffic Class: 0x%02X\n",
		       (pkt->ip.ipv6.vtc_flow & 0x0FF00000) >> 20);
		RTE_LOG(INFO, NAT64_TEST, "  Flow Label: 0x%05X\n",
		       pkt->ip.ipv6.vtc_flow & 0x000FFFFF);
		RTE_LOG(INFO, NAT64_TEST, "  Payload Length: %d\n",
		       ntohs(pkt->ip.ipv6.payload_len));
		RTE_LOG(INFO, NAT64_TEST, "  Next Header: 0x%02X\n", pkt->ip.ipv6.proto);
		RTE_LOG(INFO, NAT64_TEST, "  Hop Limit: %d\n", pkt->ip.ipv6.hop_limits);

		char src_ip_str[INET6_ADDRSTRLEN];
		inet_ntop(
			AF_INET6,
			&pkt->ip.ipv6.src_addr,
			src_ip_str,
			INET6_ADDRSTRLEN
		);
		RTE_LOG(INFO, NAT64_TEST, "  Source IP: %s\n", src_ip_str);

		char dst_ip_str[INET6_ADDRSTRLEN];
		inet_ntop(
			AF_INET6,
			&pkt->ip.ipv6.dst_addr,
			dst_ip_str,
			INET6_ADDRSTRLEN
		);
		RTE_LOG(INFO, NAT64_TEST, "  Destination IP: %s\n", dst_ip_str);
	}

	// Print Protocol Header
	switch (pkt->eth.ether_type) {
	case RTE_BE16(RTE_ETHER_TYPE_IPV4):
		switch (pkt->ip.ipv4.next_proto_id) {
		case IPPROTO_UDP:
			RTE_LOG(INFO, NAT64_TEST, "UDP Header:\n");
			RTE_LOG(INFO, NAT64_TEST, "  Source Port: %d\n",
			       ntohs(pkt->proto.udp.src_port));
			RTE_LOG(INFO, NAT64_TEST, "  Destination Port: %d\n",
			       ntohs(pkt->proto.udp.dst_port));
			RTE_LOG(INFO, NAT64_TEST, "  Length: %d\n",
			       ntohs(pkt->proto.udp.dgram_len));
			RTE_LOG(INFO, NAT64_TEST, "  Checksum: 0x%04X\n",
			       ntohs(pkt->proto.udp.dgram_cksum));
			break;
		case IPPROTO_TCP:
			RTE_LOG(INFO, NAT64_TEST, "TCP Header:\n");
			RTE_LOG(INFO, NAT64_TEST, "  Source Port: %d\n",
			       ntohs(pkt->proto.tcp.src_port));
			RTE_LOG(INFO, NAT64_TEST, "  Destination Port: %d\n",
			       ntohs(pkt->proto.tcp.dst_port));
			RTE_LOG(INFO, NAT64_TEST, "  Sequence Number: %u\n",
			       ntohl(pkt->proto.tcp.sent_seq));
			RTE_LOG(INFO, NAT64_TEST, "  Acknowledgment Number: %u\n",
			       ntohl(pkt->proto.tcp.recv_ack));
			RTE_LOG(INFO, NAT64_TEST, "  Data Offset: %d\n",
			       (pkt->proto.tcp.data_off & 0xF0) >> 4);
			RTE_LOG(INFO, NAT64_TEST, "  Flags: 0x%02X\n", pkt->proto.tcp.tcp_flags);
			RTE_LOG(INFO, NAT64_TEST, "  Window Size: %d\n",
			       ntohs(pkt->proto.tcp.rx_win));
			RTE_LOG(INFO, NAT64_TEST, "  Checksum: 0x%04X\n",
			       ntohs(pkt->proto.tcp.cksum));
			RTE_LOG(INFO, NAT64_TEST, "  Urgent Pointer: %d\n",
			       ntohs(pkt->proto.tcp.tcp_urp));
			break;
		case IPPROTO_ICMP:
			RTE_LOG(INFO, NAT64_TEST, "ICMP Header:\n");
			RTE_LOG(INFO, NAT64_TEST, "  Type: 0x%02X\n", pkt->proto.icmp.icmp_type);
			RTE_LOG(INFO, NAT64_TEST, "  Code: 0x%02X\n", pkt->proto.icmp.icmp_code);
			RTE_LOG(INFO, NAT64_TEST, "  Checksum: 0x%04X\n",
			       ntohs(pkt->proto.icmp.icmp_cksum));
			break;
		}
		break;
	case RTE_BE16(RTE_ETHER_TYPE_IPV6):
		switch (pkt->ip.ipv6.proto) {
		case IPPROTO_UDP:
			RTE_LOG(INFO, NAT64_TEST, "UDP Header:\n");
			RTE_LOG(INFO, NAT64_TEST, "  Source Port: %d\n",
			       ntohs(pkt->proto.udp.src_port));
			RTE_LOG(INFO, NAT64_TEST, "  Destination Port: %d\n",
			       ntohs(pkt->proto.udp.dst_port));
			RTE_LOG(INFO, NAT64_TEST, "  Length: %d\n",
			       ntohs(pkt->proto.udp.dgram_len));
			RTE_LOG(INFO, NAT64_TEST, "  Checksum: 0x%04X\n",
			       ntohs(pkt->proto.udp.dgram_cksum));
			break;
		case IPPROTO_TCP:
			RTE_LOG(INFO, NAT64_TEST, "TCP Header:\n");
			RTE_LOG(INFO, NAT64_TEST, "  Source Port: %d\n",
			       ntohs(pkt->proto.tcp.src_port));
			RTE_LOG(INFO, NAT64_TEST, "  Destination Port: %d\n",
			       ntohs(pkt->proto.tcp.dst_port));
			RTE_LOG(INFO, NAT64_TEST, "  Sequence Number: %u\n",
			       ntohl(pkt->proto.tcp.sent_seq));
			RTE_LOG(INFO, NAT64_TEST, "  Acknowledgment Number: %u\n",
			       ntohl(pkt->proto.tcp.recv_ack));
			RTE_LOG(INFO, NAT64_TEST, "  Data Offset: %d\n",
			       (pkt->proto.tcp.data_off & 0xF0) >> 4);
			RTE_LOG(INFO, NAT64_TEST, "  Flags: 0x%02X\n", pkt->proto.tcp.tcp_flags);
			RTE_LOG(INFO, NAT64_TEST, "  Window Size: %d\n",
			       ntohs(pkt->proto.tcp.rx_win));
			RTE_LOG(INFO, NAT64_TEST, "  Checksum: 0x%04X\n",
			       ntohs(pkt->proto.tcp.cksum));
			RTE_LOG(INFO, NAT64_TEST, "  Urgent Pointer: %d\n",
			       ntohs(pkt->proto.tcp.tcp_urp));
			break;
		case IPPROTO_ICMPV6:
			RTE_LOG(INFO, NAT64_TEST, "ICMPv6 Header:\n");
			RTE_LOG(INFO, NAT64_TEST, "  Type: 0x%02X\n", pkt->proto.icmp6.icmp6_type);
			RTE_LOG(INFO, NAT64_TEST, "  Code: 0x%02X\n", pkt->proto.icmp6.icmp6_code);
			RTE_LOG(INFO, NAT64_TEST, "  Checksum: 0x%04X\n",
			       ntohs(pkt->proto.icmp6.icmp6_cksum));
			break;
		}
		break;
	}

	// Print Data Length
	RTE_LOG(INFO, NAT64_TEST, "Data Length: %d\n", pkt->data_len);
}

void
print_packet(const struct packet *pkt) {
	if (!pkt) {
		RTE_LOG(INFO, NAT64_TEST, "Packet is NULL\n");
		return;
	}

	RTE_LOG(INFO, NAT64_TEST, "Packet Details:\n");

	RTE_LOG(INFO, NAT64_TEST, "Next: %p\n", (void *)pkt->next);
	RTE_LOG(INFO, NAT64_TEST, "MBUF: %p\n", (void *)pkt->mbuf);
	uint8_t *data = rte_pktmbuf_mtod(pkt->mbuf, uint8_t *);
	for (int i = 0; i < pkt->mbuf->data_len; i++) {
		RTE_LOG(INFO, NAT64_TEST, " %02X\n", *(data + i));
	}
	RTE_LOG(INFO, NAT64_TEST, "Pipeline: %p\n", (void *)pkt->pipeline);
	RTE_LOG(INFO, NAT64_TEST, "Hash: 0x%08X\n", pkt->hash);
	RTE_LOG(INFO, NAT64_TEST, "RX Device ID: %u\n", pkt->rx_device_id);
	RTE_LOG(INFO, NAT64_TEST, "TX Device ID: %u\n", pkt->tx_device_id);
	RTE_LOG(INFO, NAT64_TEST, "TX Result: %u\n", pkt->tx_result);
	RTE_LOG(INFO, NAT64_TEST, "Flags: 0x%04X\n", pkt->flags);
	RTE_LOG(INFO, NAT64_TEST, "VLAN: %u\n", pkt->vlan);
	RTE_LOG(INFO, NAT64_TEST, "Flow Label: 0x%08X (Label: 0x%05X)\n",
	       pkt->flow_label,
	       pkt->flow_label & 0xFFFFF);

	RTE_LOG(INFO, NAT64_TEST, "Network Header:\n");
	RTE_LOG(INFO, NAT64_TEST, "  Type: 0x%04X\n", pkt->network_header.type);
	RTE_LOG(INFO, NAT64_TEST, "  Offset: 0x%04X\n", pkt->network_header.offset);

	RTE_LOG(INFO, NAT64_TEST, "Transport Header:\n");
	RTE_LOG(INFO, NAT64_TEST, "  Type: 0x%04X\n", pkt->transport_header.type);
	RTE_LOG(INFO, NAT64_TEST, "  Offset: 0x%04X\n", pkt->transport_header.offset);
}

void
print_rte_mbuf(struct rte_mbuf *mbuf) {
	if (!mbuf) {
		RTE_LOG(ERR, NAT64_TEST, "Mbuf is NULL\n");
		return;
	}

	// Get the data pointer
	uint8_t *data = rte_pktmbuf_mtod(mbuf, uint8_t *);

	// Extract Ethernet header
	struct rte_ether_hdr *eth_hdr = (struct rte_ether_hdr *)data;
	RTE_LOG(INFO, NAT64_TEST, "Ethernet Header:\n");
	RTE_LOG(INFO, NAT64_TEST, "  Destination MAC: " RTE_ETHER_ADDR_PRT_FMT "\n", RTE_ETHER_ADDR_BYTES(&eth_hdr->dst_addr));
	RTE_LOG(INFO, NAT64_TEST, "  Source MAC: " RTE_ETHER_ADDR_PRT_FMT "\n", RTE_ETHER_ADDR_BYTES(&eth_hdr->src_addr));

	RTE_LOG(INFO, NAT64_TEST, "  Ether Type: 0x%04X\n", ntohs(eth_hdr->ether_type));

	uint16_t data_off = sizeof(struct rte_ether_hdr);

	// Determine the IP header type and extract it
	if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *ipv4_hdr =
			(struct rte_ipv4_hdr *)(eth_hdr + 1);
		data_off += rte_ipv4_hdr_len(ipv4_hdr);
		RTE_LOG(INFO, NAT64_TEST, "IPv4 Header:\n");
		RTE_LOG(INFO, NAT64_TEST, "  Version: %d\n", (ipv4_hdr->version_ihl & 0xF0) >> 4);
		RTE_LOG(INFO, NAT64_TEST, "  IHL: %d\n", ipv4_hdr->version_ihl & 0x0F);
		RTE_LOG(INFO, NAT64_TEST, "  Type of Service: 0x%02X\n",
		       ipv4_hdr->type_of_service);
		RTE_LOG(INFO, NAT64_TEST, "  Total Length: %d\n", ntohs(ipv4_hdr->total_length));
		RTE_LOG(INFO, NAT64_TEST, "  Identification: 0x%04X\n",
		       ntohs(ipv4_hdr->packet_id));
		RTE_LOG(INFO, NAT64_TEST, "  Flags: 0x%01X\n",
		       (ipv4_hdr->fragment_offset & 0xE000) >> 13);
		RTE_LOG(INFO, NAT64_TEST, "  Fragment Offset: %d\n",
		       ipv4_hdr->fragment_offset & 0x1FFF);
		RTE_LOG(INFO, NAT64_TEST, "  Time to Live: %d\n", ipv4_hdr->time_to_live);
		RTE_LOG(INFO, NAT64_TEST, "  Protocol: 0x%02X\n", ipv4_hdr->next_proto_id);
		RTE_LOG(INFO, NAT64_TEST, "  Header Checksum: 0x%04X\n",
		       ntohs(ipv4_hdr->hdr_checksum));

		char src_ip_str[INET_ADDRSTRLEN];
		inet_ntop(
			AF_INET,
			&ipv4_hdr->src_addr,
			src_ip_str,
			INET_ADDRSTRLEN
		);
		RTE_LOG(INFO, NAT64_TEST, "  Source IP: %s\n", src_ip_str);

		char dst_ip_str[INET_ADDRSTRLEN];
		inet_ntop(
			AF_INET,
			&ipv4_hdr->dst_addr,
			dst_ip_str,
			INET_ADDRSTRLEN
		);
		RTE_LOG(INFO, NAT64_TEST, "  Destination IP: %s\n", dst_ip_str);

		// Extract and print the protocol header
		uint8_t *proto_data = (uint8_t *)(ipv4_hdr + 1);

		switch (ipv4_hdr->next_proto_id) {
		case IPPROTO_UDP:
			data_off += sizeof(struct rte_udp_hdr);
			struct rte_udp_hdr *udp_hdr =
				(struct rte_udp_hdr *)proto_data;
			RTE_LOG(INFO, NAT64_TEST, "UDP Header:\n");
			RTE_LOG(INFO, NAT64_TEST, "  Source Port: %d\n", ntohs(udp_hdr->src_port));
			RTE_LOG(INFO, NAT64_TEST, "  Destination Port: %d\n",
			       ntohs(udp_hdr->dst_port));
			RTE_LOG(INFO, NAT64_TEST, "  Length: %d\n", ntohs(udp_hdr->dgram_len));
			RTE_LOG(INFO, NAT64_TEST, "  Checksum: 0x%04X\n",
			       ntohs(udp_hdr->dgram_cksum));
			break;
		case IPPROTO_TCP:
			data_off += sizeof(struct rte_tcp_hdr);
			struct rte_tcp_hdr *tcp_hdr =
				(struct rte_tcp_hdr *)proto_data;
			RTE_LOG(INFO, NAT64_TEST, "TCP Header:\n");
			RTE_LOG(INFO, NAT64_TEST, "  Source Port: %d\n", ntohs(tcp_hdr->src_port));
			RTE_LOG(INFO, NAT64_TEST, "  Destination Port: %d\n",
			       ntohs(tcp_hdr->dst_port));
			RTE_LOG(INFO, NAT64_TEST, "  Sequence Number: %u\n",
			       ntohl(tcp_hdr->sent_seq));
			RTE_LOG(INFO, NAT64_TEST, "  Acknowledgment Number: %u\n",
			       ntohl(tcp_hdr->recv_ack));
			RTE_LOG(INFO, NAT64_TEST, "  Data Offset: %d\n",
			       (tcp_hdr->data_off & 0xF0) >> 4);
			RTE_LOG(INFO, NAT64_TEST, "  Flags: 0x%02X\n", tcp_hdr->tcp_flags);
			RTE_LOG(INFO, NAT64_TEST, "  Window Size: %d\n", ntohs(tcp_hdr->rx_win));
			RTE_LOG(INFO, NAT64_TEST, "  Checksum: 0x%04X\n", ntohs(tcp_hdr->cksum));
			break;
		case IPPROTO_ICMP:
			data_off += sizeof(struct icmp);
			struct icmp *icmp_hdr = (struct icmp *)proto_data;
			RTE_LOG(INFO, NAT64_TEST, "ICMP Header:\n");
			RTE_LOG(INFO, NAT64_TEST, "  Type: 0x%02X\n", icmp_hdr->icmp_type);
			RTE_LOG(INFO, NAT64_TEST, "  Code: 0x%02X\n", icmp_hdr->icmp_code);
			RTE_LOG(INFO, NAT64_TEST, "  Checksum: 0x%04X\n",
			       ntohs(icmp_hdr->icmp_cksum));
			break;
		}
	} else if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *ipv6_hdr =
			(struct rte_ipv6_hdr *)(eth_hdr + 1);
		RTE_LOG(INFO, NAT64_TEST, "IPv6 Header:\n");
		RTE_LOG(INFO, NAT64_TEST, "  Version: %d\n",
		       (ipv6_hdr->vtc_flow & 0xF0000000) >> 28);
		RTE_LOG(INFO, NAT64_TEST, "  Traffic Class: 0x%02X\n",
		       (ipv6_hdr->vtc_flow & 0x0FF00000) >> 20);
		RTE_LOG(INFO, NAT64_TEST, "  Flow Label: 0x%05X\n",
		       ipv6_hdr->vtc_flow & 0x000FFFFF);
		RTE_LOG(INFO, NAT64_TEST, "  Payload Length: %d\n", ntohs(ipv6_hdr->payload_len));
		RTE_LOG(INFO, NAT64_TEST, "  Next Header: 0x%02X\n", ipv6_hdr->proto);
		RTE_LOG(INFO, NAT64_TEST, "  Hop Limit: %d\n", ipv6_hdr->hop_limits);

		char src_ip_str[INET6_ADDRSTRLEN];
		inet_ntop(
			AF_INET6,
			&ipv6_hdr->src_addr,
			src_ip_str,
			INET6_ADDRSTRLEN
		);
		RTE_LOG(INFO, NAT64_TEST, "  Source IP: %s\n", src_ip_str);

		char dst_ip_str[INET6_ADDRSTRLEN];
		inet_ntop(
			AF_INET6,
			&ipv6_hdr->dst_addr,
			dst_ip_str,
			INET6_ADDRSTRLEN
		);
		RTE_LOG(INFO, NAT64_TEST, "  Destination IP: %s\n", dst_ip_str);

		// Extract and print the protocol header
		uint8_t *proto_data = (uint8_t *)(ipv6_hdr + 1);
		switch (ipv6_hdr->proto) {
		case IPPROTO_UDP:
			data_off += sizeof(struct rte_udp_hdr);
			struct rte_udp_hdr *udp_hdr =
				(struct rte_udp_hdr *)proto_data;
			RTE_LOG(INFO, NAT64_TEST, "UDP Header:\n");
			RTE_LOG(INFO, NAT64_TEST, "  Source Port: %d\n", ntohs(udp_hdr->src_port));
			RTE_LOG(INFO, NAT64_TEST, "  Destination Port: %d\n",
			       ntohs(udp_hdr->dst_port));
			RTE_LOG(INFO, NAT64_TEST, "  Length: %d\n", ntohs(udp_hdr->dgram_len));
			RTE_LOG(INFO, NAT64_TEST, "  Checksum: 0x%04X\n",
			       ntohs(udp_hdr->dgram_cksum));
			break;
		case IPPROTO_TCP:
			data_off += sizeof(struct rte_tcp_hdr);
			struct rte_tcp_hdr *tcp_hdr =
				(struct rte_tcp_hdr *)proto_data;
			RTE_LOG(INFO, NAT64_TEST, "TCP Header:\n");
			RTE_LOG(INFO, NAT64_TEST, "  Source Port: %d\n", ntohs(tcp_hdr->src_port));
			RTE_LOG(INFO, NAT64_TEST, "  Destination Port: %d\n",
			       ntohs(tcp_hdr->dst_port));
			RTE_LOG(INFO, NAT64_TEST, "  Sequence Number: %u\n",
			       ntohl(tcp_hdr->sent_seq));
			RTE_LOG(INFO, NAT64_TEST, "  Acknowledgment Number: %u\n",
			       ntohl(tcp_hdr->recv_ack));
			RTE_LOG(INFO, NAT64_TEST, "  Data Offset: %d\n",
			       (tcp_hdr->data_off & 0xF0) >> 4);
			RTE_LOG(INFO, NAT64_TEST, "  Flags: 0x%02X\n", tcp_hdr->tcp_flags);
			RTE_LOG(INFO, NAT64_TEST, "  Window Size: %d\n", ntohs(tcp_hdr->rx_win));
			RTE_LOG(INFO, NAT64_TEST, "  Checksum: 0x%04X\n", ntohs(tcp_hdr->cksum));
			break;
		case IPPROTO_ICMPV6:
			data_off += sizeof(struct icmp6_hdr);
			struct icmp6_hdr *icmp6_hdr =
				(struct icmp6_hdr *)proto_data;
			RTE_LOG(INFO, NAT64_TEST, "ICMPv6 Header:\n");
			RTE_LOG(INFO, NAT64_TEST, "  Type: 0x%02X\n", icmp6_hdr->icmp6_type);
			RTE_LOG(INFO, NAT64_TEST, "  Code: 0x%02X\n", icmp6_hdr->icmp6_code);
			RTE_LOG(INFO, NAT64_TEST, "  Checksum: 0x%04X\n",
			       ntohs(icmp6_hdr->icmp6_cksum));
			break;
		}
	}
	RTE_LOG(INFO, NAT64_TEST, "Data Length: %d\n", mbuf->pkt_len - data_off);
}

static inline int
print_diff_upkt_and_rte_mbuf(struct upkt *upkt, struct rte_mbuf *mbuf) {
	if (!upkt || !mbuf) {
		RTE_LOG(ERR, NAT64_TEST, "One or both inputs are NULL\n");
		return -1;
	}

	int result = 0;

	// Extract Ethernet header from mbuf
	struct rte_ether_hdr *eth_hdr =
		rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);
	if (memcmp(eth_hdr->dst_addr.addr_bytes,
		   upkt->eth.dst_addr.addr_bytes,
		   6) != 0) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in Ethernet destination address\n");
		RTE_LOG(ERR, NAT64_TEST, "UPKT: %02x:%02x:%02x:%02x:%02x:%02x\n",
		       upkt->eth.dst_addr.addr_bytes[0],
		       upkt->eth.dst_addr.addr_bytes[1],
		       upkt->eth.dst_addr.addr_bytes[2],
		       upkt->eth.dst_addr.addr_bytes[3],
		       upkt->eth.dst_addr.addr_bytes[4],
		       upkt->eth.dst_addr.addr_bytes[5]);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %02x:%02x:%02x:%02x:%02x:%02x\n",
		       eth_hdr->dst_addr.addr_bytes[0],
		       eth_hdr->dst_addr.addr_bytes[1],
		       eth_hdr->dst_addr.addr_bytes[2],
		       eth_hdr->dst_addr.addr_bytes[3],
		       eth_hdr->dst_addr.addr_bytes[4],
		       eth_hdr->dst_addr.addr_bytes[5]);
	}

	if (memcmp(eth_hdr->src_addr.addr_bytes,
		   upkt->eth.src_addr.addr_bytes,
		   6) != 0) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in Ethernet source address\n");
		RTE_LOG(ERR, NAT64_TEST, "UPKT: %02x:%02x:%02x:%02x:%02x:%02x\n",
		       upkt->eth.src_addr.addr_bytes[0],
		       upkt->eth.src_addr.addr_bytes[1],
		       upkt->eth.src_addr.addr_bytes[2],
		       upkt->eth.src_addr.addr_bytes[3],
		       upkt->eth.src_addr.addr_bytes[4],
		       upkt->eth.src_addr.addr_bytes[5]);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %02x:%02x:%02x:%02x:%02x:%02x\n",
		       eth_hdr->src_addr.addr_bytes[0],
		       eth_hdr->src_addr.addr_bytes[1],
		       eth_hdr->src_addr.addr_bytes[2],
		       eth_hdr->src_addr.addr_bytes[3],
		       eth_hdr->src_addr.addr_bytes[4],
		       eth_hdr->src_addr.addr_bytes[5]);
	}

	if (eth_hdr->ether_type != upkt->eth.ether_type) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in Ethernet type\n");
		RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%hx\n", upkt->eth.ether_type);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%hx\n", eth_hdr->ether_type);
	}
	uint16_t data_off = sizeof(struct rte_ether_hdr);

	uint8_t *ip_hdr_offset = (uint8_t *)(eth_hdr + 1);

	if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *ipv4_hdr =
			(struct rte_ipv4_hdr *)ip_hdr_offset;
		data_off += rte_ipv4_hdr_len(ipv4_hdr);
		if (ipv4_hdr->version_ihl != upkt->ip.ipv4.version_ihl) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 version/IHL\n");
			RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%x\n", upkt->ip.ipv4.version_ihl);
			RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%x\n", ipv4_hdr->version_ihl);
		}

		if (ipv4_hdr->total_length != upkt->ip.ipv4.total_length) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 total length\n");
			RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%x\n", upkt->ip.ipv4.total_length);
			RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%x\n", ipv4_hdr->total_length);
		}

		if (ipv4_hdr->time_to_live != upkt->ip.ipv4.time_to_live) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 TTL\n");
			RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%x\n", upkt->ip.ipv4.time_to_live);
			RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%x\n", ipv4_hdr->time_to_live);
		}

		if (ipv4_hdr->next_proto_id != upkt->ip.ipv4.next_proto_id) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 next protocol\n");
			RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%x\n", upkt->ip.ipv4.next_proto_id);
			RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%x\n", ipv4_hdr->next_proto_id);
		}

		if (ipv4_hdr->src_addr != upkt->ip.ipv4.src_addr) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 source address\n");
			RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%x\n", upkt->ip.ipv4.src_addr);
			RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%x\n", ipv4_hdr->src_addr);
		}

		if (ipv4_hdr->dst_addr != upkt->ip.ipv4.dst_addr) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 destination address\n");
			RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%x\n", upkt->ip.ipv4.dst_addr);
			RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%x\n", ipv4_hdr->dst_addr);
		}

		uint8_t *proto_data = (uint8_t *)(ipv4_hdr + 1);
		switch (ipv4_hdr->next_proto_id) {
		case IPPROTO_UDP:
			data_off += sizeof(struct rte_udp_hdr);
			struct rte_udp_hdr *udp_hdr =
				(struct rte_udp_hdr *)proto_data;
			if (udp_hdr->src_port != upkt->proto.udp.src_port) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in UDP source port\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.udp.src_port));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(udp_hdr->src_port));
			}

			if (udp_hdr->dst_port != upkt->proto.udp.dst_port) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in UDP destination port\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.udp.dst_port));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(udp_hdr->dst_port));
			}

			if (udp_hdr->dgram_len != upkt->proto.udp.dgram_len) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in UDP length\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.udp.dgram_len));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(udp_hdr->dgram_len));
			}

			if (udp_hdr->dgram_cksum !=
			    upkt->proto.udp.dgram_cksum) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in UDP checksum\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%04X\n",
				       ntohs(upkt->proto.udp.dgram_cksum));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%04X\n",
				       ntohs(udp_hdr->dgram_cksum));
			}
			break;
		case IPPROTO_TCP:
			data_off += sizeof(struct rte_tcp_hdr);
			struct rte_tcp_hdr *tcp_hdr =
				(struct rte_tcp_hdr *)proto_data;
			if (tcp_hdr->src_port != upkt->proto.tcp.src_port) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP source port\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.tcp.src_port));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(tcp_hdr->src_port));
			}

			if (tcp_hdr->dst_port != upkt->proto.tcp.dst_port) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP destination port\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.tcp.dst_port));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(tcp_hdr->dst_port));
			}

			if (tcp_hdr->sent_seq != upkt->proto.tcp.sent_seq) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP sequence number\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %u\n",
				       ntohl(upkt->proto.tcp.sent_seq));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %u\n", ntohl(tcp_hdr->sent_seq));
			}

			if (tcp_hdr->recv_ack != upkt->proto.tcp.recv_ack) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP acknowledgment "
				       "number\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %u\n",
				       ntohl(upkt->proto.tcp.recv_ack));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %u\n", ntohl(tcp_hdr->recv_ack));
			}

			if ((tcp_hdr->data_off & 0xF0) >> 4 !=
			    (upkt->proto.tcp.data_off & 0xF0) >> 4) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP data offset\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       (upkt->proto.tcp.data_off & 0xF0) >> 4);
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n",
				       (tcp_hdr->data_off & 0xF0) >> 4);
			}

			if (tcp_hdr->tcp_flags != upkt->proto.tcp.tcp_flags) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP flags\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%02X\n",
				       upkt->proto.tcp.tcp_flags);
				RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%02X\n", tcp_hdr->tcp_flags);
			}

			if (tcp_hdr->rx_win != upkt->proto.tcp.rx_win) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP window size\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.tcp.rx_win));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(tcp_hdr->rx_win));
			}

			if (tcp_hdr->cksum != upkt->proto.tcp.cksum) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP checksum\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%04X\n",
				       ntohs(upkt->proto.tcp.cksum));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%04X\n", ntohs(tcp_hdr->cksum));
			}

			if (tcp_hdr->tcp_urp != upkt->proto.tcp.tcp_urp) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP urgent pointer\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.tcp.tcp_urp));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(tcp_hdr->tcp_urp));
			}
			break;
		case IPPROTO_ICMP:
			data_off += sizeof(struct icmp);
			struct icmp *icmp_hdr = (struct icmp *)proto_data;
			if (icmp_hdr->icmp_type != upkt->proto.icmp.icmp_type) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in ICMP type\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%02X\n",
				       upkt->proto.icmp.icmp_type);
				RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%02X\n", icmp_hdr->icmp_type);
			}

			if (icmp_hdr->icmp_code != upkt->proto.icmp.icmp_code) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in ICMP code\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%02X\n",
				       upkt->proto.icmp.icmp_code);
				RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%02X\n", icmp_hdr->icmp_code);
			}

			if (icmp_hdr->icmp_cksum !=
			    upkt->proto.icmp.icmp_cksum) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in ICMP checksum\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%04X\n",
				       ntohs(upkt->proto.icmp.icmp_cksum));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%04X\n",
				       ntohs(icmp_hdr->icmp_cksum));
			}
			break;
		}
	} else if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *ipv6_hdr =
			(struct rte_ipv6_hdr *)ip_hdr_offset;
		data_off += sizeof(struct rte_ipv6_hdr);
		if ((ipv6_hdr->vtc_flow & 0xF0000000) >> 28 !=
		    (upkt->ip.ipv6.vtc_flow & 0xF0000000) >> 28) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in IPv6 version\n");
			RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
			       (upkt->ip.ipv6.vtc_flow & 0xF0000000) >> 28);
			RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n",
			       (ipv6_hdr->vtc_flow & 0xF0000000) >> 28);
		}

		if (memcmp(&ipv6_hdr->src_addr, &upkt->ip.ipv6.src_addr, 16) !=
		    0) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in IPv6 source address\n");
			char src_ip_str_upkt[INET6_ADDRSTRLEN];
			char src_ip_str_mbuf[INET6_ADDRSTRLEN];
			inet_ntop(
				AF_INET6,
				&upkt->ip.ipv6.src_addr,
				src_ip_str_upkt,
				INET6_ADDRSTRLEN
			);
			inet_ntop(
				AF_INET6,
				&ipv6_hdr->src_addr,
				src_ip_str_mbuf,
				INET6_ADDRSTRLEN
			);
			RTE_LOG(ERR, NAT64_TEST, "UPKT: %s\n", src_ip_str_upkt);
			RTE_LOG(ERR, NAT64_TEST, "MBUF: %s\n", src_ip_str_mbuf);
		}

		if (memcmp(&ipv6_hdr->dst_addr, &upkt->ip.ipv6.dst_addr, 16) !=
		    0) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in IPv6 destination address\n");
			char dst_ip_str_upkt[INET6_ADDRSTRLEN];
			char dst_ip_str_mbuf[INET6_ADDRSTRLEN];
			inet_ntop(
				AF_INET6,
				&upkt->ip.ipv6.dst_addr,
				dst_ip_str_upkt,
				INET6_ADDRSTRLEN
			);
			inet_ntop(
				AF_INET6,
				&ipv6_hdr->dst_addr,
				dst_ip_str_mbuf,
				INET6_ADDRSTRLEN
			);
			RTE_LOG(ERR, NAT64_TEST, "UPKT: %s\n", dst_ip_str_upkt);
			RTE_LOG(ERR, NAT64_TEST, "MBUF: %s\n", dst_ip_str_mbuf);
		}

		if (ipv6_hdr->payload_len !=
		    rte_be_to_cpu_16(upkt->ip.ipv6.payload_len)) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in IPv6 payload length\n");
			RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
			       rte_be_to_cpu_16(upkt->ip.ipv6.payload_len));
			RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ipv6_hdr->payload_len);
		}

		if (ipv6_hdr->proto != upkt->ip.ipv6.proto) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in IPv6 next header\n");
			RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%02X\n", upkt->ip.ipv6.proto);
			RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%02X\n", ipv6_hdr->proto);
		}

		if (ipv6_hdr->hop_limits != upkt->ip.ipv6.hop_limits) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in IPv6 hop limit\n");
			RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n", upkt->ip.ipv6.hop_limits);
			RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ipv6_hdr->hop_limits);
		}

		uint8_t *proto_data = (uint8_t *)(ipv6_hdr + 1);
		switch (ipv6_hdr->proto) {
		case IPPROTO_UDP:
			data_off += sizeof(struct rte_udp_hdr);
			struct rte_udp_hdr *udp_hdr =
				(struct rte_udp_hdr *)proto_data;
			if (udp_hdr->src_port != upkt->proto.udp.src_port) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in UDP source port\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.udp.src_port));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(udp_hdr->src_port));
			}

			if (udp_hdr->dst_port != upkt->proto.udp.dst_port) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in UDP destination port\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.udp.dst_port));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(udp_hdr->dst_port));
			}

			if (udp_hdr->dgram_len != upkt->proto.udp.dgram_len) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in UDP length\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.udp.dgram_len));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(udp_hdr->dgram_len));
			}

			if (udp_hdr->dgram_cksum !=
			    upkt->proto.udp.dgram_cksum) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in UDP checksum\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%04X\n",
				       ntohs(upkt->proto.udp.dgram_cksum));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%04X\n",
				       ntohs(udp_hdr->dgram_cksum));
			}
			break;
		case IPPROTO_TCP:
			data_off += sizeof(struct rte_tcp_hdr);
			struct rte_tcp_hdr *tcp_hdr =
				(struct rte_tcp_hdr *)proto_data;
			if (tcp_hdr->src_port != upkt->proto.tcp.src_port) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP source port\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.tcp.src_port));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(tcp_hdr->src_port));
			}

			if (tcp_hdr->dst_port != upkt->proto.tcp.dst_port) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP destination port\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.tcp.dst_port));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(tcp_hdr->dst_port));
			}

			if (tcp_hdr->sent_seq != upkt->proto.tcp.sent_seq) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP sequence number\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %u\n",
				       ntohl(upkt->proto.tcp.sent_seq));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %u\n", ntohl(tcp_hdr->sent_seq));
			}

			if (tcp_hdr->recv_ack != upkt->proto.tcp.recv_ack) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP acknowledgment "
				       "number\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %u\n",
				       ntohl(upkt->proto.tcp.recv_ack));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %u\n", ntohl(tcp_hdr->recv_ack));
			}

			if ((tcp_hdr->data_off & 0xF0) >> 4 !=
			    (upkt->proto.tcp.data_off & 0xF0) >> 4) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP data offset\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       (upkt->proto.tcp.data_off & 0xF0) >> 4);
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n",
				       (tcp_hdr->data_off & 0xF0) >> 4);
			}

			if (tcp_hdr->tcp_flags != upkt->proto.tcp.tcp_flags) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP flags\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%02X\n",
				       upkt->proto.tcp.tcp_flags);
				RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%02X\n", tcp_hdr->tcp_flags);
			}

			if (tcp_hdr->rx_win != upkt->proto.tcp.rx_win) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP window size\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.tcp.rx_win));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(tcp_hdr->rx_win));
			}

			if (tcp_hdr->cksum != upkt->proto.tcp.cksum) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP checksum\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%04X\n",
				       ntohs(upkt->proto.tcp.cksum));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%04X\n", ntohs(tcp_hdr->cksum));
			}

			if (tcp_hdr->tcp_urp != upkt->proto.tcp.tcp_urp) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in TCP urgent pointer\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n",
				       ntohs(upkt->proto.tcp.tcp_urp));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(tcp_hdr->tcp_urp));
			}
			break;
		case IPPROTO_ICMPV6:
			data_off += sizeof(struct icmp6_hdr);
			struct icmp6_hdr *icmp6_hdr =
				(struct icmp6_hdr *)proto_data;
			if (icmp6_hdr->icmp6_type !=
			    upkt->proto.icmp6.icmp6_type) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in ICMPv6 type\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%02X\n",
				       upkt->proto.icmp6.icmp6_type);
				RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%02X\n", icmp6_hdr->icmp6_type);
			}

			if (icmp6_hdr->icmp6_code !=
			    upkt->proto.icmp6.icmp6_code) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in ICMPv6 code\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%02X\n",
				       upkt->proto.icmp6.icmp6_code);
				RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%02X\n", icmp6_hdr->icmp6_code);
			}

			if (icmp6_hdr->icmp6_cksum !=
			    upkt->proto.icmp6.icmp6_cksum) {
				result = -1;
				RTE_LOG(ERR, NAT64_TEST, "Difference in ICMPv6 checksum\n");
				RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%04X\n",
				       ntohs(upkt->proto.icmp6.icmp6_cksum));
				RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%04X\n",
				       ntohs(icmp6_hdr->icmp6_cksum));
			}
			break;
		}
	}

	// Print Data Length
	if ((mbuf->data_len - data_off) != upkt->data_len) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in Data Length\n");
		RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n", upkt->data_len);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", mbuf->data_len - data_off);
	}
	return result;
}

struct test_case {
	struct test_case *next;
	char *name;
	struct upkt pkt;
	struct upkt pkt_expected;
};

static struct test_case test_cases = {
	.next = NULL,
	.name = "drop unknow mapping",
	.pkt =
		{
			.eth =
				{
					.dst_addr.addr_bytes =
						"\xff\xff\xff\xff\xff\xff",
					.src_addr.addr_bytes =
						"\x02\x00\x00\x00\x00\x00",
					.ether_type =
						RTE_BE16(RTE_ETHER_TYPE_IPV4),
				},
			.ip.ipv4 =
				{
					.version_ihl = RTE_IPV4_VHL_DEF,
					.total_length = RTE_BE16(
						sizeof(struct rte_ipv4_hdr) +
						sizeof(struct rte_udp_hdr)
					),
					.time_to_live = DEFAULT_TTL,
					.next_proto_id = IPPROTO_UDP,
					.src_addr = RTE_BE32(0x01010101),
					.dst_addr = RTE_BE32(0x02020202),
				},
			.proto.udp =
				{
					.dst_port = RTE_BE16(9),
				},
		},
	.pkt_expected = {.eth.dst_addr.addr_bytes = {0}},
};

// Функция для добавления тестового случая
static inline void
append_test_case(
	struct test_case **current_case,
	struct upkt pkt,
	struct upkt pkt_expected,
	char *name
) {
	if (*current_case == NULL) {
		*current_case = malloc(sizeof(struct test_case));
		memset(*current_case, 0, sizeof(struct test_case));
	} else {
		(*current_case)->next = malloc(sizeof(struct test_case));
		*current_case = (*current_case)->next;
		memset(*current_case, 0, sizeof(struct test_case));
	}
	(*current_case)->pkt = pkt;
	(*current_case)->name = strdup(name);
	(*current_case)->pkt_expected = pkt_expected;
}

static inline void
fix_checksums(struct upkt *pkt) {
    if (pkt->eth.ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV4)) {
        struct rte_ipv4_hdr *ipv4_hdr = &pkt->ip.ipv4;
        ipv4_hdr->hdr_checksum = 0;
        ipv4_hdr->hdr_checksum = rte_ipv4_cksum(ipv4_hdr);

        switch (ipv4_hdr->next_proto_id) {
        case IPPROTO_UDP:
            struct rte_udp_hdr *udp_hdr = &pkt->proto.udp;
            udp_hdr->dgram_cksum = 0;
            udp_hdr->dgram_cksum = rte_ipv4_udptcp_cksum(ipv4_hdr, udp_hdr);
            break;
        case IPPROTO_TCP:
            struct rte_tcp_hdr *tcp_hdr = &pkt->proto.tcp;
            tcp_hdr->cksum = 0;
            tcp_hdr->cksum = rte_ipv4_udptcp_cksum(ipv4_hdr, tcp_hdr);
            break;
        case IPPROTO_ICMP:
            struct icmp *icmp_hdr = &pkt->proto.icmp;
            icmp_hdr->icmp_cksum = 0;
            icmp_hdr->icmp_cksum = rte_raw_cksum(icmp_hdr, sizeof(*icmp_hdr) + pkt->data_len);
            break;
        }
    } else if (pkt->eth.ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV6)) {
        struct rte_ipv6_hdr *ipv6_hdr = &pkt->ip.ipv6;
        switch (ipv6_hdr->proto) {
        case IPPROTO_UDP:
            struct rte_udp_hdr *udp_hdr = &pkt->proto.udp;
            udp_hdr->dgram_cksum = 0;
            udp_hdr->dgram_cksum = rte_ipv6_udptcp_cksum(ipv6_hdr, udp_hdr);
            break;
        case IPPROTO_TCP:
            struct rte_tcp_hdr *tcp_hdr = &pkt->proto.tcp;
            tcp_hdr->cksum = 0;
            tcp_hdr->cksum = rte_ipv6_udptcp_cksum(ipv6_hdr, tcp_hdr);
            break;
        case IPPROTO_ICMPV6:
            struct icmp6_hdr *icmp6_hdr = &pkt->proto.icmp6;
            icmp6_hdr->icmp6_cksum = 0;
			
			uint32_t sum = __rte_raw_cksum(ipv6_hdr->src_addr, 32, 0);
		
			uint32_t tmp = ((uint32_t)RTE_BE16(IPPROTO_ICMPV6) << 16) + rte_cpu_to_be_16(ipv6_hdr->payload_len);
			sum = __rte_raw_cksum(&tmp, 4, sum);
			sum = __rte_raw_cksum(icmp6_hdr, ipv6_hdr->payload_len, sum);
		
			icmp6_hdr->icmp6_cksum = ~__rte_raw_cksum_reduce(sum);
            break;
        }
    }
}

static inline int
push_packet(struct upkt *pkt) {
	struct rte_mbuf *mbuf = rte_pktmbuf_alloc(test_params.mbuf_pool);
	if (!mbuf) {
		RTE_LOG(ERR, NAT64_TEST, "Failed to allocate mbuf\n");
		return -1;
	}

	uint16_t pkt_len = sizeof(struct rte_ether_hdr);
	uint16_t l3_len = 0;
	uint16_t l4_len = 0;
	uint8_t proto = 0;
	if (pkt->eth.ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV4)) {
		l3_len = rte_ipv4_hdr_len(&pkt->ip.ipv4);
		proto = pkt->ip.ipv4.next_proto_id;
	} else if (pkt->eth.ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV6)) {
		l3_len = sizeof(struct rte_ipv6_hdr);
		proto = pkt->ip.ipv6.proto;
	} else {
		RTE_LOG(ERR, NAT64_TEST, "Usupported ether type\n");
		return -1;
	}
	switch (proto) {
		case IPPROTO_UDP:
			l4_len = sizeof(struct rte_udp_hdr);
			break;
		case IPPROTO_TCP:
			l4_len = sizeof(struct rte_tcp_hdr);
			break;
		case IPPROTO_ICMP:
			l4_len = sizeof(struct icmp);
			break;
		case IPPROTO_ICMPV6:
			l4_len = sizeof(struct icmp6_hdr);
			break;
	}
	pkt_len += l3_len + l4_len + pkt->data_len;

	rte_pktmbuf_append(mbuf, pkt_len);
	struct rte_ether_hdr *eth_hdr =
		rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);
	rte_memcpy(eth_hdr, &pkt->eth, sizeof(struct rte_ether_hdr));
	rte_memcpy(
		rte_pktmbuf_mtod_offset(
			mbuf, void *, sizeof(struct rte_ether_hdr)
		),
		&pkt->ip,
		l3_len
	);
	rte_memcpy(
		rte_pktmbuf_mtod_offset(
			mbuf, void *, sizeof(struct rte_ether_hdr) + l3_len
		),
		&pkt->proto,
		l4_len
	);
	rte_memcpy(
		rte_pktmbuf_mtod_offset(
			mbuf,
			void *,
			sizeof(struct rte_ether_hdr) + l3_len + l4_len
		),
		pkt->data,
		pkt->data_len
	);

	mbuf->port = 0;

	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(struct packet));
	packet->mbuf = mbuf;
	packet->rx_device_id = 0;
	packet->tx_device_id = 0;

	parse_packet(packet);

	packet_list_add(&test_params.packet_front.input, packet);
	return 0;
}

static struct test_case *
append_test_cases_from_mappings(struct test_case *test_case) {
	struct nat64_module_config *nat64_config =
	container_of(test_params.module_config, struct nat64_module_config, config);
	struct test_case *current_case = test_case;
	for (uint32_t i = 0; i < config_data.count; i++) {
		struct upkt pkt = {
			.eth =
				{
					.dst_addr.addr_bytes =
						"\xff\xff\xff\xff\xff\xff",
					.src_addr.addr_bytes =
						"\x02\x00\x00\x00\x00\x00",
					.ether_type = rte_cpu_to_be_16(
						RTE_ETHER_TYPE_IPV4
					),
				},
			.ip.ipv4 =
				{
					.version_ihl = RTE_IPV4_VHL_DEF,
					.total_length = rte_cpu_to_be_16(
						sizeof(struct rte_ipv4_hdr) +
						sizeof(struct rte_udp_hdr)
					),
					.time_to_live = DEFAULT_TTL,
					.next_proto_id = IPPROTO_UDP,
					.src_addr = outer_ip4,
					.dst_addr = config_data.mapping[i].ip4,
				},
			.proto.udp =
				{
					.dst_port = rte_cpu_to_be_16(9
					), /* Discard port */
				},
		};
		struct upkt pkt_expected = {
			.eth =
				{
					.dst_addr.addr_bytes =
						"\xff\xff\xff\xff\xff\xff",
					.src_addr.addr_bytes =
						"\x02\x00\x00\x00\x00\x00",
					.ether_type = rte_cpu_to_be_16(
						RTE_ETHER_TYPE_IPV6
					),
				},
			.ip.ipv6 =
				{
					.hop_limits = DEFAULT_TTL,
					.proto = IPPROTO_UDP,
					.src_addr = {0},
					.vtc_flow = RTE_BE16(0x60000000),
				},
			.proto.udp =
				{
					.dst_port = rte_cpu_to_be_16(9
					), /* Discard port */
				},
		};
		rte_memcpy(
			&pkt_expected.ip.ipv6.dst_addr,
			&config_data.mapping[i].ip6,
			16
		);
		SET_IPV4_MAPPED_IPV6(
			&pkt_expected.ip.ipv6.src_addr, nat64_config->ipv6_prefixes[0].prefix, &outer_ip4
		);
		char buf[1024];
		snprintf(
			buf,
			1023,
			"v4 -> v6 " IPv4_BYTES_FMT " -> " IPv6_BYTES_FMT,
			IPv4_BYTES(RTE_BE32(outer_ip4)),
			IPv6_BYTES(pkt_expected.ip.ipv6.dst_addr)
		);
		append_test_case(&current_case, pkt, pkt_expected, buf);
		snprintf(
			buf,
			1023,
			"v6 -> v4 " IPv6_BYTES_FMT " -> " IPv4_BYTES_FMT,
			IPv6_BYTES(pkt_expected.ip.ipv6.src_addr),
			IPv4_BYTES(RTE_BE32(outer_ip4))
		);
		append_test_case(&current_case, pkt_expected, pkt, buf);
	}
	return test_case;
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
packet_list_cleanup(struct packet_list *list) {
	if (list == NULL) {
		return;
	}
	struct packet *pkt = list->first;
	while (pkt != NULL) {
		struct packet *next = pkt->next;
		rte_pktmbuf_free(pkt->mbuf);
		// free(pkt);
		pkt = next;
	}
	list->first = NULL;
}

static int
test_nat64_v4_to_v6_generic() {

	append_test_cases_from_mappings(&test_cases);

	struct test_case *tc;
	for (tc = &test_cases; tc != NULL; tc = tc->next) {

		packet_list_cleanup(&test_params.packet_front.input);
		packet_list_cleanup(&test_params.packet_front.output);
		packet_list_cleanup(&test_params.packet_front.drop);

		
		fix_checksums(&tc->pkt);
		fix_checksums(&tc->pkt_expected);

		push_packet(&tc->pkt);

		// Convert IPv4 to IPv6
		test_params.module->handler(
			test_params.module,
			test_params.module_config,
			&test_params.packet_front
		);

		if (tc->pkt_expected.eth.dst_addr.addr_bytes[0] == 0) {
			int count = packet_list_counter(
				&test_params.packet_front.drop
			);
			TEST_ASSERT_EQUAL(
				count,
				1,
				"Expected 1 packet droped, got %d\n",
				count
			);
			count = packet_list_counter(
				&test_params.packet_front.output
			);
			TEST_ASSERT_EQUAL(
				count,
				0,
				"Expected 0 packet output, got %d\n",
				count
			);
			continue;
		}

		int count =
			packet_list_counter(&test_params.packet_front.output);
		TEST_ASSERT_EQUAL(
			count,
			1,
			"%s: Expected 1 packet output, got %d\n",
			tc->name,
			count
		);
		count = packet_list_counter(&test_params.packet_front.drop);
		TEST_ASSERT_EQUAL(
			count,
			0,
			"%s: Expected 0 packet droped, got %d\n",
			tc->name,
			count
		);
		struct packet *pkt_out;
		while ((pkt_out =
				packet_list_pop(&test_params.packet_front.output
				)) != NULL) {
			// Parse again
			struct packet *packet =
				mbuf_to_packet(packet_to_mbuf(pkt_out));
			parse_packet(packet);

			int res = print_diff_upkt_and_rte_mbuf(
				&tc->pkt_expected, packet_to_mbuf(packet)
			);
			if (res != 0) {
				print_upkt(&tc->pkt_expected);
			}
			TEST_ASSERT_EQUAL(
				res,
				0,
				"%s: Expected and actual packet difference. "
				"See log for details.\n",
				tc->name
			);
		}
	}
	return 0;
}

static int
test_nat64_v6_to_v4_icmp() {
	// Create an IPv6 packet
	RTE_LOG(INFO, NAT64_TEST, "Create ipv6 packet.\n");
	struct packet *pkt = create_ipv6_packet(0, IPPROTO_ICMPV6);
	TEST_ASSERT_NOT_NULL(pkt, "Failed to create IPv6 packet\n");
	// hack
	struct icmp6_hdr *icmpHeader = rte_pktmbuf_mtod_offset(
		packet_to_mbuf(pkt),
		struct icmp6_hdr *,
		pkt->transport_header.offset
	);

	icmpHeader->icmp6_type = ICMP6_DST_UNREACH;
	icmpHeader->icmp6_code = ICMP6_DST_UNREACH_ADDR;

	packet_list_add(&test_params.packet_front.input, pkt);

	// Convert IPv6 to IPv4
	test_params.module->handler(
		test_params.module,
		test_params.module_config,
		&test_params.packet_front
	);

	int count = 0;
	while ((pkt = packet_list_pop(&test_params.packet_front.output)) != NULL
	) {

		// parse again
		struct packet *packet = mbuf_to_packet(packet_to_mbuf(pkt));
		TEST_ASSERT_EQUAL(parse_packet(packet), 0, "Failed to parse packet\n");

		struct rte_ipv4_hdr *ipv4_header = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		char ip_str[2 * INET_ADDRSTRLEN + 2];
		inet_ntop(
			AF_INET, &ipv4_header->src_addr, ip_str, INET_ADDRSTRLEN
		);
		inet_ntop(
			AF_INET,
			&ipv4_header->dst_addr,
			ip_str + INET_ADDRSTRLEN + 1,
			INET_ADDRSTRLEN
		);
		RTE_LOG(INFO,
			NAT64_TEST,
			"Got packet %s -> %s\n",
			ip_str,
			ip_str + INET_ADDRSTRLEN + 1);

		// for test no difference in icmp and icmp6 hdr
		struct icmp6_hdr *icmpHeader = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(pkt),
			struct icmp6_hdr *,
			packet->transport_header.offset
		);

		TEST_ASSERT_EQUAL(
			icmpHeader->icmp6_type,
			ICMP_UNREACH,
			"Expected ICMP type %d, got %d\n",
			ICMP_UNREACH,
			icmpHeader->icmp6_type
		);
		TEST_ASSERT_EQUAL(
			icmpHeader->icmp6_code,
			ICMP_HOST_UNREACH,
			"Expected ICMP code %d, got %d\n",
			ICMP_HOST_UNREACH,
			icmpHeader->icmp6_code
		);
		count++;
	}
	TEST_ASSERT_EQUAL(count, 1, "Expected 4 packets, got %d\n", count);
	return 0;
}

static void
testsuite_teardown(void) {
		packet_list_cleanup(&test_params.packet_front.input);
		packet_list_cleanup(&test_params.packet_front.output);
		packet_list_cleanup(&test_params.packet_front.drop);

}

static struct unit_test_suite nat64_test_suite =
	{.suite_name = "NAT64 Unit Test Suite",
	 .setup = test_setup,
	 .teardown = testsuite_teardown,
	 .unit_test_cases = {
		 TEST_CASE_NAMED(
			 "test_nat64_new_module", test_new_module_nat64
		 ),
		 TEST_CASE_NAMED(
			 "test_nat64_config_handler", test_module_config_handler
		 ),
		 TEST_CASE_NAMED(
			 "test_nat64_v6_to_v4_generic",
			 test_nat64_v6_to_v4_generic
		 ),
		 TEST_CASE_NAMED(
			 "test_nat64_v4_to_v6_generic",
			 test_nat64_v4_to_v6_generic
		 ),
		 TEST_CASE_NAMED(
			 "test_nat64_v6_to_v4_icmp", test_nat64_v6_to_v4_icmp
		 ),

		 TEST_CASES_END() /**< NULL terminate unit test array */
	 }};

static int
nat64_testsuite(void) {
	return unit_test_suite_runner(&nat64_test_suite);
}

REGISTER_FAST_TEST(nat64_autotest, false, true, nat64_testsuite);