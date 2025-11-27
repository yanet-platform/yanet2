#pragma once

#include <stddef.h>

#include <common/memory.h>
#include <common/network.h>

#include <lib/dataplane/module/module.h>
#include <lib/dataplane/packet/packet.h>

////////////////////////////////////////////////////////////////////////////////

int
fill_packet_net4(
	struct packet *packet,
	const uint8_t src_ip[NET4_LEN],
	const uint8_t dst_ip[NET4_LEN],
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t proto,
	uint16_t flags
);

int
fill_packet_net6(
	struct packet *packet,
	const uint8_t src_ip[NET6_LEN],
	const uint8_t dst_ip[NET6_LEN],
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t proto,
	uint16_t flags
);

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
);

void
free_packet(struct packet *packet);

////////////////////////////////////////////////////////////////////////////////

struct packet_data {
	const uint8_t *data;
	uint16_t size;
	uint16_t tx_device_id;
	uint16_t rx_device_id;
};

struct packet_data
packet_data(const struct packet *p);

////////////////////////////////////////////////////////////////////////////////

int
fill_packet_list(
	struct packet_list *packet_list,
	size_t packets_count,
	struct packet_data *packets,
	uint16_t mbuf_size
);

/// Fills packet list and uses `arena` as storage for `rte_mbuf`.
int
fill_packet_list_arena(
	struct packet_list *packet_list,
	size_t packets_count,
	struct packet_data *packets,
	uint16_t mbuf_size,
	void *arena,
	size_t arena_size
);

/// Free packet list in case its `rte_mbuf`s were allocated with malloc.
void
free_packet_list(struct packet_list *packet_list);

////////////////////////////////////////////////////////////////////////////////

int
fill_packet_from_data(struct packet *packet, struct packet_data *data);