/*
 * FWState sync record and packet builder regression tests.
 *
 * Verifies transport-port byte order in the captured frame, the refusal to
 * capture a packet without a transport header, and that the multi-frame
 * builder packs frames byte-identically, clamps to the mbuf tailroom and
 * keeps configurable outer destinations without changing the packet
 * metadata.
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
#include "lib/dataplane_ut/mempool.h"
#include "lib/fwstate/sync.h"
#include "lib/fwstate/types.h"

// Test ports chosen so that byte-swap is visible:
// 12345 = 0x3039, byte-swapped = 0x3930 = 14640
// 80    = 0x0050, byte-swapped = 0x5000 = 20480
#define TEST_SRC_PORT 12345
#define TEST_DST_PORT 80

static const struct ether_addr test_dst_ether = {
	.addr = {0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
};
static struct rte_mempool *test_pool;

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
 * Capture the source packet into a record and build a one-frame sync
 * packet from it, the shape a single stashed event produces.
 */
static int
craft_single_sync_packet(
	const struct packet *src_pkt,
	enum sync_packet_direction direction,
	struct fwstate_sync_record *record,
	struct packet *sync_pkt
) {
	if (fwstate_fill_sync_record(src_pkt, direction, record) != 0) {
		return -1;
	}
	const struct fw_state_sync_frame *frames[] = {&record->frame};
	int written = fwstate_build_sync_packet(
		frames, 1, record->rx_device_id, record->tx_device_id, sync_pkt
	);
	return written == 1 ? 0 : -1;
}

static void
test_sync_packet_destination(void) {
	struct packet src_pkt = {};
	int rc = build_test_packet(
		&src_pkt, IPPROTO_UDP, TEST_SRC_PORT, TEST_DST_PORT
	);
	assert(rc == 0);
	src_pkt.rx_device_id = 7;
	src_pkt.tx_device_id = 9;

	struct rte_mbuf *sync_mbuf = rte_pktmbuf_alloc(test_pool);
	assert(sync_mbuf != NULL);
	struct packet sync_pkt = {.mbuf = sync_mbuf};
	const uint8_t dst_addr[16] = {
		0x20,
		0x01,
		0x0d,
		0xb8,
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
		2,
	};
	const uint16_t dst_port = rte_cpu_to_be_16(10000);

	struct fwstate_sync_record record;
	rc = craft_single_sync_packet(
		&src_pkt, SYNC_INGRESS, &record, &sync_pkt
	);
	assert(rc == 0);
	fwstate_sync_set_destination(
		&sync_pkt, &test_dst_ether, dst_addr, dst_port
	);

	struct rte_ether_hdr *ether_hdr =
		rte_pktmbuf_mtod(sync_mbuf, struct rte_ether_hdr *);
	const uint16_t ipv6_offset =
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_vlan_hdr);
	const uint16_t udp_offset = ipv6_offset + sizeof(struct rte_ipv6_hdr);
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		sync_mbuf, struct rte_ipv6_hdr *, ipv6_offset
	);
	struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
		sync_mbuf, struct rte_udp_hdr *, udp_offset
	);
	const uint16_t payload_len =
		sizeof(struct rte_udp_hdr) + sizeof(struct fw_state_sync_frame);

	assert(memcmp(&ether_hdr->dst_addr,
		      &test_dst_ether,
		      sizeof(test_dst_ether)) == 0);
	assert(memcmp(ipv6_hdr->dst_addr, dst_addr, sizeof(dst_addr)) == 0);
	assert(ipv6_hdr->payload_len == rte_cpu_to_be_16(payload_len));
	assert(udp_hdr->src_port == dst_port);
	assert(udp_hdr->dst_port == dst_port);
	assert(udp_hdr->dgram_len == rte_cpu_to_be_16(payload_len));
	assert((sync_pkt.flags & (1U << PACKET_FLAG_FWSTATE_SYNC_INTERNAL)) == 0
	);
	assert(sync_pkt.rx_device_id == src_pkt.rx_device_id);
	assert(sync_pkt.tx_device_id == src_pkt.tx_device_id);
	assert(sync_pkt.network_header.offset == ipv6_offset);
	assert(sync_pkt.transport_header.offset == udp_offset);
	assert(sync_pkt.data_len == udp_offset + payload_len);

	rte_pktmbuf_free(src_pkt.mbuf);
	rte_pktmbuf_free(sync_mbuf);
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

	/* Capture the sync record */
	struct fwstate_sync_record record;
	rc = fwstate_fill_sync_record(&src_pkt, SYNC_INGRESS, &record);
	assert(rc == 0);

	/* Verify sync frame ports */
	struct fw_state_sync_frame *frame = &record.frame;

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

	/* Test EGRESS direction (ports should be swapped) */
	rc = build_test_packet(
		&src_pkt, IPPROTO_TCP, TEST_SRC_PORT, TEST_DST_PORT
	);
	assert(rc == 0);

	rc = fwstate_fill_sync_record(&src_pkt, SYNC_EGRESS, &record);
	assert(rc == 0);

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

	/* Capture the sync record */
	struct fwstate_sync_record record;
	rc = fwstate_fill_sync_record(&src_pkt, SYNC_INGRESS, &record);
	assert(rc == 0);

	/* Verify sync frame ports */
	struct fw_state_sync_frame *frame = &record.frame;

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

	/* Test EGRESS direction (ports should be swapped) */
	rc = build_test_packet(
		&src_pkt, IPPROTO_UDP, TEST_SRC_PORT, TEST_DST_PORT
	);
	assert(rc == 0);

	rc = fwstate_fill_sync_record(&src_pkt, SYNC_EGRESS, &record);
	assert(rc == 0);

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

	printf("  UDP sync frame port endianness: PASSED\n");
}

/*
 * Verifies that capture refuses a packet whose transport header is marked
 * unavailable: no sync frame may be fabricated from fragment payload bytes,
 * and the record must stay untouched.
 */
static void
test_record_refuses_unavailable_header(void) {
	printf("\n--- Record Refuses Unavailable Transport Header ---\n");

	struct packet src_pkt = {};
	int rc = build_test_packet(
		&src_pkt, IPPROTO_UDP, TEST_SRC_PORT, TEST_DST_PORT
	);
	assert(rc == 0);
	src_pkt.transport_header.type |= PACKET_TRANSPORT_HEADER_UNAVAILABLE;

	struct fwstate_sync_record record;
	memset(&record, 0xA5, sizeof(record));
	struct fwstate_sync_record before = record;

	rc = fwstate_fill_sync_record(&src_pkt, SYNC_INGRESS, &record);
	assert(rc == -1 &&
	       "capture must refuse a packet without a transport header");
	assert(memcmp(&record, &before, sizeof(record)) == 0 &&
	       "refused capture must leave the record untouched");

	rte_pktmbuf_free(src_pkt.mbuf);

	printf("  Record refusal for unavailable transport header: PASSED\n");
}

/*
 * Verifies that the builder packs several frames after one header stack,
 * each byte-identical to the frame of its record and in record order, and
 * takes the device ids it is given.
 */
static void
test_build_packs_frames(void) {
	printf("\n--- Build Packs Several Frames ---\n");

	struct fwstate_sync_record records[3];
	const uint16_t src_ports[3] = {1000, 2000, 3000};
	for (size_t idx = 0; idx < 3; ++idx) {
		struct packet src_pkt = {};
		int rc = build_test_packet(
			&src_pkt, IPPROTO_TCP, src_ports[idx], TEST_DST_PORT
		);
		assert(rc == 0);
		rc = fwstate_fill_sync_record(
			&src_pkt, SYNC_INGRESS, &records[idx]
		);
		assert(rc == 0);
		rte_pktmbuf_free(src_pkt.mbuf);
	}

	const struct fw_state_sync_frame *frames[3] = {
		&records[0].frame, &records[1].frame, &records[2].frame
	};
	struct rte_mbuf *sync_mbuf = rte_pktmbuf_alloc(test_pool);
	assert(sync_mbuf != NULL);
	struct packet sync_pkt = {.mbuf = sync_mbuf};

	int written = fwstate_build_sync_packet(frames, 3, 4, 5, &sync_pkt);
	assert(written == 3);

	const uint16_t ipv6_offset =
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_vlan_hdr);
	const uint16_t udp_offset = ipv6_offset + sizeof(struct rte_ipv6_hdr);
	const uint16_t payload_offset = udp_offset + sizeof(struct rte_udp_hdr);
	const uint16_t payload_len = 3 * sizeof(struct fw_state_sync_frame);
	struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
		sync_mbuf, struct rte_ipv6_hdr *, ipv6_offset
	);
	struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
		sync_mbuf, struct rte_udp_hdr *, udp_offset
	);
	assert(ipv6_hdr->payload_len ==
	       rte_cpu_to_be_16(sizeof(struct rte_udp_hdr) + payload_len));
	assert(udp_hdr->dgram_len ==
	       rte_cpu_to_be_16(sizeof(struct rte_udp_hdr) + payload_len));
	assert(sync_pkt.data_len == payload_offset + payload_len);
	assert(sync_pkt.rx_device_id == 4);
	assert(sync_pkt.tx_device_id == 5);

	for (size_t idx = 0; idx < 3; ++idx) {
		const struct fw_state_sync_frame *packed =
			rte_pktmbuf_mtod_offset(
				sync_mbuf,
				struct fw_state_sync_frame *,
				payload_offset +
					idx * sizeof(struct fw_state_sync_frame)
			);
		assert(memcmp(packed,
			      &records[idx].frame,
			      sizeof(struct fw_state_sync_frame)) == 0 &&
		       "a packed frame must equal its record's frame");
	}

	rte_pktmbuf_free(sync_mbuf);

	printf("  Build packs several frames: PASSED\n");
}

/*
 * Verifies that the builder zeroes every header byte it does not set, the
 * Ethernet source and the VLAN tag among them, even over a dirty mbuf.
 */
static void
test_build_zeroes_unset_header_bytes(void) {
	printf("\n--- Build Zeroes Unset Header Bytes ---\n");

	struct packet src_pkt = {};
	int rc = build_test_packet(
		&src_pkt, IPPROTO_UDP, TEST_SRC_PORT, TEST_DST_PORT
	);
	assert(rc == 0);
	struct fwstate_sync_record record;
	rc = fwstate_fill_sync_record(&src_pkt, SYNC_INGRESS, &record);
	assert(rc == 0);
	rte_pktmbuf_free(src_pkt.mbuf);

	test_mempool_poison(test_pool, 1);
	struct rte_mbuf *sync_mbuf = rte_pktmbuf_alloc(test_pool);
	test_mempool_poison(test_pool, 0);
	assert(sync_mbuf != NULL);
	struct packet sync_pkt = {.mbuf = sync_mbuf};

	const struct fw_state_sync_frame *frames[] = {&record.frame};
	int written = fwstate_build_sync_packet(frames, 1, 0, 0, &sync_pkt);
	assert(written == 1);

	const struct rte_ether_hdr *ether_hdr =
		rte_pktmbuf_mtod(sync_mbuf, struct rte_ether_hdr *);
	const struct rte_vlan_hdr *vlan_hdr = rte_pktmbuf_mtod_offset(
		sync_mbuf, struct rte_vlan_hdr *, sizeof(struct rte_ether_hdr)
	);
	static const uint8_t zero_mac[RTE_ETHER_ADDR_LEN] = {0};
	assert(memcmp(ether_hdr->src_addr.addr_bytes, zero_mac, sizeof(zero_mac)
	       ) == 0 &&
	       "the Ethernet source must be zero");
	assert(vlan_hdr->vlan_tci == 0 && "the VLAN tag must be zero");

	rte_pktmbuf_free(sync_mbuf);

	printf("  Build zeroes unset header bytes: PASSED\n");
}

/*
 * Verifies that the builder writes only as many frames as the mbuf
 * tailroom holds and reports that count.
 */
static void
test_build_clamps_to_tailroom(void) {
	printf("\n--- Build Clamps To Tailroom ---\n");

	struct packet src_pkt = {};
	int rc = build_test_packet(
		&src_pkt, IPPROTO_UDP, TEST_SRC_PORT, TEST_DST_PORT
	);
	assert(rc == 0);
	struct fwstate_sync_record record;
	rc = fwstate_fill_sync_record(&src_pkt, SYNC_INGRESS, &record);
	assert(rc == 0);
	rte_pktmbuf_free(src_pkt.mbuf);

	struct rte_mbuf *sync_mbuf = rte_pktmbuf_alloc(test_pool);
	assert(sync_mbuf != NULL);
	struct packet sync_pkt = {.mbuf = sync_mbuf};

	const uint32_t header_len =
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_vlan_hdr) +
		sizeof(struct rte_ipv6_hdr) + sizeof(struct rte_udp_hdr);
	const uint32_t fit = (rte_pktmbuf_tailroom(sync_mbuf) - header_len) /
			     sizeof(struct fw_state_sync_frame);
	const uint32_t count = fit + 5;
	const struct fw_state_sync_frame **frames =
		malloc(count * sizeof(*frames));
	assert(frames != NULL);
	for (uint32_t idx = 0; idx < count; ++idx) {
		frames[idx] = &record.frame;
	}

	int written = fwstate_build_sync_packet(frames, count, 0, 0, &sync_pkt);
	assert(written == (int)fit &&
	       "the builder must stop at the frames the tailroom holds");
	assert(sync_pkt.data_len ==
	       header_len + fit * sizeof(struct fw_state_sync_frame));

	free(frames);
	rte_pktmbuf_free(sync_mbuf);

	printf("  Build clamps to tailroom: PASSED\n");
}

/*
 * Verifies the frames-per-packet bound: 25 at the default MTU, zero
 * selecting the default, and at least one frame below the minimum.
 */
static void
test_frames_per_packet(void) {
	printf("\n--- Frames Per Packet ---\n");

	assert(fwstate_sync_frames_per_packet(1500) == 25);
	assert(fwstate_sync_frames_per_packet(0) == 25);
	assert(fwstate_sync_frames_per_packet(FWSTATE_SYNC_MIN_MTU) == 1);
	assert(fwstate_sync_frames_per_packet(FWSTATE_SYNC_MIN_MTU - 1) == 1);
	assert(fwstate_sync_frames_per_packet(9000) == (9000 - 48) / 56);

	printf("  Frames per packet: PASSED\n");
}

int
main(void) {
	printf("=== Sync Frame Endianness Test ===\n");

	test_pool = test_mempool_create();
	if (test_pool == NULL) {
		fprintf(stderr, "Failed to create test mempool\n");
		return EXIT_FAILURE;
	}

	test_sync_packet_destination();

	/* TCP test (control — should always pass) */
	test_tcp_sync_frame_ports();

	/* UDP test (will fail if endianness bug is present) */
	test_udp_sync_frame_ports();

	test_record_refuses_unavailable_header();

	test_build_packs_frames();

	test_build_clamps_to_tailroom();

	test_build_zeroes_unset_header_bytes();

	test_frames_per_packet();

	printf("\n=== All sync tests PASSED ===\n");
	return EXIT_SUCCESS;
}
