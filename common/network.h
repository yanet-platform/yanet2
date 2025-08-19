#pragma once

#include <stdint.h>

struct ether_addr {
	uint8_t addr[6];
};

struct net6 {
	// IPv6 address in the host byte order.
	uint8_t ip[16];

	// Network prefix length for 8 higher bytes.
	uint8_t pref_hi;

	// Network prefix length for 8 lower bytes.
	uint8_t pref_lo;
};

struct net4 {
	// IPv4 address in the host byte order.
	uint32_t addr;

	// IPv4 network mask in the host byte order.
	// Only prefix-consecutive masks are supported.
	uint32_t mask;
};
