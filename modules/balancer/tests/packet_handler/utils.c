#include "utils.h"
#include "common/network.h"
#include "config.h"

////////////////////////////////////////////////////////////////////////////////

int
config_data_init(
	struct balancer_module_config *config,
	struct memory_context *mctx,
	size_t workers_cnt
);

////////////////////////////////////////////////////////////////////////////////

struct cp_module *
make_balancer(
	struct memory_context *mctx,
	size_t workers,
	struct balancer_state_config *state_cfg
) {
	struct balancer_module_config *cfg =
		memory_balloc(mctx, sizeof(*state_cfg));
	memcpy(&cfg->state_config, state_cfg, sizeof(*state_cfg));
	int res = config_data_init(cfg, mctx, workers);
	if (res != 0) {
		return NULL;
	}
	return &cfg->cp_module;
}

////////////////////////////////////////////////////////////////////////////////

static struct rte_mbuf *
make_mbuf4(
	const uint8_t src_ip[NET4_LEN],
	const uint8_t dst_ip[NET4_LEN],
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t proto,
	uint16_t flags
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
	memcpy(&ip->src_addr, src_ip, 4);
	memcpy(&ip->dst_addr, dst_ip, 4);
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

////////////////////////////////////////////////////////////////////////////////

// IPv6 in host byte order
static struct rte_mbuf *
make_mbuf6(
	const uint8_t src_ip[NET6_LEN],
	const uint8_t dst_ip[NET6_LEN],
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t proto,
	uint8_t flags
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
	memcpy(ip->src_addr, src_ip, 16);
	memcpy(ip->dst_addr, dst_ip, 16);

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

int
make_packet4(
	struct packet *packet,
	const uint8_t src_ip[NET4_LEN],
	const uint8_t dst_ip[NET4_LEN],
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t proto,
	uint16_t flags
) {
	packet->mbuf =
		make_mbuf4(src_ip, dst_ip, src_port, dst_port, proto, flags);
	assert(packet->mbuf != NULL);
	return parse_packet(packet);
}

// IPv6 in host byte order
int
make_packet6(
	struct packet *packet,
	const uint8_t src_ip[NET6_LEN],
	const uint8_t dst_ip[NET6_LEN],
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t proto,
	uint16_t flags
) {
	packet->mbuf =
		make_mbuf6(src_ip, dst_ip, src_port, dst_port, proto, flags);
	assert(packet->mbuf != NULL);
	return parse_packet(packet);
}

void
free_packet(struct packet *packet) {
	free(packet->mbuf);
}