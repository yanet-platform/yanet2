#pragma once

#include "action.h"
#include "dataplane/packet/packet.h"

#include "filter.h"

void
free_packet(struct packet *packet);

struct packet
make_packet(
	uint32_t src_ip, uint32_t dst_ip, uint16_t src_port, uint16_t dst_port
);

void
query_filter_and_expect_action(
	struct filter *filter, struct packet *packet, uint32_t expected_action
);

void
query_filter_and_expect_no_actions(
	struct filter *filter, struct packet *packet
);

struct filter_action_builder {
	struct net6 net6_dst[10];
	size_t net6_dst_count;

	struct net6 net6_src[10];
	size_t net6_src_count;

	struct net4 net4_dst[10];
	size_t net4_dst_count;

	struct net4 net4_src[10];
	size_t net4_src_count;

	struct filter_port_range dst_port_ranges[10];
	size_t port_dst_ranges_count;

	struct filter_port_range src_port_ranges[10];
	size_t port_src_ranges_count;
};

void
builder_init(struct filter_action_builder *builder);

void
builder_add_net6_dst(struct filter_action_builder *builder, struct net6 dst);

void
builder_add_net6_src(struct filter_action_builder *builder, struct net6 src);

void
builder_add_net4_dst(
	struct filter_action_builder *builder, uint32_t addr, uint32_t mask
);

void
builder_add_net4_src(
	struct filter_action_builder *builder, uint32_t addr, uint32_t mask
);

void
builder_add_port_dst_range(
	struct filter_action_builder *builder, uint16_t from, uint16_t to
);

void
builder_add_port_src_range(
	struct filter_action_builder *builder, uint16_t from, uint16_t to
);

struct filter_action
build_action(struct filter_action_builder *builder, uint32_t action);

////////////////////////////////////////////////////////////////////////////////

inline static uint32_t
ip(uint8_t a, uint8_t b, uint8_t c, uint8_t d) {
	return (a << 24) | (b << 16) | (c << 8) | d;
}
