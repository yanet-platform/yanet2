#include "utils.h"
#include "filter.h"
#include "rule.h"

#include <assert.h>

#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

static struct rte_mbuf *
make_mbuf(
	uint32_t src_ip,
	uint32_t dst_ip,
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t proto,
	uint16_t flags,
	uint16_t vlan
) {
	size_t total_size =
		sizeof(struct rte_mbuf) + RTE_PKTMBUF_HEADROOM + 2048;
	struct rte_mbuf *mbuf = malloc(total_size);
	memset(mbuf, 0, sizeof(struct rte_mbuf));
	mbuf->refcnt = 1;

	if (!mbuf)
		return NULL;

	uint16_t total_len = sizeof(struct rte_ether_hdr) +
			     sizeof(struct rte_ipv4_hdr) +
			     sizeof(struct rte_udp_hdr);

	mbuf->buf_addr = ((char *)mbuf) + sizeof(struct rte_mbuf);
	mbuf->data_len = 2048;
	mbuf->data_off = RTE_PKTMBUF_HEADROOM;
	mbuf->buf_len = 2048 + RTE_PKTMBUF_HEADROOM;

	mbuf->pkt_len = total_len;
	mbuf->l2_len = sizeof(struct rte_ether_hdr);
	mbuf->l3_len = sizeof(struct rte_ipv4_hdr);

	if (vlan != 0) {
		mbuf->l2_len += sizeof(struct rte_ether_hdr);
		mbuf->vlan_tci = rte_cpu_to_be_16(vlan);
		int res = rte_vlan_insert(&mbuf);
		assert(res == 0);
	}

	struct rte_ether_hdr *eth =
		rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV4);

	struct rte_ipv4_hdr *ip = (struct rte_ipv4_hdr *)(eth + 1);

	ip->version_ihl = 0x45;
	ip->type_of_service = 0;
	ip->total_length = rte_cpu_to_be_16(total_len - sizeof(*eth));
	ip->packet_id = 0;
	ip->fragment_offset = 0;
	ip->time_to_live = 64;
	ip->next_proto_id = proto;
	ip->src_addr = rte_cpu_to_be_32(src_ip);
	ip->dst_addr = rte_cpu_to_be_32(dst_ip);
	ip->hdr_checksum = 0;

	if (proto == IPPROTO_UDP) {
		struct rte_udp_hdr *udp = (struct rte_udp_hdr *)(ip + 1);
		udp->src_port = rte_cpu_to_be_16(src_port);
		udp->dst_port = rte_cpu_to_be_16(dst_port);
		udp->dgram_len = rte_cpu_to_be_16(sizeof(*udp));
		udp->dgram_cksum = 0;
	} else { // tcp
		struct rte_tcp_hdr *tcp = (struct rte_tcp_hdr *)(ip + 1);
		tcp->src_port = rte_cpu_to_be_16(src_port);
		tcp->dst_port = rte_cpu_to_be_16(dst_port);
		tcp->tcp_flags = flags;
	}

	return mbuf;
}

// IPv6 in host byte order
static struct rte_mbuf *
make_mbuf_net6(
	const uint8_t src_ip[NET6_LEN],
	const uint8_t dst_ip[NET6_LEN],
	uint16_t src_port,
	uint16_t dst_port
) {
	size_t total_size =
		sizeof(struct rte_mbuf) + RTE_PKTMBUF_HEADROOM + 2048;
	struct rte_mbuf *mbuf = malloc(total_size);
	memset(mbuf, 0, sizeof(struct rte_mbuf));
	mbuf->refcnt = 1;

	if (!mbuf) {
		return NULL;
	}

	uint16_t total_len = sizeof(struct rte_ether_hdr) +
			     sizeof(struct rte_ipv6_hdr) +
			     sizeof(struct rte_udp_hdr);

	mbuf->buf_addr = ((char *)mbuf) + sizeof(struct rte_mbuf);
	mbuf->data_len = 2048;
	mbuf->data_off = RTE_PKTMBUF_HEADROOM;
	mbuf->buf_len = 2048 + RTE_PKTMBUF_HEADROOM;

	mbuf->pkt_len = total_len;
	mbuf->l2_len = sizeof(struct rte_ether_hdr);
	mbuf->l3_len = sizeof(struct rte_ipv6_hdr);
	mbuf->l4_len = sizeof(struct rte_udp_hdr);
	mbuf->packet_type =
		RTE_PTYPE_L2_ETHER | RTE_PTYPE_L3_IPV6 | RTE_PTYPE_L4_UDP;

	struct rte_ether_hdr *eth =
		rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	struct rte_ipv6_hdr *ip = (struct rte_ipv6_hdr *)(eth + 1);
	ip->proto = IPPROTO_UDP;
	ip->payload_len = rte_cpu_to_be_16(sizeof(struct rte_udp_hdr));
	for (size_t i = 0; i < NET6_LEN; ++i) {
		ip->src_addr[i] = src_ip[NET6_LEN - i - 1];
		ip->dst_addr[i] = dst_ip[NET6_LEN - i - 1];
	}

	struct rte_udp_hdr *udp = (struct rte_udp_hdr *)(ip + 1);
	udp->src_port = rte_cpu_to_be_16(src_port);
	udp->dst_port = rte_cpu_to_be_16(dst_port);
	udp->dgram_len = rte_cpu_to_be_16(sizeof(*udp));
	udp->dgram_cksum = 0;

	return mbuf;
}

void
free_packet(struct packet *packet) {
	free(packet->mbuf);
}

struct packet
make_packet(
	uint32_t src_ip,
	uint32_t dst_ip,
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t proto,
	uint16_t flags,
	uint16_t vlan
) {
	struct packet packet;
	packet.mbuf = make_mbuf(
		src_ip, dst_ip, src_port, dst_port, proto, flags, vlan
	);
	assert(packet.mbuf != NULL);
	int parse_result = parse_packet(&packet);
	assert(parse_result == 0);
	return packet;
}

// IPv6 in host byte order
struct packet
make_packet_net6(
	const uint8_t src_ip[NET6_LEN],
	const uint8_t dst_ip[NET6_LEN],
	uint16_t src_port,
	uint16_t dst_port
) {
	struct packet packet;
	packet.mbuf = make_mbuf_net6(src_ip, dst_ip, src_port, dst_port);
	assert(packet.mbuf != NULL);
	int parse_result = parse_packet(&packet);
	assert(parse_result == 0);
	return packet;
}

////////////////////////////////////////////////////////////////////////////////

void
query_filter_and_expect_action(
	struct filter *filter, struct packet *packet, uint32_t expected_action
) {
	uint32_t *actions;
	uint32_t count;
	int res = filter_query(filter, packet, &actions, &count);
	assert(res == 0);
	assert(count == 1);
	assert(expected_action == actions[0]);
}

void
query_filter_and_expect_no_actions(
	struct filter *filter, struct packet *packet
) {
	uint32_t *actions;
	uint32_t count;
	int res = filter_query(filter, packet, &actions, &count);
	assert(res == 0);
	assert(count == 0);
}

////////////////////////////////////////////////////////////////////////////////

void
builder_add_net6_dst(struct filter_rule_builder *builder, struct net6 dst) {
	builder->net6_dst[builder->net6_dst_count++] = dst;
}

void
builder_add_net6_src(struct filter_rule_builder *builder, struct net6 src) {
	builder->net6_src[builder->net6_src_count++] = src;
}

void
builder_add_net4_dst(
	struct filter_rule_builder *builder, uint32_t addr, uint32_t mask
) {
	struct net4 dst = {addr, mask};
	builder->net4_dst[builder->net4_dst_count++] = dst;
}

void
builder_add_net4_src(
	struct filter_rule_builder *builder, uint32_t addr, uint32_t mask
) {
	struct net4 src = {addr, mask};
	builder->net4_src[builder->net4_src_count++] = src;
}

void
builder_add_port_dst_range(
	struct filter_rule_builder *builder, uint16_t from, uint16_t to
) {
	struct filter_port_range port_range = {from, to};
	builder->dst_port_ranges[builder->port_dst_ranges_count++] = port_range;
}

void
builder_add_port_src_range(
	struct filter_rule_builder *builder, uint16_t from, uint16_t to
) {
	struct filter_port_range port_range = {from, to};
	builder->src_port_ranges[builder->port_src_ranges_count++] = port_range;
}

void
builer_set_proto(
	struct filter_rule_builder *builder,
	uint8_t proto,
	uint16_t enable_bits,
	uint16_t disable_bits
) {
	builder->proto =
		(struct filter_proto){proto, enable_bits, disable_bits};
}

void
builder_set_vlan(struct filter_rule_builder *builder, uint16_t vlan) {
	builder->vlan = vlan;
}

void
builder_init(struct filter_rule_builder *builder) {
	memset(builder, 0, sizeof(struct filter_rule_builder));
	builder->proto.proto = PROTO_UNSPEC;
	builder->vlan = VLAN_UNSPEC;
}

struct filter_rule
build_rule(struct filter_rule_builder *builder, uint32_t action) {
	struct filter_rule result_action = {
		.action = action,
		.net4 =
			{
				.dst_count = builder->net4_dst_count,
				.dsts = builder->net4_dst,
				.src_count = builder->net4_src_count,
				.srcs = builder->net4_src,
			},
		.net6 =
			{
				.dst_count = builder->net6_dst_count,
				.dsts = builder->net6_dst,
				.src_count = builder->net6_src_count,
				.srcs = builder->net6_src,
			},
		.transport =
			{
				.proto = builder->proto,
				.dst_count = builder->port_dst_ranges_count,
				.dsts = builder->dst_port_ranges,
				.src_count = builder->port_src_ranges_count,
				.srcs = builder->src_port_ranges,
			},
		.vlan = builder->vlan
	};
	return result_action;
}