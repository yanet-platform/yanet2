// Regression tests for the filter2 compiler: rules with wide network
// lists and rules with non-prefix masks.
//
// Every scenario compiles host side through the public compile entries
// and classifies real packets. Two regressions are pinned: the network
// dedup stage must be sized from the real per rule network count (a
// single wide rule used to overflow it), and a non-contiguous mask must
// be rejected by the compile entry instead of walking out of bounds.

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "common/memory.h"
#include "common/memory_address.h"
#include "common/memory_block.h"

#include "common/container_of.h"
#include "common/lpm.h"
#include "common/memory_address.h"
#include "common/network.h"
#include "common/value.h"

#include "lib/dataplane/packet/data.h"
#include "lib/dataplane/packet/packet.h"

#include "lib/filter2/classifiers/net4.h"
#include "lib/filter2/classifiers/net6.h"
#include "lib/filter2/classifiers/port.h"
#include "lib/filter2/classifiers/proto_range.h"
#include "lib/filter2/compiler.h"
#include "lib/filter2/filter.h"
#include "lib/filter2/query.h"

#include "lib/utils/packet.h"

#include <rte_icmp.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <stdint.h>
#include <string.h>

FILTER_COMPILER_DECLARE(c_net4, net4_src, net4_dst);
FILTER_COMPILER_DECLARE(c_net6, net6_src, net6_dst);
FILTER_COMPILER_DECLARE(c_ports, port_src, port_dst);
FILTER_COMPILER_DECLARE(c_port_proto, proto_range, port_src, port_dst);

#define TEST_LOOKUP_NET4(variant, getter)                                      \
	static inline void test_lookup_##variant(                              \
		const struct filter_query_attr *attr,                          \
		const struct filter_query_attr_handlers *handlers,             \
		const struct packet **packets,                                 \
		uint32_t *results,                                             \
		uint32_t packet_count                                          \
	) {                                                                    \
		(void)handlers;                                                \
		const struct filter_query_attr_net4 *attr_net4 = container_of( \
			attr, const struct filter_query_attr_net4, attr        \
		);                                                             \
		uint8_t addrs[packet_count][NET4_LEN];                         \
		getter(packets, addrs[0], packet_count);                       \
		for (uint32_t idx = 0; idx < packet_count; ++idx) {            \
			results[idx] = vline_get(                              \
				&attr_net4->line,                              \
				lpm4_lookup(&attr_net4->lpm, addrs[idx])       \
			);                                                     \
		}                                                              \
	}                                                                      \
	FILTER_QUERY_ATTR(test_attr_##variant, test_lookup_##variant)

#define TEST_LOOKUP_NET6(variant, getter)                                      \
	static inline void test_lookup_##variant(                              \
		const struct filter_query_attr *attr,                          \
		const struct filter_query_attr_handlers *handlers,             \
		const struct packet **packets,                                 \
		uint32_t *results,                                             \
		uint32_t packet_count                                          \
	) {                                                                    \
		(void)handlers;                                                \
		const struct filter_query_attr_net6 *attr_net6 = container_of( \
			attr, const struct filter_query_attr_net6, attr        \
		);                                                             \
		uint32_t *row_scalar = ADDR_OF(&attr_net6->row_scalar);        \
		uint32_t *row_index = ADDR_OF(&attr_net6->row_index);          \
		uint8_t addrs[packet_count][NET6_LEN];                         \
		getter(packets, addrs[0], packet_count);                       \
		for (uint32_t idx = 0; idx < packet_count; ++idx) {            \
			const uint8_t *addr = addrs[idx];                      \
			uint32_t hi = lpm8_lookup(&attr_net6->hi, addr);       \
			uint32_t scalar = row_scalar[hi];                      \
			if (scalar != FILTER_NET6_ROW_2D) {                    \
				results[idx] = scalar;                         \
			} else {                                               \
				uint32_t lo =                                  \
					lpm8_lookup(&attr_net6->lo, addr + 8); \
				results[idx] = value_table_get(                \
					&attr_net6->comb, row_index[hi], lo    \
				);                                             \
			}                                                      \
		}                                                              \
	}                                                                      \
	FILTER_QUERY_ATTR(test_attr_##variant, test_lookup_##variant)

static inline void
test_get_net4_src_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET4_LEN, &ipv4_hdr->src_addr, NET4_LEN);
	}
}

static inline void
test_get_net4_dst_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv4_hdr *ipv4_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv4_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET4_LEN, &ipv4_hdr->dst_addr, NET4_LEN);
	}
}

TEST_LOOKUP_NET4(net4_src, test_get_net4_src_batch)
TEST_LOOKUP_NET4(net4_dst, test_get_net4_dst_batch)

static inline void
test_get_net6_src_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET6_LEN, ipv6_hdr->src_addr, NET6_LEN);
	}
}

static inline void
test_get_net6_dst_batch(
	const struct packet **packets, uint8_t *addrs, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_ipv6_hdr *ipv6_hdr = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_ipv6_hdr *,
			packet->network_header.offset
		);
		memcpy(addrs + idx * NET6_LEN, ipv6_hdr->dst_addr, NET6_LEN);
	}
}

TEST_LOOKUP_NET6(net6_src, test_get_net6_src_batch)
TEST_LOOKUP_NET6(net6_dst, test_get_net6_dst_batch)

#define TEST_LOOKUP_PORT(variant, getter)                                      \
	static inline void test_lookup_##variant(                              \
		const struct filter_query_attr *attr,                          \
		const struct filter_query_attr_handlers *handlers,             \
		const struct packet **packets,                                 \
		uint32_t *results,                                             \
		uint32_t packet_count                                          \
	) {                                                                    \
		(void)handlers;                                                \
		const struct filter_query_attr_port *cls = container_of(       \
			attr, const struct filter_query_attr_port, attr        \
		);                                                             \
		uint16_t ports[packet_count];                                  \
		getter(packets, ports, packet_count);                          \
		for (uint32_t idx = 0; idx < packet_count; ++idx) {            \
			results[idx] = vline_get(&cls->line, ports[idx]);      \
		}                                                              \
	}                                                                      \
	FILTER_QUERY_ATTR(test_attr_##variant, test_lookup_##variant)

static inline void
test_get_port_src_batch(
	const struct packet **packets, uint16_t *ports, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		if (packet->transport_header.type == IPPROTO_TCP) {
			struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_tcp_hdr *,
				packet->transport_header.offset
			);
			ports[idx] = rte_be_to_cpu_16(tcp_hdr->src_port);
		} else {
			struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_udp_hdr *,
				packet->transport_header.offset
			);
			ports[idx] = rte_be_to_cpu_16(udp_hdr->src_port);
		}
	}
}

static inline void
test_get_port_dst_batch(
	const struct packet **packets, uint16_t *ports, uint32_t packet_count
) {
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		const struct packet *packet = packets[idx];
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		if (packet->transport_header.type == IPPROTO_TCP) {
			struct rte_tcp_hdr *tcp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_tcp_hdr *,
				packet->transport_header.offset
			);
			ports[idx] = rte_be_to_cpu_16(tcp_hdr->dst_port);
		} else {
			struct rte_udp_hdr *udp_hdr = rte_pktmbuf_mtod_offset(
				mbuf,
				struct rte_udp_hdr *,
				packet->transport_header.offset
			);
			ports[idx] = rte_be_to_cpu_16(udp_hdr->dst_port);
		}
	}
}

TEST_LOOKUP_PORT(port_src, test_get_port_src_batch)
TEST_LOOKUP_PORT(port_dst, test_get_port_dst_batch)

static inline uint16_t
test_get_proto_range(const struct packet *packet) {
	uint16_t proto = packet->transport_header.type * 256;
	if (packet->transport_header.type == IPPROTO_TCP) {
		struct rte_tcp_hdr *tcp_header = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_tcp_hdr *,
			packet->transport_header.offset
		);
		proto += tcp_header->tcp_flags;
	}
	if (packet->transport_header.type == IPPROTO_ICMP) {
		struct rte_icmp_hdr *icmp_header = rte_pktmbuf_mtod_offset(
			packet_to_mbuf(packet),
			struct rte_icmp_hdr *,
			packet->transport_header.offset
		);
		proto += icmp_header->icmp_type;
	}
	return proto;
}

static inline void
test_lookup_proto_range(
	const struct filter_query_attr *attr,
	const struct filter_query_attr_handlers *handlers,
	const struct packet **packets,
	uint32_t *results,
	uint32_t packet_count
) {
	(void)handlers;
	const struct filter_query_attr_proto_range *cls = container_of(
		attr, const struct filter_query_attr_proto_range, attr
	);
	for (uint32_t idx = 0; idx < packet_count; ++idx) {
		results[idx] = vline_get(
			&cls->line, test_get_proto_range(packets[idx])
		);
	}
}

FILTER_QUERY_ATTR(test_attr_proto_range, test_lookup_proto_range)

static const struct filter_query_attr_handlers *q_ports[] = {
	&test_attr_port_src,
	&test_attr_port_dst,
};

static const struct filter_query_attr_handlers *q_port_proto[] = {
	&test_attr_proto_range,
	&test_attr_port_src,
	&test_attr_port_dst,
};

static const struct filter_query_attr_handlers *q_net4[] = {
	&test_attr_net4_src,
	&test_attr_net4_dst,
};

static const struct filter_query_attr_handlers *q_net6[] = {
	&test_attr_net6_src,
	&test_attr_net6_dst,
};

// 13 distinct networks in one rule: one past the 12 entries the dedup
// view was sized to when it assumed at most four networks per rule.
#define WIDE_NET_COUNT 13

// Wide rule fanout of the production shape that triggered the overflow.
#define FANOUT_NET4_COUNT 20000

// Wide rule fanout of the production shape that triggered the overflow
// on the v6 side as well.
#define FANOUT_NET6_COUNT 20000

#define CHECK(cond)                                                            \
	do {                                                                   \
		if (!(cond)) {                                                 \
			fprintf(stderr, "%s: failed: %s\n", __func__, #cond);  \
			return -1;                                             \
		}                                                              \
	} while (0)

// One query against a port filter: classifies a single tcp packet with
// the given ports and returns the rule index.
static uint32_t
query_port_one(struct filter *filter, uint16_t sport, uint16_t dport) {
	uint8_t src[NET4_LEN] = {192, 0, 2, 1};
	uint8_t dst[NET4_LEN] = {198, 51, 100, 7};
	struct packet *packet = calloc(1, sizeof(*packet));
	if (packet == NULL) {
		return FILTER_RULE_INVALID;
	}
	if (fill_packet_net4(
		    packet, src, dst, sport, dport, IPPROTO_TCP, 0x02
	    ) != 0) {
		free(packet);
		return FILTER_RULE_INVALID;
	}
	const struct packet *packets[1] = {packet};
	uint32_t results[1] = {FILTER_RULE_INVALID};
	filter_query(filter, q_ports, packets, results, 1);
	free_packet(packet);
	free(packet);
	return results[0];
}

// A query with explicit transport protocol and flag control.
static uint32_t
query_transport_one(
	struct filter *filter,
	uint8_t proto,
	uint16_t sport,
	uint16_t dport,
	uint8_t flags
) {
	uint8_t src[NET4_LEN] = {192, 0, 2, 1};
	uint8_t dst[NET4_LEN] = {198, 51, 100, 7};
	struct packet *packet = calloc(1, sizeof(*packet));
	if (packet == NULL) {
		return FILTER_RULE_INVALID;
	}
	if (fill_packet_net4(packet, src, dst, sport, dport, proto, flags) !=
	    0) {
		free(packet);
		return FILTER_RULE_INVALID;
	}
	const struct packet *packets[1] = {packet};
	uint32_t results[1] = {FILTER_RULE_INVALID};
	filter_query(filter, q_port_proto, packets, results, 1);
	free_packet(packet);
	free(packet);
	return results[0];
}

static uint32_t
query_tcp_one(
	struct filter *filter, uint16_t sport, uint16_t dport, uint8_t flags
) {
	return query_transport_one(filter, IPPROTO_TCP, sport, dport, flags);
}

static uint32_t
query_icmp_one(struct filter *filter, uint8_t icmp_type) {
	uint8_t src[NET4_LEN] = {192, 0, 2, 1};
	uint8_t dst[NET4_LEN] = {198, 51, 100, 7};
	struct packet *packet = calloc(1, sizeof(*packet));
	if (packet == NULL) {
		return FILTER_RULE_INVALID;
	}
	if (fill_packet_net4(packet, src, dst, 0, 0, IPPROTO_ICMP, 0) != 0) {
		free(packet);
		return FILTER_RULE_INVALID;
	}
	// stamp the icmp type the getter reads
	{
		struct rte_mbuf *mbuf = packet_to_mbuf(packet);
		struct rte_icmp_hdr *icmp_header = rte_pktmbuf_mtod_offset(
			mbuf,
			struct rte_icmp_hdr *,
			packet->transport_header.offset
		);
		icmp_header->icmp_type = icmp_type;
	}
	const struct packet *packets[1] = {packet};
	uint32_t results[1] = {FILTER_RULE_INVALID};
	filter_query(filter, q_port_proto, packets, results, 1);
	free_packet(packet);
	free(packet);
	return results[0];
}

static uint32_t
query_udp_one(struct filter *filter, uint16_t sport, uint16_t dport) {
	return query_transport_one(filter, IPPROTO_UDP, sport, dport, 0);
}

// One query against a compiled filter: classifies a single v4 or v6
// packet built by the fill helpers and returns the rule index.
static uint32_t
query_net4_one(struct filter *filter, const uint8_t src[NET4_LEN]) {
	uint8_t dst[NET4_LEN] = {192, 0, 2, 1};
	struct packet *packet = calloc(1, sizeof(*packet));
	if (packet == NULL) {
		return FILTER_RULE_INVALID;
	}
	if (fill_packet_net4(packet, src, dst, 1234, 80, 6, 0x12) != 0) {
		free(packet);
		return FILTER_RULE_INVALID;
	}
	const struct packet *packets[1] = {packet};
	uint32_t results[1] = {FILTER_RULE_INVALID};
	filter_query(filter, q_net4, packets, results, 1);
	free_packet(packet);
	free(packet);
	return results[0];
}

static uint32_t
query_net6_one(struct filter *filter, const uint8_t src[NET6_LEN]) {
	uint8_t dst[NET6_LEN] = {
		0x20, 0x01, 0x0d, 0xb8, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1
	};
	struct packet *packet = calloc(1, sizeof(*packet));
	if (packet == NULL) {
		return FILTER_RULE_INVALID;
	}
	if (fill_packet_net6(packet, src, dst, 1234, 80, 6, 0x12) != 0) {
		free(packet);
		return FILTER_RULE_INVALID;
	}
	const struct packet *packets[1] = {packet};
	uint32_t results[1] = {FILTER_RULE_INVALID};
	filter_query(filter, q_net6, packets, results, 1);
	free_packet(packet);
	free(packet);
	return results[0];
}

/*
 * A single rule carries 13 distinct /24 source networks; the compile
 * must succeed and addresses inside and outside the list must classify
 * accordingly.
 */
static int
run_net4_dedup_wide_rule_test(void) {
	struct net4 srcs[WIDE_NET_COUNT];
	struct net4 dsts[1];
	memset(srcs, 0, sizeof(srcs));
	memset(dsts, 0, sizeof(dsts));
	for (uint32_t k = 0; k < WIDE_NET_COUNT; ++k) {
		srcs[k].addr[0] = 10;
		srcs[k].addr[2] = (uint8_t)k;
		srcs[k].mask[0] = 255;
		srcs[k].mask[1] = 255;
		srcs[k].mask[2] = 255;
	}

	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.net4.srcs = srcs;
	rule.net4.src_count = WIDE_NET_COUNT;
	rule.net4.dsts = dsts;
	rule.net4.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 20);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 20);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net4, ptrs, 1, &mctx) == 0);

	uint8_t inside[NET4_LEN] = {10, 0, 5, 77};
	uint8_t outside[NET4_LEN] = {10, 0, 99, 77};
	CHECK(query_net4_one(&filter, inside) == 0);
	CHECK(query_net4_one(&filter, outside) == FILTER_RULE_INVALID);

	filter_free(&filter, c_net4);
	free(arena);
	return 0;
}

/*
 * A single rule carries 13 distinct /48 source networks; same shape as
 * the v4 scenario and the production rule that held tens of thousands
 * of v6 networks.
 */
static int
run_net6_dedup_wide_rule_test(void) {
	struct net6 srcs[WIDE_NET_COUNT];
	struct net6 dsts[1];
	memset(srcs, 0, sizeof(srcs));
	memset(dsts, 0, sizeof(dsts));
	for (uint32_t k = 0; k < WIDE_NET_COUNT; ++k) {
		srcs[k].addr[0] = 0x20;
		srcs[k].addr[1] = 0x01;
		srcs[k].addr[2] = 0x0d;
		srcs[k].addr[3] = 0xb8;
		srcs[k].addr[5] = (uint8_t)k;
		for (uint32_t b = 0; b < 6; ++b) {
			srcs[k].mask[b] = 255;
		}
	}

	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.net6.srcs = srcs;
	rule.net6.src_count = WIDE_NET_COUNT;
	rule.net6.dsts = dsts;
	rule.net6.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 20);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 20);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net6, ptrs, 1, &mctx) == 0);

	uint8_t inside[NET6_LEN] = {0x20, 0x01, 0x0d, 0xb8, 0, 5};
	uint8_t outside[NET6_LEN] = {0x20, 0x01, 0x0d, 0xb8, 0, 0x63};
	CHECK(query_net6_one(&filter, inside) == 0);
	CHECK(query_net6_one(&filter, outside) == FILTER_RULE_INVALID);

	filter_free(&filter, c_net6);
	free(arena);
	return 0;
}

// A rule with the production fanout shape: twenty thousand distinct
// /24 source networks in one rule, compiled and queried.
static int
run_net4_dedup_large_fanout_test(void) {
	static struct net4 srcs[FANOUT_NET4_COUNT];
	struct net4 dsts[1];
	memset(dsts, 0, sizeof(dsts));
	memset(srcs, 0, sizeof(srcs));
	for (uint32_t k = 0; k < FANOUT_NET4_COUNT; ++k) {
		srcs[k].addr[0] = 10;
		srcs[k].addr[1] = (uint8_t)(k >> 8);
		srcs[k].addr[2] = (uint8_t)(k & 0xff);
		srcs[k].mask[0] = 255;
		srcs[k].mask[1] = 255;
		srcs[k].mask[2] = 255;
	}

	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.net4.srcs = srcs;
	rule.net4.src_count = FANOUT_NET4_COUNT;
	rule.net4.dsts = dsts;
	rule.net4.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 26);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 26);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net4, ptrs, 1, &mctx) == 0);

	uint8_t inside[NET4_LEN] = {10, 5, 100, 7};
	uint8_t outside[NET4_LEN] = {10, 200, 0, 7};
	CHECK(query_net4_one(&filter, inside) == 0);
	CHECK(query_net4_one(&filter, outside) == FILTER_RULE_INVALID);

	filter_free(&filter, c_net4);
	free(arena);
	return 0;
}

/*
 * A v6 rule with the production fanout shape that overflowed the
 * dedup view: twenty thousand distinct /48 source networks in one
 * rule, compiled and queried.
 */
static int
run_net6_dedup_large_fanout_test(void) {
	static struct net6 srcs[FANOUT_NET6_COUNT];
	struct net6 dsts[1];
	memset(dsts, 0, sizeof(dsts));
	memset(srcs, 0, sizeof(srcs));
	for (uint32_t k = 0; k < FANOUT_NET6_COUNT; ++k) {
		srcs[k].addr[0] = 0x20;
		srcs[k].addr[1] = 0x01;
		srcs[k].addr[2] = 0x0d;
		srcs[k].addr[3] = 0xb8;
		srcs[k].addr[4] = (uint8_t)(k >> 8);
		srcs[k].addr[5] = (uint8_t)(k & 0xff);
		for (uint32_t b = 0; b < 6; ++b) {
			srcs[k].mask[b] = 255;
		}
	}

	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.net6.srcs = srcs;
	rule.net6.src_count = FANOUT_NET6_COUNT;
	rule.net6.dsts = dsts;
	rule.net6.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 28);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 28);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net6, ptrs, 1, &mctx) == 0);

	uint8_t inside[NET6_LEN] = {0x20, 0x01, 0x0d, 0xb8, 0x10, 0x63};
	uint8_t outside[NET6_LEN] = {0x20, 0x01, 0x0d, 0xb8, 0x50, 0x00};
	CHECK(query_net6_one(&filter, inside) == 0);
	CHECK(query_net6_one(&filter, outside) == FILTER_RULE_INVALID);

	filter_free(&filter, c_net6);
	free(arena);
	return 0;
}

/*
 * A rule with the v4 mask 255.0.255.0 is rejected at the compile entry
 * while the same rule with a contiguous mask compiles.
 */
static int
run_net4_mask_non_contiguous_rejected_test(void) {
	struct net4 srcs[1];
	struct net4 dsts[1];
	memset(srcs, 0, sizeof(srcs));
	memset(dsts, 0, sizeof(dsts));
	srcs[0].addr[0] = 10;
	srcs[0].mask[0] = 255;
	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.net4.srcs = srcs;
	rule.net4.src_count = 1;
	rule.net4.dsts = dsts;
	rule.net4.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 20);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 20);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net4, ptrs, 1, &mctx) == 0);
	filter_free(&filter, c_net4);

	srcs[0].mask[2] = 255;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net4, ptrs, 1, &mctx) == -1);

	free(arena);
	return 0;
}

/*
 * A rule with the v6 mask ff00:ff00:: is rejected at the compile entry
 * while the same rule with a contiguous mask compiles.
 */
static int
run_net6_mask_non_contiguous_rejected_test(void) {
	struct net6 srcs[1];
	struct net6 dsts[1];
	memset(&srcs, 0, sizeof(srcs));
	memset(&dsts, 0, sizeof(dsts));
	srcs[0].addr[0] = 0x20;
	srcs[0].mask[0] = 0xff;
	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.net6.srcs = srcs;
	rule.net6.src_count = 1;
	rule.net6.dsts = dsts;
	rule.net6.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 20);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 20);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net6, ptrs, 1, &mctx) == 0);
	filter_free(&filter, c_net6);

	srcs[0].mask[2] = 0xff;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_net6, ptrs, 1, &mctx) == -1);

	free(arena);
	return 0;
}

// A single value port range: the region machinery must still separate
// the covered port from its neighbours.
static int
run_port_single_value_test(void) {
	struct filter_port_range sp[1] = {{80, 80}};
	struct filter_port_range dp[1] = {{0, 65535}};
	struct filter_rule rule;
	memset(&rule, 0, sizeof(rule));
	rule.transport.srcs = sp;
	rule.transport.src_count = 1;
	rule.transport.dsts = dp;
	rule.transport.dst_count = 1;
	const struct filter_rule *ptrs[1] = {&rule};

	void *arena = malloc(1 << 22);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 22);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_ports, ptrs, 1, &mctx) == 0);

	CHECK(query_port_one(&filter, 80, 443) == 0);
	CHECK(query_port_one(&filter, 79, 443) == FILTER_RULE_INVALID);
	CHECK(query_port_one(&filter, 81, 443) == FILTER_RULE_INVALID);
	CHECK(query_port_one(&filter, 80, 8080) == 0);
	CHECK(query_port_one(&filter, 12345, 54321) == FILTER_RULE_INVALID);

	filter_free(&filter, c_ports);
	free(arena);
	return 0;
}

// Many rules sharing one port spec (the content group path) and a
// disjoint one-off spec; the classes of each must stay separate.
static int
run_port_group_test(void) {
	enum { RULES = 256 };
	struct filter_port_range sps[RULES];
	struct filter_port_range dps[RULES];
	struct filter_rule rules[RULES];
	memset(rules, 0, sizeof(rules));
	for (uint32_t idx = 0; idx < RULES; ++idx) {
		sps[idx].from = 1024;
		sps[idx].to = 2048;
		dps[idx].from = 0;
		dps[idx].to = 65535;
		rules[idx].transport.srcs = sps + idx;
		rules[idx].transport.src_count = 1;
		rules[idx].transport.dsts = dps + idx;
		rules[idx].transport.dst_count = 1;
	}
	// one odd rule at the end
	sps[RULES - 1].from = 4096;
	sps[RULES - 1].to = 4099;
	const struct filter_rule *ptrs[RULES];
	for (uint32_t idx = 0; idx < RULES; ++idx) {
		ptrs[idx] = rules + idx;
	}

	void *arena = malloc(1 << 22);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 22);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_ports, ptrs, RULES, &mctx) == 0);

	CHECK(query_port_one(&filter, 1500, 80) == 0);
	CHECK(query_port_one(&filter, 1023, 80) == FILTER_RULE_INVALID);
	CHECK(query_port_one(&filter, 2049, 80) == FILTER_RULE_INVALID);
	CHECK(query_port_one(&filter, 4097, 999) == RULES - 1);
	CHECK(query_port_one(&filter, 4100, 999) == FILTER_RULE_INVALID);

	filter_free(&filter, c_ports);
	free(arena);
	return 0;
}

// Proto range region build: a tcp-only rule and a wide icmp block.
static int
run_proto_range_test(void) {
	struct filter_proto_range protos[2] = {
		{6 * 256, 6 * 256 + 255},
		{1 * 256, 1 * 256 + 31},
	};
	struct filter_port_range sps[2] = {{0, 65535}, {0, 65535}};
	struct filter_port_range dps[2] = {{0, 65535}, {0, 65535}};
	struct filter_rule rules[2];
	memset(rules, 0, sizeof(rules));
	for (int idx = 0; idx < 2; ++idx) {
		rules[idx].transport.protos = protos + idx;
		rules[idx].transport.proto_count = 1;
		rules[idx].transport.srcs = sps + idx;
		rules[idx].transport.src_count = 1;
		rules[idx].transport.dsts = dps + idx;
		rules[idx].transport.dst_count = 1;
	}
	const struct filter_rule *ptrs[2] = {rules, rules + 1};

	void *arena = malloc(1 << 22);
	struct block_allocator ba;
	block_allocator_init(&ba);
	block_allocator_put_arena(&ba, arena, 1 << 22);
	struct memory_context mctx;
	memory_context_init(&mctx, "filter2_regression", &ba);

	struct filter filter;
	memset(&filter, 0, sizeof(filter));
	CHECK(filter_init(&filter, c_port_proto, ptrs, 2, &mctx) == 0);

	// tcp packet matches rule 0, icmp echo matches rule 1
	CHECK(query_tcp_one(&filter, 443, 80, 0x02) == 0);
	CHECK(query_tcp_one(&filter, 443, 80, 0x12) == 0);
	CHECK(query_icmp_one(&filter, 8) == 1);
	CHECK(query_icmp_one(&filter, 40) == FILTER_RULE_INVALID);
	CHECK(query_udp_one(&filter, 5000, 53) == FILTER_RULE_INVALID);

	filter_free(&filter, c_port_proto);
	free(arena);
	return 0;
}

int
main(void) {
	int failed = 0;
	failed += run_net4_dedup_wide_rule_test() != 0;
	failed += run_net6_dedup_wide_rule_test() != 0;
	failed += run_net4_dedup_large_fanout_test() != 0;
	failed += run_net6_dedup_large_fanout_test() != 0;
	failed += run_port_single_value_test() != 0;
	failed += run_port_group_test() != 0;
	failed += run_proto_range_test() != 0;
	failed += run_net4_mask_non_contiguous_rejected_test() != 0;
	failed += run_net6_mask_non_contiguous_rejected_test() != 0;
	return failed;
}
