/*
 * FWState Sync Frame Endianness Test
 *
 * Verifies that fwstate_craft_state_sync_packet() correctly converts
 * transport layer ports from big-endian byte order to little-endian
 * in the sync frame for both TCP and UDP.
 *
 * This is a regression test for a bug where UDP ports were copied
 * directly from the packet header without rte_be_to_cpu_16() conversion,
 * while TCP ports were correctly converted.
 */

#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <rte_byteorder.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include "lib/dataplane/packet/packet.h"
#include "lib/fwstate/sync.h"
#include "lib/fwstate/types.h"

#include "mock/worker_mempool.h"

// Test ports chosen so that byte-swap is visible:
// 12345 = 0x3039, byte-swapped = 0x3930 = 14640
// 80    = 0x0050, byte-swapped = 0x5000 = 20480
#define TEST_SRC_PORT 12345
#define TEST_DST_PORT 80

// Dummy sync config (only used for packet construction, not for port logic)
static struct fwstate_sync_config test_sync_config;
static struct rte_mempool *test_pool;

static void
init_sync_config(void) {
	memset(&test_sync_config, 0, sizeof(test_sync_config));
	// Multicast IPv6 address: ff02::1
	test_sync_config.dst_addr_multicast[0] = 0xff;
	test_sync_config.dst_addr_multicast[1] = 0x02;
	test_sync_config.dst_addr_multicast[15] = 0x01;
	// Port in big-endian as expected by the sync packet builder
	test_sync_config.port_multicast = rte_cpu_to_be_16(9999);
}

/*
 * Build a minimal IPv6 + transport packet in an mbuf.
 * Returns a packet struct with headers set up.
 */
static int
build_test_packet(
	struct packet *pkt,
	uint8_t proto,
	uint16_t src_port_host,
	uint16_t dst_port_host
) {
	struct rte_mbuf *mbuf = rte_pktmbuf_alloc(test_pool);
	assert(mbuf != NULL);
	pkt->mbuf = mbuf;

	/* Layout: Ethernet + IPv6 + Transport */
	const uint16_t eth_offset = 0;
	const uint16_t ipv6_offset = sizeof(struct rte_ether_hdr);
	uint16_t transport_offset = ipv6_offset + sizeof(struct rte_ipv6_hdr);
	uint16_t transport_size = 0;

	if (proto == IPPROTO_TCP) {
		transport_size = sizeof(struct rte_tcp_hdr);
	} else if (proto == IPPROTO_UDP) {
		transport_size = sizeof(struct rte_udp_hdr);
	} else {
		return -1;
	}

	uint16_t total_size = transport_offset + transport_size +
			      4; /* +4 for "test" payload */
	char *data = rte_pktmbuf_append(mbuf, total_size);
	if (data == NULL) {
		return -1;
	}
	memset(data, 0, total_size);
	memcpy(data + transport_offset + transport_size, "test", 4);

	/* Ethernet header */
	struct rte_ether_hdr *eth = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ether_hdr *, eth_offset
	);
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	/* IPv6 header */
	struct rte_ipv6_hdr *ipv6 = rte_pktmbuf_mtod_offset(
		mbuf, struct rte_ipv6_hdr *, ipv6_offset
	);
	ipv6->vtc_flow = rte_cpu_to_be_32(0x6 << 28);
	ipv6->payload_len = rte_cpu_to_be_16(transport_size + 4);
	ipv6->proto = proto;
	ipv6->hop_limits = 64;
	/* src: 2001:db8::1 */
	ipv6->src_addr[0] = 0x20;
	ipv6->src_addr[1] = 0x01;
	ipv6->src_addr[2] = 0x0d;
	ipv6->src_addr[3] = 0xb8;
	ipv6->src_addr[15] = 0x01;
	/* dst: 2001:db8::2 */
	ipv6->dst_addr[0] = 0x20;
	ipv6->dst_addr[1] = 0x01;
	ipv6->dst_addr[2] = 0x0d;
	ipv6->dst_addr[3] = 0xb8;
	ipv6->dst_addr[15] = 0x02;

	if (proto == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_tcp_hdr *, transport_offset
		);
		tcp->src_port = rte_cpu_to_be_16(src_port_host);
		tcp->dst_port = rte_cpu_to_be_16(dst_port_host);
		tcp->tcp_flags = 0x02; /* SYN */
		// 0b0101_0000: The first nibble (5) indicates the TCP header
		// length in 32-bit words (5 * 4 bytes = 20 bytes).
		tcp->data_off = 0x50;
	} else {
		struct rte_udp_hdr *udp = rte_pktmbuf_mtod_offset(
			mbuf, struct rte_udp_hdr *, transport_offset
		);
		udp->src_port = rte_cpu_to_be_16(src_port_host);
		udp->dst_port = rte_cpu_to_be_16(dst_port_host);
		udp->dgram_len = rte_cpu_to_be_16(transport_size + 4);
	}

	/* Set packet metadata */
	pkt->network_header.type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);
	pkt->network_header.offset = ipv6_offset;
	pkt->transport_header.type = proto;
	pkt->transport_header.offset = transport_offset;

	return 0;
}

/*
 * Extract the sync frame from a crafted sync packet.
 * Returns pointer to the sync frame within the mbuf.
 */
static struct fw_state_sync_frame *
extract_sync_frame(struct rte_mbuf *mbuf) {
	uint16_t payload_offset =
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_vlan_hdr) +
		sizeof(struct rte_ipv6_hdr) + sizeof(struct rte_udp_hdr);
	return rte_pktmbuf_mtod_offset(
		mbuf, struct fw_state_sync_frame *, payload_offset
	);
}

/*
 * Test that TCP ports in sync frames are in host byte order.
 * This should always pass (TCP conversion is correct).
 */
static void
test_tcp_sync_frame_ports(void) {
	printf("\n--- TCP Sync Frame Port Endianness ---\n");

	/* Create source packet */
	struct packet src_pkt = {};
	int rc = build_test_packet(
		&src_pkt, IPPROTO_TCP, TEST_SRC_PORT, TEST_DST_PORT
	);
	assert(rc == 0);

	/* Create sync packet */
	struct rte_mbuf *sync_mbuf = rte_pktmbuf_alloc(test_pool);
	assert(sync_mbuf != NULL);
	struct packet sync_pkt = {.mbuf = sync_mbuf};

	rc = fwstate_craft_state_sync_packet(
		&test_sync_config, &src_pkt, SYNC_INGRESS, &sync_pkt
	);
	assert(rc == 0);

	/* Extract and verify sync frame ports */
	struct fw_state_sync_frame *frame = extract_sync_frame(sync_mbuf);

	printf("  TCP INGRESS: src_port=%u (expected %u), dst_port=%u "
	       "(expected %u)\n",
	       frame->src_port,
	       TEST_SRC_PORT,
	       frame->dst_port,
	       TEST_DST_PORT);

	assert(frame->src_port == TEST_SRC_PORT &&
	       "TCP INGRESS src_port should be in host byte order");
	assert(frame->dst_port == TEST_DST_PORT &&
	       "TCP INGRESS dst_port should be in host byte order");
	assert(frame->proto == IPPROTO_TCP);

	rte_pktmbuf_free(src_pkt.mbuf);
	rte_pktmbuf_free(sync_mbuf);

	/* Test EGRESS direction (ports should be swapped) */
	rc = build_test_packet(
		&src_pkt, IPPROTO_TCP, TEST_SRC_PORT, TEST_DST_PORT
	);
	assert(rc == 0);

	sync_mbuf = rte_pktmbuf_alloc(test_pool);
	assert(sync_mbuf != NULL);
	sync_pkt.mbuf = sync_mbuf;

	rc = fwstate_craft_state_sync_packet(
		&test_sync_config, &src_pkt, SYNC_EGRESS, &sync_pkt
	);
	assert(rc == 0);

	frame = extract_sync_frame(sync_mbuf);

	printf("  TCP EGRESS:  src_port=%u (expected %u), dst_port=%u "
	       "(expected %u)\n",
	       frame->src_port,
	       TEST_DST_PORT,
	       frame->dst_port,
	       TEST_SRC_PORT);

	/* EGRESS swaps src/dst to match initial 5-tuple */
	assert(frame->src_port == TEST_DST_PORT &&
	       "TCP EGRESS src_port should be swapped dst_port in host byte "
	       "order");
	assert(frame->dst_port == TEST_SRC_PORT &&
	       "TCP EGRESS dst_port should be swapped src_port in host byte "
	       "order");

	rte_pktmbuf_free(src_pkt.mbuf);
	rte_pktmbuf_free(sync_mbuf);

	printf("  TCP sync frame port endianness: PASSED\n");
}

/*
 * Test that UDP ports in sync frames are in host byte order.
 * This test will FAIL if the endianness bug is present.
 */
static void
test_udp_sync_frame_ports(void) {
	printf("\n--- UDP Sync Frame Port Endianness ---\n");

	/* Create source packet */
	struct packet src_pkt = {};
	int rc = build_test_packet(
		&src_pkt, IPPROTO_UDP, TEST_SRC_PORT, TEST_DST_PORT
	);
	assert(rc == 0);

	/* Create sync packet */
	struct rte_mbuf *sync_mbuf = rte_pktmbuf_alloc(test_pool);
	assert(sync_mbuf != NULL);
	struct packet sync_pkt = {.mbuf = sync_mbuf};

	rc = fwstate_craft_state_sync_packet(
		&test_sync_config, &src_pkt, SYNC_INGRESS, &sync_pkt
	);
	assert(rc == 0);

	/* Extract and verify sync frame ports */
	struct fw_state_sync_frame *frame = extract_sync_frame(sync_mbuf);

	uint16_t be_src_port = rte_cpu_to_be_16(TEST_SRC_PORT);
	uint16_t be_dst_port = rte_cpu_to_be_16(TEST_DST_PORT);

	printf("  UDP INGRESS: src_port=%u (expected %u, BE would be %u), "
	       "dst_port=%u (expected %u, BE would be %u)\n",
	       frame->src_port,
	       TEST_SRC_PORT,
	       be_src_port,
	       frame->dst_port,
	       TEST_DST_PORT,
	       be_dst_port);

	if (frame->src_port == be_src_port || frame->dst_port == be_dst_port) {
		printf("  *** BUG DETECTED: UDP ports are in network byte "
		       "order "
		       "(big-endian) instead of host byte order! ***\n");
		printf("  *** This means fwstate_fill_sync_frame() is missing "
		       "rte_be_to_cpu_16() for UDP ports ***\n");
	}

	assert(frame->src_port == TEST_SRC_PORT &&
	       "UDP INGRESS src_port should be in host byte order "
	       "(BUG: missing rte_be_to_cpu_16 in sync.c UDP case)");
	assert(frame->dst_port == TEST_DST_PORT &&
	       "UDP INGRESS dst_port should be in host byte order "
	       "(BUG: missing rte_be_to_cpu_16 in sync.c UDP case)");
	assert(frame->proto == IPPROTO_UDP);

	rte_pktmbuf_free(src_pkt.mbuf);
	rte_pktmbuf_free(sync_mbuf);

	/* Test EGRESS direction (ports should be swapped) */
	rc = build_test_packet(
		&src_pkt, IPPROTO_UDP, TEST_SRC_PORT, TEST_DST_PORT
	);
	assert(rc == 0);

	sync_mbuf = rte_pktmbuf_alloc(test_pool);
	assert(sync_mbuf != NULL);
	sync_pkt.mbuf = sync_mbuf;

	rc = fwstate_craft_state_sync_packet(
		&test_sync_config, &src_pkt, SYNC_EGRESS, &sync_pkt
	);
	assert(rc == 0);

	frame = extract_sync_frame(sync_mbuf);

	be_src_port = rte_cpu_to_be_16(TEST_DST_PORT);
	be_dst_port = rte_cpu_to_be_16(TEST_SRC_PORT);

	printf("  UDP EGRESS:  src_port=%u (expected %u, BE would be %u), "
	       "dst_port=%u (expected %u, BE would be %u)\n",
	       frame->src_port,
	       TEST_DST_PORT,
	       be_src_port,
	       frame->dst_port,
	       TEST_SRC_PORT,
	       be_dst_port);

	if (frame->src_port == be_src_port || frame->dst_port == be_dst_port) {
		printf("  *** BUG DETECTED: UDP EGRESS ports are in network "
		       "byte order! ***\n");
	}

	/* EGRESS swaps src/dst to match initial 5-tuple */
	assert(frame->src_port == TEST_DST_PORT &&
	       "UDP EGRESS src_port should be swapped dst_port in host byte "
	       "order");
	assert(frame->dst_port == TEST_SRC_PORT &&
	       "UDP EGRESS dst_port should be swapped src_port in host byte "
	       "order");

	rte_pktmbuf_free(src_pkt.mbuf);
	rte_pktmbuf_free(sync_mbuf);

	printf("  UDP sync frame port endianness: PASSED\n");
}

int
main(void) {
	printf("=== Sync Frame Endianness Test ===\n");

	/* Initialize mock mempool (same approach as mock/worker_mempool.h) */
	test_pool = mock_mempool_create();
	if (test_pool == NULL) {
		fprintf(stderr, "Failed to create mock mempool\n");
		return EXIT_FAILURE;
	}

	init_sync_config();

	/* TCP test (control — should always pass) */
	test_tcp_sync_frame_ports();

	/* UDP test (will fail if endianness bug is present) */
	test_udp_sync_frame_ports();

	printf("\n=== All sync frame endianness tests PASSED ===\n");
	return EXIT_SUCCESS;
}
