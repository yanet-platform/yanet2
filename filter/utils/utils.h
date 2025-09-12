#pragma once

#include "common/network.h"
#include "dataplane/packet/packet.h"
#include "rule.h"

#include "filter.h"

////////////////////////////////////////////////////////////////////////////////

#define MAX_RULES 10

void
free_packet(struct packet *packet);

struct packet
make_packet(
	uint8_t *src_ip,
	uint8_t *dst_ip,
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t proto,
	uint16_t flags,
	uint16_t vlan
);

struct packet
make_packet_net6(
	const uint8_t src_ip[NET6_LEN],
	const uint8_t dst_ip[NET6_LEN],
	uint16_t src_port,
	uint16_t dst_port
);

void
query_filter_and_expect_action(
	struct filter *filter, struct packet *packet, uint32_t expected_action
);

void
query_filter_and_expect_no_actions(
	struct filter *filter, struct packet *packet
);

struct filter_rule_builder {
	struct net6 net6_dst[MAX_RULES];
	size_t net6_dst_count;

	struct net6 net6_src[MAX_RULES];
	size_t net6_src_count;

	struct net4 net4_dst[MAX_RULES];
	size_t net4_dst_count;

	struct net4 net4_src[MAX_RULES];
	size_t net4_src_count;

	struct filter_proto proto;

	struct filter_port_range dst_port_ranges[MAX_RULES];
	size_t port_dst_ranges_count;

	struct filter_port_range src_port_ranges[MAX_RULES];
	size_t port_src_ranges_count;

	uint16_t vlan;
};

void
builder_init(struct filter_rule_builder *builder);

void
builder_add_net6_dst(struct filter_rule_builder *builder, struct net6 dst);

void
builder_add_net6_src(struct filter_rule_builder *builder, struct net6 src);

void
builder_add_net4_dst(
	struct filter_rule_builder *builder, uint8_t *addr, uint8_t *mask
);

void
builder_add_net4_src(
	struct filter_rule_builder *builder, uint8_t *addr, uint8_t *mask
);

void
builder_add_port_dst_range(
	struct filter_rule_builder *builder, uint16_t from, uint16_t to
);

void
builder_add_port_src_range(
	struct filter_rule_builder *builder, uint16_t from, uint16_t to
);

void
builer_set_proto(
	struct filter_rule_builder *builder,
	uint8_t proto,
	uint16_t enable_bits,
	uint16_t disable_bits
);

void
builder_set_vlan(struct filter_rule_builder *builder, uint16_t vlan);

struct filter_rule
build_rule(struct filter_rule_builder *builder, uint32_t action);

////////////////////////////////////////////////////////////////////////////////

#define ip(a, b, c, d)                                                         \
	(uint8_t[4]) {                                                         \
		a, b, c, d                                                     \
	}
