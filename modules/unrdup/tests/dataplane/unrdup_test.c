#include <netinet/icmp6.h>
#include <netinet/in.h>
#include <netinet/ip_icmp.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include <rte_byteorder.h>
#include <rte_eal.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_mempool.h>
#include <rte_tcp.h>

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "common/test_assert.h"

#include "lib/dataplane/config/zone.h"
#include "lib/dataplane/module/packet_front.h"
#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/icmp.h"
#include "lib/dataplane/packet/packet.h"
#include "lib/dataplane/pipeline/econtext.h"
#include "lib/logging/log.h"
#include "lib/utils/packet.h"

#include "config.h"
#include "modules/unrdup/api/controlplane.h"

void
unrdup_handle_packets(
	struct dp_worker *dp_worker,
	struct module_ectx *module_ectx,
	struct packet_front *packet_front
);

#define DEFAULT_HEADROOM 128
#define DEFAULT_TAILROOM 256

#define SERVICE_PORT 443
#define UNSERVED_PORT 8080

#define OFFENDING4_FULL (sizeof(struct rte_ipv4_hdr) + 4)
#define OFFENDING4_NO_PORTS sizeof(struct rte_ipv4_hdr)
#define OFFENDING4_PARTIAL_IP 12

#define OFFENDING6_FULL (sizeof(struct rte_ipv6_hdr) + 4)
#define OFFENDING6_NO_PORTS sizeof(struct rte_ipv6_hdr)

#define OFFENDING4_ORIGINAL_LEN 1400
#define OFFENDING6_ORIGINAL_LEN 1360

#define OFFENDING6_EXT_LEN 8
#define OFFENDING6_FULL_EXT (OFFENDING6_FULL + OFFENDING6_EXT_LEN)

static const uint8_t vip4[NET4_LEN] = {192, 0, 2, 1};
static const uint8_t unserved_vip4[NET4_LEN] = {192, 0, 2, 9};
static const uint8_t client4[NET4_LEN] = {198, 51, 100, 7};
static const uint8_t client4_other[NET4_LEN] = {198, 51, 100, 8};
static const uint8_t router4[NET4_LEN] = {203, 0, 113, 1};

static const uint8_t vip6[NET6_LEN] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
};
static const uint8_t client6[NET6_LEN] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 7
};
static const uint8_t router6[NET6_LEN] = {
	0xfe, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
};

static const uint8_t peer4[NET4_LEN] = {10, 0, 0, 10};
static const uint8_t peer6[NET6_LEN] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0xb, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x11
};

static const uint8_t source4[NET4_LEN] = {10, 0, 0, 1};
static const uint8_t source6[NET6_LEN] = {
	0x20, 0x01, 0x0d, 0xb8, 0, 0xa, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0
};

static const uint8_t mask4_full[NET4_LEN] = {0xff, 0xff, 0xff, 0xff};
static const uint8_t mask4_free[NET4_LEN] = {0xff, 0xff, 0xff, 0xfc};
static const uint8_t mask4_byte_free[NET4_LEN] = {0xff, 0xff, 0xff, 0};
static const uint8_t mask6_free[NET6_LEN] = {
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0xff,
	0,
	0,
	0,
	0
};

#define TEST_ARENA_SIZE (1 << 26)

static struct rte_mempool *clone_pool;
static struct dp_worker worker;

static void *test_arena;
static struct block_allocator test_ba;
static struct memory_context test_mctx;
static struct counter_storage *test_counter_storage;

static struct unrdup_module_config config;

static void
build_config_proto(
	uint64_t peer_count,
	const uint8_t *mask4,
	const uint8_t *addr6,
	uint8_t proto
) {
	memset(&config.source4, 0, sizeof(config.source4));
	memset(&config.source6, 0, sizeof(config.source6));

	memcpy(config.source4.v4.addr, source4, NET4_LEN);
	memcpy(config.source4.v4.mask, mask4, NET4_LEN);

	if (addr6 != NULL) {
		memcpy(config.source6.v6.addr, addr6, NET6_LEN);
		memcpy(config.source6.v6.mask, mask6_free, NET6_LEN);
	}

	struct unrdup_peer_config peers[2] = {0};
	peers[0].family = ip_family_ip4;
	memcpy(peers[0].addr.v4.bytes, peer4, NET4_LEN);
	peers[1].family = ip_family_ip6;
	memcpy(peers[1].addr.v6.bytes, peer6, NET6_LEN);

	struct unrdup_port_config port = {.port = SERVICE_PORT, .proto = proto};

	struct unrdup_service_config service_configs[2] = {0};
	service_configs[0].family = ip_family_ip4;
	memcpy(service_configs[0].vip.v4.bytes, vip4, NET4_LEN);
	service_configs[1].family = ip_family_ip6;
	memcpy(service_configs[1].vip.v6.bytes, vip6, NET6_LEN);

	for (uint64_t idx = 0; idx < 2; ++idx) {
		service_configs[idx].peers = peers;
		service_configs[idx].peer_count = peer_count;
		service_configs[idx].ports = &port;
		service_configs[idx].port_count = 1;
	}

	yanet_error *err = NULL;
	if (unrdup_module_config_update_services(
		    &config.cp_module, service_configs, 2, &err
	    )) {
		char *message = yanet_error_format(err);
		LOG(ERROR,
		    "failed to publish the test configuration: %s",
		    message != NULL ? message : "unknown");
		free(message);
		abort();
	}
}

static void
build_config(uint64_t peer_count, const uint8_t *mask4, const uint8_t *addr6) {
	build_config_proto(peer_count, mask4, addr6, IPPROTO_TCP);
}

static int
build_icmp4(
	struct packet *packet,
	uint8_t icmp_type,
	const uint8_t *inner_src,
	uint8_t inner_proto,
	uint16_t inner_port,
	size_t offending_len
) {
	uint8_t offending[OFFENDING4_FULL];
	memset(offending, 0, sizeof(offending));

	struct rte_ipv4_hdr *inner = (struct rte_ipv4_hdr *)offending;
	inner->version_ihl = 0x45;
	inner->total_length = rte_cpu_to_be_16(OFFENDING4_ORIGINAL_LEN);
	inner->time_to_live = 64;
	inner->next_proto_id = inner_proto;
	memcpy(&inner->src_addr, inner_src, NET4_LEN);
	memcpy(&inner->dst_addr, client4, NET4_LEN);

	uint16_t port = rte_cpu_to_be_16(inner_port);
	memcpy(offending + sizeof(struct rte_ipv4_hdr), &port, sizeof(port));

	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv4_hdr) +
			   sizeof(struct yanet_icmp_hdr) + offending_len;

	memset(packet, 0, sizeof(*packet));
	packet->mbuf = alloc_mbuf(DEFAULT_HEADROOM, pkt_len, DEFAULT_TAILROOM);
	if (packet->mbuf == NULL) {
		return -1;
	}

	uint8_t *data = rte_pktmbuf_mtod(packet->mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *outer = (struct rte_ipv4_hdr *)(eth + 1);
	outer->version_ihl = 0x45;
	outer->total_length = rte_cpu_to_be_16(pkt_len - sizeof(*eth));
	outer->time_to_live = 64;
	outer->next_proto_id = IPPROTO_ICMP;
	memcpy(&outer->src_addr, router4, NET4_LEN);
	memcpy(&outer->dst_addr, vip4, NET4_LEN);
	outer->hdr_checksum = 0;
	outer->hdr_checksum = rte_ipv4_cksum(outer);

	struct yanet_icmp_hdr *icmp = (struct yanet_icmp_hdr *)(outer + 1);
	icmp->icmp_type = icmp_type;
	icmp->icmp_code = 0;

	memcpy((uint8_t *)icmp + sizeof(*icmp), offending, offending_len);

	return parse_packet(packet);
}

static int
build_icmp6(
	struct packet *packet,
	uint8_t icmp_type,
	const uint8_t *inner_src,
	uint8_t inner_proto,
	uint16_t inner_port,
	size_t offending_len,
	int with_ext
) {
	uint8_t offending[OFFENDING6_FULL_EXT];
	memset(offending, 0, sizeof(offending));

	size_t transport_at = sizeof(struct rte_ipv6_hdr);

	struct rte_ipv6_hdr *inner = (struct rte_ipv6_hdr *)offending;
	inner->vtc_flow = rte_cpu_to_be_32(0x6u << 28);
	inner->proto = inner_proto;
	inner->hop_limits = 64;
	memcpy(inner->src_addr, inner_src, NET6_LEN);
	memcpy(inner->dst_addr, client6, NET6_LEN);

	if (with_ext) {
		struct yanet_ipv6_ext_2byte *ext =
			(struct yanet_ipv6_ext_2byte *)(offending + transport_at
			);
		ext->next_header = inner_proto;
		ext->extension_length = 0;

		inner->proto = IPPROTO_DSTOPTS;
		transport_at += OFFENDING6_EXT_LEN;
	}

	inner->payload_len = rte_cpu_to_be_16(OFFENDING6_ORIGINAL_LEN);

	uint16_t port = rte_cpu_to_be_16(inner_port);
	memcpy(offending + transport_at, &port, sizeof(port));

	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv6_hdr) +
			   sizeof(struct yanet_icmp6_hdr) + offending_len;

	memset(packet, 0, sizeof(*packet));
	packet->mbuf = alloc_mbuf(DEFAULT_HEADROOM, pkt_len, DEFAULT_TAILROOM);
	if (packet->mbuf == NULL) {
		return -1;
	}

	uint8_t *data = rte_pktmbuf_mtod(packet->mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	struct rte_ipv6_hdr *outer = (struct rte_ipv6_hdr *)(eth + 1);
	outer->vtc_flow = rte_cpu_to_be_32(0x6u << 28);
	outer->payload_len = rte_cpu_to_be_16(
		sizeof(struct yanet_icmp6_hdr) + offending_len
	);
	outer->proto = IPPROTO_ICMPV6;
	outer->hop_limits = 64;
	memcpy(outer->src_addr, router6, NET6_LEN);
	memcpy(outer->dst_addr, vip6, NET6_LEN);

	struct yanet_icmp6_hdr *icmp = (struct yanet_icmp6_hdr *)(outer + 1);
	icmp->icmp6_type = icmp_type;
	icmp->icmp6_code = 0;

	memcpy((uint8_t *)icmp + sizeof(*icmp), offending, offending_len);

	return parse_packet(packet);
}

static int
build_plain_tcp4(struct packet *packet) {
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv4_hdr) +
			   sizeof(struct rte_tcp_hdr);

	memset(packet, 0, sizeof(*packet));
	packet->mbuf = alloc_mbuf(DEFAULT_HEADROOM, pkt_len, DEFAULT_TAILROOM);
	if (packet->mbuf == NULL) {
		return -1;
	}

	uint8_t *data = rte_pktmbuf_mtod(packet->mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ip = (struct rte_ipv4_hdr *)(eth + 1);
	ip->version_ihl = 0x45;
	ip->total_length = rte_cpu_to_be_16(pkt_len - sizeof(*eth));
	ip->time_to_live = 64;
	ip->next_proto_id = IPPROTO_TCP;
	memcpy(&ip->src_addr, client4, NET4_LEN);
	memcpy(&ip->dst_addr, vip4, NET4_LEN);
	ip->hdr_checksum = 0;
	ip->hdr_checksum = rte_ipv4_cksum(ip);

	return parse_packet(packet);
}

static void
run_module(struct packet *packet, struct packet_front *packet_front) {
	packet_front_init(packet_front);
	packet_list_add(&packet_front->input, packet);

	struct module_ectx module_ectx;
	memset(&module_ectx, 0, sizeof(module_ectx));
	SET_OFFSET_OF(&module_ectx.cp_module, &config.cp_module);
	SET_OFFSET_OF(&module_ectx.counter_storage, test_counter_storage);

	unrdup_handle_packets(&worker, &module_ectx, packet_front);
}

static void
free_clones(struct packet_front *packet_front) {
	struct packet *clone;
	while ((clone = packet_list_pop(&packet_front->output)) != NULL) {
		rte_pktmbuf_free(clone->mbuf);
	}
}

static struct rte_ipv4_hdr *
clone_outer_ip4(struct packet *clone) {
	return rte_pktmbuf_mtod_offset(
		clone->mbuf, struct rte_ipv4_hdr *, clone->network_header.offset
	);
}

static struct rte_ipv6_hdr *
clone_outer_ip6(struct packet *clone) {
	return rte_pktmbuf_mtod_offset(
		clone->mbuf, struct rte_ipv6_hdr *, clone->network_header.offset
	);
}

static int
assert_passed_through(struct packet *packet) {
	struct packet_front packet_front;
	run_module(packet, &packet_front);

	TEST_ASSERT_EQUAL(
		packet_front_output_count(&packet_front), 1, "output"
	);
	TEST_ASSERT_EQUAL(packet_front_drop_count(&packet_front), 0, "drop");

	free_packet(packet);
	return TEST_SUCCESS;
}

static int
assert_consumed(struct packet *packet) {
	struct packet_front packet_front;
	run_module(packet, &packet_front);

	TEST_ASSERT_EQUAL(
		packet_front_output_count(&packet_front), 0, "output"
	);
	TEST_ASSERT_EQUAL(packet_front_drop_count(&packet_front), 1, "drop");

	free_packet(packet);
	return TEST_SUCCESS;
}

static int
test_served_endpoint(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);
	return assert_consumed(&packet);
}

static int
test_served_endpoint_v6(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp6(
			&packet,
			ICMP6_PACKET_TOO_BIG,
			vip6,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING6_FULL,
			0
		),
		"build"
	);
	return assert_consumed(&packet);
}

static int
test_offending_extension_header_v6(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp6(
			&packet,
			ICMP6_PACKET_TOO_BIG,
			vip6,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING6_FULL_EXT,
			1
		),
		"build"
	);
	return assert_consumed(&packet);
}

static int
test_offending_extension_truncated_v6(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp6(
			&packet,
			ICMP6_PACKET_TOO_BIG,
			vip6,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING6_NO_PORTS + OFFENDING6_EXT_LEN,
			1
		),
		"build"
	);
	return assert_passed_through(&packet);
}

static int
test_not_icmp(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(build_plain_tcp4(&packet), "build");
	return assert_passed_through(&packet);
}

static int
test_informational_icmp(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_ECHO,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);
	return assert_passed_through(&packet);
}

static int
test_unserved_vip(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			unserved_vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);
	return assert_passed_through(&packet);
}

static int
test_unserved_port(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			UNSERVED_PORT,
			OFFENDING4_FULL
		),
		"build"
	);
	return assert_passed_through(&packet);
}

static int
test_unserved_proto(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_UDP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);
	return assert_passed_through(&packet);
}

static int
test_offending_not_transport(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_ICMP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);
	return assert_passed_through(&packet);
}

static int
test_truncated_before_ports(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_NO_PORTS
		),
		"build"
	);
	return assert_passed_through(&packet);
}

static int
test_truncated_inside_ip(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_PARTIAL_IP
		),
		"build"
	);
	return assert_passed_through(&packet);
}

static int
test_empty_payload(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			0
		),
		"build"
	);
	return assert_passed_through(&packet);
}

static int
test_icmp_variant_mismatch(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	packet.transport_header.type = IPPROTO_ICMPV6;

	return assert_passed_through(&packet);
}

static int
test_outer_fragment(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	packet.flags |= 1 << PACKET_FLAG_FRAGMENTED;
	packet.fragment_offset = 1;

	return assert_passed_through(&packet);
}

static int
test_outer_first_fragment(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	packet.flags |= 1 << PACKET_FLAG_FRAGMENTED;

	return assert_passed_through(&packet);
}

static int
test_offending_fragment(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
		packet.mbuf,
		struct rte_ipv4_hdr *,
		packet.transport_header.offset + sizeof(struct yanet_icmp_hdr)
	);
	inner->fragment_offset = rte_cpu_to_be_16(1);

	return assert_passed_through(&packet);
}

static int
test_offending_wrong_version(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
		packet.mbuf,
		struct rte_ipv4_hdr *,
		packet.transport_header.offset + sizeof(struct yanet_icmp_hdr)
	);
	inner->version_ihl = 0x65;

	return assert_passed_through(&packet);
}

static int
test_offending_length_before_ports(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
		packet.mbuf,
		struct rte_ipv4_hdr *,
		packet.transport_header.offset + sizeof(struct yanet_icmp_hdr)
	);
	inner->total_length = rte_cpu_to_be_16(sizeof(struct rte_ipv4_hdr));

	return assert_passed_through(&packet);
}

static int
test_offending_declared_transport_too_short(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
		packet.mbuf,
		struct rte_ipv4_hdr *,
		packet.transport_header.offset + sizeof(struct yanet_icmp_hdr)
	);
	inner->total_length = rte_cpu_to_be_16(
		sizeof(struct rte_ipv4_hdr) + sizeof(uint16_t)
	);

	return assert_passed_through(&packet);
}

static int
test_outer_wrong_version(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct rte_ipv4_hdr *outer = rte_pktmbuf_mtod_offset(
		packet.mbuf, struct rte_ipv4_hdr *, packet.network_header.offset
	);
	outer->version_ihl = 0x65;

	return assert_passed_through(&packet);
}

static int
test_outer_length_before_ports(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct rte_ipv4_hdr *outer = rte_pktmbuf_mtod_offset(
		packet.mbuf, struct rte_ipv4_hdr *, packet.network_header.offset
	);
	outer->total_length = rte_cpu_to_be_16(
		sizeof(struct rte_ipv4_hdr) + sizeof(struct yanet_icmp_hdr) +
		OFFENDING4_NO_PORTS
	);

	return assert_passed_through(&packet);
}

static int
test_offending_undersized_ah_v6(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp6(
			&packet,
			ICMP6_PACKET_TOO_BIG,
			vip6,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING6_FULL_EXT,
			1
		),
		"build"
	);

	struct rte_ipv6_hdr *inner = rte_pktmbuf_mtod_offset(
		packet.mbuf,
		struct rte_ipv6_hdr *,
		packet.transport_header.offset + sizeof(struct yanet_icmp6_hdr)
	);
	inner->proto = IPPROTO_AH;

	return assert_passed_through(&packet);
}

static int
test_misaddressed_error(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct rte_ipv4_hdr *outer = rte_pktmbuf_mtod_offset(
		packet.mbuf, struct rte_ipv4_hdr *, packet.network_header.offset
	);
	memcpy(&outer->dst_addr, unserved_vip4, NET4_LEN);

	return assert_passed_through(&packet);
}

static int
test_offending_header_too_short(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
		packet.mbuf,
		struct rte_ipv4_hdr *,
		packet.transport_header.offset + sizeof(struct yanet_icmp_hdr)
	);
	inner->version_ihl = 0x44;

	return assert_passed_through(&packet);
}

static int
test_truncated_before_ports_v6(void) {
	build_config(0, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp6(
			&packet,
			ICMP6_PACKET_TOO_BIG,
			vip6,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING6_NO_PORTS,
			0
		),
		"build"
	);
	return assert_passed_through(&packet);
}

static int
test_fanout_v4(void) {
	build_config(1, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct packet_front packet_front;
	run_module(&packet, &packet_front);

	TEST_ASSERT_EQUAL(
		packet_front_output_count(&packet_front), 1, "one clone"
	);
	TEST_ASSERT_EQUAL(
		packet_front_drop_count(&packet_front), 1, "error consumed"
	);

	struct packet *clone = packet_list_first(&packet_front.output);
	struct rte_ipv4_hdr *outer = clone_outer_ip4(clone);

	TEST_ASSERT_EQUAL(
		outer->next_proto_id, IPPROTO_IPIP, "ipv4 in ipv4 tunnel"
	);
	TEST_ASSERT(
		memcmp(&outer->dst_addr, peer4, NET4_LEN) == 0, "peer address"
	);
	TEST_ASSERT(
		memcmp(&outer->src_addr, source4, NET4_LEN) == 0,
		"a full length mask leaves the source unchanged"
	);
	TEST_ASSERT_EQUAL(
		clone->network_header.type,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4),
		"network header type"
	);

	free_clones(&packet_front);
	free_packet(&packet);
	return TEST_SUCCESS;
}

static int
test_fanout_v6(void) {
	build_config(2, mask4_full, source6);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct packet_front packet_front;
	run_module(&packet, &packet_front);

	TEST_ASSERT_EQUAL(
		packet_front_output_count(&packet_front),
		2,
		"one clone per peer"
	);

	struct packet *clone = packet_list_first(&packet_front.output)->next;
	struct rte_ipv6_hdr *outer = clone_outer_ip6(clone);

	TEST_ASSERT_EQUAL(outer->proto, IPPROTO_IPIP, "ipv4 in ipv6 tunnel");
	TEST_ASSERT(
		memcmp(outer->dst_addr, peer6, NET6_LEN) == 0, "peer address"
	);
	TEST_ASSERT(
		memcmp(outer->src_addr, source6, NET6_LEN / 2) == 0,
		"the masked half of the source is the configured prefix"
	);
	TEST_ASSERT_EQUAL(
		clone->network_header.type,
		rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6),
		"network header type"
	);

	free_clones(&packet_front);
	free_packet(&packet);
	return TEST_SUCCESS;
}

static int
test_source_stays_in_prefix(void) {
	build_config(1, mask4_free, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct packet_front packet_front;
	run_module(&packet, &packet_front);

	struct packet *clone = packet_list_first(&packet_front.output);
	struct rte_ipv4_hdr *outer = clone_outer_ip4(clone);

	uint8_t source[NET4_LEN];
	memcpy(source, &outer->src_addr, NET4_LEN);

	for (uint8_t idx = 0; idx < NET4_LEN; ++idx) {
		TEST_ASSERT_EQUAL(
			source[idx] & mask4_free[idx],
			source4[idx] & mask4_free[idx],
			"masked bits must match the configured prefix"
		);
	}

	free_clones(&packet_front);
	free_packet(&packet);
	return TEST_SUCCESS;
}

static int
test_source_varies_with_flow(void) {
	uint8_t sources[2][NET4_LEN];

	for (uint8_t run = 0; run < 2; ++run) {
		build_config(1, mask4_byte_free, NULL);

		struct packet packet;
		TEST_ASSERT_SUCCESS(
			build_icmp4(
				&packet,
				ICMP_DEST_UNREACH,
				vip4,
				IPPROTO_TCP,
				SERVICE_PORT,
				OFFENDING4_FULL
			),
			"build"
		);

		if (run == 1) {
			struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
				packet.mbuf,
				struct rte_ipv4_hdr *,
				packet.transport_header.offset +
					sizeof(struct yanet_icmp_hdr)
			);
			memcpy(&inner->dst_addr, client4_other, NET4_LEN);
		}

		struct packet_front packet_front;
		run_module(&packet, &packet_front);

		struct packet *clone = packet_list_first(&packet_front.output);
		TEST_ASSERT(clone != NULL, "one clone per run");

		memcpy(sources[run], &clone_outer_ip4(clone)->src_addr, NET4_LEN
		);

		free_clones(&packet_front);
		free_packet(&packet);
	}

	TEST_ASSERT(
		memcmp(sources[0], sources[1], NET4_LEN) != 0,
		"two clients must not share a source"
	);

	return TEST_SUCCESS;
}

static int
test_source_avoids_reserved_v4(void) {
	for (uint8_t client = 0; client < 8; ++client) {
		build_config(1, mask4_free, NULL);

		struct packet packet;
		TEST_ASSERT_SUCCESS(
			build_icmp4(
				&packet,
				ICMP_DEST_UNREACH,
				vip4,
				IPPROTO_TCP,
				SERVICE_PORT,
				OFFENDING4_FULL
			),
			"build"
		);

		struct rte_ipv4_hdr *inner = rte_pktmbuf_mtod_offset(
			packet.mbuf,
			struct rte_ipv4_hdr *,
			packet.transport_header.offset +
				sizeof(struct yanet_icmp_hdr)
		);
		uint8_t other[NET4_LEN] = {198, 51, 100, client};
		memcpy(&inner->dst_addr, other, NET4_LEN);

		struct packet_front packet_front;
		run_module(&packet, &packet_front);

		struct packet *clone = packet_list_first(&packet_front.output);
		TEST_ASSERT(clone != NULL, "one clone per run");

		uint8_t source[NET4_LEN];
		memcpy(source, &clone_outer_ip4(clone)->src_addr, NET4_LEN);

		uint8_t host = source[NET4_LEN - 1] & 0x03;
		TEST_ASSERT(
			host != 0 && host != 0x03,
			"the subnet and broadcast addresses are not sources"
		);

		free_clones(&packet_front);
		free_packet(&packet);
	}

	return TEST_SUCCESS;
}

static int
test_peer_without_source(void) {
	build_config(2, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct packet_front packet_front;
	run_module(&packet, &packet_front);

	TEST_ASSERT_EQUAL(
		packet_front_output_count(&packet_front),
		1,
		"only the peer whose family has a source"
	);
	TEST_ASSERT_EQUAL(
		packet_front_drop_count(&packet_front), 1, "error consumed"
	);

	free_clones(&packet_front);
	free_packet(&packet);
	return TEST_SUCCESS;
}

static int
test_error_is_preserved(void) {
	build_config(1, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	uint16_t original_len = packet_data_len(&packet);
	uint8_t original[256];
	TEST_ASSERT(original_len <= sizeof(original), "fixture fits");
	memcpy(original, packet_data(&packet), original_len);

	struct packet_front packet_front;
	run_module(&packet, &packet_front);

	struct packet *clone = packet_list_first(&packet_front.output);

	TEST_ASSERT_EQUAL(
		packet_data_len(clone),
		original_len + sizeof(struct rte_ipv4_hdr),
		"the clone grew by exactly the outer header"
	);

	uint8_t *carried = rte_pktmbuf_mtod_offset(
		clone->mbuf,
		uint8_t *,
		clone->network_header.offset + sizeof(struct rte_ipv4_hdr)
	);
	TEST_ASSERT(
		memcmp(carried,
		       original + sizeof(struct rte_ether_hdr),
		       original_len - sizeof(struct rte_ether_hdr)) == 0,
		"the error travels unchanged inside the tunnel"
	);

	free_clones(&packet_front);
	free_packet(&packet);
	return TEST_SUCCESS;
}

static uint64_t
counter_value(uint64_t counter_id, uint64_t idx) {
	uint64_t *counters =
		counter_get_address(counter_id, test_counter_storage);
	return counters[idx];
}

static int
test_served_endpoint_udp(void) {
	build_config_proto(1, mask4_full, NULL, IPPROTO_UDP);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_UDP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct packet_front packet_front;
	run_module(&packet, &packet_front);

	TEST_ASSERT_EQUAL(
		packet_front_output_count(&packet_front), 1, "one clone"
	);
	TEST_ASSERT_EQUAL(
		packet_front_drop_count(&packet_front), 1, "error consumed"
	);

	struct packet *clone = packet_list_first(&packet_front.output);
	struct rte_ipv4_hdr *outer = clone_outer_ip4(clone);

	TEST_ASSERT(
		memcmp(&outer->dst_addr, peer4, NET4_LEN) == 0, "peer address"
	);

	free_clones(&packet_front);
	free_packet(&packet);
	return TEST_SUCCESS;
}

static int
test_fanout_v6_error(void) {
	build_config(2, mask4_full, source6);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp6(
			&packet,
			ICMP6_PACKET_TOO_BIG,
			vip6,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING6_FULL,
			0
		),
		"build"
	);

	uint16_t original_len = packet_data_len(&packet);

	struct packet_front packet_front;
	run_module(&packet, &packet_front);

	TEST_ASSERT_EQUAL(
		packet_front_output_count(&packet_front),
		2,
		"one clone per peer"
	);

	struct packet *v4_clone = packet_list_first(&packet_front.output);
	struct rte_ipv4_hdr *outer4 = clone_outer_ip4(v4_clone);

	TEST_ASSERT_EQUAL(
		outer4->next_proto_id, IPPROTO_IPV6, "ipv6 in ipv4 tunnel"
	);
	TEST_ASSERT(
		memcmp(&outer4->dst_addr, peer4, NET4_LEN) == 0, "peer address"
	);
	TEST_ASSERT_EQUAL(
		packet_data_len(v4_clone),
		original_len + sizeof(struct rte_ipv4_hdr),
		"the clone grew by exactly the outer header"
	);

	struct packet *v6_clone = v4_clone->next;
	struct rte_ipv6_hdr *outer6 = clone_outer_ip6(v6_clone);

	TEST_ASSERT_EQUAL(outer6->proto, IPPROTO_IPV6, "ipv6 in ipv6 tunnel");
	TEST_ASSERT(
		memcmp(outer6->dst_addr, peer6, NET6_LEN) == 0, "peer address"
	);
	TEST_ASSERT_EQUAL(
		packet_data_len(v6_clone),
		original_len + sizeof(struct rte_ipv6_hdr),
		"the clone grew by exactly the outer header"
	);

	free_clones(&packet_front);
	free_packet(&packet);
	return TEST_SUCCESS;
}

static int
test_counters_fanout(void) {
	build_config(2, mask4_full, source6);

	uint64_t redistributed =
		counter_value(config.redistributed_counter_id, 0);
	uint64_t redistributed_bytes =
		counter_value(config.redistributed_counter_id, 1);
	uint64_t sent = counter_value(config.clones_sent_counter_id, 0);
	uint64_t sent_bytes = counter_value(config.clones_sent_counter_id, 1);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	uint16_t original_len = packet_data_len(&packet);

	struct packet_front packet_front;
	run_module(&packet, &packet_front);

	uint64_t clone_bytes = 0;
	for (struct packet *clone = packet_list_first(&packet_front.output);
	     clone != NULL;
	     clone = clone->next) {
		clone_bytes += packet_data_len(clone);
	}

	TEST_ASSERT_EQUAL(
		counter_value(config.redistributed_counter_id, 0),
		redistributed + 1,
		"one error redistributed"
	);
	TEST_ASSERT_EQUAL(
		counter_value(config.redistributed_counter_id, 1),
		redistributed_bytes + original_len,
		"the error's own bytes"
	);
	TEST_ASSERT_EQUAL(
		counter_value(config.clones_sent_counter_id, 0),
		sent + 2,
		"one clone per peer"
	);
	TEST_ASSERT_EQUAL(
		counter_value(config.clones_sent_counter_id, 1),
		sent_bytes + clone_bytes,
		"the bytes every clone carries"
	);

	free_clones(&packet_front);
	free_packet(&packet);
	return TEST_SUCCESS;
}

static int
test_counters_peer_without_source(void) {
	build_config(2, mask4_full, NULL);

	uint64_t skipped = counter_value(config.peer_no_source_counter_id, 0);
	uint64_t sent = counter_value(config.clones_sent_counter_id, 0);

	struct packet packet;
	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	struct packet_front packet_front;
	run_module(&packet, &packet_front);

	TEST_ASSERT_EQUAL(
		counter_value(config.peer_no_source_counter_id, 0),
		skipped + 1,
		"the peer whose family has no source"
	);
	TEST_ASSERT_EQUAL(
		counter_value(config.clones_sent_counter_id, 0),
		sent + 1,
		"only the peer that could be reached"
	);

	free_clones(&packet_front);
	free_packet(&packet);
	return TEST_SUCCESS;
}

static int
test_counters_rejections(void) {
	build_config(0, mask4_full, NULL);

	uint64_t unserved = counter_value(config.unserved_counter_id, 0);
	uint64_t misaddressed =
		counter_value(config.misaddressed_counter_id, 0);
	uint64_t malformed = counter_value(config.malformed_counter_id, 0);

	struct packet packet;
	struct packet_front packet_front;

	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			unserved_vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build unserved"
	);

	struct rte_ipv4_hdr *outer = rte_pktmbuf_mtod_offset(
		packet.mbuf, struct rte_ipv4_hdr *, packet.network_header.offset
	);
	memcpy(&outer->dst_addr, unserved_vip4, NET4_LEN);

	run_module(&packet, &packet_front);
	free_packet(&packet);

	TEST_ASSERT_EQUAL(
		counter_value(config.unserved_counter_id, 0),
		unserved + 1,
		"an error naming no served endpoint"
	);

	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build misaddressed"
	);

	outer = rte_pktmbuf_mtod_offset(
		packet.mbuf, struct rte_ipv4_hdr *, packet.network_header.offset
	);
	memcpy(&outer->dst_addr, unserved_vip4, NET4_LEN);

	run_module(&packet, &packet_front);
	free_packet(&packet);

	TEST_ASSERT_EQUAL(
		counter_value(config.misaddressed_counter_id, 0),
		misaddressed + 1,
		"an error addressed away from its own payload"
	);

	TEST_ASSERT_SUCCESS(
		build_icmp4(
			&packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_NO_PORTS
		),
		"build malformed"
	);

	run_module(&packet, &packet_front);
	free_packet(&packet);

	TEST_ASSERT_EQUAL(
		counter_value(config.malformed_counter_id, 0),
		malformed + 1,
		"an error whose quote stops before the ports"
	);

	return TEST_SUCCESS;
}

#define ALLOC_ARENA_SIZE (1 << 24)
#define ALLOC_MAX_BLOCKS 4096
#define ALLOC_CHUNK (1 << 16)

static int
test_service_update_alloc_failure(void) {
	void *arena = malloc(ALLOC_ARENA_SIZE);
	TEST_ASSERT(arena != NULL, "arena");

	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, ALLOC_ARENA_SIZE);

	struct memory_context ctx;
	memory_context_init(&ctx, "unrdup_alloc", &ba);

	struct unrdup_module_config subject;
	memset(&subject, 0, sizeof(subject));
	memory_context_init_from(
		&subject.cp_module.memory_context, &ctx, "unrdup_alloc_cfg"
	);
	SET_OFFSET_OF(&subject.services, NULL);

	struct unrdup_peer_config peer = {.family = ip_family_ip4};
	memcpy(peer.addr.v4.bytes, peer4, NET4_LEN);

	struct unrdup_port_config port = {
		.port = SERVICE_PORT, .proto = IPPROTO_TCP
	};

	struct unrdup_service_config installed = {
		.family = ip_family_ip4,
		.peers = &peer,
		.peer_count = 1,
		.ports = &port,
		.port_count = 1,
	};
	memcpy(installed.vip.v4.bytes, vip4, NET4_LEN);

	yanet_error *err = NULL;
	TEST_ASSERT_SUCCESS(
		unrdup_module_config_update_services(
			&subject.cp_module, &installed, 1, &err
		),
		"installing the first table"
	);

	struct unrdup_service *previous = ADDR_OF(&subject.services);
	TEST_ASSERT(previous != NULL, "the table is published");

	// Nothing is left for the replacement to allocate, so the update fails
	// wherever it first asks for memory.
	static void *blocks[ALLOC_MAX_BLOCKS];
	size_t block_count = 0;
	while (block_count < ALLOC_MAX_BLOCKS) {
		void *block = memory_balloc(
			&subject.cp_module.memory_context, ALLOC_CHUNK
		);
		if (block == NULL) {
			break;
		}
		blocks[block_count++] = block;
	}
	TEST_ASSERT(block_count < ALLOC_MAX_BLOCKS, "the arena is exhausted");

	TEST_ASSERT(
		unrdup_module_config_update_services(
			&subject.cp_module, &installed, 1, &err
		) != 0,
		"an update that cannot allocate must fail"
	);
	TEST_ASSERT_EQUAL(
		subject.service_count, 1, "the previous table is kept"
	);
	TEST_ASSERT(
		ADDR_OF(&subject.services) == previous,
		"the previous table is untouched"
	);

	for (size_t idx = 0; idx < block_count; ++idx) {
		memory_bfree(
			&subject.cp_module.memory_context,
			blocks[idx],
			ALLOC_CHUNK
		);
	}

	unrdup_module_config_data_fini(&subject);

	TEST_ASSERT_EQUAL(
		subject.cp_module.memory_context.balloc_count,
		subject.cp_module.memory_context.bfree_count,
		"every allocation of the failed update is released"
	);

	free(arena);
	return TEST_SUCCESS;
}

// A clone that reaches a peer arrives still tunnelled, so the peer's own
// unrdup sees it before decap strips the outer header.
static int
build_received_clone(struct packet *packet, struct packet_front *packet_front) {
	build_config(1, mask4_full, NULL);

	TEST_ASSERT_SUCCESS(
		build_icmp4(
			packet,
			ICMP_DEST_UNREACH,
			vip4,
			IPPROTO_TCP,
			SERVICE_PORT,
			OFFENDING4_FULL
		),
		"build"
	);

	run_module(packet, packet_front);

	struct packet *clone = packet_list_first(&packet_front->output);
	TEST_ASSERT(clone != NULL, "one clone");

	return parse_packet(clone);
}

static int
test_tunneled_error_passes(void) {
	struct packet packet;
	struct packet_front sender_front;
	TEST_ASSERT_SUCCESS(
		build_received_clone(&packet, &sender_front), "clone"
	);

	struct packet *clone = packet_list_first(&sender_front.output);
	uint64_t received =
		counter_value(config.tunneled_received_counter_id, 0);
	uint64_t received_bytes =
		counter_value(config.tunneled_received_counter_id, 1);
	uint16_t clone_len = packet_data_len(clone);

	struct packet_front peer_front;
	run_module(clone, &peer_front);

	TEST_ASSERT_EQUAL(
		packet_front_output_count(&peer_front),
		1,
		"the clone travels on"
	);
	TEST_ASSERT_EQUAL(
		packet_front_drop_count(&peer_front), 0, "nothing is dropped"
	);
	TEST_ASSERT_EQUAL(
		counter_value(config.tunneled_received_counter_id, 0),
		received + 1,
		"one tunnelled error received"
	);
	TEST_ASSERT_EQUAL(
		counter_value(config.tunneled_received_counter_id, 1),
		received_bytes + clone_len,
		"the bytes it carries"
	);

	free_clones(&peer_front);
	free_packet(&packet);
	return TEST_SUCCESS;
}

static int
test_tunneled_non_error_passes(void) {
	struct packet packet;
	struct packet_front sender_front;
	TEST_ASSERT_SUCCESS(
		build_received_clone(&packet, &sender_front), "clone"
	);

	struct packet *clone = packet_list_first(&sender_front.output);
	uint64_t received =
		counter_value(config.tunneled_received_counter_id, 0);

	struct yanet_icmp_hdr *inner_icmp = rte_pktmbuf_mtod_offset(
		clone->mbuf,
		struct yanet_icmp_hdr *,
		clone->transport_header.offset + sizeof(struct rte_ipv4_hdr)
	);
	inner_icmp->icmp_type = ICMP_ECHO;

	struct packet_front peer_front;
	run_module(clone, &peer_front);

	TEST_ASSERT_EQUAL(
		packet_front_output_count(&peer_front),
		1,
		"the packet travels on"
	);
	TEST_ASSERT_EQUAL(
		counter_value(config.tunneled_received_counter_id, 0),
		received,
		"only errors are counted"
	);

	free_clones(&peer_front);
	free_packet(&packet);
	return TEST_SUCCESS;
}

// A router may put an extension header before ICMPv6, and the sender accepts
// such an error because the parser walks the chain for it.
static int
build_tunneled_icmp6_ext(struct packet *packet) {
	uint16_t inner_len = sizeof(struct rte_ipv6_hdr) + OFFENDING6_EXT_LEN +
			     sizeof(struct yanet_icmp6_hdr);
	uint16_t pkt_len = sizeof(struct rte_ether_hdr) +
			   sizeof(struct rte_ipv4_hdr) + inner_len;

	memset(packet, 0, sizeof(*packet));
	packet->mbuf = alloc_mbuf(DEFAULT_HEADROOM, pkt_len, DEFAULT_TAILROOM);
	if (packet->mbuf == NULL) {
		return -1;
	}

	uint8_t *data = rte_pktmbuf_mtod(packet->mbuf, uint8_t *);
	struct rte_ether_hdr *eth = (struct rte_ether_hdr *)data;
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *outer = (struct rte_ipv4_hdr *)(eth + 1);
	outer->version_ihl = 0x45;
	outer->total_length = rte_cpu_to_be_16(pkt_len - sizeof(*eth));
	outer->time_to_live = 64;
	outer->next_proto_id = IPPROTO_IPV6;
	memcpy(&outer->src_addr, source4, NET4_LEN);
	memcpy(&outer->dst_addr, peer4, NET4_LEN);
	outer->hdr_checksum = 0;
	outer->hdr_checksum = rte_ipv4_cksum(outer);

	struct rte_ipv6_hdr *inner = (struct rte_ipv6_hdr *)(outer + 1);
	inner->vtc_flow = rte_cpu_to_be_32(0x6u << 28);
	inner->payload_len = rte_cpu_to_be_16(
		OFFENDING6_EXT_LEN + sizeof(struct yanet_icmp6_hdr)
	);
	inner->proto = IPPROTO_HOPOPTS;
	inner->hop_limits = 64;
	memcpy(inner->src_addr, router6, NET6_LEN);
	memcpy(inner->dst_addr, vip6, NET6_LEN);

	struct yanet_ipv6_ext_2byte *ext =
		(struct yanet_ipv6_ext_2byte *)(inner + 1);
	ext->next_header = IPPROTO_ICMPV6;
	ext->extension_length = 0;

	struct yanet_icmp6_hdr *icmp =
		(struct yanet_icmp6_hdr *)((uint8_t *)ext + OFFENDING6_EXT_LEN);
	icmp->icmp6_type = ICMP6_PACKET_TOO_BIG;
	icmp->icmp6_code = 0;

	return parse_packet(packet);
}

static int
test_tunneled_error_with_extension(void) {
	build_config(1, mask4_full, NULL);

	struct packet packet;
	TEST_ASSERT_SUCCESS(build_tunneled_icmp6_ext(&packet), "build");

	uint64_t received =
		counter_value(config.tunneled_received_counter_id, 0);

	struct packet_front packet_front;
	run_module(&packet, &packet_front);

	TEST_ASSERT_EQUAL(
		packet_front_output_count(&packet_front),
		1,
		"the clone travels on"
	);
	TEST_ASSERT_EQUAL(
		counter_value(config.tunneled_received_counter_id, 0),
		received + 1,
		"an extension header still names a tunnelled error"
	);

	free_packet(&packet);
	return TEST_SUCCESS;
}

static int
setup_dpdk(void) {
	char *argv[] = {
		"unrdup_test",
		"--no-huge",
		"--no-pci",
		"--iova-mode=va",
		"--file-prefix",
		"unrdup_test",
		NULL
	};

	if (rte_eal_init(6, argv) < 0) {
		return -1;
	}

	clone_pool = rte_pktmbuf_pool_create(
		"unrdup_clones",
		1024,
		0,
		0,
		RTE_MBUF_DEFAULT_BUF_SIZE,
		SOCKET_ID_ANY
	);
	if (clone_pool == NULL) {
		return -1;
	}

	memset(&worker, 0, sizeof(worker));
	worker.rx_mempool = clone_pool;

	return 0;
}

static int
setup_counters(void) {
	test_arena = malloc(TEST_ARENA_SIZE);
	if (test_arena == NULL) {
		return -1;
	}

	block_allocator_init(&test_ba);
	block_allocator_put_arena(&test_ba, test_arena, TEST_ARENA_SIZE);
	memory_context_init(&test_mctx, "unrdup_test", &test_ba);

	memset(&config, 0, sizeof(config));
	memory_context_init_from(
		&config.cp_module.memory_context, &test_mctx, "unrdup_config"
	);

	if (counter_registry_init(
		    &config.cp_module.counter_registry, &test_mctx, 0
	    )) {
		return -1;
	}

	yanet_error *err = NULL;
	if (unrdup_module_config_register_counters(&config.cp_module, &err)) {
		return -1;
	}

	if (counter_registry_link(
		    &config.cp_module.counter_registry, NULL, &err
	    )) {
		return -1;
	}

	test_counter_storage = counter_storage_spawn(
		&test_mctx, NULL, &config.cp_module.counter_registry
	);

	return test_counter_storage == NULL ? -1 : 0;
}

static int
test_empty_service_update(void) {
	struct unrdup_module_config subject;
	memset(&subject, 0, sizeof(subject));
	memory_context_init_from(
		&subject.cp_module.memory_context, &test_mctx, "unrdup_empty"
	);
	SET_OFFSET_OF(&subject.services, NULL);

	struct unrdup_peer_config peer = {.family = ip_family_ip4};
	memcpy(peer.addr.v4.bytes, peer4, NET4_LEN);

	struct unrdup_port_config port = {
		.port = SERVICE_PORT, .proto = IPPROTO_TCP
	};

	struct unrdup_service_config service_config = {
		.family = ip_family_ip4,
		.peers = &peer,
		.peer_count = 1,
		.ports = &port,
		.port_count = 1,
	};
	memcpy(service_config.vip.v4.bytes, vip4, NET4_LEN);

	yanet_error *err = NULL;
	TEST_ASSERT_SUCCESS(
		unrdup_module_config_update_services(
			&subject.cp_module, &service_config, 1, &err
		),
		"populating the table"
	);
	TEST_ASSERT_EQUAL(subject.service_count, 1, "one service configured");

	TEST_ASSERT_SUCCESS(
		unrdup_module_config_update_services(
			&subject.cp_module, NULL, 0, &err
		),
		"an empty update must succeed"
	);
	TEST_ASSERT_EQUAL(subject.service_count, 0, "the table is cleared");
	TEST_ASSERT(
		ADDR_OF(&subject.services) == NULL, "the table is released"
	);

	return TEST_SUCCESS;
}

int
main(void) {
	log_enable_name("info");

	LOG(INFO, "=== Starting unrdup test suite ===");

	if (setup_dpdk()) {
		LOG(ERROR, "failed to set up dpdk");
		return TEST_FAILED;
	}

	if (setup_counters()) {
		LOG(ERROR, "failed to set up counters");
		return TEST_FAILED;
	}

	struct {
		const char *name;
		int (*fn)(void);
	} tests[] = {
		{"served_endpoint", test_served_endpoint},
		{"served_endpoint_v6", test_served_endpoint_v6},
		{"not_icmp", test_not_icmp},
		{"informational_icmp", test_informational_icmp},
		{"unserved_vip", test_unserved_vip},
		{"unserved_port", test_unserved_port},
		{"unserved_proto", test_unserved_proto},
		{"offending_not_transport", test_offending_not_transport},
		{"truncated_before_ports", test_truncated_before_ports},
		{"truncated_inside_ip", test_truncated_inside_ip},
		{"empty_payload", test_empty_payload},
		{"icmp_variant_mismatch", test_icmp_variant_mismatch},
		{"outer_fragment", test_outer_fragment},
		{"outer_first_fragment", test_outer_first_fragment},
		{"offending_fragment", test_offending_fragment},
		{"offending_wrong_version", test_offending_wrong_version},
		{"offending_length_before_ports",
		 test_offending_length_before_ports},
		{"offending_declared_transport_too_short",
		 test_offending_declared_transport_too_short},
		{"outer_wrong_version", test_outer_wrong_version},
		{"outer_length_before_ports", test_outer_length_before_ports},
		{"offending_undersized_ah_v6", test_offending_undersized_ah_v6},
		{"misaddressed_error", test_misaddressed_error},
		{"offending_header_too_short", test_offending_header_too_short},
		{"truncated_before_ports_v6", test_truncated_before_ports_v6},
		{"offending_extension_header_v6",
		 test_offending_extension_header_v6},
		{"offending_extension_truncated_v6",
		 test_offending_extension_truncated_v6},
		{"fanout_v4", test_fanout_v4},
		{"fanout_v6", test_fanout_v6},
		{"source_stays_in_prefix", test_source_stays_in_prefix},
		{"source_varies_with_flow", test_source_varies_with_flow},
		{"source_avoids_reserved_v4", test_source_avoids_reserved_v4},
		{"peer_without_source", test_peer_without_source},
		{"error_is_preserved", test_error_is_preserved},
		{"empty_service_update", test_empty_service_update},
		{"served_endpoint_udp", test_served_endpoint_udp},
		{"fanout_v6_error", test_fanout_v6_error},
		{"counters_fanout", test_counters_fanout},
		{"counters_peer_without_source",
		 test_counters_peer_without_source},
		{"counters_rejections", test_counters_rejections},
		{"service_update_alloc_failure",
		 test_service_update_alloc_failure},
		{"tunneled_error_passes", test_tunneled_error_passes},
		{"tunneled_non_error_passes", test_tunneled_non_error_passes},
		{"tunneled_error_with_extension",
		 test_tunneled_error_with_extension},
	};

	size_t total = sizeof(tests) / sizeof(tests[0]);
	size_t failed = 0;

	for (size_t idx = 0; idx < total; idx++) {
		LOG(INFO,
		    "[%zu/%zu] running %s...",
		    idx + 1,
		    total,
		    tests[idx].name);
		if (tests[idx].fn() != TEST_SUCCESS) {
			LOG(ERROR, "%s FAILED", tests[idx].name);
			failed++;
		} else {
			LOG(INFO, "%s passed", tests[idx].name);
		}
	}

	if (failed == 0) {
		LOG(INFO, "=== All %zu unrdup tests passed! ===", total);
	} else {
		LOG(ERROR, "=== %zu/%zu unrdup tests failed ===", failed, total
		);
	}

	return failed == 0 ? TEST_SUCCESS : TEST_FAILED;
}
