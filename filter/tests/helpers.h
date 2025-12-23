#pragma once

#include "common/network.h"
#include "filter/rule.h"
#include <stddef.h>
#include <stdint.h>

#ifndef filter_TEST_MAX_RANGES
#define filter_TEST_MAX_RANGES 32
#endif

struct filter_rule_builder {
	// Only what tests need today
	struct filter_port_range src_port_ranges[filter_TEST_MAX_RANGES];
	size_t src_port_ranges_count;

	struct filter_port_range dst_port_ranges[filter_TEST_MAX_RANGES];
	size_t dst_port_ranges_count;

	struct net4 net4_src[filter_TEST_MAX_RANGES];
	size_t net4_src_count;

	struct net4 net4_dst[filter_TEST_MAX_RANGES];
	size_t net4_dst_count;

	struct net6 net6_src[filter_TEST_MAX_RANGES];
	size_t net6_src_count;

	struct net6 net6_dst[filter_TEST_MAX_RANGES];
	size_t net6_dst_count;

	struct filter_vlan_range vlan_ranges[filter_TEST_MAX_RANGES];
	size_t vlan_range_count;

	struct filter_proto_range proto_ranges[filter_TEST_MAX_RANGES];
	size_t proto_ranges_count;

	struct filter_proto proto;
};

static inline void
builder_init(struct filter_rule_builder *b) {
	b->src_port_ranges_count = 0;
	b->dst_port_ranges_count = 0;
	b->net4_src_count = 0;
	b->net4_dst_count = 0;
	b->net6_src_count = 0;
	b->net6_dst_count = 0;
	b->vlan_range_count = 0;
	b->proto_ranges_count = 0;
	b->proto = (struct filter_proto
	){.proto = PROTO_UNSPEC, .enable_bits = 0, .disable_bits = 0};
}

static inline void
builder_add_port_src_range(
	struct filter_rule_builder *b, uint16_t from, uint16_t to
) {
	size_t i = b->src_port_ranges_count++;
	b->src_port_ranges[i] = (struct filter_port_range){from, to};
}

static inline void
builder_add_port_dst_range(
	struct filter_rule_builder *b, uint16_t from, uint16_t to
) {
	size_t i = b->dst_port_ranges_count++;
	b->dst_port_ranges[i] = (struct filter_port_range){from, to};
}

static inline void
builder_add_net4_src(
	struct filter_rule_builder *b,
	const uint8_t addr[NET4_LEN],
	const uint8_t mask[NET4_LEN]
) {
	size_t i = b->net4_src_count++;
	for (int k = 0; k < 4; ++k) {
		b->net4_src[i].addr[k] = addr[k];
		b->net4_src[i].mask[k] = mask[k];
	}
}

static inline void
builder_add_net4_dst(
	struct filter_rule_builder *b,
	const uint8_t addr[NET4_LEN],
	const uint8_t mask[NET4_LEN]
) {
	size_t i = b->net4_dst_count++;
	for (int k = 0; k < 4; ++k) {
		b->net4_dst[i].addr[k] = addr[k];
		b->net4_dst[i].mask[k] = mask[k];
	}
}

static inline void
builder_add_net6_src(struct filter_rule_builder *b, struct net6 net) {
	size_t i = b->net6_src_count++;
	b->net6_src[i] = net;
}

static inline void
builder_add_net6_dst(struct filter_rule_builder *b, struct net6 net) {
	size_t i = b->net6_dst_count++;
	b->net6_dst[i] = net;
}

static inline void
builder_add_proto_range(
	struct filter_rule_builder *b, uint16_t from, uint16_t to
) {
	size_t i = b->proto_ranges_count++;
	b->proto_ranges[i] = (struct filter_proto_range){from, to};
}

static inline void
builder_set_proto(
	struct filter_rule_builder *b,
	uint8_t proto,
	uint16_t enable_bits,
	uint16_t disable_bits
) {
	b->proto = (struct filter_proto){proto, enable_bits, disable_bits};
}

static inline void
builder_set_vlan(struct filter_rule_builder *b, uint16_t vlan) {
	b->vlan_ranges[0].from = vlan;
	b->vlan_ranges[0].to = vlan;
	b->vlan_range_count = 1;
}

static inline struct filter_rule
build_rule(struct filter_rule_builder *b, uint32_t action) {
	struct filter_rule r = {0};
	r.action = action;

	r.net4.src_count = (uint32_t)b->net4_src_count;
	r.net4.srcs = b->net4_src;
	r.net4.dst_count = (uint32_t)b->net4_dst_count;
	r.net4.dsts = b->net4_dst;

	r.net6.src_count = (uint32_t)b->net6_src_count;
	r.net6.srcs = b->net6_src;
	r.net6.dst_count = (uint32_t)b->net6_dst_count;
	r.net6.dsts = b->net6_dst;

	r.transport.proto = b->proto;
	r.transport.src_count = (uint16_t)b->src_port_ranges_count;
	r.transport.srcs = b->src_port_ranges;
	r.transport.dst_count = (uint16_t)b->dst_port_ranges_count;
	r.transport.dsts = b->dst_port_ranges;
	r.transport.proto_count = (uint16_t)b->proto_ranges_count;
	r.transport.protos = b->proto_ranges;

	r.device_count = 0;
	r.devices = NULL;

	r.vlan_range_count = (uint16_t)b->vlan_range_count;
	r.vlan_ranges = b->vlan_ranges;
	r.vlan = VLAN_UNSPEC;

	return r;
}

#define ip(a, b, c, d)                                                         \
	(uint8_t[4]) {                                                         \
		(uint8_t)(a), (uint8_t)(b), (uint8_t)(c), (uint8_t)(d)         \
	}
