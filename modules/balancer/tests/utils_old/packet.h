#pragma once

#include <stddef.h>

#include <common/memory.h>
#include <common/network.h>
#include <lib/dataplane/packet/packet.h>

////////////////////////////////////////////////////////////////////////////////

int
make_packet4(
	struct packet *packet,
	const uint8_t src_ip[NET4_LEN],
	const uint8_t dst_ip[NET4_LEN],
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t proto,
	uint16_t flags
);

int
make_packet6(
	struct packet *packet,
	const uint8_t src_ip[NET6_LEN],
	const uint8_t dst_ip[NET6_LEN],
	uint16_t src_port,
	uint16_t dst_port,
	uint8_t proto,
	uint16_t flags
);

int
make_packet_generic(
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