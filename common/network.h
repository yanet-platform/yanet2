#pragma once

#include <stdint.h>

struct ether_addr {
	uint8_t addr[6];
};

#define NET6_LEN 16
#define NET4_LEN 4

struct net6 {
	uint8_t addr[NET6_LEN];
	uint8_t mask[NET6_LEN];
};

struct net4 {
	uint8_t addr[NET4_LEN];
	uint8_t mask[NET4_LEN];
};
