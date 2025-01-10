#pragma once

#include <stdint.h>

struct ether_addr {
	uint8_t addr[6];
};

struct net6 {
	uint64_t addr_hi;
	uint64_t addr_lo;
	uint64_t mask_hi;
	uint64_t mask_lo;
};

struct net4 {
	uint32_t addr;
	uint32_t mask;
};
