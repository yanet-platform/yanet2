#include "../utils/helpers.h"
#include "../utils/packet.h"
#include "../utils/rng.h"
#include "common/network.h"
#include "dataplane/meta.h"
#include "dataplane/select.h"
#include "logging/log.h"
#include "rte_ether.h"
#include "rte_ip.h"
#include "rte_tcp.h"
#include <netinet/in.h>
#include <string.h>

////////////////////////////////////////////////////////////////////////////////

static int
check_meta(struct packet *packet) {
	struct packet_metadata meta;
	fill_packet_metadata(packet, &meta);
	TEST_ASSERT_EQUAL(packet->hash, meta.hash, "hash not equals");
	TEST_ASSERT_EQUAL(
		packet->transport_header.type,
		meta.transport_proto,
		"transport proto not equals"
	);
	if (packet->network_header.type ==
	    rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4)) {
		TEST_ASSERT_EQUAL(
			meta.network_proto,
			IPPROTO_IP,
			"network proto not equals"
		);
		struct rte_ipv4_hdr *ip = rte_pktmbuf_mtod_offset(
			packet->mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		TEST_ASSERT_EQUAL(
			memcmp(&ip->src_addr, meta.src_addr, NET4_LEN),
			0,
			"src addr not equals"
		);
		TEST_ASSERT_EQUAL(
			memcmp(&ip->dst_addr, meta.dst_addr, NET4_LEN),
			0,
			"dst addr not equals"
		);
	} else if (packet->network_header.type ==
		   rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6)) {
		TEST_ASSERT_EQUAL(
			meta.network_proto,
			IPPROTO_IPV6,
			"network proto not equals"
		);
		struct rte_ipv6_hdr *ip = rte_pktmbuf_mtod_offset(
			packet->mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		TEST_ASSERT_EQUAL(
			memcmp(&ip->src_addr, meta.src_addr, NET6_LEN),
			0,
			"src addr not equals"
		);
		TEST_ASSERT_EQUAL(
			memcmp(&ip->dst_addr, meta.dst_addr, NET6_LEN),
			0,
			"dst addr not equals"
		);
	} else {
		LOG(ERROR, "unexpected network protocol");
		return TEST_FAILED;
	}
	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp = rte_pktmbuf_mtod_offset(
			packet->mbuf,
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		TEST_ASSERT_EQUAL(
			tcp->tcp_flags, meta.tcp_flags, "tcp flags not equals"
		);
		TEST_ASSERT_EQUAL(
			tcp->src_port, meta.src_port, "src port not equals"
		);
		TEST_ASSERT_EQUAL(
			tcp->dst_port, meta.dst_port, "dst port not equals"
		);
	} else if (packet->transport_header.type == IPPROTO_UDP) {
		struct rte_udp_hdr *udp = rte_pktmbuf_mtod_offset(
			packet->mbuf,
			struct rte_udp_hdr *,
			packet->transport_header.offset
		);
		TEST_ASSERT_EQUAL(
			udp->src_port, meta.src_port, "src port not equals"
		);
		TEST_ASSERT_EQUAL(
			udp->dst_port, meta.dst_port, "dst port not equals"
		);
	} else {
		LOG(ERROR, "unexpected transport protocol");
		return TEST_FAILED;
	}
	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

static int
reschedule() {
	struct packet_metadata meta;
	meta.transport_proto = IPPROTO_UDP;
	TEST_ASSERT(reschedule_real(&meta), "udp packets must be rescheduled");

	meta.transport_proto = IPPROTO_TCP;
	meta.tcp_flags = 0;
	TEST_ASSERT(
		!reschedule_real(&meta),
		"tcp packets without SYN flag must not be rescheduled"
	);

	meta.tcp_flags = RTE_TCP_SYN_FLAG;
	TEST_ASSERT(
		reschedule_real(&meta),
		"tcp packets with SYN flag must be rescheduled"
	);

	meta.tcp_flags = RTE_TCP_SYN_FLAG | RTE_TCP_RST_FLAG;
	TEST_ASSERT(
		!reschedule_real(&meta),
		"tcp packets with SYN and RST flags must not be rescheduled"
	);

	return TEST_SUCCESS;
}

////////////////////////////////////////////////////////////////////////////////

int
main() {
	log_enable_name("debug");

	uint64_t rng = 1231;

	struct packet packet;
	uint16_t tcp_flags[] = {
		RTE_TCP_ACK_FLAG,
		RTE_TCP_CWR_FLAG,
		RTE_TCP_FIN_FLAG,
		RTE_TCP_RST_FLAG,
		RTE_TCP_SYN_FLAG
	};
	size_t flags_count = sizeof(tcp_flags) / sizeof(*tcp_flags);
	for (size_t i = 0; i < 1000; ++i) {
		if (i % 100 == 0) {
			LOG(INFO, "%lu-th test iteration...", i);
		}
		uint8_t src_ip[NET6_LEN];
		for (size_t j = 0; j < NET6_LEN; ++j) {
			src_ip[j] = rng_next(&rng) & 0xFF;
		}
		uint8_t dst_ip[NET6_LEN];
		for (size_t j = 0; j < NET6_LEN; ++j) {
			dst_ip[j] = rng_next(&rng) & 0xFF;
		}
		uint16_t src_port = rng_next(&rng) & 0xFFFF;
		uint16_t dst_port = rng_next(&rng) & 0xFFFF;
		uint8_t transport_proto =
			(rng_next(&rng) % 2 == 0 ? IPPROTO_UDP : IPPROTO_TCP);
		uint16_t flags = 0;
		if (transport_proto == IPPROTO_TCP) {
			for (size_t j = 0; j < flags_count; ++j) {
				if (rng_next(&rng) % 2 == 0) {
					flags |= tcp_flags[j];
				}
			}
		}
		uint8_t network_proto =
			(rng_next(&rng) % 2 == 0 ? IPPROTO_IP : IPPROTO_IPV6);
		int res = make_packet_generic(
			&packet,
			src_ip,
			dst_ip,
			src_port,
			dst_port,
			transport_proto,
			network_proto,
			flags
		);
		TEST_ASSERT_EQUAL(res, 0, "failed to make packet");
		res = check_meta(&packet);
		TEST_ASSERT_EQUAL(res, TEST_SUCCESS, "meta-packet mismatch");
		if (i % 100 == 0) {
			LOG(INFO, "%lu-th test iteration succeed", i);
		}
	}

	LOG(INFO, "testing reschedule...");
	int res = reschedule();
	TEST_ASSERT_EQUAL(res, TEST_SUCCESS, "reschedule failed");

	LOG(INFO, "Test passed");

	return 0;
}