#pragma once

#include <netinet/in.h>
#include <stdint.h>

struct ether_addr {
	uint8_t addr[6];
};

#define NET4_LEN 4
#define NET6_LEN 16

struct net6_addr {
	uint8_t bytes[NET6_LEN];
};

struct net6_addr_range {
	struct net6_addr start;
	struct net6_addr end;
};

struct net4_addr {
	uint8_t bytes[NET4_LEN];
};

struct net4_addr_range {
	struct net4_addr from;
	struct net4_addr to;
};

struct net_addr {
	union {
		struct net4_addr v4;
		struct net6_addr v6;
	};
};

struct net_addr_range {
	struct net_addr from;
	struct net_addr to;
};

struct net6 {
	uint8_t addr[NET6_LEN];
	uint8_t mask[NET6_LEN];
};

struct net4 {
	uint8_t addr[NET4_LEN];
	uint8_t mask[NET4_LEN];
};

struct net {
	union {
		struct net4 v4;
		struct net6 v6;
	};
};
