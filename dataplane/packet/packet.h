#ifndef PACKET_H
#define PACKET_H

#include <stdlib.h>
#include <stdint.h>

#include "rte_mbuf.h"
#include "rte_ether.h"
#include "rte_ip.h"

#define PACKET_HEADER_TYPE_UNKNOWN 0

struct network_header {
	uint16_t type;
	uint16_t offset;
};

struct transport_header {
	uint16_t type;
	uint16_t offset;
};

struct packet {
	struct packet *next;
	struct rte_mbuf *mbuf;
	uint16_t flags;
	uint16_t vlan;
	struct network_header network_header;
	struct transport_header transport_header;
};

struct packet_list {
	struct packet *first;
	struct packet **last;
};

static inline void
packet_list_init(struct packet_list *list)
{
	list->first = NULL;
	list->last = &list->first;
}

static inline void
packet_list_add(struct packet_list *list, struct packet *packet)
{
	if (*list->last != NULL)
		(*list->last)->next = packet;
	*list->last = packet;
	packet->next = NULL;
}

static inline struct packet *
packet_list_first(struct packet_list *list)
{
	return list->first;
}

int
parse_packet(struct packet *packet);

static inline struct rte_mbuf *
packet_to_mbuf(const struct packet *packet)
{
	return packet->mbuf;
}

static inline struct packet *
mbuf_to_packet(struct rte_mbuf *mbuf)
{
	return (struct packet *)((void*)mbuf->buf_addr);
}

struct ipv6_ext_2byte {
	uint8_t next_type;
	uint8_t size;
} __rte_packed;

struct ipv6_ext_fragment {
        uint8_t next_type;
        uint8_t reserved;
        uint16_t offset_flag;
        uint32_t identification;
} __rte_packed;


#endif
