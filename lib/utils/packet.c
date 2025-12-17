#include "packet.h"
#include "rte_mbuf_core.h"

#include <netinet/in.h>
#include <rte_ether.h>
#include <rte_ip.h>
#include <rte_mbuf.h>
#include <rte_tcp.h>
#include <rte_udp.h>

#include <assert.h>
#include <stdlib.h>

#include <rte_build_config.h>
#include <string.h>

#include "lib/dataplane/packet/packet.h"
#include "yanet_build_config.h"

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
		sizeof(struct rte_mbuf) + RTE_PKTMBUF_HEADROOM + MBUF_MAX_SIZE;
	struct rte_mbuf *mbuf = aligned_alloc(64, total_size);
	if (!mbuf) {
		return NULL;
	}

	memset(mbuf, 0, sizeof(struct rte_mbuf));
	mbuf->refcnt = 1;

	uint16_t total_len = sizeof(struct rte_ether_hdr) +
			     sizeof(struct rte_ipv4_hdr) +
			     sizeof(struct rte_udp_hdr);

	mbuf->buf_addr = ((char *)mbuf) + sizeof(struct rte_mbuf);
	mbuf->data_len = MBUF_MAX_SIZE;
	mbuf->data_off = RTE_PKTMBUF_HEADROOM;
	mbuf->buf_len = MBUF_MAX_SIZE + RTE_PKTMBUF_HEADROOM;

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
		sizeof(struct rte_mbuf) + RTE_PKTMBUF_HEADROOM + MBUF_MAX_SIZE;
	struct rte_mbuf *mbuf = aligned_alloc(64, total_size);
	if (!mbuf) {
		return NULL;
	}
	memset(mbuf, 0, sizeof(struct rte_mbuf));
	mbuf->refcnt = 1;

	uint16_t total_len =
		sizeof(struct rte_ether_hdr) + sizeof(struct rte_ipv6_hdr) +
		(proto == IPPROTO_UDP ? sizeof(struct rte_udp_hdr)
				      : sizeof(struct rte_tcp_hdr));

	mbuf->buf_addr = ((char *)mbuf) + sizeof(struct rte_mbuf);
	mbuf->data_len = MBUF_MAX_SIZE;
	mbuf->data_off = RTE_PKTMBUF_HEADROOM;
	mbuf->buf_len = MBUF_MAX_SIZE + RTE_PKTMBUF_HEADROOM;

	mbuf->pkt_len = total_len;
	mbuf->l2_len = sizeof(struct rte_ether_hdr);
	mbuf->l3_len = sizeof(struct rte_ipv6_hdr);
	mbuf->l4_len =
		(proto == IPPROTO_UDP ? sizeof(struct rte_udp_hdr)
				      : sizeof(struct rte_tcp_hdr));
	mbuf->packet_type =
		RTE_PTYPE_L2_ETHER | RTE_PTYPE_L3_IPV6 |
		(proto == IPPROTO_UDP ? RTE_PTYPE_L4_UDP : RTE_PTYPE_L4_TCP);

	struct rte_ether_hdr *eth =
		rte_pktmbuf_mtod(mbuf, struct rte_ether_hdr *);
	eth->ether_type = rte_cpu_to_be_16(RTE_ETHER_TYPE_IPV6);

	struct rte_ipv6_hdr *ip = (struct rte_ipv6_hdr *)(eth + 1);
	ip->proto = proto;
	ip->payload_len = rte_cpu_to_be_16(
		proto == IPPROTO_UDP ? sizeof(struct rte_udp_hdr)
				     : sizeof(struct rte_tcp_hdr)
	);
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
fill_packet_net4(
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
fill_packet_net6(
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

int
fill_packet(
	struct packet *packet,
	const uint8_t *src_ip,
	const uint8_t *dst_ip,
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t transport_proto,
	uint8_t network_proto,
	uint16_t flags
) {
	if (network_proto == IPPROTO_IP) {
		return fill_packet_net4(
			packet,
			src_ip,
			dst_ip,
			src_port,
			dst_port,
			transport_proto,
			flags
		);
	} else if (network_proto == IPPROTO_IPV6) {
		return fill_packet_net6(
			packet,
			src_ip,
			dst_ip,
			src_port,
			dst_port,
			transport_proto,
			flags
		);
	} else {
		return -1;
	}
}

void
free_packet(struct packet *packet) {
	free(packet->mbuf);
}

////////////////////////////////////////////////////////////////////////////////

void
init_mbuf(struct rte_mbuf *m, struct packet_data *data, uint16_t buf_len) {
	m->priv_size = 0;
	m->buf_len = buf_len;
	uint32_t mbuf_size = sizeof(struct rte_mbuf) + m->priv_size;

	/* start of buffer is after mbuf structure and priv data */
	m->buf_addr = (char *)m + mbuf_size;

	/* keep some headroom between start of buffer and data */
	m->data_off = RTE_MIN(RTE_PKTMBUF_HEADROOM, m->buf_len);

	/* init some constant fields */
	m->pool = NULL;
	m->nb_segs = 1;
	m->port = 1; // fix RTE_MBUF_PORT_INVALID;
	rte_mbuf_refcnt_set(m, 1);
	m->next = NULL;

	// Initialize mbuf data
	m->data_len = data->size;
	// TODO: multisegment packets
	m->pkt_len = (uint32_t)data->size;
	memcpy(rte_pktmbuf_mtod(m, uint8_t *), data->data, data->size);
}

static int
init_packet_with_mbuf(
	struct packet *packet, struct rte_mbuf *mbuf, struct packet_data *data
) {
	// here mbuf is initialized
	memset(packet, 0, sizeof(struct packet));
	packet->mbuf = mbuf;
	packet->tx_device_id = data->tx_device_id;
	packet->rx_device_id = data->rx_device_id;
	return parse_packet(packet);
}

////////////////////////////////////////////////////////////////////////////////

static int
fill_packets(
	size_t packets_count,
	struct packet_data *packets,
	size_t mbuf_size,
	struct packet_list *packet_list,
	void *arena,
	size_t arena_size
) {
	(void)arena_size;
	assert(arena_size >= packets_count * mbuf_size);
	assert((uintptr_t)arena % alignof(struct rte_mbuf) == 0);

	packet_list_init(packet_list);

	for (size_t i = 0; i < packets_count; i++) {
		struct packet_data *data = &packets[i];
		struct rte_mbuf *m =
			(struct rte_mbuf *)((uint8_t *)arena + mbuf_size * i);
		init_mbuf(m, data, mbuf_size);
		struct packet *p = mbuf_to_packet(m);
		if (init_packet_with_mbuf(p, m, data) != 0) {
			return -1;
		}
		packet_list_add(packet_list, p);
	}

	return 0;
}

int
fill_packet_list(
	struct packet_list *packet_list,
	size_t packets_count,
	struct packet_data *packets,
	uint16_t mbuf_size
) {
	packet_list_init(packet_list);

	for (size_t i = 0; i < packets_count; i++) {
		struct packet_data *data = &packets[i];
		struct rte_mbuf *m =
			aligned_alloc(alignof(struct rte_mbuf), mbuf_size);
		init_mbuf(m, data, mbuf_size);
		struct packet *p = mbuf_to_packet(m);
		if (init_packet_with_mbuf(p, m, data) != 0) {
			return -1;
		}

		// Initialize packet
		memset(p, 0, sizeof(struct packet));
		p->mbuf = m;
		p->rx_device_id = data->rx_device_id;
		p->tx_device_id = data->tx_device_id;
		packet_list_add(packet_list, p);
	}

	return 0;
}

int
fill_packet_list_arena(
	struct packet_list *packet_list,
	size_t packets_count,
	struct packet_data *packets,
	uint16_t mbuf_size,
	void *arena,
	size_t arena_size
) {
	(void)arena_size;
	if ((uintptr_t)arena % alignof(struct rte_mbuf) != 0) {
		size_t align = alignof(struct rte_mbuf);
		size_t d = align - (uintptr_t)arena % align;
		arena += d;
		arena_size -= d;
		assert((uintptr_t)arena % align == 0);
	}
	return fill_packets(
		packets_count,
		packets,
		mbuf_size,
		packet_list,
		arena,
		arena_size
	);
}

void
free_packet_list(struct packet_list *packet_list) {
	while (1) {
		struct packet *packet = packet_list_pop(packet_list);
		if (packet == NULL) {
			break;
		}
		free_packet(packet);
	}
}

struct packet_data
packet_data(const struct packet *p) {
	struct rte_mbuf *m = packet_to_mbuf(p);
	// TODO: multisegment packets
	size_t size = m->data_len;
	uint8_t *data = rte_pktmbuf_mtod(m, uint8_t *);
	return (struct packet_data){data, size, p->tx_device_id, p->rx_device_id
	};
}

////////////////////////////////////////////////////////////////////////////////

int
fill_packet_from_data(struct packet *packet, struct packet_data *data) {
	size_t buf_len = RTE_PKTMBUF_HEADROOM + data->size;
	if (buf_len % alignof(struct rte_mbuf) != 0) {
		size_t a = alignof(struct rte_mbuf);
		buf_len += a - buf_len % a;
	}
	struct rte_mbuf *mbuf = aligned_alloc(
		alignof(struct rte_mbuf), sizeof(struct rte_mbuf) + buf_len
	);
	init_mbuf(mbuf, data, buf_len);
	init_packet_with_mbuf(packet, mbuf, data);
	return parse_packet(packet);
}
