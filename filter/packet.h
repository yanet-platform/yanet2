#pragma once

#include <stdint.h>

#include "common/network.h"

union net_addr {
	struct net6 ipv6;
	struct net4 ipv4;
};

struct packet_info {
	union net_addr src_addr;
	uint16_t src_port;

	union net_addr dst_addr;
	uint16_t dst_port;

	uint16_t proto_flags;
};