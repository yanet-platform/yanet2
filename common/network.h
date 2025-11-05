#pragma once

#include <stdint.h>

struct ether_addr {
	uint8_t addr[6];
};

#define NET4_LEN 4
#define NET6_LEN 16

struct net6 {
	uint8_t addr[16];
	uint8_t mask[16];
};

struct net4 {
	uint8_t addr[4];
	uint8_t mask[4];
};
