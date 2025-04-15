/* System headers */
#include <dlfcn.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

/* Protocol headers */
#include <netinet/icmp6.h>
#include <netinet/ip_icmp.h>

/* DPDK headers */
#include <rte_common.h>
#include <rte_config.h>
#include <rte_errno.h>
#include <rte_log.h>
#include <rte_malloc.h>
#include <rte_mbuf.h>
#include <rte_memcpy.h>
#include <rte_timer.h>

/* DPDK protocol headers */
#include <rte_ether.h>
#include <rte_icmp.h>
#include <rte_ip.h>
#include <rte_tcp.h>
#include <rte_udp.h>

/* Project headers */
#include "api/nat64cp.h"
#include "common.h"
#include "common/memory.h"
#include "dataplane/dpdk.h"
#include "dataplane/module/module.h"
#include "dataplane/nat64dp.h"
#include "logging/log.h"
#include "test.h"

#ifdef DEBUG_NAT64
RTE_LOG_REGISTER_DEFAULT(nat64test_logtype, DEBUG);
#else
RTE_LOG_REGISTER_DEFAULT(nat64test_logtype, INFO);
#endif
#define RTE_LOGTYPE_NAT64_TEST nat64test_logtype

#define ARENA_SIZE (1 << 20)

/**
 * @brief Test environment parameters for NAT64 unit testing
 *
 * This structure contains all necessary parameters and resources for executing
 * NAT64 tests:
 * - Packet front for managing test packet flows
 * - Module instance being tested
 * - Module configuration data
 * - Memory management resources (arena, allocator, context)
 * - DPDK mbuf pool for packet allocation
 * - Configuration data and size
 *
 * The structure provides a centralized way to manage test state and resources,
 * ensuring proper initialization and cleanup between test cases.
 *
 * @note The mbuf_pool is initialized during test setup and used for all packet
 *       allocations during testing
 * @note Memory resources (arena, allocator) are used for dynamic allocations
 *       during testing
 * @note Configuration data is used to set up NAT64 mappings and prefixes
 *
 * @see test_setup() For initialization of these parameters
 * @see packet_front For packet management
 * @see cp_module For NAT64 module configuration
 */
struct nat64_unittest_params {
	struct packet_front packet_front; /**< Packet front for testing */
	struct module *module; /**< Pointer to the module being tested */
	struct nat64_module_config module_config; /**< Module configuration */

	void *arena0;
	struct block_allocator ba;
	struct memory_context *memory_context;

	struct rte_mempool *mbuf_pool; /**< Packet buffer pool */
	uint8_t *config;	       /**< Pointer to configuration data */
	uint32_t config_size;	       /**< Size of configuration data */
};

/**
 * @brief Global test parameters instance
 *
 * Static instance of test parameters used across all test cases.
 * Initialized with NULL mbuf pool that gets created during test setup.
 */
static struct nat64_unittest_params test_params = {
	.mbuf_pool = NULL,
};

/**
 * @brief External IPv4 address used for testing
 *
 * IPv4 address from TEST-NET-1 range (192.0.2.0/24) per RFC 5737
 * Used as source/destination address in test packets
 */
static uint32_t outer_ip4 = RTE_BE32(RTE_IPV4(192, 0, 2, 34));

/**
 * @brief NAT64 address mapping configuration
 *
 * Contains IPv4-IPv6 address mappings for testing:
 * - IPv4 addresses from TEST-NET-2 range (198.51.100.0/24) per RFC 5737
 * - IPv6 addresses from Documentation prefix (2001:DB8::/32) per RFC 3849
 *
 * Used to configure NAT64 module with test address mappings
 */
static struct {
	uint32_t count; /**< Number of address mappings */
	struct {
		uint32_t ip4;	 /**< IPv4 address in network byte order */
		uint32_t ip6[4]; /**< IPv6 address as 4 32-bit segments */
	} mapping[8];		 /**< Array of address mappings */
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

/**
 * @brief Initialize test environment and resources for NAT64 testing
 *
 * This function performs comprehensive test environment setup:
 * 1. Configures logging:
 *    - Sets debug level if DEBUG_NAT64 is defined
 *    - Enables detailed logging of test execution
 *
 * 2. Creates DPDK resources:
 *    - Allocates mbuf pool with 4096 elements
 *    - Sets buffer size to RTE_MBUF_DEFAULT_BUF_SIZE
 *    - Configures cache size of 250 mbufs
 *    - Uses current CPU socket for optimal performance
 *
 * 3. Initializes memory management:
 *    - Allocates arena of ARENA_SIZE bytes
 *    - Sets up block allocator for dynamic memory
 *    - Creates memory context for NAT64 module
 *
 * 4. Sets up packet processing:
 *    - Initializes packet front for managing test packets
 *    - Prepares input/output packet queues
 *
 * The setup ensures all resources needed for NAT64 testing are properly
 * allocated and initialized.
 *
 * @return TEST_SUCCESS on successful setup,
 *         Negative error code on failure:
 *         - ENOMEM: Memory allocation failed
 *         - EINVAL: Invalid parameter/configuration
 *
 * @note Calls rte_pktmbuf_pool_create() which may fail if system lacks huge
 * pages
 * @note Memory arena size is defined by ARENA_SIZE macro
 * @note Resources must be freed by corresponding cleanup function
 *
 * @see test_params Global test parameters structure
 * @see packet_front_init() For packet management initialization
 * @see memory_context_init() For memory management setup
 */
static int
test_setup(void) {
#ifdef DEBUG_NAT64
	rte_log_set_level(RTE_LOGTYPE_NAT64_TEST, RTE_LOG_DEBUG);
	log_enable_name("debug");
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

	// arena initialization
	test_params.arena0 = malloc(ARENA_SIZE);
	if (test_params.arena0 == NULL) {
		RTE_LOG(ERR, NAT64_TEST, "could not allocate arena0\n");
		return -1;
	}

	block_allocator_init(&test_params.ba);
	block_allocator_put_arena(
		&test_params.ba, test_params.arena0, ARENA_SIZE
	);

	test_params.memory_context =
		&test_params.module_config.cp_module.memory_context;
	memory_context_init(
		test_params.memory_context, "nat64 tests", &test_params.ba
	);

	return TEST_SUCCESS;
}

/**
 * @brief Configure NAT64 module for testing
 *
 * Sets up:
 * - Memory and basic parameters
 * - LPM tables for address lookup
 * - NAT64 prefix (2001:db8::/96)
 * - Address mappings from config_data
 *
 * @param cp_module Pointer to store module configuration
 * @return 0 on success, negative error code on failure
 *
 * @see config_data Address mapping definitions
 * @see nat64_module_config Configuration structure
 */
static int
nat64_test_config(struct nat64_module_config *module_config) {
	// Initialize module configuration using nat64_module_config_init_config
	if (nat64_module_config_data_init(
		    module_config, test_params.memory_context
	    )) {
		RTE_LOG(ERR, NAT64_TEST, "Failed to initialize module config\n"
		);
		return -ENOMEM;
	}

	// Add prefix
	uint8_t pfx[12] = {
		0x20,
		0x01,
		0x0d,
		0xb8,
		0x00,
		0x00,
		0x00,
		0x00,
		0x00,
		0x00,
		0x00,
		0x00
	};
	if (nat64_module_config_add_prefix(&module_config->cp_module, pfx) <
	    0) {
		goto error_add;
	}

	// Add mappings
	uint32_t mapping_count = config_data.count;
	for (uint32_t i = 0; i < mapping_count; i++) {
		if (nat64_module_config_add_mapping(
			    &module_config->cp_module,
			    config_data.mapping[i].ip4,
			    (uint8_t *)config_data.mapping[i].ip6,
			    0
		    ) < 0) {
			goto error_add;
		}
	}

	LOG_DBG(NAT64_TEST,
		"NAT64 module configured successfully\n"
		"  Mappings: %lu\n"
		"  Prefixes: %lu\n"
		"  MTU IPv4: %u\n"
		"  MTU IPv6: %u\n",
		config->mappings.count,
		config->prefixes.count,
		config->mtu.ipv4,
		config->mtu.ipv6);

	return 0;

error_add:
	nat64_module_config_data_destroy(
		module_config, test_params.memory_context
	);
	return -EINVAL;
}

/**
 * @brief Test NAT64 module configuration handling
 *
 * Tests that the module's config_handler correctly:
 * 1. Processes configuration data
 * 2. Creates module configuration structure
 * 3. Returns non-NULL configuration
 *
 * @return TEST_SUCCESS on successful configuration, error code otherwise
 */
static inline int
test_module_config_handler(void) {
	TEST_ASSERT_SUCCESS(
		nat64_test_config(&test_params.module_config),
		"nat64_test_config failed\n"
	);
	return TEST_SUCCESS;
}

/**
 * @brief Test NAT64 module creation
 *
 * Tests that new_module_nat64() correctly:
 * 1. Creates new NAT64 module instance
 * 2. Returns non-NULL module pointer
 * 3. Initializes module structure properly
 *
 * @return TEST_SUCCESS on successful module creation, error code otherwise
 */
static int
test_new_module_nat64(void) {
	test_params.module = new_module_nat64();
	TEST_ASSERT_NOT_NULL(test_params.module, "new_module_nat64 failed\n");
	return TEST_SUCCESS;
}

/**
 * @brief Test packet structure
 *
 * Contains:
 * - Protocol headers (IPv4/v6, UDP/TCP/ICMP)
 * - Payload data
 *
 * Used for test input and verification
 */
struct upkt {
	struct rte_ether_hdr eth; /**< Ethernet header */
	union {
		struct rte_ipv4_hdr
			ipv4; /**< IPv4 header when eth.ether_type is IPv4 */
		struct rte_ipv6_hdr
			ipv6; /**< IPv6 header when eth.ether_type is IPv6 */
	} ip;		      /**< IP header union for v4/v6 */
	union {
		struct rte_udp_hdr
			udp; /**< UDP header when ip.proto is IPPROTO_UDP */
		struct rte_tcp_hdr
			tcp; /**< TCP header when ip.proto is IPPROTO_TCP */
		struct icmphdr
			icmp; /**< ICMP header when ip.proto is IPPROTO_ICMP */
		struct icmp6_hdr icmp6; /**< ICMPv6 header when ip.proto is
					   IPPROTO_ICMPV6 */
	} proto;			/**< Protocol header union */
	uint16_t data_len;		/**< Length of payload data */
	void *data;			/**< Pointer to payload data */
};

/**
 * @brief Print universal packet structure contents
 *
 * Prints detailed information about packet headers including:
 * - Ethernet header
 * - IP header (v4 or v6)
 * - Protocol header (UDP, TCP, ICMP, ICMPv6)
 * - Data length
 *
 * @param pkt Pointer to the universal packet structure to print
 */
void
print_upkt(struct upkt *pkt) {
	if (!pkt) {
		RTE_LOG(ERR, NAT64_TEST, "Packet is NULL\n");
		return;
	}

	// Print Ethernet Header
	RTE_LOG(INFO, NAT64_TEST, "Ethernet Header:\n");
	RTE_LOG(INFO,
		NAT64_TEST,
		"  Destination MAC: " RTE_ETHER_ADDR_PRT_FMT "\n",
		RTE_ETHER_ADDR_BYTES(&pkt->eth.dst_addr));
	RTE_LOG(INFO,
		NAT64_TEST,
		"  Source MAC: " RTE_ETHER_ADDR_PRT_FMT "\n",
		RTE_ETHER_ADDR_BYTES(&pkt->eth.src_addr));

	RTE_LOG(INFO,
		NAT64_TEST,
		"  Ether Type: 0x%04X\n",
		ntohs(pkt->eth.ether_type));

	// Print IP Header
	if (pkt->eth.ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV4)) {
		RTE_LOG(INFO, NAT64_TEST, "IPv4 Header:\n");
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Version: %d\n",
			(pkt->ip.ipv4.version_ihl & 0xF0) >> 4);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  IHL: %d\n",
			pkt->ip.ipv4.version_ihl & 0x0F);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Type of Service: 0x%02X\n",
			pkt->ip.ipv4.type_of_service);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Total Length: %d\n",
			ntohs(pkt->ip.ipv4.total_length));
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Identification: 0x%04X\n",
			ntohs(pkt->ip.ipv4.packet_id));
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Flags: 0x%01X\n",
			(pkt->ip.ipv4.fragment_offset & 0x00E0) >> 5);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Fragment Offset: %d\n",
			rte_be_to_cpu_16(pkt->ip.ipv4.fragment_offset) &
				RTE_IPV4_HDR_OFFSET_MASK);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Time to Live: %d\n",
			pkt->ip.ipv4.time_to_live);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Protocol: 0x%02X\n",
			pkt->ip.ipv4.next_proto_id);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Header Checksum: 0x%04X\n",
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
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Version: %d\n",
			(htonl(pkt->ip.ipv6.vtc_flow) & 0xF0000000) >> 28);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Traffic Class: 0x%02X\n",
			(htonl(pkt->ip.ipv6.vtc_flow) & 0x0FF00000) >> 20);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Flow Label: 0x%05X\n",
			htonl(pkt->ip.ipv6.vtc_flow) & 0x000FFFFF);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Payload Length: %d\n",
			ntohs(pkt->ip.ipv6.payload_len));
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Next Header: 0x%02X\n",
			pkt->ip.ipv6.proto);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Hop Limit: %d\n",
			pkt->ip.ipv6.hop_limits);

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
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Source Port: %d\n",
				ntohs(pkt->proto.udp.src_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Destination Port: %d\n",
				ntohs(pkt->proto.udp.dst_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Length: %d\n",
				ntohs(pkt->proto.udp.dgram_len));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Checksum: 0x%04X\n",
				ntohs(pkt->proto.udp.dgram_cksum));
			break;
		case IPPROTO_TCP:
			RTE_LOG(INFO, NAT64_TEST, "TCP Header:\n");
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Source Port: %d\n",
				ntohs(pkt->proto.tcp.src_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Destination Port: %d\n",
				ntohs(pkt->proto.tcp.dst_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Sequence Number: %u\n",
				ntohl(pkt->proto.tcp.sent_seq));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Acknowledgment Number: %u\n",
				ntohl(pkt->proto.tcp.recv_ack));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Data Offset: %d\n",
				(pkt->proto.tcp.data_off & 0xF0) >> 4);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Flags: 0x%02X\n",
				pkt->proto.tcp.tcp_flags);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Window Size: %d\n",
				ntohs(pkt->proto.tcp.rx_win));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Checksum: 0x%04X\n",
				ntohs(pkt->proto.tcp.cksum));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Urgent Pointer: %d\n",
				ntohs(pkt->proto.tcp.tcp_urp));
			break;
		case IPPROTO_ICMP:
			RTE_LOG(INFO, NAT64_TEST, "ICMP Header:\n");
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Type: 0x%02X\n",
				pkt->proto.icmp.type);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Code: 0x%02X\n",
				pkt->proto.icmp.code);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Checksum: 0x%04X\n",
				ntohs(pkt->proto.icmp.checksum));
			break;
		}
		break;
	case RTE_BE16(RTE_ETHER_TYPE_IPV6):
		switch (pkt->ip.ipv6.proto) {
		case IPPROTO_UDP:
			RTE_LOG(INFO, NAT64_TEST, "UDP Header:\n");
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Source Port: %d\n",
				ntohs(pkt->proto.udp.src_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Destination Port: %d\n",
				ntohs(pkt->proto.udp.dst_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Length: %d\n",
				ntohs(pkt->proto.udp.dgram_len));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Checksum: 0x%04X\n",
				ntohs(pkt->proto.udp.dgram_cksum));
			break;
		case IPPROTO_TCP:
			RTE_LOG(INFO, NAT64_TEST, "TCP Header:\n");
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Source Port: %d\n",
				ntohs(pkt->proto.tcp.src_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Destination Port: %d\n",
				ntohs(pkt->proto.tcp.dst_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Sequence Number: %u\n",
				ntohl(pkt->proto.tcp.sent_seq));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Acknowledgment Number: %u\n",
				ntohl(pkt->proto.tcp.recv_ack));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Data Offset: %d\n",
				(pkt->proto.tcp.data_off & 0xF0) >> 4);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Flags: 0x%02X\n",
				pkt->proto.tcp.tcp_flags);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Window Size: %d\n",
				ntohs(pkt->proto.tcp.rx_win));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Checksum: 0x%04X\n",
				ntohs(pkt->proto.tcp.cksum));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Urgent Pointer: %d\n",
				ntohs(pkt->proto.tcp.tcp_urp));
			break;
		case IPPROTO_ICMPV6:
			RTE_LOG(INFO, NAT64_TEST, "ICMPv6 Header:\n");
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Type: 0x%02X\n",
				pkt->proto.icmp6.icmp6_type);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Code: 0x%02X\n",
				pkt->proto.icmp6.icmp6_code);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Checksum: 0x%04X\n",
				ntohs(pkt->proto.icmp6.icmp6_cksum));
			break;
		}
		break;
	}

	// Print Data Length
	RTE_LOG(INFO, NAT64_TEST, "Data Length: %d\n", pkt->data_len);
}

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
		RTE_LOG(ERR, NAT64_TEST, "Mbuf is NULL\n");
		return;
	}

	// Get the data pointer
	uint8_t *data = rte_pktmbuf_mtod(mbuf, uint8_t *);

	// Extract Ethernet header
	struct rte_ether_hdr *eth_hdr = (struct rte_ether_hdr *)data;
	RTE_LOG(INFO, NAT64_TEST, "Ethernet Header:\n");
	RTE_LOG(INFO,
		NAT64_TEST,
		"  Destination MAC: " RTE_ETHER_ADDR_PRT_FMT "\n",
		RTE_ETHER_ADDR_BYTES(&eth_hdr->dst_addr));
	RTE_LOG(INFO,
		NAT64_TEST,
		"  Source MAC: " RTE_ETHER_ADDR_PRT_FMT "\n",
		RTE_ETHER_ADDR_BYTES(&eth_hdr->src_addr));

	RTE_LOG(INFO,
		NAT64_TEST,
		"  Ether Type: 0x%04X\n",
		ntohs(eth_hdr->ether_type));

	uint16_t data_off = sizeof(struct rte_ether_hdr);

	// Determine the IP header type and extract it
	if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *ipv4_hdr =
			(struct rte_ipv4_hdr *)(eth_hdr + 1);
		data_off += rte_ipv4_hdr_len(ipv4_hdr);
		RTE_LOG(INFO, NAT64_TEST, "IPv4 Header:\n");
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Version: %d\n",
			(ipv4_hdr->version_ihl & 0xF0) >> 4);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  IHL: %d\n",
			ipv4_hdr->version_ihl & 0x0F);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Type of Service: 0x%02X\n",
			ipv4_hdr->type_of_service);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Total Length: %d\n",
			ntohs(ipv4_hdr->total_length));
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Identification: 0x%04X\n",
			ntohs(ipv4_hdr->packet_id));
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Flags: 0x%01X\n",
			(ipv4_hdr->fragment_offset & 0x00E0) >> 5);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Fragment Offset: %d\n",
			rte_be_to_cpu_16(ipv4_hdr->fragment_offset) &
				RTE_IPV4_HDR_OFFSET_MASK);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Time to Live: %d\n",
			ipv4_hdr->time_to_live);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Protocol: 0x%02X\n",
			ipv4_hdr->next_proto_id);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Header Checksum: 0x%04X\n",
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
		case IPPROTO_UDP: {
			data_off += sizeof(struct rte_udp_hdr);
			struct rte_udp_hdr *udp_hdr =
				(struct rte_udp_hdr *)proto_data;
			RTE_LOG(INFO, NAT64_TEST, "UDP Header:\n");
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Source Port: %d\n",
				ntohs(udp_hdr->src_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Destination Port: %d\n",
				ntohs(udp_hdr->dst_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Length: %d\n",
				ntohs(udp_hdr->dgram_len));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Checksum: 0x%04X\n",
				ntohs(udp_hdr->dgram_cksum));
			break;
		}
		case IPPROTO_TCP: {
			data_off += sizeof(struct rte_tcp_hdr);
			struct rte_tcp_hdr *tcp_hdr =
				(struct rte_tcp_hdr *)proto_data;
			RTE_LOG(INFO, NAT64_TEST, "TCP Header:\n");
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Source Port: %d\n",
				ntohs(tcp_hdr->src_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Destination Port: %d\n",
				ntohs(tcp_hdr->dst_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Sequence Number: %u\n",
				ntohl(tcp_hdr->sent_seq));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Acknowledgment Number: %u\n",
				ntohl(tcp_hdr->recv_ack));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Data Offset: %d\n",
				(tcp_hdr->data_off & 0xF0) >> 4);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Flags: 0x%02X\n",
				tcp_hdr->tcp_flags);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Window Size: %d\n",
				ntohs(tcp_hdr->rx_win));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Checksum: 0x%04X\n",
				ntohs(tcp_hdr->cksum));
			break;
		}
		case IPPROTO_ICMP: {
			data_off += sizeof(struct icmphdr);
			struct icmphdr *icmp_hdr = (struct icmphdr *)proto_data;
			RTE_LOG(INFO, NAT64_TEST, "ICMP Header:\n");
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Type: 0x%02X\n",
				icmp_hdr->type);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Code: 0x%02X\n",
				icmp_hdr->code);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Checksum: 0x%04X\n",
				ntohs(icmp_hdr->checksum));
			break;
		}
		}
	} else if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *ipv6_hdr =
			(struct rte_ipv6_hdr *)(eth_hdr + 1);
		data_off += sizeof(struct rte_ipv6_hdr);
		RTE_LOG(INFO, NAT64_TEST, "IPv6 Header:\n");
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Version: %d\n",
			(htonl(ipv6_hdr->vtc_flow) & 0xF0000000) >> 28);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Traffic Class: 0x%02X\n",
			(htonl(ipv6_hdr->vtc_flow) & 0x0FF00000) >> 20);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Flow Label: 0x%05X\n",
			htonl(ipv6_hdr->vtc_flow) & 0x000FFFFF);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Payload Length: %d\n",
			ntohs(ipv6_hdr->payload_len));
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Next Header: 0x%02X\n",
			ipv6_hdr->proto);
		RTE_LOG(INFO,
			NAT64_TEST,
			"  Hop Limit: %d\n",
			ipv6_hdr->hop_limits);

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
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Source Port: %d\n",
				ntohs(udp_hdr->src_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Destination Port: %d\n",
				ntohs(udp_hdr->dst_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Length: %d\n",
				ntohs(udp_hdr->dgram_len));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Checksum: 0x%04X\n",
				ntohs(udp_hdr->dgram_cksum));
			break;
		case IPPROTO_TCP:
			data_off += sizeof(struct rte_tcp_hdr);
			struct rte_tcp_hdr *tcp_hdr =
				(struct rte_tcp_hdr *)proto_data;
			RTE_LOG(INFO, NAT64_TEST, "TCP Header:\n");
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Source Port: %d\n",
				ntohs(tcp_hdr->src_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Destination Port: %d\n",
				ntohs(tcp_hdr->dst_port));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Sequence Number: %u\n",
				ntohl(tcp_hdr->sent_seq));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Acknowledgment Number: %u\n",
				ntohl(tcp_hdr->recv_ack));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Data Offset: %d\n",
				(tcp_hdr->data_off & 0xF0) >> 4);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Flags: 0x%02X\n",
				tcp_hdr->tcp_flags);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Window Size: %d\n",
				ntohs(tcp_hdr->rx_win));
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Checksum: 0x%04X\n",
				ntohs(tcp_hdr->cksum));
			break;
		case IPPROTO_ICMPV6:
			data_off += sizeof(struct icmp6_hdr);
			struct icmp6_hdr *icmp6_hdr =
				(struct icmp6_hdr *)proto_data;
			RTE_LOG(INFO, NAT64_TEST, "ICMPv6 Header:\n");
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Type: 0x%02X\n",
				icmp6_hdr->icmp6_type);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Code: 0x%02X\n",
				icmp6_hdr->icmp6_code);
			RTE_LOG(INFO,
				NAT64_TEST,
				"  Checksum: 0x%04X\n",
				ntohs(icmp6_hdr->icmp6_cksum));
			break;
		}
	}
	RTE_LOG(INFO, NAT64_TEST, "Data Length: %d\n", mbuf->pkt_len - data_off
	);
}

/**
 * @brief Compare Ethernet headers between DPDK mbuf and universal packet
 *
 * Performs detailed comparison of Ethernet header fields:
 * - Destination MAC address
 * - Source MAC address
 * - Ethernet type
 *
 * Logs any differences found between the headers for debugging purposes.
 *
 * @param eth_hdr Pointer to DPDK Ethernet header
 * @param upkt Pointer to universal packet structure
 * @return 0 if headers match, -1 if any differences found
 */
static inline int
compare_ethernet_headers(struct rte_ether_hdr *eth_hdr, struct upkt *upkt) {
	int result = 0;

	if (memcmp(eth_hdr->dst_addr.addr_bytes,
		   upkt->eth.dst_addr.addr_bytes,
		   6) != 0) {
		result = -1;
		RTE_LOG(ERR,
			NAT64_TEST,
			"Difference in Ethernet destination address\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: " RTE_ETHER_ADDR_PRT_FMT "\n",
			RTE_ETHER_ADDR_BYTES(&upkt->eth.dst_addr));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF:" RTE_ETHER_ADDR_PRT_FMT "\n",
			RTE_ETHER_ADDR_BYTES(&eth_hdr->dst_addr));
	}

	if (memcmp(eth_hdr->src_addr.addr_bytes,
		   upkt->eth.src_addr.addr_bytes,
		   6) != 0) {
		result = -1;
		RTE_LOG(ERR,
			NAT64_TEST,
			"Difference in Ethernet source address\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: " RTE_ETHER_ADDR_PRT_FMT "\n",
			RTE_ETHER_ADDR_BYTES(&upkt->eth.src_addr));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: " RTE_ETHER_ADDR_PRT_FMT "\n",
			RTE_ETHER_ADDR_BYTES(&eth_hdr->src_addr));
	}

	if (eth_hdr->ether_type != upkt->eth.ether_type) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in Ethernet type\n");
		RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%hx\n", upkt->eth.ether_type);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%hx\n", eth_hdr->ether_type);
	}

	return result;
}

/**
 * @brief Compare IPv4 headers between DPDK mbuf and universal packet
 *
 * Performs detailed comparison of IPv4 header fields including:
 * - Version and IHL
 * - Type of Service
 * - Total Length
 * - Packet ID
 * - Fragment Offset
 * - Time to Live
 * - Protocol
 * - Header Checksum
 * - Source and Destination Addresses
 *
 * Logs any differences found between the headers for debugging purposes.
 *
 * @param ipv4_hdr Pointer to DPDK IPv4 header
 * @param upkt Pointer to universal packet structure
 * @return 0 if headers match, -1 if any differences found
 */
static inline int
compare_ipv4_headers(struct rte_ipv4_hdr *ipv4_hdr, struct upkt *upkt) {
	int result = 0;

	if (ipv4_hdr->version_ihl != upkt->ip.ipv4.version_ihl) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 version/IHL\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%x\n",
			upkt->ip.ipv4.version_ihl);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%x\n", ipv4_hdr->version_ihl);
	}

	if (ipv4_hdr->type_of_service != upkt->ip.ipv4.type_of_service) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 type of service\n"
		);
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%x\n",
			upkt->ip.ipv4.type_of_service);
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: 0x%x\n",
			ipv4_hdr->type_of_service);
	}

	if (ipv4_hdr->total_length != upkt->ip.ipv4.total_length) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 total length\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%x\n",
			upkt->ip.ipv4.total_length);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%x\n", ipv4_hdr->total_length
		);
	}

	if (ipv4_hdr->packet_id != upkt->ip.ipv4.packet_id) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 packet ID\n");
		RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%x\n", upkt->ip.ipv4.packet_id
		);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%x\n", ipv4_hdr->packet_id);
	}

	if (ipv4_hdr->fragment_offset != upkt->ip.ipv4.fragment_offset) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 fragment offset\n"
		);
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%x\n",
			upkt->ip.ipv4.fragment_offset);
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: 0x%x\n",
			ipv4_hdr->fragment_offset);
	}

	if (ipv4_hdr->time_to_live != upkt->ip.ipv4.time_to_live) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 TTL\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%x\n",
			upkt->ip.ipv4.time_to_live);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%x\n", ipv4_hdr->time_to_live
		);
	}

	if (ipv4_hdr->next_proto_id != upkt->ip.ipv4.next_proto_id) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 next protocol\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%x\n",
			upkt->ip.ipv4.next_proto_id);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%x\n", ipv4_hdr->next_proto_id
		);
	}

	if (ipv4_hdr->hdr_checksum != upkt->ip.ipv4.hdr_checksum) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 header checksum\n"
		);
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%x\n",
			htons(upkt->ip.ipv4.hdr_checksum));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: 0x%x\n",
			htons(ipv4_hdr->hdr_checksum));
	}

	if (ipv4_hdr->src_addr != upkt->ip.ipv4.src_addr) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in IPv4 source address\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: " IPv4_BYTES_FMT "\n",
			IPv4_BYTES_LE(upkt->ip.ipv4.src_addr));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: " IPv4_BYTES_FMT "\n",
			IPv4_BYTES_LE(ipv4_hdr->src_addr));
	}

	if (ipv4_hdr->dst_addr != upkt->ip.ipv4.dst_addr) {
		result = -1;
		RTE_LOG(ERR,
			NAT64_TEST,
			"Difference in IPv4 destination address\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: " IPv4_BYTES_FMT "\n",
			IPv4_BYTES_LE(upkt->ip.ipv4.dst_addr));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: " IPv4_BYTES_FMT "\n",
			IPv4_BYTES_LE(ipv4_hdr->dst_addr));
	}

	return result;
}
/**
 * @brief Compare IPv6 headers between DPDK mbuf and universal packet
 *
 * Performs detailed comparison of IPv6 header fields including:
 * - Version
 * - Traffic Class
 * - Flow Label
 * - Payload Length
 * - Next Header
 * - Hop Limit
 * - Source and Destination Addresses
 *
 * Logs any differences found between the headers for debugging purposes.
 *
 * @param ipv6_hdr Pointer to DPDK IPv6 header
 * @param upkt Pointer to universal packet structure
 * @return 0 if headers match, -1 if any differences found
 */
static inline int
compare_ipv6_headers(struct rte_ipv6_hdr *ipv6_hdr, struct upkt *upkt) {
	int result = 0;

	if ((htonl(ipv6_hdr->vtc_flow) & 0xF0000000) >> 28 !=
	    (htonl(upkt->ip.ipv6.vtc_flow) & 0xF0000000) >> 28) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in IPv6 version\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: %d\n",
			(htonl(upkt->ip.ipv6.vtc_flow) & 0xF0000000) >> 28);
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: %d\n",
			(htonl(ipv6_hdr->vtc_flow) & 0xF0000000) >> 28);
	}

	if (memcmp(&ipv6_hdr->src_addr,
		   &upkt->ip.ipv6.src_addr,
		   sizeof(struct in6_addr)) != 0) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in IPv6 source address\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: " IPv6_BYTES_FMT "\n",
			IPv6_BYTES(upkt->ip.ipv6.src_addr));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: " IPv6_BYTES_FMT "\n",
			IPv6_BYTES(ipv6_hdr->src_addr));
	}

	if (memcmp(&ipv6_hdr->dst_addr,
		   &upkt->ip.ipv6.dst_addr,
		   sizeof(struct in6_addr)) != 0) {
		result = -1;
		RTE_LOG(ERR,
			NAT64_TEST,
			"Difference in IPv6 destination address\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: " IPv6_BYTES_FMT "\n",
			IPv6_BYTES(upkt->ip.ipv6.dst_addr));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: " IPv6_BYTES_FMT "\n",
			IPv6_BYTES(ipv6_hdr->dst_addr));
	}

	if (ipv6_hdr->payload_len != upkt->ip.ipv6.payload_len) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in IPv6 payload length\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: %d\n",
			rte_be_to_cpu_16(upkt->ip.ipv6.payload_len));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: %d\n",
			rte_be_to_cpu_16(ipv6_hdr->payload_len));
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
		RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n", upkt->ip.ipv6.hop_limits
		);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ipv6_hdr->hop_limits);
	}

	return result;
}

/**
 * @brief Compare UDP headers between DPDK mbuf and universal packet
 *
 * Performs detailed comparison of UDP header fields including:
 * - Source Port
 * - Destination Port
 * - Datagram Length
 * - Checksum
 *
 * Logs any differences found between the headers for debugging purposes.
 *
 * @param udp_hdr Pointer to DPDK UDP header
 * @param upkt Pointer to universal packet structure
 * @return 0 if headers match, -1 if any differences found
 */
static inline int
compare_udp_headers(struct rte_udp_hdr *udp_hdr, struct upkt *upkt) {
	int result = 0;

	if (udp_hdr->src_port != upkt->proto.udp.src_port) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in UDP source port\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: %d\n",
			ntohs(upkt->proto.udp.src_port));
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(udp_hdr->src_port)
		);
	}

	if (udp_hdr->dst_port != upkt->proto.udp.dst_port) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in UDP destination port\n"
		);
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: %d\n",
			ntohs(upkt->proto.udp.dst_port));
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(udp_hdr->dst_port)
		);
	}

	if (udp_hdr->dgram_len != upkt->proto.udp.dgram_len) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in UDP length\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: %d\n",
			ntohs(upkt->proto.udp.dgram_len));
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(udp_hdr->dgram_len)
		);
	}
	if (udp_hdr->dgram_cksum != upkt->proto.udp.dgram_cksum) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in UDP checksum\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%04X\n",
			ntohs(upkt->proto.udp.dgram_cksum));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: 0x%04X\n",
			ntohs(udp_hdr->dgram_cksum));
	}

	return result;
}

/**
 * @brief Compare TCP headers between DPDK mbuf and universal packet
 *
 * Performs detailed comparison of TCP header fields including:
 * - Source Port
 * - Destination Port
 * - Sequence Number
 * - Acknowledgment Number
 * - Data Offset
 * - TCP Flags
 * - Window Size
 * - Checksum
 * - Urgent Pointer
 *
 * Logs any differences found between the headers for debugging purposes.
 *
 * @param tcp_hdr Pointer to DPDK TCP header
 * @param upkt Pointer to universal packet structure
 * @return 0 if headers match, -1 if any differences found
 */
static inline int
compare_tcp_headers(struct rte_tcp_hdr *tcp_hdr, struct upkt *upkt) {
	int result = 0;

	if (tcp_hdr->src_port != upkt->proto.tcp.src_port) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in TCP source port\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: %d\n",
			ntohs(upkt->proto.tcp.src_port));
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(tcp_hdr->src_port)
		);
	}

	if (tcp_hdr->dst_port != upkt->proto.tcp.dst_port) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in TCP destination port\n"
		);
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: %d\n",
			ntohs(upkt->proto.tcp.dst_port));
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(tcp_hdr->dst_port)
		);
	}

	if (tcp_hdr->sent_seq != upkt->proto.tcp.sent_seq) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in TCP sequence number\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: %u\n",
			ntohl(upkt->proto.tcp.sent_seq));
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %u\n", ntohl(tcp_hdr->sent_seq)
		);
	}

	if (tcp_hdr->recv_ack != upkt->proto.tcp.recv_ack) {
		result = -1;
		RTE_LOG(ERR,
			NAT64_TEST,
			"Difference in TCP acknowledgment number\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: %u\n",
			ntohl(upkt->proto.tcp.recv_ack));
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %u\n", ntohl(tcp_hdr->recv_ack)
		);
	}

	if ((tcp_hdr->data_off & 0xF0) >> 4 !=
	    (upkt->proto.tcp.data_off & 0xF0) >> 4) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in TCP data offset\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: %d\n",
			(upkt->proto.tcp.data_off & 0xF0) >> 4);
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: %d\n",
			(tcp_hdr->data_off & 0xF0) >> 4);
	}

	if (tcp_hdr->tcp_flags != upkt->proto.tcp.tcp_flags) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in TCP flags\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%02X\n",
			upkt->proto.tcp.tcp_flags);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%02X\n", tcp_hdr->tcp_flags);
	}

	if (tcp_hdr->rx_win != upkt->proto.tcp.rx_win) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in TCP window size\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: %d\n",
			ntohs(upkt->proto.tcp.rx_win));
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(tcp_hdr->rx_win));
	}

	if (tcp_hdr->cksum != upkt->proto.tcp.cksum) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in TCP checksum\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%04X\n",
			ntohs(upkt->proto.tcp.cksum));
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%04X\n", ntohs(tcp_hdr->cksum)
		);
	}

	if (tcp_hdr->tcp_urp != upkt->proto.tcp.tcp_urp) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in TCP urgent pointer\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: %d\n",
			ntohs(upkt->proto.tcp.tcp_urp));
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", ntohs(tcp_hdr->tcp_urp));
	}

	return result;
}

/**
 * @brief Compare ICMP headers between DPDK mbuf and universal packet
 *
 * Performs detailed comparison of ICMP header fields including:
 * - Type
 * - Code
 * - Gateway/Data field
 * - Checksum
 *
 * Logs any differences found between the headers for debugging purposes.
 * Used for verifying ICMP packet translations in NAT64 testing.
 *
 * @param icmp_hdr Pointer to ICMP header
 * @param upkt Pointer to universal packet structure
 * @return 0 if headers match, -1 if any differences found
 */
static inline int
compare_icmp_headers(struct icmphdr *icmp_hdr, struct upkt *upkt) {
	int result = 0;

	if (icmp_hdr->type != upkt->proto.icmp.type) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in ICMP type\n");
		RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%02X\n", upkt->proto.icmp.type
		);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%02X\n", icmp_hdr->type);
	}

	if (icmp_hdr->code != upkt->proto.icmp.code) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in ICMP code\n");
		RTE_LOG(ERR, NAT64_TEST, "UPKT: 0x%02X\n", upkt->proto.icmp.code
		);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%02X\n", icmp_hdr->code);
	}

	if (icmp_hdr->un.gateway != upkt->proto.icmp.un.gateway) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in ICMP data\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%08X\n",
			htonl(upkt->proto.icmp.un.gateway));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: 0x%08X\n",
			htonl(icmp_hdr->un.gateway));
	}

	if (icmp_hdr->checksum != upkt->proto.icmp.checksum) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in ICMP checksum\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%04X\n",
			ntohs(upkt->proto.icmp.checksum));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: 0x%04X\n",
			ntohs(icmp_hdr->checksum));
	}

	return result;
}

/**
 * @brief Compare ICMPv6 headers between DPDK mbuf and universal packet
 *
 * Performs detailed comparison of ICMPv6 header fields including:
 * - Type
 * - Code
 * - Checksum
 * - Parameter Pointer (for Parameter Problem messages)
 *
 * Logs any differences found between the headers for debugging purposes.
 * Used for verifying ICMPv6 packet translations in NAT64 testing.
 *
 * @param icmp6_hdr Pointer to ICMPv6 header
 * @param upkt Pointer to universal packet structure
 * @return 0 if headers match, -1 if any differences found
 */
static inline int
compare_icmp6_headers(struct icmp6_hdr *icmp6_hdr, struct upkt *upkt) {
	int result = 0;

	if (icmp6_hdr->icmp6_type != upkt->proto.icmp6.icmp6_type) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in ICMPv6 type\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%02X\n",
			upkt->proto.icmp6.icmp6_type);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%02X\n", icmp6_hdr->icmp6_type
		);
	}

	if (icmp6_hdr->icmp6_code != upkt->proto.icmp6.icmp6_code) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in ICMPv6 code\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%02X\n",
			upkt->proto.icmp6.icmp6_code);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: 0x%02X\n", icmp6_hdr->icmp6_code
		);
	}

	if (icmp6_hdr->icmp6_cksum != upkt->proto.icmp6.icmp6_cksum) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in ICMPv6 checksum\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%04X\n",
			ntohs(upkt->proto.icmp6.icmp6_cksum));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: 0x%04X\n",
			ntohs(icmp6_hdr->icmp6_cksum));
	}

	if (icmp6_hdr->icmp6_pptr != upkt->proto.icmp6.icmp6_pptr) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in ICMPv6 data\n");
		RTE_LOG(ERR,
			NAT64_TEST,
			"UPKT: 0x%08X\n",
			htonl(upkt->proto.icmp6.icmp6_pptr));
		RTE_LOG(ERR,
			NAT64_TEST,
			"MBUF: 0x%08X\n",
			htonl(icmp6_hdr->icmp6_pptr));
	}

	return result;
}

/**
 * @brief Compare packet structures
 *
 * Compares:
 * - Headers (Ethernet, IP, Protocol)
 * - Packet data
 * - Checksums
 *
 * @param upkt Universal packet structure
 * @param mbuf DPDK mbuf structure
 * @return 0 if match, -1 if different
 */
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
	result |= compare_ethernet_headers(eth_hdr, upkt);

	uint16_t data_off = sizeof(struct rte_ether_hdr);
	uint8_t *ip_hdr_offset = (uint8_t *)(eth_hdr + 1);

	if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *ipv4_hdr =
			(struct rte_ipv4_hdr *)ip_hdr_offset;
		data_off += rte_ipv4_hdr_len(ipv4_hdr);
		result |= compare_ipv4_headers(ipv4_hdr, upkt);
		uint8_t *proto_data = (uint8_t *)(ipv4_hdr + 1);

		switch (ipv4_hdr->next_proto_id) {
		case IPPROTO_UDP: {
			data_off += sizeof(struct rte_udp_hdr);
			struct rte_udp_hdr *udp_hdr =
				(struct rte_udp_hdr *)proto_data;
			result |= compare_udp_headers(udp_hdr, upkt);
			break;
		}
		case IPPROTO_TCP: {
			data_off += sizeof(struct rte_tcp_hdr);
			struct rte_tcp_hdr *tcp_hdr =
				(struct rte_tcp_hdr *)proto_data;
			result |= compare_tcp_headers(tcp_hdr, upkt);
			break;
		}
		case IPPROTO_ICMP: {
			data_off += sizeof(struct icmphdr);
			struct icmphdr *icmp_hdr = (struct icmphdr *)proto_data;
			result |= compare_icmp_headers(icmp_hdr, upkt);
			break;
		}
		}
	} else if (eth_hdr->ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *ipv6_hdr =
			(struct rte_ipv6_hdr *)ip_hdr_offset;
		data_off += sizeof(struct rte_ipv6_hdr);
		result |= compare_ipv6_headers(ipv6_hdr, upkt);
		uint8_t *proto_data = (uint8_t *)(ipv6_hdr + 1);

		switch (ipv6_hdr->proto) {
		case IPPROTO_UDP: {
			data_off += sizeof(struct rte_udp_hdr);
			struct rte_udp_hdr *udp_hdr =
				(struct rte_udp_hdr *)proto_data;
			result |= compare_udp_headers(udp_hdr, upkt);
			break;
		}
		case IPPROTO_TCP: {
			data_off += sizeof(struct rte_tcp_hdr);
			struct rte_tcp_hdr *tcp_hdr =
				(struct rte_tcp_hdr *)proto_data;
			result |= compare_tcp_headers(tcp_hdr, upkt);
			break;
		}
		case IPPROTO_ICMPV6: {
			data_off += sizeof(struct icmp6_hdr);
			struct icmp6_hdr *icmp6_hdr =
				(struct icmp6_hdr *)proto_data;
			result |= compare_icmp6_headers(icmp6_hdr, upkt);
			break;
		}
		}
	}

	// Compare Data Length and Content
	if ((mbuf->data_len - data_off) != upkt->data_len) {
		result = -1;
		RTE_LOG(ERR, NAT64_TEST, "Difference in Data Length\n");
		RTE_LOG(ERR, NAT64_TEST, "UPKT: %d\n", upkt->data_len);
		RTE_LOG(ERR, NAT64_TEST, "MBUF: %d\n", mbuf->data_len - data_off
		);
	} else if (upkt->data_len > 0 && upkt->data != NULL) {
		uint8_t *mbuf_data =
			rte_pktmbuf_mtod_offset(mbuf, uint8_t *, data_off);
		if (memcmp(mbuf_data, upkt->data, upkt->data_len) != 0) {
			result = -1;
			RTE_LOG(ERR, NAT64_TEST, "Difference in Data Content\n"
			);
			for (int i = 0; i < upkt->data_len; i++) {
				uint8_t u = ((uint8_t *)upkt->data)[i];
				RTE_LOG(ERR,
					NAT64_TEST,
					"%d: 0x%02x %s 0x%02x\n",
					i,
					u,
					u == mbuf_data[i] ? "=" : "!=",
					mbuf_data[i]);
			}
		}
	}

	return result;
}

/**
 * @brief Test case structure for NAT64 packet translation tests
 *
 * Contains all information needed for a single NAT64 translation test:
 * - Input packet to be translated
 * - Expected output packet after translation
 * - Test case name for identification
 * - Drop flag for cases where packet should be dropped
 *
 * Used to build linked list of test cases for comprehensive testing
 * of NAT64 translation scenarios.
 */
struct test_case {
	struct test_case *next;	  /**< Pointer to next test case in list */
	char *name;		  /**< Test case name/description */
	struct upkt pkt;	  /**< Input packet for translation */
	struct upkt pkt_expected; /**< Expected output after translation */
};

/**
 * @brief Add a new test case to the test suite
 *
 * Creates and initializes a new test case with:
 * - Input packet to be translated
 * - Expected output packet after translation
 * - Test case name for identification
 *
 * Adds the test case to the linked list of test cases.
 * Used to build up the test suite for NAT64 packet translation testing.
 *
 * @param head Pointer to head of test case linked list
 * @param pkt Input packet for translation
 * @param pkt_expected Expected output packet after translation
 * @param name Test case name/description
 */
static inline void
append_test_case(
	struct test_case **head,
	struct upkt pkt,
	struct upkt pkt_expected,
	char *name
) {
	struct test_case *new_case = malloc(sizeof(struct test_case));
	memset(new_case, 0, sizeof(struct test_case));

	new_case->pkt = pkt;
	new_case->pkt_expected = pkt_expected;
	new_case->name = strdup(name);

	if (*head == NULL) {
		RTE_LOG(INFO, NAT64_TEST, "Creating first test case\n");
		*head = new_case;
	} else {
		RTE_LOG(INFO, NAT64_TEST, "Creating next test case\n");
		struct test_case *current = *head;
		while (current->next != NULL) {
			current = current->next;
		}
		current->next = new_case;
	}
}

/**
 * @brief Calculate IPv4 UDP/TCP checksum
 *
 * Per RFC 768/793:
 * - L4 header checksum
 * - Payload checksum
 * - IPv4 pseudo-header
 * - UDP zero checksum handling
 *
 * @param ipv4_hdr IPv4 header
 * @param l4_hdr Protocol header
 * @param l4_len Protocol header length
 * @param payload Payload data
 * @param payload_len Payload length
 * @return Checksum in network byte order
 */
static inline uint16_t
upkt_ipv4_updtcp_checksum(
	struct rte_ipv4_hdr *ipv4_hdr,
	void *l4_hdr,
	uint16_t l4_len,
	void *payload,
	uint16_t payload_len
) {
	uint32_t cksum;

	cksum = __rte_raw_cksum(l4_hdr, l4_len, 0);
	cksum = __rte_raw_cksum(payload, payload_len, cksum);
	cksum += rte_ipv4_phdr_cksum(ipv4_hdr, 0);

	cksum = ((cksum & 0xffff0000) >> 16) + (cksum & 0xffff);

	cksum = ~cksum;

	/*
	 * Per RFC 768: If the computed checksum is zero for UDP,
	 * it is transmitted as all ones
	 * (the equivalent in one's complement arithmetic).
	 */
	if (cksum == 0 && ipv4_hdr->next_proto_id == IPPROTO_UDP) {
		cksum = 0xffff;
	}

	return (uint16_t)cksum;
}

/**
 * @brief Calculate UDP/TCP checksum for IPv6 packets
 *
 * Calculates checksum according to RFC 2460:
 * 1. Computes checksum over L4 header
 * 2. Adds checksum of payload data if present
 * 3. Adds IPv6 pseudo-header checksum
 * 4. Handles special case for UDP zero checksum
 *
 * Used to verify correct checksum calculation during NAT64 translation.
 *
 * @param ipv6_hdr Pointer to IPv6 header for pseudo-header
 * @param l4_hdr Pointer to UDP/TCP header
 * @param l4_len Length of UDP/TCP header
 * @param payload Pointer to payload data
 * @param payload_len Length of payload data
 * @return Calculated checksum in network byte order
 */
static inline uint16_t
upkt_ipv6_updtcp_checksum(
	struct rte_ipv6_hdr *ipv6_hdr,
	void *l4_hdr,
	uint16_t l4_len,
	void *payload,
	uint16_t payload_len
) {
	uint32_t cksum;

	cksum = __rte_raw_cksum(l4_hdr, l4_len, 0);

	cksum = __rte_raw_cksum(payload, payload_len, cksum);

	cksum = __rte_raw_cksum_reduce(cksum);

	cksum += rte_ipv6_phdr_cksum(ipv6_hdr, 0);

	cksum = ((cksum & 0xffff0000) >> 16) + (cksum & 0xffff);

	cksum = ~cksum;

	/*
	 * Per RFC 768: If the computed checksum is zero for UDP,
	 * it is transmitted as all ones
	 * (the equivalent in one's complement arithmetic).
	 */
	if (cksum == 0 && ipv6_hdr->proto == IPPROTO_UDP) {
		cksum = 0xffff;
	}
	return (uint16_t)cksum;
}

/**
 * @brief Calculate and update checksums for packet headers
 *
 * For IPv4 packets:
 * - Recalculates IPv4 header checksum
 * - For UDP: Updates checksum including IPv4 pseudo-header (RFC 768)
 * - For TCP: Updates checksum including IPv4 pseudo-header
 * - For ICMP: Updates checksum for ICMP header and payload
 *
 * For IPv6 packets:
 * - For UDP: Updates checksum including IPv6 pseudo-header (RFC 2460)
 * - For TCP: Updates checksum including IPv6 pseudo-header
 * - For ICMPv6: Updates checksum including IPv6 pseudo-header (RFC 4443)
 *
 * @param pkt Pointer to universal packet structure to update checksums for
 */
static inline void
fix_checksums(struct upkt *pkt) {
	if (pkt->eth.ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV4)) {
		struct rte_ipv4_hdr *ipv4_hdr = &pkt->ip.ipv4;

		// Recompute IPv4 header checksum
		ipv4_hdr->hdr_checksum = 0;
		ipv4_hdr->hdr_checksum = rte_ipv4_cksum(ipv4_hdr);

		switch (ipv4_hdr->next_proto_id) {
		case IPPROTO_UDP: {
			struct rte_udp_hdr *udp_hdr = &pkt->proto.udp;
			udp_hdr->dgram_cksum = 0;
			udp_hdr->dgram_cksum = upkt_ipv4_updtcp_checksum(
				ipv4_hdr,
				udp_hdr,
				sizeof(struct rte_udp_hdr),
				pkt->data,
				pkt->data_len
			);
			break;
		}
		case IPPROTO_TCP: {
			struct rte_tcp_hdr *tcp_hdr = &pkt->proto.tcp;
			tcp_hdr->cksum = 0;
			tcp_hdr->cksum = upkt_ipv4_updtcp_checksum(
				ipv4_hdr,
				tcp_hdr,
				sizeof(struct rte_tcp_hdr),
				pkt->data,
				pkt->data_len
			);
			break;
		}
		case IPPROTO_ICMP: {
			struct icmphdr *icmp_hdr = &pkt->proto.icmp;
			icmp_hdr->checksum = 0;
			uint32_t cksum = __rte_raw_cksum(
				icmp_hdr, sizeof(struct icmphdr), 0
			);
			if (pkt->data_len > 0 && pkt->data != NULL) {
				cksum = __rte_raw_cksum(
					pkt->data, pkt->data_len, cksum
				);
			}
			icmp_hdr->checksum = ~__rte_raw_cksum_reduce(cksum);
			break;
		}
		}
	} else if (pkt->eth.ether_type == RTE_BE16(RTE_ETHER_TYPE_IPV6)) {
		struct rte_ipv6_hdr *ipv6_hdr = &pkt->ip.ipv6;
		switch (ipv6_hdr->proto) {
		case IPPROTO_UDP: {
			struct rte_udp_hdr *udp_hdr = &pkt->proto.udp;
			udp_hdr->dgram_cksum = 0;
			udp_hdr->dgram_cksum = upkt_ipv6_updtcp_checksum(
				ipv6_hdr,
				udp_hdr,
				sizeof(struct rte_udp_hdr),
				pkt->data,
				pkt->data_len
			);
			break;
		}
		case IPPROTO_TCP: {
			struct rte_tcp_hdr *tcp_hdr = &pkt->proto.tcp;
			tcp_hdr->cksum = 0;
			tcp_hdr->cksum = upkt_ipv6_updtcp_checksum(
				ipv6_hdr,
				tcp_hdr,
				sizeof(struct rte_tcp_hdr),
				pkt->data,
				pkt->data_len
			);
			break;
		}
		case IPPROTO_ICMPV6: {
			struct icmp6_hdr *icmp6_hdr = &pkt->proto.icmp6;
			icmp6_hdr->icmp6_cksum = 0;

			uint32_t sum = rte_ipv6_phdr_cksum(ipv6_hdr, 0);
			sum = __rte_raw_cksum(
				icmp6_hdr, sizeof(struct icmp6_hdr), sum
			);
			sum = __rte_raw_cksum(pkt->data, pkt->data_len, sum);

			icmp6_hdr->icmp6_cksum = ~__rte_raw_cksum_reduce(sum);
			break;
		}
		}
	}
}

/**
 * @brief Create DPDK mbuf from universal packet and add to test packet list
 *
 * For IPv4 packets:
 * - Allocates new mbuf from test pool
 * - Copies Ethernet header
 * - Copies IPv4 header
 * - Copies protocol header (UDP, TCP, ICMP)
 * - Copies payload data if present
 * - Sets packet metadata (port, device IDs)
 * - Parses packet headers
 *
 * For IPv6 packets:
 * - Allocates new mbuf from test pool
 * - Copies Ethernet header
 * - Copies IPv6 header
 * - Copies protocol header (UDP, TCP, ICMPv6)
 * - Copies payload data if present
 * - Sets packet metadata (port, device IDs)
 * - Parses packet headers
 *
 * @param pkt Pointer to universal packet structure to convert to mbuf
 * @return 0 on success, -1 on failure (allocation or parsing error)
 */
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
		RTE_LOG(ERR,
			NAT64_TEST,
			"Usupported ether type %04X\n",
			pkt->eth.ether_type);
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
		l4_len = sizeof(struct icmphdr); // ICMP header
		break;
	case IPPROTO_ICMPV6:
		l4_len = sizeof(struct icmp6_hdr); // ICMPv6 header
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
	if (pkt->data_len > 0 && pkt->data != NULL) {
		rte_memcpy(
			rte_pktmbuf_mtod_offset(
				mbuf,
				void *,
				sizeof(struct rte_ether_hdr) + l3_len + l4_len
			),
			pkt->data,
			pkt->data_len
		);
	}

	mbuf->port = 0;

	struct packet *packet = mbuf_to_packet(mbuf);
	memset(packet, 0, sizeof(struct packet));
	packet->mbuf = mbuf;
	packet->rx_device_id = 0;
	packet->tx_device_id = 0;

	if (parse_packet(packet)) {
		RTE_LOG(ERR,
			NAT64_TEST,
			"Failed to parse packet after creation\n");
		return -1;
	}

	packet_list_add(&test_params.packet_front.input, packet);
	return 0;
}

/**
 * @brief Create basic UDP test cases from NAT64 address mappings
 *
 * For each configured address mapping:
 * 1. Creates IPv4->IPv6 test case with:
 *    - UDP packet with source from TEST-NET-1 range
 *    - Destination from mapping IPv4 address
 *    - Expected translation to IPv6 with correct prefix
 * 2. Creates IPv6->IPv4 test case with:
 *    - Reversed source/destination addresses
 *    - Appropriate checksum updates
 *
 * Used to verify basic NAT64 UDP translation functionality.
 *
 * @param test_case Pointer to test case list to append to
 * @return 0 on success, -1 on failure
 */
static int
append_test_cases_from_mappings(struct test_case **test_case) {
	struct nat64_module_config *nat64_config =
		(struct nat64_module_config *)&test_params.module_config;
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
						sizeof(struct rte_udp_hdr) +
						10 // Data length
					),
					.time_to_live = DEFAULT_TTL,
					.next_proto_id = IPPROTO_UDP,
					.src_addr = outer_ip4,
					.dst_addr = config_data.mapping[i].ip4,
				},
			.proto.udp =
				{
					.src_port = rte_cpu_to_be_16(12345),
					.dst_port = rte_cpu_to_be_16(53
					), /* DNS port */
					.dgram_len = rte_cpu_to_be_16(
						sizeof(struct rte_udp_hdr) + 10
					), /* UDP header + data */
				},
			.data_len = 10,
			.data = "0123456789"
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
					.vtc_flow = RTE_BE32(0x60000000),
					.payload_len = RTE_BE16(
						sizeof(struct rte_udp_hdr) + 10
					),
				},
			.proto.udp =
				{
					.src_port = rte_cpu_to_be_16(12345),
					.dst_port = rte_cpu_to_be_16(53
					), /* DNS port */
					.dgram_len = rte_cpu_to_be_16(
						sizeof(struct rte_udp_hdr) + 10
					),
				},
			.data_len = 10,
			.data = "0123456789"
		};
		rte_memcpy(
			&pkt_expected.ip.ipv6.dst_addr,
			&config_data.mapping[i].ip6,
			16
		);

		SET_IPV4_MAPPED_IPV6(
			&pkt_expected.ip.ipv6.src_addr,
			ADDR_OF(&nat64_config->prefixes.prefixes)[0].prefix,
			&outer_ip4
		);
		char buf[1024];
		snprintf(
			buf,
			1023,
			"v4 -> v6 " IPv4_BYTES_FMT " -> " IPv6_BYTES_FMT,
			IPv4_BYTES(RTE_BE32(outer_ip4)),
			IPv6_BYTES(pkt_expected.ip.ipv6.dst_addr)
		);
		append_test_case(test_case, pkt, pkt_expected, buf);
		snprintf(
			buf,
			1023,
			"v6 -> v4 " IPv6_BYTES_FMT " -> " IPv4_BYTES_FMT,
			IPv6_BYTES(pkt_expected.ip.ipv6.src_addr),
			IPv4_BYTES(RTE_BE32(outer_ip4))
		);
		// Swap src and dst ip for the second test case
		uint8_t tmp_ip[16];
		rte_memcpy(tmp_ip, &pkt_expected.ip.ipv6.src_addr, 16);
		rte_memcpy(
			&pkt_expected.ip.ipv6.src_addr,
			&pkt_expected.ip.ipv6.dst_addr,
			16
		);
		rte_memcpy(&pkt_expected.ip.ipv6.dst_addr, tmp_ip, 16);

		// Swap src and dst ip for the second test case for ipv4 pkt
		uint32_t tmp = pkt.ip.ipv4.src_addr;
		pkt.ip.ipv4.src_addr = pkt.ip.ipv4.dst_addr;
		pkt.ip.ipv4.dst_addr = tmp;

		append_test_case(test_case, pkt_expected, pkt, buf);
	}
	return 0;
}

/**
 * @brief ICMP test parameters
 *
 * Contains:
 * - ICMP types/codes
 * - Translation flags
 * - MTU/pointer values
 *
 * Used for ICMP translation tests
 */
struct icmp_type_info_t {
	const char *name;    /**< Test case name */
	uint8_t type;	     /**< ICMPv4 type */
	uint8_t code;	     /**< ICMPv4 code */
	uint8_t type6;	     /**< ICMPv6 type */
	uint8_t code6;	     /**< ICMPv6 code */
	bool from_ipv4;	     /**< Whether packet is from IPv4 or IPv6 */
	uint8_t embed_proto; /**< if > 0 include embedded packet with proto */
	uint32_t mtu;	     /**< For PTB tests, 0 if not applicable */
	uint32_t mtu6;	     /**< IPv6 MTU value */
	uint32_t pointer;  /**< For Parameter Problem tests, 0 if not applicable
			    */
	uint32_t pointer6; /**< IPv6 pointer value */
	bool should_drop;  /**< Whether packet should be dropped */
};

/**
 * @brief Create ICMP test packet
 *
 * Creates packet with:
 * - Headers (Ethernet, IP, ICMP)
 * - Optional embedded packet
 * - Optional payload
 *
 * @param info ICMP parameters
 * @param is_v6 Create IPv6 packet if true
 * @param prefix NAT64 prefix
 * @return New packet or NULL on failure
 */
static struct upkt *
create_icmp_packet(
	const struct icmp_type_info_t *info, bool is_v6, uint8_t *prefix
) {
	struct upkt *pkt = rte_malloc(NULL, sizeof(struct upkt), 0);

	if (!pkt) {
		return NULL;
	}

	// Common fields
	rte_memcpy(
		&pkt->eth.dst_addr.addr_bytes, "\xff\xff\xff\xff\xff\xff", 6
	);
	rte_memcpy(
		&pkt->eth.src_addr.addr_bytes, "\x02\x00\x00\x00\x00\x00", 6
	);

	// Create embedded packet for ICMP error messages if needed
	uint8_t *embedded_pkt = NULL;
	uint16_t embedded_len = 0;

	if (is_v6) {
		pkt->eth.ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
		pkt->ip.ipv6.vtc_flow = RTE_BE32(0x60000000);
		pkt->ip.ipv6.proto = IPPROTO_ICMPV6;
		pkt->ip.ipv6.hop_limits = 64;
		// For IPv6 packets, use the IPv6 ICMP type/code
		pkt->proto.icmp6.icmp6_type = info->type6;
		pkt->proto.icmp6.icmp6_code = info->code6;

		// For Echo Request/Reply, set ID and sequence
		if (info->type6 == ICMP6_ECHO_REQUEST ||
		    info->type6 == ICMP6_ECHO_REPLY) {
			pkt->proto.icmp6.icmp6_id = rte_cpu_to_be_16(0x1234);
			pkt->proto.icmp6.icmp6_seq = rte_cpu_to_be_16(1);
		}

		uint16_t payload_len = sizeof(struct icmp6_hdr);

		pkt->ip.ipv6.payload_len = rte_cpu_to_be_16(payload_len);

		// For non-error messages that need data
		if (!info->embed_proto && (info->type6 == ICMP6_ECHO_REQUEST ||
					   info->type6 == ICMP6_ECHO_REPLY)) {
			pkt->data = rte_malloc(NULL, 8, 0);
			if (!pkt->data) {
				rte_free(embedded_pkt);
				rte_free(pkt);
				return NULL;
			}
			memset(pkt->data, 0x42, 8);
			pkt->data_len = 8;
			pkt->ip.ipv6.payload_len =
				rte_cpu_to_be_16(payload_len + 8);
		}
		// Set source/destination IPs
		if (info->from_ipv4) {
			rte_memcpy(
				&pkt->ip.ipv6.dst_addr,
				&config_data.mapping[0].ip6,
				16
			);
			SET_IPV4_MAPPED_IPV6(
				&pkt->ip.ipv6.src_addr, prefix, &outer_ip4
			);
		} else {
			rte_memcpy(
				&pkt->ip.ipv6.src_addr,
				&config_data.mapping[0].ip6,
				16
			);
			SET_IPV4_MAPPED_IPV6(
				&pkt->ip.ipv6.dst_addr, prefix, &outer_ip4
			);
		}
	} else {
		pkt->eth.ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);
		pkt->ip.ipv4.version_ihl = RTE_IPV4_VHL_DEF;
		pkt->ip.ipv4.type_of_service = 0;
		pkt->ip.ipv4.next_proto_id = IPPROTO_ICMP;
		pkt->ip.ipv4.time_to_live = 64;
		pkt->ip.ipv4.packet_id = 0;
		pkt->proto.icmp.type = info->type;
		pkt->proto.icmp.code = info->code;

		if (info->from_ipv4) {
			pkt->ip.ipv4.dst_addr = config_data.mapping[0].ip4;
			pkt->ip.ipv4.src_addr = outer_ip4;
		} else {
			pkt->ip.ipv4.dst_addr = outer_ip4;
			pkt->ip.ipv4.src_addr = config_data.mapping[0].ip4;
		}

		// For Echo Request/Reply, set ID and sequence
		if (info->type == ICMP_ECHO || info->type == ICMP_ECHOREPLY) {
			pkt->proto.icmp.un.echo.id = rte_cpu_to_be_16(0x1234);
			pkt->proto.icmp.un.echo.sequence = rte_cpu_to_be_16(1);
		}

		uint16_t total_len =
			sizeof(struct rte_ipv4_hdr) + sizeof(struct icmphdr);

		// For non-error messages that need data
		if (!info->embed_proto &&
		    (info->type == ICMP_ECHO || info->type == ICMP_ECHOREPLY)) {
			pkt->data = rte_malloc(NULL, 8, 0);
			if (!pkt->data) {
				rte_free(embedded_pkt);
				rte_free(pkt);
				return NULL;
			}
			memset(pkt->data, 0x42, 8);
			pkt->data_len = 8;
			total_len += 8;
		}

		pkt->ip.ipv4.total_length = rte_cpu_to_be_16(total_len);
	}

	if (info->embed_proto) {
		// Create embedded packet with appropriate protocol
		uint16_t proto_hdr_len;
		uint8_t proto = info->embed_proto;

		switch (info->embed_proto) {
		case IPPROTO_UDP:
			proto_hdr_len = sizeof(struct rte_udp_hdr);
			break;
		case IPPROTO_TCP:
			proto_hdr_len = sizeof(struct rte_tcp_hdr);
			break;
		case IPPROTO_ICMP:
		case IPPROTO_ICMPV6:
			proto = is_v6 ? IPPROTO_ICMPV6 : IPPROTO_ICMP;
			proto_hdr_len = is_v6 ? sizeof(struct icmp6_hdr)
					      : sizeof(struct icmphdr);
			break;

		default:
			proto_hdr_len = sizeof(struct rte_udp_hdr);
			proto = IPPROTO_UDP;
			break;
		}

		// Calculate total embedded packet length
		embedded_len =
			is_v6 ? sizeof(struct rte_ipv6_hdr) + proto_hdr_len + 8
			      : sizeof(struct rte_ipv4_hdr) + proto_hdr_len + 8;

		embedded_pkt = rte_malloc(NULL, embedded_len, 0);
		if (!embedded_pkt) {
			rte_free(pkt);
			return NULL;
		}

		// Initialize embedded packet
		if (is_v6) {
			pkt->ip.ipv6.payload_len = rte_cpu_to_be_16(
				sizeof(struct icmp6_hdr) + embedded_len
			);
			struct rte_ipv6_hdr *ip6 =
				(struct rte_ipv6_hdr *)embedded_pkt;
			ip6->vtc_flow = RTE_BE32(0x60000000);
			ip6->payload_len = rte_cpu_to_be_16(proto_hdr_len + 8);
			ip6->proto = proto;
			ip6->hop_limits = 64;
			rte_memcpy(
				&ip6->src_addr,
				pkt->ip.ipv6.dst_addr,
				sizeof(struct in6_addr)
			);
			rte_memcpy(
				&ip6->dst_addr,
				pkt->ip.ipv6.src_addr,
				sizeof(struct in6_addr)
			);

			void *proto_hdr = (void *)(ip6 + 1);

			// Initialize protocol-specific header
			switch (proto) {
			case IPPROTO_UDP: {
				struct rte_udp_hdr *udp =
					(struct rte_udp_hdr *)proto_hdr;
				udp->src_port = rte_cpu_to_be_16(12345);
				udp->dst_port = rte_cpu_to_be_16(53);
				udp->dgram_len = rte_cpu_to_be_16(
					sizeof(struct rte_udp_hdr) + 8
				);
				udp->dgram_cksum = 0;
				break;
			}

			case IPPROTO_TCP: {
				struct rte_tcp_hdr *tcp =
					(struct rte_tcp_hdr *)proto_hdr;
				tcp->src_port = rte_cpu_to_be_16(12345);
				tcp->dst_port = rte_cpu_to_be_16(80);
				tcp->sent_seq = rte_cpu_to_be_32(1);
				tcp->recv_ack = 0;
				tcp->data_off = 0x50;
				tcp->tcp_flags = RTE_TCP_SYN_FLAG;
				tcp->rx_win = rte_cpu_to_be_16(8192);
				tcp->cksum = 0;
				tcp->tcp_urp = 0;
				break;
			}

			case IPPROTO_ICMPV6: {
				struct icmp6_hdr *icmp6 =
					(struct icmp6_hdr *)proto_hdr;
				icmp6->icmp6_type = ICMP6_ECHO_REQUEST;
				icmp6->icmp6_code = 0;
				icmp6->icmp6_cksum = 0;
				icmp6->icmp6_id = rte_cpu_to_be_16(0x1234);
				icmp6->icmp6_seq = rte_cpu_to_be_16(1);
				break;
			}
			}

			uint8_t *data = (uint8_t *)proto_hdr + proto_hdr_len;
			memset(data, 0x42, 8);

			// For PTB messages, set MTU in the ICMP header
			if (info->type6 == ICMP6_PACKET_TOO_BIG) {
				pkt->proto.icmp6.icmp6_mtu = htonl(info->mtu6);
			}

			// For Parameter Problem messages, set pointer
			if (info->type6 == ICMP6_PARAM_PROB) {
				pkt->proto.icmp6.icmp6_pptr =
					htonl(info->pointer6);
			}
		} else {
			pkt->ip.ipv4.total_length = rte_cpu_to_be_16(
				sizeof(struct rte_ipv4_hdr) +
				sizeof(struct icmphdr) + embedded_len
			);
			struct rte_ipv4_hdr *ip4 =
				(struct rte_ipv4_hdr *)embedded_pkt;
			ip4->version_ihl = RTE_IPV4_VHL_DEF;
			ip4->type_of_service = 0;
			ip4->total_length = rte_cpu_to_be_16(embedded_len);
			ip4->packet_id = 0;
			ip4->fragment_offset = 0;
			ip4->time_to_live = 64;
			ip4->next_proto_id = proto;
			ip4->hdr_checksum = 0;
			ip4->src_addr = pkt->ip.ipv4.dst_addr;
			ip4->dst_addr = pkt->ip.ipv4.src_addr;

			void *proto_hdr = (void *)(ip4 + 1);

			// Initialize protocol-specific header
			switch (proto) {
			case IPPROTO_UDP: {
				struct rte_udp_hdr *udp =
					(struct rte_udp_hdr *)proto_hdr;
				udp->src_port = rte_cpu_to_be_16(12345);
				udp->dst_port = rte_cpu_to_be_16(53);
				udp->dgram_len = rte_cpu_to_be_16(
					sizeof(struct rte_udp_hdr) + 8
				);
				udp->dgram_cksum = 0;
				break;
			}

			case IPPROTO_TCP: {
				struct rte_tcp_hdr *tcp =
					(struct rte_tcp_hdr *)proto_hdr;
				tcp->src_port = rte_cpu_to_be_16(12345);
				tcp->dst_port = rte_cpu_to_be_16(80);
				tcp->sent_seq = rte_cpu_to_be_32(1);
				tcp->recv_ack = 0;
				tcp->data_off = 0x50;
				tcp->tcp_flags = RTE_TCP_SYN_FLAG;
				tcp->rx_win = rte_cpu_to_be_16(8192);
				tcp->cksum = 0;
				tcp->tcp_urp = 0;
				break;
			}

			case IPPROTO_ICMP: {
				struct icmphdr *icmp =
					(struct icmphdr *)proto_hdr;
				icmp->type = ICMP_ECHO;
				icmp->code = 0;
				icmp->checksum = 0;
				icmp->un.echo.id = rte_cpu_to_be_16(0x1234);
				icmp->un.echo.sequence = rte_cpu_to_be_16(1);
				break;
			}
			}

			uint8_t *data = (uint8_t *)proto_hdr + proto_hdr_len;
			memset(data, 0x42, 8);

			// For Fragmentation Needed messages, set MTU
			if (info->type == ICMP_DEST_UNREACH &&
			    info->code == ICMP_FRAG_NEEDED) {
				pkt->proto.icmp.un.frag.mtu = htons(info->mtu);
			}

			// For Parameter Problem messages, set pointer
			if (info->type == ICMP_PARAMPROB) {
				// For Parameter Problem messages, store pointer
				// in identifier field
				pkt->proto.icmp.un.echo.id =
					htons(info->pointer << 8);
			}

			// Calculate IPv4 header checksum
			ip4->hdr_checksum = rte_ipv4_cksum(ip4);
		}

		// Calculate checksums for embedded packet
		if (is_v6) {
			struct rte_ipv6_hdr *ip6 =
				(struct rte_ipv6_hdr *)embedded_pkt;
			void *proto_hdr = (void *)(ip6 + 1);

			switch (proto) {
			case IPPROTO_UDP: {
				struct rte_udp_hdr *udp =
					(struct rte_udp_hdr *)proto_hdr;
				udp->dgram_cksum =
					rte_ipv6_udptcp_cksum(ip6, udp);
				break;
			}

			case IPPROTO_TCP: {
				struct rte_tcp_hdr *tcp =
					(struct rte_tcp_hdr *)proto_hdr;
				tcp->cksum = rte_ipv6_udptcp_cksum(ip6, tcp);
				break;
			}

			case IPPROTO_ICMPV6: {
				struct icmp6_hdr *icmp6 =
					(struct icmp6_hdr *)proto_hdr;
				uint32_t cksum = rte_ipv6_phdr_cksum(ip6, 0);
				cksum = __rte_raw_cksum(
					icmp6, sizeof(struct icmp6_hdr), cksum
				);
				cksum = __rte_raw_cksum(
					(uint8_t *)proto_hdr +
						sizeof(struct icmp6_hdr),
					8,
					cksum
				);
				icmp6->icmp6_cksum =
					~__rte_raw_cksum_reduce(cksum);
				break;
			}
			}
		} else {
			struct rte_ipv4_hdr *ip4 =
				(struct rte_ipv4_hdr *)embedded_pkt;
			void *proto_hdr = (void *)(ip4 + 1);

			switch (proto) {
			case IPPROTO_UDP: {
				struct rte_udp_hdr *udp =
					(struct rte_udp_hdr *)proto_hdr;
				udp->dgram_cksum =
					rte_ipv4_udptcp_cksum(ip4, udp);
				break;
			}
			case IPPROTO_TCP: {
				struct rte_tcp_hdr *tcp =
					(struct rte_tcp_hdr *)proto_hdr;
				tcp->cksum = rte_ipv4_udptcp_cksum(ip4, tcp);
				break;
			}
			case IPPROTO_ICMP: {
				struct icmphdr *icmp =
					(struct icmphdr *)proto_hdr;
				uint32_t cksum = __rte_raw_cksum(
					icmp, sizeof(struct icmphdr), 0
				);
				cksum = __rte_raw_cksum(
					(uint8_t *)proto_hdr +
						sizeof(struct icmphdr),
					8,
					cksum
				);
				icmp->checksum = ~__rte_raw_cksum_reduce(cksum);
				break;
			}
			}
		}
	}

	// Set data pointer and length for error messages
	if (embedded_pkt) {
		pkt->data = embedded_pkt;
		pkt->data_len = embedded_len;
	} else if (!pkt->data) {
		pkt->data = NULL;
		pkt->data_len = 0;
	}

	return pkt;
}

/**
 * @brief Create comprehensive ICMP test cases for NAT64 translation
 *
 * Creates test cases for all ICMP translation scenarios per RFC 7915:
 * 1. Echo Request/Reply translations
 * 2. Destination Unreachable variations
 * 3. Packet Too Big with different MTU values
 * 4. Time Exceeded messages
 * 5. Parameter Problem with pointer translations
 * 6. Drop cases for unsupported messages (MLD, ND)
 * 7. Edge cases and invalid message handling
 *
 * Each test case verifies:
 * - Correct type/code translation
 * - Proper handling of embedded packets
 * - MTU and pointer value adjustments
 * - Expected packet drops
 *
 * @param test_case Pointer to test case list to append to
 * @return 0 on success, -1 on failure
 */
static int
append_test_cases_from_mappings_icmp_more(struct test_case **test_case) {
	struct nat64_module_config *nat64_config =
		(struct nat64_module_config *)&test_params.module_config;

	const struct icmp_type_info_t icmp_types[] = {
		{"Echo Request v4->v6",
		 ICMP_ECHO,
		 0,
		 ICMP6_ECHO_REQUEST,
		 0,
		 true,
		 false,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Echo Reply v4->v6",
		 ICMP_ECHOREPLY,
		 0,
		 ICMP6_ECHO_REPLY,
		 0,
		 true,
		 false,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Echo Request v6->v4",
		 ICMP_ECHO,
		 0,
		 ICMP6_ECHO_REQUEST,
		 0,
		 false,
		 false,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Echo Reply v6->v4",
		 ICMP_ECHOREPLY,
		 0,
		 ICMP6_ECHO_REPLY,
		 0,
		 false,
		 false,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Extended Echo Request v6->v4 (drop)",
		 ICMP_ECHO,
		 0,
		 ICMPV6_EXT_ECHO_REQUEST,
		 0,
		 false,
		 false,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Extended Echo Reply v6->v4 (drop)",
		 ICMP_ECHOREPLY,
		 0,
		 ICMPV6_EXT_ECHO_REPLY,
		 0,
		 false,
		 false,
		 0,
		 0,
		 0,
		 0,
		 true},

		// Destination Unreachable variations
		{"No Route v6->v4",
		 ICMP_DEST_UNREACH,
		 ICMP_HOST_UNREACH,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_NOROUTE,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Host Unreachable v6->v4",
		 ICMP_DEST_UNREACH,
		 ICMP_HOST_UNREACH,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_ADDR,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Protocol Unreachable v4->v6",
		 ICMP_DEST_UNREACH,
		 ICMP_PROT_UNREACH,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_NEXTHEADER,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 6,
		 false},
		{"Port Unreachable v6->v4",
		 ICMP_DEST_UNREACH,
		 ICMP_PORT_UNREACH,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_NOPORT,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Admin Prohibited v6->v4",
		 ICMP_DEST_UNREACH,
		 ICMP_HOST_ANO,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_ADMIN,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Beyond Scope v6->v4",
		 ICMP_DEST_UNREACH,
		 ICMP_HOST_UNREACH,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_BEYONDSCOPE,
		 false,
		 IPPROTO_TCP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Source Route Failed v4->v6",
		 ICMP_DEST_UNREACH,
		 ICMP_SR_FAILED,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_NOROUTE,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Network Unknown v4->v6",
		 ICMP_DEST_UNREACH,
		 ICMP_NET_UNKNOWN,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_NOROUTE,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Host Unknown v4->v6",
		 ICMP_DEST_UNREACH,
		 ICMP_HOST_UNKNOWN,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_NOROUTE,
		 true,
		 IPPROTO_ICMP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Network Prohibited v4->v6",
		 ICMP_DEST_UNREACH,
		 ICMP_NET_ANO,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_ADMIN,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Host Prohibited v4->v6",
		 ICMP_DEST_UNREACH,
		 ICMP_HOST_ANO,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_ADMIN,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"TOS & Network v4->v6",
		 ICMP_DEST_UNREACH,
		 ICMP_NET_UNR_TOS,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_NOROUTE,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"TOS & Host v4->v6",
		 ICMP_DEST_UNREACH,
		 ICMP_HOST_UNR_TOS,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_NOROUTE,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Filtered v4->v6",
		 ICMP_DEST_UNREACH,
		 ICMP_PKT_FILTERED,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_ADMIN,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Precedence Violation v4->v6 (drop)",
		 ICMP_DEST_UNREACH,
		 ICMP_PREC_VIOLATION,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_ADMIN,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Precedence Cutoff v4->v6",
		 ICMP_DEST_UNREACH,
		 ICMP_PREC_CUTOFF,
		 ICMP6_DST_UNREACH,
		 ICMP6_DST_UNREACH_ADMIN,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},

		// Packet Too Big with different MTU values
		{"PTB MTU=1280 v6->v4",
		 ICMP_DEST_UNREACH,
		 ICMP_FRAG_NEEDED,
		 ICMP6_PACKET_TOO_BIG,
		 0,
		 false,
		 IPPROTO_UDP,
		 1260,
		 1280,
		 0,
		 0,
		 false},
		{"PTB MTU=1500 v6->v4",
		 ICMP_DEST_UNREACH,
		 ICMP_FRAG_NEEDED,
		 ICMP6_PACKET_TOO_BIG,
		 0,
		 false,
		 IPPROTO_UDP,
		 1260,
		 1500,
		 0,
		 0,
		 false},
		{"PTB MTU=576 v6->v4",
		 ICMP_DEST_UNREACH,
		 ICMP_FRAG_NEEDED,
		 ICMP6_PACKET_TOO_BIG,
		 0,
		 false,
		 IPPROTO_UDP,
		 547,
		 567,
		 0,
		 0,
		 false},
		{"PTB MTU=0 v6->v4",
		 ICMP_DEST_UNREACH,
		 ICMP_FRAG_NEEDED,
		 ICMP6_PACKET_TOO_BIG,
		 0,
		 false,
		 IPPROTO_UDP,
		 1260,
		 0,
		 0,
		 0,
		 false},
		{"PTB MTU=65535 v6->v4",
		 ICMP_DEST_UNREACH,
		 ICMP_FRAG_NEEDED,
		 ICMP6_PACKET_TOO_BIG,
		 0,
		 false,
		 IPPROTO_UDP,
		 1260,
		 65535,
		 0,
		 0,
		 false},

		// Time Exceeded
		{"TTL Exceeded v6->v4",
		 ICMP_TIME_EXCEEDED,
		 ICMP_EXC_TTL,
		 ICMP6_TIME_EXCEEDED,
		 ICMP6_TIME_EXCEED_TRANSIT,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Fragment Reassembly v6->v4",
		 ICMP_TIME_EXCEEDED,
		 ICMP_EXC_FRAGTIME,
		 ICMP6_TIME_EXCEEDED,
		 ICMP6_TIME_EXCEED_REASSEMBLY,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},

		// Parameter Problem with pointer translations - IPv4 to IPv6
		{"Header Error v4->v6 Version",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Header Error v4->v6 Traffic Class",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 1,
		 1,
		 false},
		{"Header Error v4->v6 Flow Label",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 true,
		 IPPROTO_TCP,
		 0,
		 0,
		 5,
		 2,
		 true},
		{"Header Error v4->v6 Payload Length",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 2,
		 4,
		 false},
		{"Header Error v4->v6 Next Header",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 9,
		 6,
		 false},
		{"Header Error v4->v6 Hop Limit",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 true,
		 IPPROTO_TCP,
		 0,
		 0,
		 8,
		 7,
		 false},
		{"Header Error v4->v6 Source Address",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 true,
		 IPPROTO_ICMP,
		 0,
		 0,
		 12,
		 8,
		 false},
		{"Header Error v4->v6 Destination Address",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 16,
		 24,
		 false},

		// Parameter Problem with pointer translations - IPv6 to IPv4
		{"Header Error v6->v4 ptr=0",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},
		{"Header Error v6->v4 ptr=4",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 2,
		 4,
		 false},
		{"Header Error v6->v4 ptr=6",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 false,
		 IPPROTO_TCP,
		 0,
		 0,
		 9,
		 6,
		 false},
		{"Header Error v6->v4 ptr=7",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 8,
		 7,
		 false},
		{"Header Error v6->v4 ptr=8",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 false,
		 IPPROTO_ICMP,
		 0,
		 0,
		 12,
		 8,
		 false},
		{"Header Error v6->v4 ptr=20",
		 ICMP_PARAMPROB,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 12,
		 20,
		 false},
		{"Next Header v6->v4",
		 ICMP_DEST_UNREACH,
		 ICMP_PROT_UNREACH,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_NEXTHEADER,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 false},

		// Drop cases - Non-translatable ICMPv6 messages
		{"MLD Query (drop)",
		 0,
		 0,
		 MLD_LISTENER_QUERY,
		 0,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"MLD Report (drop)",
		 0,
		 0,
		 MLD_LISTENER_REPORT,
		 0,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"MLD Done (drop)",
		 0,
		 0,
		 MLD_LISTENER_REDUCTION,
		 0,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Router Solicitation (drop)",
		 0,
		 0,
		 ND_ROUTER_SOLICIT,
		 0,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Router Advertisement (drop)",
		 0,
		 0,
		 ND_ROUTER_ADVERT,
		 0,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Neighbor Solicitation (drop)",
		 0,
		 0,
		 ND_NEIGHBOR_SOLICIT,
		 0,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Neighbor Advertisement (drop)",
		 0,
		 0,
		 ND_NEIGHBOR_ADVERT,
		 0,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Redirect (drop)",
		 0,
		 0,
		 ND_REDIRECT,
		 0,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Router Renumbering (drop)",
		 0,
		 0,
		 ICMP6_ROUTER_RENUMBERING,
		 0,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},

		// Invalid cases that should be dropped
		{"Invalid Parameter Problem ptr=40 (drop)",
		 0,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_HEADER,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 40,
		 true},
		{"Invalid Parameter Problem code=2 (drop)",
		 0,
		 0,
		 ICMP6_PARAM_PROB,
		 ICMP6_PARAMPROB_OPTION,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},

		// Additional edge cases - ICMPv4 messages that should be
		// dropped
		{"Source Quench v4->v6 (drop)",
		 ICMP_SOURCE_QUENCH,
		 0,
		 0,
		 0,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		// Obsolete ICMPv4 messages that should be dropped during v4->v6
		// translation
		{"Timestamp Request v4->v6 (drop)",
		 ICMP_TIMESTAMP,
		 0,
		 0,
		 0,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Timestamp Reply v4->v6 (drop)",
		 ICMP_TIMESTAMPREPLY,
		 0,
		 0,
		 0,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Information Request v4->v6 (drop)",
		 ICMP_INFO_REQUEST,
		 0,
		 0,
		 0,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Information Reply v4->v6 (drop)",
		 ICMP_INFO_REPLY,
		 0,
		 0,
		 0,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Address Mask Request v4->v6 (drop)",
		 ICMP_ADDRESS,
		 0,
		 0,
		 0,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		{"Address Mask Reply v4->v6 (drop)",
		 ICMP_ADDRESSREPLY,
		 0,
		 0,
		 0,
		 true,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true},
		// Test v6->v4 direction with invalid ICMPv6 type (should be
		// dropped)
		{"Invalid ICMPv6 type v6->v4 (drop)",
		 0,
		 0,
		 255,
		 0,
		 false,
		 IPPROTO_UDP,
		 0,
		 0,
		 0,
		 0,
		 true}
	};

	for (size_t i = 0;
	     i < sizeof(icmp_types) / sizeof(struct icmp_type_info_t);
	     ++i) {
		const struct icmp_type_info_t *info = &icmp_types[i];

		// For drop cases, create only one direction (v6->v4)
		if (info->should_drop) {
			struct upkt *pkt = create_icmp_packet(
				info,
				!info->from_ipv4,
				ADDR_OF(&nat64_config->prefixes.prefixes)[0]
					.prefix
			);
			if (!pkt) {
				return -1;
			}

			// Create empty expected packet to indicate drop
			struct upkt pkt_drop = {0};

			char buf[128];
			snprintf(buf, sizeof(buf), "%s", info->name);

			fix_checksums(pkt);
			append_test_case(test_case, *pkt, pkt_drop, buf);

			rte_free(pkt);
			continue;
		}

		// For regular translation cases
		struct upkt *pkt_v4 = create_icmp_packet(
			info,
			false,
			ADDR_OF(&nat64_config->prefixes.prefixes)[0].prefix
		);
		struct upkt *pkt_v6 = create_icmp_packet(
			info,
			true,
			ADDR_OF(&nat64_config->prefixes.prefixes)[0].prefix
		);

		if (!pkt_v4 || !pkt_v6) {
			if (pkt_v4)
				rte_free(pkt_v4);
			if (pkt_v6)
				rte_free(pkt_v6);
			return -1;
		}

		fix_checksums(pkt_v4);
		fix_checksums(pkt_v6);

		char buf_name[128];
		if (info->from_ipv4) {
			snprintf(
				buf_name,
				sizeof(buf_name),
				"%s " IPv4_BYTES_FMT " -> v6",
				info->name,
				IPv4_BYTES_LE(pkt_v4->ip.ipv4.src_addr)
			);
			append_test_case(test_case, *pkt_v4, *pkt_v6, buf_name);
		} else {
			snprintf(
				buf_name,
				sizeof(buf_name),
				"%s " IPv6_BYTES_FMT " -> v4",
				info->name,
				IPv6_BYTES(pkt_v6->ip.ipv6.src_addr)
			);
			append_test_case(test_case, *pkt_v6, *pkt_v4, buf_name);
		}

		rte_free(pkt_v4);
		rte_free(pkt_v6);
	}

	return 0;
}

/**
 * @brief Create basic ICMP test cases from NAT64 address mappings
 *
 * For each configured address mapping:
 * 1. Creates IPv4->IPv6 test case with:
 *    - ICMP Echo Request packet with source from TEST-NET-1 range
 *    - Destination from mapping IPv4 address
 *    - Expected translation to ICMPv6 Echo Request
 * 2. Creates IPv6->IPv4 test case with:
 *    - Reversed source/destination addresses
 *    - Echo Reply messages
 *    - Appropriate checksum updates
 *
 * Used to verify basic ICMP translation functionality:
 * - Echo Request/Reply translation
 * - ID and sequence number preservation
 * - Checksum recalculation
 *
 * @param test_case Pointer to test case list to append to
 * @return 0 on success, -1 on failure
 */
static int
append_test_cases_from_mappings_icmp(struct test_case **test_case) {
	struct nat64_module_config *nat64_config =
		(struct nat64_module_config *)&test_params.module_config;
	for (uint32_t i = 0; i < config_data.count; i++) {
		struct upkt pkt = {
			.eth =
				{
					.dst_addr.addr_bytes =
						"\xff\xff\xfa\xff\xff\xff",
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
						sizeof(struct icmphdr) +
						10 // Data length
					),
					.time_to_live = DEFAULT_TTL,
					.next_proto_id = IPPROTO_ICMP,
					.src_addr = outer_ip4,
					.dst_addr = config_data.mapping[i].ip4,
				},
			.proto.icmp =
				{.type = ICMP_ECHO,
				 .code = 0,
				 .un.echo.id = rte_cpu_to_be_16(1),
				 .un.echo.sequence = rte_cpu_to_be_16(1)},
			.data_len = 10,
			.data = "0123456789"
		};
		struct upkt pkt_expected = {
			.eth =
				{
					.dst_addr.addr_bytes =
						"\xff\xff\xfa\xff\xff\xff",
					.src_addr.addr_bytes =
						"\x02\x00\x00\x00\x00\x00",
					.ether_type = rte_cpu_to_be_16(
						RTE_ETHER_TYPE_IPV6
					),
				},
			.ip.ipv6 =
				{
					.hop_limits = DEFAULT_TTL,
					.proto = IPPROTO_ICMPV6,
					.src_addr = {0},
					.vtc_flow = RTE_BE32(0x60000000),
					.payload_len = RTE_BE16(
						sizeof(struct icmp6_hdr) + 10
					),
				},
			.proto.icmp6 =
				{.icmp6_type = ICMP6_ECHO_REQUEST,
				 .icmp6_code = 0,
				 .icmp6_id = rte_cpu_to_be_16(1),
				 .icmp6_seq = rte_cpu_to_be_16(1)},
			.data_len = 10,
			.data = "0123456789"
		};
		rte_memcpy(
			&pkt_expected.ip.ipv6.dst_addr,
			&config_data.mapping[i].ip6,
			16
		);
		SET_IPV4_MAPPED_IPV6(
			&pkt_expected.ip.ipv6.src_addr,
			ADDR_OF(&nat64_config->prefixes.prefixes)[0].prefix,
			&outer_ip4
		);
		char buf[1024];
		snprintf(
			buf,
			1023,
			"v4 -> v6 " IPv4_BYTES_FMT " -> " IPv6_BYTES_FMT,
			IPv4_BYTES_LE(outer_ip4),
			IPv6_BYTES(pkt_expected.ip.ipv6.dst_addr)
		);
		append_test_case(test_case, pkt, pkt_expected, buf);

		snprintf(
			buf,
			1023,
			"v6 -> v4 " IPv6_BYTES_FMT " -> " IPv4_BYTES_FMT,
			IPv6_BYTES(pkt_expected.ip.ipv6.src_addr),
			IPv4_BYTES_LE(outer_ip4)
		);
		// Swap src and dst ip for the second test case
		uint8_t tmp_ip[16];
		rte_memcpy(tmp_ip, &pkt_expected.ip.ipv6.src_addr, 16);
		rte_memcpy(
			&pkt_expected.ip.ipv6.src_addr,
			&pkt_expected.ip.ipv6.dst_addr,
			16
		);
		rte_memcpy(&pkt_expected.ip.ipv6.dst_addr, tmp_ip, 16);

		// Swap src and dst ip for the second test case for ipv4 pkt
		uint32_t tmp = pkt.ip.ipv4.src_addr;
		pkt.ip.ipv4.src_addr = pkt.ip.ipv4.dst_addr;
		pkt.ip.ipv4.dst_addr = tmp;

		// Change ICMP type for the second test case since it will be a
		// response
		pkt.proto.icmp.type = ICMP_ECHOREPLY;
		pkt_expected.proto.icmp6.icmp6_type = ICMP6_ECHO_REPLY;

		append_test_case(test_case, pkt_expected, pkt, buf);
	}
	return 0;
}

/**
 * @brief Count number of packets in a packet list
 *
 * Traverses the linked list of packets and counts total number.
 * Used for test verification to check expected vs actual packet counts
 * in input, output and drop lists.
 *
 * @param list Pointer to packet list structure to count
 * @return Total number of packets in the list
 */
// static inline int
// packet_list_counter(struct packet_list *list) {
// 	int count = 0;
// 	for (struct packet *pkt = list->first; pkt != NULL; pkt = pkt->next) {
// 		count++;
// 	}
// 	return count;
// }
/**
 * @brief Clean up and free resources for a packet list
 *
 * Frees all DPDK mbufs in the packet list and reinitializes the list.
 * Used during test cleanup and between test cases to ensure clean state.
 * Handles input, output and drop packet lists.
 *
 * @param list Pointer to packet list structure to clean up
 */
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
	packet_list_init(list);
}

/**
 * @brief Test UDP checksum calculation during NAT64 translation
 *
 * Tests UDP checksum handling by:
 * 1. Creating IPv4 UDP packet with payload
 * 2. Creating expected IPv6 UDP packet after translation
 * 3. Setting appropriate addresses and checksums
 * 4. Running packet through NAT64 translation
 * 5. Verifying:
 *    - One output packet produced
 *    - Output packet matches expected packet including checksums
 *    - No packets dropped
 *
 * Ensures proper UDP checksum calculation during v4->v6 translation.
 *
 * @return 0 on success, error code on failure
 */
static inline int
test_nat64_udp_checksum() {
	struct nat64_module_config *nat64_config =
		(struct nat64_module_config *)&test_params.module_config;

	// Create IPv4 UDP packet
	struct upkt pkt = {
		.eth =
			{
				.dst_addr.addr_bytes =
					"\xff\xff\xff\xff\xff\xff",
				.src_addr.addr_bytes =
					"\x02\x00\x00\x00\x00\x00",
				.ether_type = RTE_BE16(RTE_ETHER_TYPE_IPV4),
			},
		.ip.ipv4 =
			{
				.version_ihl = RTE_IPV4_VHL_DEF,
				.total_length = RTE_BE16(
					sizeof(struct rte_ipv4_hdr) +
					sizeof(struct rte_udp_hdr) + 10
				),
				.time_to_live = DEFAULT_TTL,
				.next_proto_id = IPPROTO_UDP,
				.src_addr = outer_ip4,
				.dst_addr = config_data.mapping[0].ip4,
			},
		.proto.udp =
			{
				.src_port = RTE_BE16(12345),
				.dst_port = RTE_BE16(53),
				.dgram_len = RTE_BE16(18
				), // UDP header (8) + data (10)
			},
		.data_len = 10,
		.data = "0123456789"
	};

	// Expected IPv6 UDP packet
	struct upkt pkt_expected = {
		.eth =
			{
				.dst_addr.addr_bytes =
					"\xff\xff\xff\xff\xff\xff",
				.src_addr.addr_bytes =
					"\x02\x00\x00\x00\x00\x00",
				.ether_type = RTE_BE16(RTE_ETHER_TYPE_IPV6),
			},
		.ip.ipv6 =
			{
				.vtc_flow = RTE_BE32(0x60000000),
				.payload_len = RTE_BE16(
					sizeof(struct rte_udp_hdr) + 10
				), // UDP header (8) + data (10)
				.proto = IPPROTO_UDP,
				.hop_limits = DEFAULT_TTL,
			},
		.proto.udp =
			{
				.src_port = RTE_BE16(12345),
				.dst_port = RTE_BE16(53),
				.dgram_len = RTE_BE16(
					sizeof(struct rte_udp_hdr) + 10
				),
			},
		.data_len = 10,
		.data = "0123456789"
	};

	// Set IPv6 addresses
	rte_memcpy(
		&pkt_expected.ip.ipv6.dst_addr, config_data.mapping[0].ip6, 16
	);
	SET_IPV4_MAPPED_IPV6(
		&pkt_expected.ip.ipv6.src_addr,
		ADDR_OF(&nat64_config->prefixes.prefixes)[0].prefix,
		&outer_ip4
	);

	// Calculate checksums
	fix_checksums(&pkt);
	fix_checksums(&pkt_expected);

	// Push packet and run NAT64
	packet_list_cleanup(&test_params.packet_front.input);
	packet_list_cleanup(&test_params.packet_front.output);
	packet_list_cleanup(&test_params.packet_front.drop);

	TEST_ASSERT_EQUAL(push_packet(&pkt), 0, "Failed to push packet\n");

	test_params.module->handler(
		NULL,
		0,
		&test_params.module_config.cp_module,
		NULL,
		&test_params.packet_front
	);

	// Verify output
	int count = packet_list_counter(&test_params.packet_front.output);
	TEST_ASSERT_EQUAL(
		count, 1, "Expected 1 packet output, got %d\n", count
	);

	struct packet *packet =
		packet_list_pop(&test_params.packet_front.output);
	TEST_ASSERT_NOT_NULL(packet, "Output packet is NULL\n");

	// Parse and verify packet
	TEST_ASSERT_EQUAL(parse_packet(packet), 0, "Failed to parse packet\n");
	LOG_DBG(NAT64_TEST, "Expected packet:\n");
	print_upkt(&pkt_expected);
	LOG_DBG(NAT64_TEST, "Actual packet:\n");
	print_rte_mbuf(packet_to_mbuf(packet));

	// Print both packets for comparison
	RTE_LOG(INFO, NAT64_TEST, "Expected packet:\n");
	print_upkt(&pkt_expected);
	RTE_LOG(INFO, NAT64_TEST, "Actual packet:\n");
	print_rte_mbuf(packet_to_mbuf(packet));

	// Compare packets
	int res = print_diff_upkt_and_rte_mbuf(
		&pkt_expected, packet_to_mbuf(packet)
	);
	if (res != 0) {
		RTE_LOG(ERR,
			NAT64_TEST,
			"Packets differ. See above for details.\n");
		TEST_ASSERT_EQUAL(res, 0, "Packet verification failed.\n");
	}

	return 0;
}

/**
 * @brief Execute and verify a single NAT64 translation test case
 *
 * For each test case:
 * 1. Cleans up packet lists from previous tests
 * 2. Calculates checksums for input and expected packets
 * 3. Pushes input packet to test front
 * 4. Runs NAT64 translation
 * 5. Verifies:
 *    - For drop cases: packet appears in drop list
 *    - For translation cases:
 *      * One packet in output list
 *      * No packets in drop list
 *      * Output packet matches expected packet
 *
 * @param tc Pointer to test case structure to process
 * @return TEST_SUCCESS on success, error code on failure
 */
static int
process_test_case(struct test_case *tc) {
	packet_list_cleanup(&test_params.packet_front.input);
	packet_list_cleanup(&test_params.packet_front.output);
	packet_list_cleanup(&test_params.packet_front.drop);

	fix_checksums(&tc->pkt);
	fix_checksums(&tc->pkt_expected);

	TEST_ASSERT_EQUAL(
		push_packet(&tc->pkt),
		0,
		"%s: Failed to push packet \n",
		tc->name
	);

	test_params.module->handler(
		NULL,
		0,
		&test_params.module_config.cp_module,
		NULL,
		&test_params.packet_front
	);

	if (tc->pkt_expected.eth.dst_addr.addr_bytes[0] == 0) {
		int count = packet_list_counter(&test_params.packet_front.drop);
		TEST_ASSERT_EQUAL(
			count, 1, "Expected 1 packet droped, got %d\n", count
		);
		count = packet_list_counter(&test_params.packet_front.output);
		TEST_ASSERT_EQUAL(
			count, 0, "Expected 0 packet output, got %d\n", count
		);
		return TEST_SUCCESS;
	}

	int count = packet_list_counter(&test_params.packet_front.output);
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
	while ((pkt_out = packet_list_pop(&test_params.packet_front.output)) !=
	       NULL) {
		// Parse again
		struct packet *packet = mbuf_to_packet(packet_to_mbuf(pkt_out));
		parse_packet(packet);

		int res = print_diff_upkt_and_rte_mbuf(
			&tc->pkt_expected, packet_to_mbuf(packet)
		);
		RTE_LOG(DEBUG, NAT64_TEST, "%s: res = %d\n", tc->name, res);
		// FIXME:
		TEST_ASSERT_EQUAL(
			res,
			0,
			"%s: Expected and actual packet difference. "
			"See log for details.\n",
			tc->name
		);
	}
	return TEST_SUCCESS;
}

/**
 * @brief Run NAT64 tests using provided test case provider function
 *
 * Generic test runner that:
 * 1. Gets test cases from provided test case provider function
 * 2. Iterates through all test cases
 * 3. Processes each test case through process_test_case()
 * 4. Accumulates test results
 *
 * Used by specific test functions (UDP, TCP, ICMP) to run their test cases
 * through a common execution path.
 *
 * @param tc_provider Function pointer to test case provider
 * @return 0 on success, accumulated error count on failures
 */
static int
test_nat64_generic(int (*tc_provider)(struct test_case **)) {
	struct test_case *test_cases = NULL;
	int result = 0;

	TEST_ASSERT_EQUAL(
		tc_provider(&test_cases), 0, "Failed to get test cases\n"
	);

	struct test_case *tc;
	for (tc = test_cases; tc != NULL; tc = tc->next) {
		LOG_DBG(NAT64_TEST, "Processing test case %s\n", tc->name);
		result += process_test_case(tc);
	}

	return result;
}

/**
 * @brief Create test cases for unknown prefix and mapping handling
 *
 * Creates test cases to verify:
 * 1. Packets with unknown IPv6 prefix are dropped when drop_unknown_prefix=true
 * 2. Packets with unknown IPv4/IPv6 mappings are dropped when
 * drop_unknown_mapping=true
 * 3. Packets are forwarded when corresponding drop flags are false
 *
 * @param test_case Pointer to test case list to append to
 * @return 0 on success, -1 on failure
 */
static int
append_test_cases_unknown_handling(struct test_case **test_case) {
	// Test case 1: IPv6 packet with unknown source prefix
	struct upkt pkt_unknown_prefix = {
		.eth =
			{
				.dst_addr.addr_bytes =
					"\xff\xff\xff\xff\xff\xff",
				.src_addr.addr_bytes =
					"\x02\x00\x00\x00\x00\x00",
				.ether_type = RTE_BE16(RTE_ETHER_TYPE_IPV6),
			},
		.ip.ipv6 =
			{.vtc_flow = RTE_BE32(0x60000000),
			 .payload_len =
				 RTE_BE16(sizeof(struct rte_udp_hdr) + 10),
			 .proto = IPPROTO_UDP,
			 .hop_limits = DEFAULT_TTL,
			 // Unknown prefix 2001:db9::/96 (different from
			 // configured 2001:db8::/96)
			 .src_addr =
				 {0x20,
				  0x01,
				  0x0d,
				  0xb9,
				  0,
				  0,
				  0,
				  0,
				  0,
				  0,
				  0,
				  0,
				  0,
				  0,
				  0,
				  1},
			 .dst_addr =
				 {0x20,
				  0x01,
				  0x0d,
				  0xb9,
				  0,
				  0,
				  0,
				  0,
				  0,
				  0,
				  0,
				  0,
				  192,
				  0,
				  2,
				  1}},
		.proto.udp =
			{.src_port = RTE_BE16(12345),
			 .dst_port = RTE_BE16(53),
			 .dgram_len = RTE_BE16(sizeof(struct rte_udp_hdr) + 10)
			},
		.data_len = 10,
		.data = "0123456789"
	};

	// Empty packet means expected drop
	struct upkt pkt_drop = {.eth.dst_addr.addr_bytes = {0}};

	// Test case 2: IPv4 packet with unknown destination mapping
	struct upkt pkt_unknown_mapping = {
		.eth =
			{
				.dst_addr.addr_bytes =
					"\xff\xff\xff\xff\xff\xff",
				.src_addr.addr_bytes =
					"\x02\x00\x00\x00\x00\x00",
				.ether_type = RTE_BE16(RTE_ETHER_TYPE_IPV4),
			},
		.ip.ipv4 =
			{.version_ihl = RTE_IPV4_VHL_DEF,
			 .total_length = RTE_BE16(
				 sizeof(struct rte_ipv4_hdr) +
				 sizeof(struct rte_udp_hdr) + 10
			 ),
			 .time_to_live = DEFAULT_TTL,
			 .next_proto_id = IPPROTO_UDP,
			 .src_addr = RTE_BE32(RTE_IPV4(192, 0, 2, 1)),
			 // Unknown IPv4 address not in mappings
			 .dst_addr = RTE_BE32(RTE_IPV4(198, 51, 100, 99))},
		.proto.udp =
			{.src_port = RTE_BE16(12345),
			 .dst_port = RTE_BE16(53),
			 .dgram_len = RTE_BE16(sizeof(struct rte_udp_hdr) + 10)
			},
		.data_len = 10,
		.data = "0123456789"
	};

	struct nat64_module_config *cfg = &test_params.module_config;
	struct upkt pkt_expected_v6, pkt_expected_v4;
	const char *msg_v6, *msg_v4;
	if (cfg && (cfg->prefixes.drop_unknown_prefix ||
		    cfg->mappings.drop_unknown_mapping)) {
		pkt_expected_v6 = pkt_drop;
		msg_v6 = "IPv6 unknown prefix: should be dropped";
	} else {
		pkt_expected_v6 = pkt_unknown_prefix;
		msg_v6 = "IPv6 unknown prefix: should be passed";
	}
	if (cfg && cfg->mappings.drop_unknown_mapping) {
		pkt_expected_v4 = pkt_drop;
		msg_v4 = "IPv4 unknown mapping: should be dropped";
	} else {
		pkt_expected_v4 = pkt_unknown_mapping;
		msg_v4 = "IPv4 unknown mapping: should be passed";
	}

	append_test_case(
		test_case, pkt_unknown_prefix, pkt_expected_v6, (char *)msg_v6
	);
	append_test_case(
		test_case, pkt_unknown_mapping, pkt_expected_v4, (char *)msg_v4
	);

	return 0;
}

/**
 * @brief Test combined unknown prefix and mapping handling
 *
 * Verifies NAT64 behavior when both drop_unknown_prefix and
 * drop_unknown_mapping are true. Tests that packets with:
 * - Unknown IPv6 prefixes (not in prefix table)
 * - Unknown IPv4-IPv6 mappings (not in mapping table)
 * are properly dropped according to configuration.
 * Uses default test prefix (2001:db8::/96) and mappings.
 *
 * @return 0 if all tests pass, number of failures otherwise
 */
static int
test_nat64_unknown_handling_prefix_mapping(void) {
	// Save original configuration
	bool original_drop_unknown_prefix =
		test_params.module_config.prefixes.drop_unknown_prefix;
	bool original_drop_unknown_mapping =
		test_params.module_config.mappings.drop_unknown_mapping;

	// Set flags for this test
	nat64_module_config_set_drop_unknown(
		&test_params.module_config.cp_module, true, true
	);

	// Run the test
	int result = test_nat64_generic(append_test_cases_unknown_handling);

	// Restore original configuration
	nat64_module_config_set_drop_unknown(
		&test_params.module_config.cp_module,
		original_drop_unknown_prefix,
		original_drop_unknown_mapping
	);

	return result;
}

/**
 * @brief Test unknown prefix handling only
 *
 * Verifies NAT64 behavior when only drop_unknown_prefix is true.
 * Tests that:
 * - IPv6 packets with unknown prefixes are dropped
 * - IPv4 packets are processed normally (mapping check skipped)
 * - Known prefixes are passed through
 * Checks proper interaction between prefix LPM and drop flag.
 *
 * @return 0 if all tests pass, number of failures otherwise
 */
static int
test_nat64_unknown_handling_prefix_only(void) {
	// Save original configuration
	bool original_drop_unknown_prefix =
		test_params.module_config.prefixes.drop_unknown_prefix;
	bool original_drop_unknown_mapping =
		test_params.module_config.mappings.drop_unknown_mapping;

	// Set flags for this test
	nat64_module_config_set_drop_unknown(
		&test_params.module_config.cp_module, true, false
	);

	// Run the test
	int result = test_nat64_generic(append_test_cases_unknown_handling);

	// Restore original configuration
	nat64_module_config_set_drop_unknown(
		&test_params.module_config.cp_module,
		original_drop_unknown_prefix,
		original_drop_unknown_mapping
	);

	return result;
}

/**
 * @brief Test unknown mapping handling only
 *
 * Verifies NAT64 behavior when only drop_unknown_mapping is true.
 * Tests that:
 * - IPv6 packets with unknown mappings are dropped
 * - IPv4 packets with unknown mappings are dropped
 * - Packets with known mappings are passed through
 * Validates mapping table lookup and flag processing.
 *
 * @return 0 if all tests pass, number of failures otherwise
 */
static int
test_nat64_unknown_handling_mapping_only(void) {
	// Save original configuration
	bool original_drop_unknown_prefix =
		test_params.module_config.prefixes.drop_unknown_prefix;
	bool original_drop_unknown_mapping =
		test_params.module_config.mappings.drop_unknown_mapping;

	// Set flags for this test
	nat64_module_config_set_drop_unknown(
		&test_params.module_config.cp_module, false, true
	);

	// Run the test
	int result = test_nat64_generic(append_test_cases_unknown_handling);

	// Restore original configuration
	nat64_module_config_set_drop_unknown(
		&test_params.module_config.cp_module,
		original_drop_unknown_prefix,
		original_drop_unknown_mapping
	);

	return result;
}

/**
 * @brief Test passthrough behavior
 *
 * Verifies NAT64 behavior when both drop_unknown_prefix and
 * drop_unknown_mapping are false. Tests that:
 * - All IPv6 packets are passed through (prefix/mapping ignored)
 * - All IPv4 packets are passed through (mapping ignored)
 * Validates default permissive mode operation.
 *
 * @return 0 if all tests pass, number of failures otherwise
 */
static int
test_nat64_unknown_handling_none(void) {
	// Save original configuration
	bool original_drop_unknown_prefix =
		test_params.module_config.prefixes.drop_unknown_prefix;
	bool original_drop_unknown_mapping =
		test_params.module_config.mappings.drop_unknown_mapping;

	// Set flags for this test
	nat64_module_config_set_drop_unknown(
		&test_params.module_config.cp_module, false, false
	);

	// Run the test
	int result = test_nat64_generic(append_test_cases_unknown_handling);

	// Restore original configuration
	nat64_module_config_set_drop_unknown(
		&test_params.module_config.cp_module,
		original_drop_unknown_prefix,
		original_drop_unknown_mapping
	);

	return result;
}

/**
 * @brief Test UDP packet translation
 *
 * Verifies:
 * - IPv4->IPv6 and IPv6->IPv4 translation
 * - Port mapping
 * - Payload handling
 * - Checksum calculation
 *
 * @return 0 on success, error count on failure
 */
static inline int
test_nat64_udp() {
	return test_nat64_generic(append_test_cases_from_mappings);
}

/**
 * @brief Test basic ICMP packet translation through NAT64
 *
 * Tests basic ICMP translation scenarios including:
 * - Echo Request/Reply translation between ICMPv4 and ICMPv6
 * - ID and sequence number preservation
 * - Checksum recalculation
 * - Proper address mapping in ICMP headers
 *
 * Focuses on common ICMP types used for ping/echo functionality.
 *
 * @return 0 on success, error count on failures
 */
static inline int
test_nat64_icmp() {
	return test_nat64_generic(append_test_cases_from_mappings_icmp);
}

/**
 * @brief Create TCP test cases from NAT64 address mappings
 *
 * For each configured address mapping:
 * 1. Creates IPv4->IPv6 test case with:
 *    - TCP SYN packet with source from TEST-NET-1 range
 *    - Destination from mapping IPv4 address
 *    - Expected translation to IPv6 with correct prefix
 *    - Proper TCP flags, sequence numbers, window size
 * 2. Creates IPv6->IPv4 test case with:
 *    - Reversed source/destination addresses
 *    - Preserved TCP header fields
 *    - Appropriate checksum updates
 *
 * Used to verify TCP-specific aspects of NAT64 translation:
 * - Sequence/ACK number preservation
 * - TCP flags handling
 * - Window size preservation
 * - Checksum recalculation
 *
 * @param test_case Pointer to test case list to append to
 * @return 0 on success, -1 on failure
 */
static inline int
append_test_cases_from_mappings_tcp(struct test_case **test_case) {
	struct nat64_module_config *nat64_config = &test_params.module_config;
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
						sizeof(struct rte_tcp_hdr) +
						10 // Data length
					),
					.time_to_live = DEFAULT_TTL,
					.next_proto_id = IPPROTO_TCP,
					.src_addr = outer_ip4,
					.dst_addr = config_data.mapping[i].ip4,
				},
			.proto.tcp =
				{.src_port = rte_cpu_to_be_16(12345),
				 .dst_port = rte_cpu_to_be_16(80),
				 .sent_seq = rte_cpu_to_be_32(1),
				 .recv_ack = 0,
				 .data_off = 0x50, // 5 words * 4 bytes = 20
						   // bytes TCP header
				 .tcp_flags = RTE_TCP_SYN_FLAG,
				 .rx_win = rte_cpu_to_be_16(8192),
				 .tcp_urp = 0},
			.data_len = 10,
			.data = "0123456789"
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
					.vtc_flow = RTE_BE32(0x60000000),
					.payload_len = RTE_BE16(
						sizeof(struct rte_tcp_hdr) + 10
					),
					.proto = IPPROTO_TCP,
					.hop_limits = DEFAULT_TTL,
				},
			.proto.tcp =
				{.src_port = rte_cpu_to_be_16(12345),
				 .dst_port = rte_cpu_to_be_16(80),
				 .sent_seq = rte_cpu_to_be_32(1),
				 .recv_ack = 0,
				 .data_off = 0x50,
				 .tcp_flags = RTE_TCP_SYN_FLAG,
				 .rx_win = rte_cpu_to_be_16(8192),
				 .tcp_urp = 0},
			.data_len = 10,
			.data = "0123456789"
		};

		rte_memcpy(
			&pkt_expected.ip.ipv6.dst_addr,
			&config_data.mapping[i].ip6,
			16
		);
		SET_IPV4_MAPPED_IPV6(
			&pkt_expected.ip.ipv6.src_addr,
			ADDR_OF(&nat64_config->prefixes.prefixes)[0].prefix,
			&outer_ip4
		);

		char buf[1024];
		snprintf(
			buf,
			1023,
			"v4 -> v6 " IPv4_BYTES_FMT " -> " IPv6_BYTES_FMT,
			IPv4_BYTES_LE(outer_ip4),
			IPv6_BYTES(pkt_expected.ip.ipv6.dst_addr)
		);
		append_test_case(test_case, pkt, pkt_expected, buf);

		snprintf(
			buf,
			1023,
			"v6 -> v4 " IPv6_BYTES_FMT " -> " IPv4_BYTES_FMT,
			IPv6_BYTES(pkt_expected.ip.ipv6.src_addr),
			IPv4_BYTES_LE(outer_ip4)
		);

		// Swap src and dst ip for the second test case
		uint8_t tmp_ip[16];
		rte_memcpy(tmp_ip, &pkt_expected.ip.ipv6.src_addr, 16);
		rte_memcpy(
			&pkt_expected.ip.ipv6.src_addr,
			&pkt_expected.ip.ipv6.dst_addr,
			16
		);
		rte_memcpy(&pkt_expected.ip.ipv6.dst_addr, tmp_ip, 16);

		// Swap src and dst ip for the second test case for ipv4 pkt
		uint32_t tmp = pkt.ip.ipv4.src_addr;
		pkt.ip.ipv4.src_addr = pkt.ip.ipv4.dst_addr;
		pkt.ip.ipv4.dst_addr = tmp;

		append_test_case(test_case, pkt_expected, pkt, buf);
	}
	return 0;
}

/**
 * @brief Test TCP packet translation through NAT64
 *
 * Tests TCP packet translation by:
 * 1. Creating test cases with TCP packets in both directions (v4->v6 and
 * v6->v4)
 * 2. Setting appropriate TCP flags, sequence numbers, ports
 * 3. Running packets through NAT64 translation
 * 4. Verifying header translations and checksum calculations
 *
 * Covers TCP-specific aspects like:
 * - Port translation
 * - Sequence/ACK number preservation
 * - TCP flags handling
 * - Checksum recalculation
 *
 * @return 0 on success, error count on failures
 */
static inline int
test_nat64_tcp() {
	return test_nat64_generic(append_test_cases_from_mappings_tcp);
}

/**
 * @brief Test handling of unknown prefixes and mappings
 *
 * Tests NAT64 behavior with:
 * - Unknown IPv6 prefixes
 * - Unknown address mappings
 * - Proper packet dropping based on configuration
 *
 * @return 0 on success, error count on failures
 */
static inline int
test_nat64_unknown_handling() {
	return test_nat64_generic(append_test_cases_unknown_handling);
}

/**
 * @brief Test ICMP packet translation
 *
 * Verifies:
 * - Error message translation
 * - MTU and pointer handling
 * - Message filtering
 * - Embedded packet handling
 *
 * @see RFC 7915 sections 4.2, 4.3
 * @return 0 on success, error count on failure
 */
static inline int
test_nat64_icmp_more() {
	return test_nat64_generic(append_test_cases_from_mappings_icmp_more);
}

/**
 * @brief Test default configuration values for NAT64 module
 *
 * Verifies that the NAT64 module is initialized with correct default values:
 * - MTU values (IPv4: 1450, IPv6: 1280)
 * - Empty mappings list
 * - Empty prefixes list
 * - Memory context initialization
 *
 * @return TEST_SUCCESS on success, error code on failure
 */
static inline int
test_default_values(void) {
	struct nat64_module_config module_config;
	// Initialize module configuration using nat64_module_config_init_config
	if (nat64_module_config_data_init(
		    &module_config, test_params.memory_context
	    )) {
		RTE_LOG(ERR, NAT64_TEST, "Failed to initialize module config\n"
		);
		return -ENOMEM;
	}

	struct nat64_module_config *config = &module_config;

	// Verify MTU defaults
	TEST_ASSERT_EQUAL(
		config->mtu.ipv4, 1450, "Incorrect IPv4 MTU default\n"
	);
	TEST_ASSERT_EQUAL(
		config->mtu.ipv6, 1280, "Incorrect IPv6 MTU default\n"
	);

	// Verify empty mappings
	TEST_ASSERT_EQUAL(
		config->mappings.count, 0, "Mappings count should be 0\n"
	);
	TEST_ASSERT_NULL(
		config->mappings.list, "Mappings list should be NULL\n"
	);

	// Verify empty prefixes
	TEST_ASSERT_EQUAL(
		config->prefixes.count, 0, "Prefixes count should be 0\n"
	);
	TEST_ASSERT_NULL(
		config->prefixes.prefixes, "Prefixes list should be NULL\n"
	);

	// Verify default drop flags
	TEST_ASSERT_EQUAL(
		config->mappings.drop_unknown_mapping,
		false,
		"drop_unknown_mapping default should be false\n"
	);
	TEST_ASSERT_EQUAL(
		config->prefixes.drop_unknown_prefix,
		false,
		"drop_unknown_prefix default should be false\n"
	);

	nat64_module_config_data_destroy(config, test_params.memory_context);

	return TEST_SUCCESS;
}

/**
 * @brief Clean up test suite resources
 *
 * Performs cleanup after test suite execution:
 * - Frees all packets in input list
 * - Frees all packets in output list
 * - Frees all packets in drop list
 *
 * Ensures clean state between test runs and prevents memory leaks.
 */
static void
testsuite_teardown(void) {
	nat64_module_config_data_destroy(
		&test_params.module_config, test_params.memory_context
	);
	packet_list_cleanup(&test_params.packet_front.input);
	packet_list_cleanup(&test_params.packet_front.output);
	packet_list_cleanup(&test_params.packet_front.drop);
}

/**
 * @brief NAT64 test suite definition
 *
 * Tests:
 * - Module setup and configuration
 * - Protocol translation (UDP, TCP, ICMP)
 * - Error handling and packet drops
 * - Checksum calculations
 *
 * @see RFC 7915 for translation requirements
 */
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
			 "test_nat64_default_values", test_default_values
		 ),
		 TEST_CASE_NAMED(
			 "test_nat64_unknown_handling_prefix_mapping",
			 test_nat64_unknown_handling_prefix_mapping
		 ),
		 TEST_CASE_NAMED(
			 "test_nat64_unknown_handling_prefix_only",
			 test_nat64_unknown_handling_prefix_only
		 ),
		 TEST_CASE_NAMED(
			 "test_nat64_unknown_handling_mapping_only",
			 test_nat64_unknown_handling_mapping_only
		 ),
		 TEST_CASE_NAMED(
			 "test_nat64_unknown_handling_none",
			 test_nat64_unknown_handling_none
		 ),
		 TEST_CASE_NAMED("test_nat64_udp", test_nat64_udp),
		 TEST_CASE_NAMED("test_nat64_tcp", test_nat64_tcp),
		 TEST_CASE_NAMED("test_nat64_icmp", test_nat64_icmp),
		 TEST_CASE_NAMED("test_nat64_icmp_more", test_nat64_icmp_more),
		 TEST_CASE_NAMED(
			 "test_nat64_udp_checksum", test_nat64_udp_checksum
		 ),
		 TEST_CASE_NAMED(
			 "test_nat64_unknown_handling",
			 test_nat64_unknown_handling
		 ),

		 TEST_CASES_END() /**< NULL terminate unit test array */
	 }};

/**
 * @brief Execute NAT64 test suite
 *
 * Runs all tests to verify:
 * - Basic module functionality
 * - Protocol translation
 * - Error handling
 *
 * @return 0 on success, error count on failures
 */
static int
nat64_testsuite(void) {
	return unit_test_suite_runner(&nat64_test_suite);
}

REGISTER_FAST_TEST(nat64_autotest, false, true, nat64_testsuite);
