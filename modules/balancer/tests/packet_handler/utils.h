#pragma once

#include "config.h"
#include <stddef.h>

#include <common/memory.h>

////////////////////////////////////////////////////////////////////////////////

struct cp_module *
make_balancer(
	struct memory_context *mctx,
	size_t workers,
	struct balancer_state_config *cfg
);

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

void
free_packet(struct packet *packet);