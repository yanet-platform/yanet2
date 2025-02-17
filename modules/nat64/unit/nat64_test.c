#include <stdio.h>

#include <dlfcn.h>
#include <string.h>

#include <pcap.h>
#include <netinet/icmp6.h>
#include <netinet/ip_icmp.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>

#include "dataplane/dpdk.h"

#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include <rte_common.h>
#include <rte_memcpy.h>

#include "dataplane/module/module.h"

#include "test.h"

#include "dataplane.h"

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
	RTE_LOG(INFO, NAT64_TEST, "Init packet front done.\n");

	return TEST_SUCCESS;
}

static void
testsuite_teardown(void) {
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

// static void
// mbuf_prepare(struct dummy_mbuf *dm, uint32_t plen)
// {
// 	struct {
// 		struct rte_ether_hdr eth;
// 		struct rte_ipv4_hdr ip;
// 		struct rte_udp_hdr udp;
// 	} pkt = {
// 		.eth = {
// 			.dst_addr.addr_bytes = "\xff\xff\xff\xff\xff\xff",
// 			.ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
// 		},
// 		.ip = {
// 			.version_ihl = RTE_IPV4_VHL_DEF,
// 			.time_to_live = 1,
// 			.next_proto_id = IPPROTO_UDP,
// 			.src_addr = rte_cpu_to_be_32(RTE_IPV4_LOOPBACK),
// 			.dst_addr = rte_cpu_to_be_32(RTE_IPV4_BROADCAST),
// 		},
// 		.udp = {
// 			.dst_port = rte_cpu_to_be_16(9), /* Discard port */
// 		},
// 	};

// 	memset(dm, 0, sizeof(*dm));
// 	dummy_mbuf_prep(&dm->mb[0], dm->buf[0], sizeof(dm->buf[0]), plen);

// 	rte_eth_random_addr(pkt.eth.src_addr.addr_bytes);
// 	plen -= sizeof(struct rte_ether_hdr);

// 	pkt.ip.total_length = rte_cpu_to_be_16(plen);
// 	pkt.ip.hdr_checksum = rte_ipv4_cksum(&pkt.ip);

// 	plen -= sizeof(struct rte_ipv4_hdr);
// 	pkt.udp.src_port = rte_rand();
// 	pkt.udp.dgram_len = rte_cpu_to_be_16(plen);

// 	memcpy(rte_pktmbuf_mtod(dm->mb, void *), &pkt, sizeof(pkt));
// }

/**
 * @brief Creates an IPv6 packet.
 *
 * This function creates a new IPv6 packet with the specified mapping number and protocol. The packet
 * contains Ethernet, IPv6, and TCP headers, as well as data filled with random values.
 *
 * @param num Mapping number in the `config_data.mapping` array used to configure the source IPv6 addresses.
 *            Must be within the range from 0 to `config_data.count - 1`.
 *
 * @param proto Protocol used in the IPv6 header. For example, IPPROTO_TCP for TCP packets.
 *
 * @return Pointer to a `struct packet` containing the created packet if the operation is successful.
 *         In case of failure (e.g., when mbuf allocation fails), returns `NULL`.
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

static int
test_nat64_v4_to_v6_generic() {
	// FIXME: Implement test_nat64_v4_to_v6_generic
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
			 "test_nat64_v6_to_v4_generic", test_nat64_v6_to_v4_generic
		 ),
		 TEST_CASE_NAMED(
			 "test_nat64_v4_to_v6_generic", test_nat64_v4_to_v6_generic
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